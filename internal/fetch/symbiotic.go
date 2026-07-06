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

// SymbioticClient fetches real TVL data for the Symbiotic restaking
// protocol from DefiLlama's protocols API
// (https://api.llama.fi/protocol/symbiotic).
//
// Symbiotic is a shared security protocol — it does not have a simple
// "yield" in the traditional sense. Network rewards vary per network and
// operator, so APY is reported as 0 (not available). The TVL represents
// the total value locked in Symbiotic vaults, converted from USD to ETH
// using the current ETH price.
//
// The client is always active (keyless, no API key needed). If a custom
// API URL is supplied via SYMBIOTIC_API, it overrides the default DefiLlama
// endpoint.
type SymbioticClient struct {
	apiURL     string
	httpClient *http.Client
	fetchETHPriceFn  func(ctx context.Context) (float64, error)
	fetchProtocolFn  func(ctx context.Context) (protocolTVLResponse, error)
}

const defaultSymbioticURL = "https://api.llama.fi/protocol/symbiotic"

func NewSymbioticClient(cfg config.Config) *SymbioticClient {
	url := cfg.SymbioticURL
	if url == "" {
		url = defaultSymbioticURL
	}
	return &SymbioticClient{
		apiURL:     url,
		httpClient: standardHTTPClient(newRetryClient()),
	}
}

func (c *SymbioticClient) Name() string { return "symbiotic" }

// Fetch retrieves the current Symbiotic restaking TVL from DefiLlama and
// returns it as a Metric with APY=0 and TVL converted from USD to ETH.
func (c *SymbioticClient) Fetch(ctx context.Context) ([]model.Metric, error) {
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
		return nil, fmt.Errorf("symbiotic: eth price: %w", pr.err)
	}
	if rr.err != nil {
		return nil, fmt.Errorf("symbiotic: protocol tvl: %w", rr.err)
	}
	if pr.price <= 0 {
		return nil, fmt.Errorf("symbiotic: invalid eth price %f", pr.price)
	}
	if len(rr.resp.TVL) == 0 {
		return nil, fmt.Errorf("symbiotic: no tvl data from DefiLlama")
	}

	tvlUSD := rr.resp.TVL[len(rr.resp.TVL)-1].TotalLiquidityUSD
	tvlETH := tvlUSD / pr.price

	logrus.Debugf("symbiotic: TVL %.0f USD = %.0f ETH (ETH price %.2f)", tvlUSD, tvlETH, pr.price)

	return []model.Metric{{
		Provider:     "symbiotic",
		APY:          0,
		TVL:          tvlETH,
		PointsPerETH: 0,
		CollectedAt:  time.Now().Unix(),
		Chain:        "ethereum",
	}}, nil
}

func (c *SymbioticClient) fetchETHPrice(ctx context.Context) (float64, error) {
	return fetchETHPriceFromDefiLlama(ctx, c.httpClient)
}

func (c *SymbioticClient) fetchProtocol(ctx context.Context) (protocolTVLResponse, error) {
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
