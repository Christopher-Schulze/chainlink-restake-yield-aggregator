// Package fetch provides data retrieval from various DeFi protocols across
// multiple chains. The MultiChainClient fans out across configured chains,
// each of which fans out across its registered providers, and merges the
// results into a single metric slice with per-chain weights attached.
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/christopher/restake-yield-ea/internal/provider"
	"github.com/christopher/restake-yield-ea/internal/types"
	"github.com/sirupsen/logrus"
)

// SupportedChain is re-exported from the types package for convenience.
type SupportedChain = types.SupportedChain

// Supported blockchain networks (aliased from the types package).
const (
	ChainEthereum  = types.ChainEthereum
	ChainPolygon   = types.ChainPolygon
	ChainArbitrum  = types.ChainArbitrum
	ChainOptimism  = types.ChainOptimism
	ChainAvalanche = types.ChainAvalanche
	ChainBSC       = types.ChainBSC
	ChainBase      = types.ChainBase
)

// ChainConfig is re-exported from the types package.
type ChainConfig = types.ChainConfig

// MultiChainClient fetches yield data from multiple blockchains concurrently,
// caching results per chain for a configurable TTL.
type MultiChainClient struct {
	cfg           config.Config
	httpClient    *http.Client
	chains        map[SupportedChain]ChainConfig
	dataProviders map[SupportedChain][]provider.Provider
	mutex         sync.RWMutex
	cacheTTL      time.Duration
	cachedData    map[SupportedChain][]model.Metric
	cacheTime     map[SupportedChain]time.Time
}

// NewMultiChainClient creates a client that can fetch from multiple chains.
func NewMultiChainClient(cfg config.Config, chains map[SupportedChain]ChainConfig) *MultiChainClient {
	return &MultiChainClient{
		cfg:           cfg,
		httpClient:    standardHTTPClient(newRetryClient()),
		chains:        chains,
		dataProviders: make(map[SupportedChain][]provider.Provider),
		cacheTTL:      5 * time.Minute,
		cachedData:    make(map[SupportedChain][]model.Metric),
		cacheTime:     make(map[SupportedChain]time.Time),
	}
}

// RegisterProvider adds a data provider for a specific chain.
func (c *MultiChainClient) RegisterProvider(chain SupportedChain, p provider.Provider) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.dataProviders[chain] = append(c.dataProviders[chain], p)
	logrus.Infof("Registered provider %q for chain %s", p.Name(), chain)
}

// Fetch retrieves data from all enabled chains concurrently.
func (c *MultiChainClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	c.mutex.RLock()
	enabledChains := c.getEnabledChains()
	c.mutex.RUnlock()

	var (
		mu         sync.Mutex
		allMetrics []model.Metric
		errs       map[SupportedChain]error
	)

	resultCh := make(chan struct {
		chain   SupportedChain
		metrics []model.Metric
		err     error
	}, len(enabledChains))

	var wg sync.WaitGroup
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

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	errs = make(map[SupportedChain]error)
	for r := range resultCh {
		if r.err != nil {
			errs[r.chain] = r.err
			logrus.Warnf("chain %s: %v", r.chain, r.err)
			continue
		}
		// Stamp chain + weight onto each metric.
		chainMetrics := make([]model.Metric, len(r.metrics))
		for i, m := range r.metrics {
			m.Chain = string(r.chain)
			if cc, ok := c.chains[r.chain]; ok && cc.Weight > 0 {
				m.Weight = cc.Weight
			} else {
				m.Weight = 1.0
			}
			chainMetrics[i] = m
		}
		mu.Lock()
		allMetrics = append(allMetrics, chainMetrics...)
		mu.Unlock()

		c.mutex.Lock()
		c.cachedData[r.chain] = r.metrics
		c.cacheTime[r.chain] = time.Now()
		c.mutex.Unlock()
	}

	if len(allMetrics) == 0 && len(errs) > 0 {
		for _, err := range errs {
			return nil, fmt.Errorf("multi-chain fetch failed: %w", err)
		}
	}

	logrus.Infof("Fetched metrics from %d/%d chains, total metrics: %d",
		len(enabledChains)-len(errs), len(enabledChains), len(allMetrics))
	return allMetrics, nil
}

// fetchChainData retrieves data for a specific chain, using cache when fresh.
func (c *MultiChainClient) fetchChainData(ctx context.Context, chain SupportedChain) ([]model.Metric, error) {
	c.mutex.RLock()
	if metrics, ok := c.cachedData[chain]; ok && time.Since(c.cacheTime[chain]) < c.cacheTTL {
		c.mutex.RUnlock()
		return metrics, nil
	}
	providers := c.dataProviders[chain]
	c.mutex.RUnlock()

	if len(providers) == 0 {
		dp, err := c.createDefaultProvider(chain)
		if err != nil {
			return nil, fmt.Errorf("no providers for chain %s: %w", chain, err)
		}
		providers = []provider.Provider{dp}
	}

	var (
		wg           sync.WaitGroup
		mu           sync.Mutex
		metrics      []model.Metric
		providerErrs []error
	)

	for _, p := range providers {
		wg.Add(1)
		go func(p provider.Provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			pm, err := p.Fetch(pctx)
			if err != nil {
				mu.Lock()
				providerErrs = append(providerErrs, err)
				mu.Unlock()
				return
			}
			mu.Lock()
			metrics = append(metrics, pm...)
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	if len(metrics) == 0 && len(providerErrs) > 0 {
		return nil, fmt.Errorf("all providers failed for chain %s: %v", chain, providerErrs[0])
	}
	return metrics, nil
}

// createDefaultProvider builds a sensible default provider for a chain.
func (c *MultiChainClient) createDefaultProvider(chain SupportedChain) (provider.Provider, error) {
	c.mutex.RLock()
	cc, ok := c.chains[chain]
	c.mutex.RUnlock()
	if !ok || !cc.Enabled {
		return nil, fmt.Errorf("chain %s not configured or disabled", chain)
	}

	switch chain {
	case ChainEthereum:
		return NewEigenLayerClient(c.cfg), nil
	case ChainPolygon:
		return NewPolygonProvider(cc.APIEndpoint, cc.APIKey), nil
	case ChainArbitrum:
		return NewArbitrumProvider(cc.APIEndpoint, cc.APIKey), nil
	default:
		return NewGenericChainProvider(string(chain), cc.APIEndpoint, cc.APIKey), nil
	}
}

// getEnabledChains returns the list of enabled chains. Caller must hold the
// read lock (or a write lock).
func (c *MultiChainClient) getEnabledChains() []SupportedChain {
	out := make([]SupportedChain, 0, len(c.chains))
	for chain, cc := range c.chains {
		if cc.Enabled {
			out = append(out, chain)
		}
	}
	return out
}

// GenericChainProvider is a fallback provider for any supported chain that
// exposes a generic /yield REST endpoint.
type GenericChainProvider struct {
	chain      string
	apiURL     string
	apiKey     string
	httpClient *http.Client
}

// NewGenericChainProvider creates a new provider for any chain.
func NewGenericChainProvider(chain, apiURL, apiKey string) *GenericChainProvider {
	return &GenericChainProvider{
		chain:      chain,
		apiURL:     apiURL,
		apiKey:     apiKey,
		httpClient: standardHTTPClient(newRetryClient()),
	}
}

func (p *GenericChainProvider) Name() string { return "generic-" + p.chain }

// Fetch retrieves yield data from the specified chain's /yield endpoint.
func (p *GenericChainProvider) Fetch(ctx context.Context) ([]model.Metric, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+"/yield", nil)
	if err != nil {
		return nil, fmt.Errorf("%s: create request: %w", p.chain, err)
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", p.chain, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, fmt.Errorf("%s: status %d: %s", p.chain, resp.StatusCode, string(body))
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&response); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", p.chain, err)
	}
	if len(response.Data) == 0 {
		return nil, fmt.Errorf("%s: no data returned", p.chain)
	}

	now := time.Now().Unix()
	metrics := make([]model.Metric, 0, len(response.Data))
	for _, d := range response.Data {
		ts := d.Timestamp
		if ts == 0 {
			ts = now
		}
		metrics = append(metrics, model.Metric{
			Provider:     d.Protocol,
			Chain:        p.chain,
			APY:          d.APY,
			TVL:          d.TVL,
			PointsPerETH: d.PointsPerETH,
			CollectedAt:  ts,
		})
	}
	return metrics, nil
}

// PolygonProvider fetches yield data from Polygon.
type PolygonProvider struct {
	GenericChainProvider
}

// NewPolygonProvider creates a Polygon-specific provider.
func NewPolygonProvider(apiURL, apiKey string) *PolygonProvider {
	return &PolygonProvider{GenericChainProvider: *NewGenericChainProvider("polygon", apiURL, apiKey)}
}

// ArbitrumProvider fetches yield data from Arbitrum.
type ArbitrumProvider struct {
	GenericChainProvider
}

// NewArbitrumProvider creates an Arbitrum-specific provider.
func NewArbitrumProvider(apiURL, apiKey string) *ArbitrumProvider {
	return &ArbitrumProvider{GenericChainProvider: *NewGenericChainProvider("arbitrum", apiURL, apiKey)}
}
