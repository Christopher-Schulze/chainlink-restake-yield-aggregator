package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// EigenLayerClient fetches real TVL data for the EigenLayer (EigenCloud)
// restaking protocol from DefiLlama's protocols API
// (https://api.llama.fi/protocol/eigenlayer).
//
// EigenLayer is a restaking protocol — it does not have a simple "yield" in
// the traditional sense. AVS rewards vary per operator and service, so APY
// is reported as 0 (not available). The TVL represents the total value
// restaked into EigenLayer strategies (LSTs + native ETH), converted from
// USD to ETH using the current ETH price.
//
// The client is always active (keyless, no API key needed). If a custom
// API URL is supplied via EIGENLAYER_API, it overrides the default DefiLlama
// endpoint — useful for testing or pointing to a cached proxy.
type EigenLayerClient struct {
	protocolSlug string // DefiLlama protocol slug, e.g. "eigenlayer"
	apiURL       string // overrides the default DefiLlama endpoint if set
	httpClient   *http.Client
	// fetchETHPriceFn and fetchProtocolFn are injectable for testing.
	fetchETHPriceFn  func(ctx context.Context) (float64, error)
	fetchProtocolFn  func(ctx context.Context) (protocolTVLResponse, error)
}

// protocolTVLResponse is the subset of DefiLlama's /protocol/<slug> response
// that we use: the TVL history array (we take the last entry for current TVL).
type protocolTVLResponse struct {
	Name     string `json:"name"`
	Chain    string `json:"chain"`
	TVL      []struct {
		Date              int64   `json:"date"`
		TotalLiquidityUSD float64 `json:"totalLiquidityUSD"`
	} `json:"tvl"`
}

// defaultEigenLayerURL is the DefiLlama protocols API endpoint for EigenLayer.
const defaultEigenLayerURL = "https://api.llama.fi/protocol/eigenlayer"

// NewEigenLayerClient builds an EigenLayer client from the supplied config.
// If cfg.EigenURL is empty, the default DefiLlama endpoint is used.
func NewEigenLayerClient(cfg config.Config) *EigenLayerClient {
	url := cfg.EigenURL
	if url == "" {
		url = defaultEigenLayerURL
	}
	return &EigenLayerClient{
		protocolSlug: "eigenlayer",
		apiURL:       url,
		httpClient:   standardHTTPClient(newRetryClient()),
	}
}

func (c *EigenLayerClient) Name() string { return "eigenlayer" }

// Fetch retrieves the current EigenLayer restaking TVL from DefiLlama and
// returns it as a Metric with APY=0 (restaking protocols don't have a
// simple yield) and TVL converted from USD to ETH.
func (c *EigenLayerClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	fetchPrice := c.fetchETHPriceFn
	if fetchPrice == nil {
		fetchPrice = c.fetchETHPrice
	}
	fetchProto := c.fetchProtocolFn
	if fetchProto == nil {
		fetchProto = c.fetchProtocol
	}

	type priceResult struct {
		price float64
		err   error
	}
	type protoResult struct {
		resp protocolTVLResponse
		err  error
	}

	priceCh := make(chan priceResult, 1)
	protoCh := make(chan protoResult, 1)

	go func() {
		p, err := fetchPrice(ctx)
		priceCh <- priceResult{p, err}
	}()
	go func() {
		r, err := fetchProto(ctx)
		protoCh <- protoResult{r, err}
	}()

	pr := <-priceCh
	rr := <-protoCh
	if pr.err != nil {
		return nil, fmt.Errorf("eigenlayer: eth price: %w", pr.err)
	}
	if rr.err != nil {
		return nil, fmt.Errorf("eigenlayer: protocol tvl: %w", rr.err)
	}
	if pr.price <= 0 {
		return nil, fmt.Errorf("eigenlayer: invalid eth price %f", pr.price)
	}
	if len(rr.resp.TVL) == 0 {
		return nil, fmt.Errorf("eigenlayer: no tvl data from DefiLlama")
	}

	tvlUSD := rr.resp.TVL[len(rr.resp.TVL)-1].TotalLiquidityUSD
	tvlETH := tvlUSD / pr.price

	logrus.Debugf("eigenlayer: TVL %.0f USD = %.0f ETH (ETH price %.2f)", tvlUSD, tvlETH, pr.price)

	return []model.Metric{{
		Provider:     "eigenlayer",
		APY:          0, // Restaking protocols don't have a simple yield — AVS rewards vary
		TVL:          tvlETH,
		PointsPerETH: 0, // Not available for EigenLayer
		CollectedAt:  time.Now().Unix(),
		Chain:        "ethereum",
	}}, nil
}

func (c *EigenLayerClient) fetchETHPrice(ctx context.Context) (float64, error) {
	return fetchETHPriceFromDefiLlama(ctx, c.httpClient)
}

func (c *EigenLayerClient) fetchProtocol(ctx context.Context) (protocolTVLResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return protocolTVLResponse{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return protocolTVLResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return protocolTVLResponse{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result protocolTVLResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	if err := dec.Decode(&result); err != nil {
		return protocolTVLResponse{}, fmt.Errorf("decode: %w", err)
	}
	return result, nil
}
