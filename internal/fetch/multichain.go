// Package fetch provides data retrieval from various DeFi protocols across multiple chains
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// Using types from shared package
import (
	"github.com/yourorg/restake-yield-ea/internal/types"
)

// SupportedChain represents a blockchain network supported by the adapter
type SupportedChain = types.SupportedChain

// Supported blockchain networks (aliased from types package)
const (
	ChainEthereum   = types.ChainEthereum
	ChainPolygon    = types.ChainPolygon
	ChainArbitrum   = types.ChainArbitrum
	ChainOptimism   = types.ChainOptimism
	ChainAvalanche  = types.ChainAvalanche
	ChainBSC        = types.ChainBSC
	ChainBase       = types.ChainBase
)

// ChainConfig holds configuration for a specific blockchain network
type ChainConfig = types.ChainConfig

// MultiChainClient can fetch data from multiple blockchains
type MultiChainClient struct {
	httpClient    *http.Client
	chains        map[SupportedChain]ChainConfig
	dataProviders map[SupportedChain][]Provider
	mutex         sync.RWMutex
	cacheTTL      time.Duration
	cachedData    map[SupportedChain][]model.Metric
	cacheTime     map[SupportedChain]time.Time
}

// NewMultiChainClient creates a client that can fetch from multiple chains
func NewMultiChainClient(chains map[SupportedChain]ChainConfig) *MultiChainClient {
	retryClient := newRetryClient()
	
	return &MultiChainClient{
		httpClient:    StandardClient(retryClient),
		chains:        chains,
		dataProviders: make(map[SupportedChain][]Provider),
		cacheTTL:      5 * time.Minute,
		cachedData:    make(map[SupportedChain][]model.Metric),
		cacheTime:     make(map[SupportedChain]time.Time),
	}
}

// RegisterProvider adds a data provider for a specific chain
func (c *MultiChainClient) RegisterProvider(chain SupportedChain, provider Provider) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	c.dataProviders[chain] = append(c.dataProviders[chain], provider)
	logrus.Infof("Registered provider for chain %s", chain)
}

// Fetch retrieves data from all enabled chains
func (c *MultiChainClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	c.mutex.RLock()
	enabledChains := c.getEnabledChains()
	c.mutex.RUnlock()

	var wg sync.WaitGroup
	var mu sync.Mutex
	
	allMetrics := make([]model.Metric, 0)
	errors := make(map[SupportedChain]error)
	
	// Create a channel for results
	resultCh := make(chan struct {
		chain   SupportedChain
		metrics []model.Metric
		err     error
	}, len(enabledChains))
	
	// Launch a goroutine for each chain
	for _, chain := range enabledChains {
		wg.Add(1)
		go func(chain SupportedChain) {
			defer wg.Done()
			
			metrics, err := c.fetchChainData(ctx, chain)
			resultCh <- struct {
				chain   SupportedChain
				metrics []model.Metric
				err     error
			}{chain, metrics, err}
		}(chain)
	}
	
	// Launch a goroutine to close the channel when all fetches are done
	go func() {
		wg.Wait()
		close(resultCh)
	}()
	
	// Collect results from the channel
	for result := range resultCh {
		if result.err != nil {
			errors[result.chain] = result.err
			logrus.Warnf("Error fetching data for chain %s: %v", result.chain, result.err)
			continue
		}
		
		// Add chain information to each metric
		chainMetrics := make([]model.Metric, len(result.metrics))
		for i, metric := range result.metrics {
			chainMetric := metric
			chainMetric.Chain = string(result.chain)
			
			// Add chain-specific weight for cross-chain aggregation
			chainConfig, ok := c.chains[result.chain]
			if ok {
				chainMetric.Weight = chainConfig.Weight
			} else {
				chainMetric.Weight = 1.0 // Default weight
			}
			
			chainMetrics[i] = chainMetric
		}
		
		mu.Lock()
		allMetrics = append(allMetrics, chainMetrics...)
		mu.Unlock()
		
		// Update cache
		c.mutex.Lock()
		c.cachedData[result.chain] = result.metrics
		c.cacheTime[result.chain] = time.Now()
		c.mutex.Unlock()
	}
	
	if len(allMetrics) == 0 && len(errors) > 0 {
		// If all chains failed, return the first error
		for _, err := range errors {
			return nil, fmt.Errorf("multi-chain fetch failed: %w", err)
		}
	}
	
	logrus.Infof("Fetched metrics from %d/%d chains, total metrics: %d", 
		len(enabledChains)-len(errors), len(enabledChains), len(allMetrics))
	
	return allMetrics, nil
}

// fetchChainData retrieves data for a specific chain, using cache if available
func (c *MultiChainClient) fetchChainData(ctx context.Context, chain SupportedChain) ([]model.Metric, error) {
	// Check cache first
	c.mutex.RLock()
	if metrics, ok := c.cachedData[chain]; ok {
		if time.Since(c.cacheTime[chain]) < c.cacheTTL {
			c.mutex.RUnlock()
			return metrics, nil
		}
	}
	c.mutex.RUnlock()
	
	// Get providers for this chain
	c.mutex.RLock()
	providers := c.dataProviders[chain]
	c.mutex.RUnlock()
	
	if len(providers) == 0 {
		// Try using default provider for this chain
		defaultProvider, err := c.createDefaultProvider(chain)
		if err != nil {
			return nil, fmt.Errorf("no providers available for chain %s: %w", chain, err)
		}
		providers = []Provider{defaultProvider}
	}
	
	// Fetch from all providers for this chain
	var wg sync.WaitGroup
	var mu sync.Mutex
	metrics := make([]model.Metric, 0)
	providerErrors := make([]error, 0)
	
	for _, provider := range providers {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			
			// Create a timeout context for this provider
			providerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			
			providerMetrics, err := p.Fetch(providerCtx)
			if err != nil {
				mu.Lock()
				providerErrors = append(providerErrors, err)
				mu.Unlock()
				return
			}
			
			mu.Lock()
			metrics = append(metrics, providerMetrics...)
			mu.Unlock()
		}(provider)
	}
	
	wg.Wait()
	
	if len(metrics) == 0 && len(providerErrors) > 0 {
		return nil, fmt.Errorf("all providers failed for chain %s", chain)
	}
	
	return metrics, nil
}

// createDefaultProvider creates a basic provider for the specified chain
func (c *MultiChainClient) createDefaultProvider(chain SupportedChain) (Provider, error) {
	// Get chain config
	c.mutex.RLock()
	chainConfig, ok := c.chains[chain]
	c.mutex.RUnlock()
	
	if !ok || !chainConfig.Enabled {
		return nil, fmt.Errorf("chain %s not configured or disabled", chain)
	}
	
	// Create an appropriate provider based on the chain
	switch chain {
	case ChainEthereum:
		return NewEigenLayerClient(), nil
	case ChainPolygon:
		return NewPolygonProvider(chainConfig.APIEndpoint, chainConfig.APIKey), nil
	case ChainArbitrum:
		return NewArbitrumProvider(chainConfig.APIEndpoint, chainConfig.APIKey), nil
	default:
		// Generic provider for other chains
		return NewGenericChainProvider(string(chain), chainConfig.APIEndpoint, chainConfig.APIKey), nil
	}
}

// getEnabledChains returns a list of chains that are enabled
func (c *MultiChainClient) getEnabledChains() []SupportedChain {
	var enabledChains []SupportedChain
	
	for chain, config := range c.chains {
		if config.Enabled {
			enabledChains = append(enabledChains, chain)
		}
	}
	
	return enabledChains
}

// GenericChainProvider is a fallback provider for any supported chain
type GenericChainProvider struct {
	chain      string
	apiURL     string
	apiKey     string
	httpClient *http.Client
}

// NewGenericChainProvider creates a new provider for any chain
func NewGenericChainProvider(chain, apiURL, apiKey string) *GenericChainProvider {
	return &GenericChainProvider{
		chain:      chain,
		apiURL:     apiURL,
		apiKey:     apiKey,
		httpClient: StandardClient(newRetryClient()),
	}
}

// Fetch retrieves yield data from the specified chain
func (p *GenericChainProvider) Fetch(ctx context.Context) ([]model.Metric, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.apiURL+"/yield", nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching data from %s: %w", p.chain, err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s API error: status %d, body: %s", p.chain, resp.StatusCode, string(body))
	}
	
	var response struct {
		Data []struct {
			Protocol     string  `json:"protocol"`
			APY          float64 `json:"apy"`
			TVL          float64 `json:"tvl"`
			PointsPerETH float64 `json:"points_per_eth"`
			Timestamp    int64   `json:"timestamp"`
		} `json:"data"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}
	
	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no data returned from %s", p.chain)
	}
	
	metrics := make([]model.Metric, 0, len(response.Data))
	for _, data := range response.Data {
		metrics = append(metrics, model.Metric{
			Provider:     data.Protocol,
			Chain:        p.chain,
			APY:          data.APY,
			TVL:          data.TVL,
			PointsPerETH: data.PointsPerETH,
			CollectedAt:  data.Timestamp,
		})
	}
	
	return metrics, nil
}

// PolygonProvider fetches yield data from Polygon
type PolygonProvider struct {
	GenericChainProvider
}

// NewPolygonProvider creates a new Polygon-specific provider
func NewPolygonProvider(apiURL, apiKey string) *PolygonProvider {
	return &PolygonProvider{
		GenericChainProvider: *NewGenericChainProvider("polygon", apiURL, apiKey),
	}
}

// ArbitrumProvider fetches yield data from Arbitrum
type ArbitrumProvider struct {
	GenericChainProvider
}

// NewArbitrumProvider creates a new Arbitrum-specific provider
func NewArbitrumProvider(apiURL, apiKey string) *ArbitrumProvider {
	return &ArbitrumProvider{
		GenericChainProvider: *NewGenericChainProvider("arbitrum", apiURL, apiKey),
	}
}
