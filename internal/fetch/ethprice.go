package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ethPriceAPIURL is the DefiLlama coins API endpoint for the current ETH
// spot price in USD. It is shared by all protocol-specific clients that
// need to convert USD-denominated TVL into ETH.
const ethPriceAPIURL = "https://coins.llama.fi/prices/current/" + ethPriceCoinKey

// fetchETHPriceFromDefiLlama retrieves the current ETH spot price in USD
// from DefiLlama's coins API. It is shared by DefiLlamaClient, EigenLayerClient,
// KarakClient, and SymbioticClient so they all convert USD TVL to ETH using
// the same price source.
func fetchETHPriceFromDefiLlama(ctx context.Context, hc *http.Client) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ethPriceAPIURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create eth price request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("eth price request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return 0, fmt.Errorf("eth price: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out ethPriceResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return 0, fmt.Errorf("eth price decode: %w", err)
	}

	coin, ok := out.Coins[ethPriceCoinKey]
	if !ok || coin.Price <= 0 {
		return 0, fmt.Errorf("eth price not found in response (keys: %d)", len(out.Coins))
	}
	return coin.Price, nil
}
