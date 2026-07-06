package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/envx"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// LidoClient fetches the real stETH staking APR from Lido's official public
// API (https://eth-api.lido.fi). Unlike the generic restClient stubs used for
// EigenLayer/Karak/Symbiotic, this client parses Lido's actual response format
// and returns a real, protocol-specific metric.
//
// The Lido API returns the 7-day simple moving average of stETH APR:
//
//	GET https://eth-api.lido.fi/v1/protocol/steth/apr/sma
//	{
//	  "data": {
//	    "aprs": [{"timeUnix": ..., "apr": 3.18}, ...],
//	    "smaApr": 3.27
//	  },
//	  "meta": {
//	    "symbol": "stETH",
//	    "address": "0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84",
//	    "chainId": 1
//	  }
//	}
//
// The APR is returned as a percentage (e.g. 3.27 = 3.27%). We convert it to
// decimal form (0.0327) for internal consistency with other providers.
//
// TVL is fetched from DefiLlama's coins API (ETH market cap / ETH price) as an
// approximation of total staked ETH value. For a more precise TVL, the Lido
// subgraph could be queried, but the DefiLlama approximation is sufficient for
// aggregation weighting.
type LidoClient struct {
	aprURL     string // e.g. https://eth-api.lido.fi/v1/protocol/steth/apr/sma
	tvlURL     string // e.g. https://coins.llama.fi/prices/current/coingecko:staked-ether
	httpClient *http.Client
	// fallbackTVL is the conservative TVL estimate used when the
	// DefiLlama fetch fails. Configurable via LIDO_FALLBACK_TVL_ETH
	// (default: 10_000_000 — roughly Lido's staked ETH as of 2025).
	// This is NOT a hardcoded "truth" — it is a safety net so that
	// the adapter can still return a TVL-weighted metric when the
	// upstream API is temporarily unavailable.
	fallbackTVL float64
	// fetchAPRFn is injectable for testing. When nil, the real HTTP method is used.
	fetchAPRFn func(ctx context.Context) (lidoAPRResponse, error)
	// fetchTVLFn is injectable for testing. When nil, the real HTTP method is used.
	// Returns TVL in ETH terms.
	fetchTVLFn func(ctx context.Context) (float64, error)
}

// lidoAPRResponse models the JSON response from Lido's APR API.
type lidoAPRResponse struct {
	Data struct {
		SMAApr float64 `json:"smaApr"`
		APRs   []struct {
			TimeUnix int64   `json:"timeUnix"`
			APR      float64 `json:"apr"`
		} `json:"aprs"`
	} `json:"data"`
	Meta struct {
		Symbol  string `json:"symbol"`
		Address string `json:"address"`
		ChainID int    `json:"chainId"`
	} `json:"meta"`
}

// NewLidoClient builds a Lido client from the supplied config.
// The fallback TVL is configurable via the LIDO_FALLBACK_TVL_ETH env var
// (default: 10_000_000, roughly Lido's staked ETH as of 2025).
func NewLidoClient(cfg config.Config) *LidoClient {
	url := cfg.LidoAPIURL
	if url == "" {
		url = "https://eth-api.lido.fi/v1/protocol/steth/apr/sma"
	}
	tvlURL := "https://coins.llama.fi/prices/current/coingecko:staked-ether"
	return &LidoClient{
		aprURL:      url,
		tvlURL:      tvlURL,
		httpClient:  standardHTTPClient(newRetryClient()),
		fallbackTVL: envx.Float64("LIDO_FALLBACK_TVL_ETH", 10_000_000.0),
	}
}

// Name returns the provider identifier.
func (c *LidoClient) Name() string { return "lido" }

// Fetch retrieves the current stETH staking APR from Lido's API and returns it
// as a Metric. The APR is converted from percentage to decimal form.
func (c *LidoClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	var resp lidoAPRResponse
	var err error

	if c.fetchAPRFn != nil {
		resp, err = c.fetchAPRFn(ctx)
	} else {
		resp, err = c.doFetchAPR(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("lido: %w", err)
	}

	if resp.Data.SMAApr <= 0 {
		return nil, fmt.Errorf("lido: invalid APR %.4f (expected > 0)", resp.Data.SMAApr)
	}

	// Lido returns APR as a percentage (e.g. 3.27 = 3.27%). Convert to decimal.
	apy := resp.Data.SMAApr / 100.0

	// Use the latest individual APR timestamp as CollectedAt, falling back to now.
	var collectedAt int64
	if len(resp.Data.APRs) > 0 {
		collectedAt = resp.Data.APRs[len(resp.Data.APRs)-1].TimeUnix
	}
	if collectedAt == 0 {
		collectedAt = time.Now().Unix()
	}

	// TVL: fetch from DefiLlama (stETH price + supply) when available.
	// Falls back to c.fallbackTVL (configurable via LIDO_FALLBACK_TVL_ETH,
	// default 10M ETH) if the fetch fails.
	tvlETH := c.fallbackTVL
	if c.fetchTVLFn != nil {
		if fetched, err := c.fetchTVLFn(ctx); err == nil && fetched > 0 {
			tvlETH = fetched
		} else if err != nil {
			logrus.Warnf("lido: TVL fetch failed, using fallback: %v", err)
		}
	} else if c.tvlURL != "" {
		if fetched, err := c.doFetchTVL(ctx); err == nil && fetched > 0 {
			tvlETH = fetched
		} else if err != nil {
			logrus.Warnf("lido: TVL fetch failed, using fallback: %v", err)
		}
	}

	metric := model.Metric{
		Provider:     "lido",
		Symbol:       "stETH",
		APY:          apy,
		TVL:          tvlETH,
		PointsPerETH: 0, // Lido does not use a points system; 0 = not available
		CollectedAt:  collectedAt,
		Chain:        "ethereum",
	}

	logrus.Debugf("lido: stETH APR (7d SMA) = %.4f%% (decimal: %.6f)", resp.Data.SMAApr, apy)
	return []model.Metric{metric}, nil
}

// doFetchAPR makes the real HTTP request to Lido's API.
func (c *LidoClient) doFetchAPR(ctx context.Context) (lidoAPRResponse, error) {
	if c.aprURL == "" {
		return lidoAPRResponse{}, fmt.Errorf("API URL not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.aprURL, nil)
	if err != nil {
		return lidoAPRResponse{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	logrus.Debugf("lido: GET %s", c.aprURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return lidoAPRResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return lidoAPRResponse{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	// Cap response body before decoding.
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	var result lidoAPRResponse
	if err := dec.Decode(&result); err != nil {
		return lidoAPRResponse{}, fmt.Errorf("decode: %w", err)
	}

	return result, nil
}

// lidoTVLResponse models the DefiLlama protocols API response for Lido.
// We only need the TVL field (in USD).
type lidoTVLResponse struct {
	TVL float64 `json:"tvl"`
}

// doFetchTVL fetches the current Lido TVL from DefiLlama's protocols API
// and converts it to ETH terms using the current ETH price from the coins API.
// Returns TVL in ETH. If either fetch fails, returns an error so the caller
// can fall back to the conservative estimate.
func (c *LidoClient) doFetchTVL(ctx context.Context) (float64, error) {
	if c.tvlURL == "" {
		return 0, fmt.Errorf("TVL URL not configured")
	}

	// Step 1: fetch stETH price from DefiLlama coins API.
	// The tvlURL is the coins API URL for stETH, which returns the price.
	stethReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.tvlURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create stETH price request: %w", err)
	}
	stethReq.Header.Set("Accept", "application/json")

	stethResp, err := c.httpClient.Do(stethReq)
	if err != nil {
		return 0, fmt.Errorf("stETH price request failed: %w", err)
	}
	defer stethResp.Body.Close()

	type coinsResponse struct {
		Coins map[string]struct {
			Price  float64 `json:"price"`
			Symbol string  `json:"symbol"`
		} `json:"coins"`
	}

	var stethCoins coinsResponse
	if stethResp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("stETH price status %d", stethResp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(stethResp.Body, maxResponseBytes))
	if err := dec.Decode(&stethCoins); err != nil {
		return 0, fmt.Errorf("decode stETH price: %w", err)
	}

	var stethPrice float64
	for _, coin := range stethCoins.Coins {
		if coin.Price > 0 {
			stethPrice = coin.Price
			break
		}
	}
	if stethPrice <= 0 {
		return 0, fmt.Errorf("stETH price not found in response")
	}

	// Step 2: fetch Lido protocol TVL (in USD) from DefiLlama protocols API.
	protocolsURL := "https://api.llama.fi/protocol/lido"
	tvlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, protocolsURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create TVL request: %w", err)
	}
	tvlReq.Header.Set("Accept", "application/json")

	tvlResp, err := c.httpClient.Do(tvlReq)
	if err != nil {
		return 0, fmt.Errorf("TVL request failed: %w", err)
	}
	defer tvlResp.Body.Close()

	if tvlResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(tvlResp.Body, maxErrorBodyBytes))
		return 0, fmt.Errorf("TVL status %d: %s", tvlResp.StatusCode, string(body))
	}

	var proto lidoTVLResponse
	dec2 := json.NewDecoder(io.LimitReader(tvlResp.Body, maxResponseBytes))
	if err := dec2.Decode(&proto); err != nil {
		return 0, fmt.Errorf("decode TVL: %w", err)
	}

	if proto.TVL <= 0 {
		return 0, fmt.Errorf("invalid TVL %.2f", proto.TVL)
	}

	// Convert USD TVL to ETH terms using stETH price (≈ ETH price).
	tvlETH := proto.TVL / stethPrice
	logrus.Debugf("lido: TVL = $%.0f / $%.2f = %.0f ETH", proto.TVL, stethPrice, tvlETH)
	return tvlETH, nil
}
