package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// DefiLlamaClient is a real, keyless provider backed by DefiLlama's public API
// (https://defillama.com). It fetches liquid-restaking-token (LRT) pool yields
// from yields.llama.fi and the current ETH price from coins.llama.fi, then
// normalises TVL into ETH so the rest of the pipeline can treat every provider
// uniformly.
//
// This is the canonical "always works in dev" provider: no API key, no auth,
// generous rate limits. The other providers (EigenLayer/Karak/Symbiotic) target
// protocol-specific endpoints that may require keys or are not yet public; this
// one guarantees the adapter returns real data out of the box.
type DefiLlamaClient struct {
	pricesURL  string // e.g. https://coins.llama.fi
	yieldsURL  string // e.g. https://yields.llama.fi
	httpClient *http.Client
	symbols    map[string]struct{} // LRT symbols to include
	// fetchETHPriceFn and fetchYieldsFn are injectable for testing. When
	// nil, the real methods are used. This allows tests to simulate
	// edge cases (e.g. returning a zero price with no error) that are
	// impossible to trigger through the real HTTP path.
	fetchETHPriceFn func(ctx context.Context) (float64, error)
	fetchYieldsFn   func(ctx context.Context) (yieldsResponse, error)
}

// defaultLRTSymbols are the major liquid (re)staking tokens we aggregate.
// Sourced from DefiLlama's yields.llama.fi pool data.
var defaultLRTSymbols = []string{
	"weETH", "eETH", "ezETH", "rsETH", "rswETH", "ETHx",
	"pufETH", "rstETH", "jsETH", "unshETH", "wBETH", "cbETH",
}

// ethPriceCoinKey is the DefiLlama coins API key for ETH spot price.
const ethPriceCoinKey = "coingecko:ethereum"

// NewDefiLlamaClient builds a DefiLlama client from the supplied config.
func NewDefiLlamaClient(cfg config.Config) *DefiLlamaClient {
	symSet := make(map[string]struct{}, len(defaultLRTSymbols))
	for _, s := range defaultLRTSymbols {
		symSet[strings.ToUpper(s)] = struct{}{}
	}
	return &DefiLlamaClient{
		pricesURL:  cfg.DefiLlamaPrices,
		yieldsURL:  cfg.DefiLlamaYields,
		httpClient: standardHTTPClient(newRetryClient()),
		symbols:    symSet,
	}
}

func (c *DefiLlamaClient) Name() string { return "defillama" }

type ethPriceResponse struct {
	Coins map[string]struct {
		Price  float64 `json:"price"`
		Symbol string  `json:"symbol"`
	} `json:"coins"`
}

type yieldsResponse struct {
	Status string `json:"status"`
	Data   []struct {
		Symbol    string  `json:"symbol"`
		Project   string  `json:"project"`
		Chain     string  `json:"chain"`
		TVLUsd    float64 `json:"tvlUsd"`
		APY       float64 `json:"apy"`
		APYBase   float64 `json:"apyBase"`
		APYReward float64 `json:"apyReward"`
	} `json:"data"`
}

// Fetch retrieves LRT yields and the ETH price concurrently, then emits one
// metric per matching LRT pool with TVL normalised to ETH.
func (c *DefiLlamaClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	type priceResult struct {
		price float64
		err   error
	}
	type yieldsResult struct {
		pools yieldsResponse
		err   error
	}

	priceCh := make(chan priceResult, 1)
	yieldsCh := make(chan yieldsResult, 1)

	fetchPrice := c.fetchETHPriceFn
	if fetchPrice == nil {
		fetchPrice = c.fetchETHPrice
	}
	fetchYields := c.fetchYieldsFn
	if fetchYields == nil {
		fetchYields = c.fetchYields
	}

	go func() {
		p, err := fetchPrice(ctx)
		priceCh <- priceResult{p, err}
	}()
	go func() {
		pools, err := fetchYields(ctx)
		yieldsCh <- yieldsResult{pools, err}
	}()

	pr := <-priceCh
	yr := <-yieldsCh
	if pr.err != nil {
		return nil, fmt.Errorf("defillama: eth price: %w", pr.err)
	}
	if yr.err != nil {
		return nil, fmt.Errorf("defillama: yields: %w", yr.err)
	}
	if pr.price <= 0 {
		return nil, fmt.Errorf("defillama: invalid eth price %f", pr.price)
	}

	now := time.Now().Unix()
	metrics := make([]model.Metric, 0, len(yr.pools.Data))
	for _, p := range yr.pools.Data {
		sym := strings.ToUpper(strings.TrimSpace(p.Symbol))
		if _, ok := c.symbols[sym]; !ok {
			continue
		}
		// DefiLlama sometimes reports APY=0 but populates apyBase + apyReward.
		apy := p.APY
		if apy == 0 {
			apy = p.APYBase + p.APYReward
		}
		if apy < 0 || p.TVLUsd <= 0 {
			continue
		}
		metrics = append(metrics, model.Metric{
			Provider:     "defillama",
			Symbol:       sym,
			Chain:        p.Chain,
			APY:          apy / 100.0, // DefiLlama returns APY as percentage; we store decimal
			TVL:          p.TVLUsd / pr.price,
			PointsPerETH: 0, // DefiLlama does not expose points data; 0 = not available
			CollectedAt:  now,
		})
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("defillama: no matching LRT pools found")
	}
	logrus.Debugf("defillama: returning %d LRT metrics", len(metrics))
	return metrics, nil
}

// fetchETHPrice retrieves the current ETH spot price in USD from
// coins.llama.fi/prices/current/coingecko:ethereum.
func (c *DefiLlamaClient) fetchETHPrice(ctx context.Context) (float64, error) {
	url := strings.TrimRight(c.pricesURL, "/") + "/prices/current/" + ethPriceCoinKey
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var out ethPriceResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return 0, err
	}
	// Look up the specific coin key rather than iterating, to avoid returning
	// the wrong price if the API adds additional coins to the response.
	coin, ok := out.Coins[ethPriceCoinKey]
	if !ok || coin.Price <= 0 {
		return 0, fmt.Errorf("eth price not found in response (keys: %d)", len(out.Coins))
	}
	return coin.Price, nil
}

// fetchYields retrieves the full pool universe from yields.llama.fi/pools.
func (c *DefiLlamaClient) fetchYields(ctx context.Context) (yieldsResponse, error) {
	url := strings.TrimRight(c.yieldsURL, "/") + "/pools"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return yieldsResponse{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return yieldsResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return yieldsResponse{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var out yieldsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return yieldsResponse{}, err
	}
	if out.Status != "success" {
		return yieldsResponse{}, fmt.Errorf("api status: %s", out.Status)
	}
	return out, nil
}
