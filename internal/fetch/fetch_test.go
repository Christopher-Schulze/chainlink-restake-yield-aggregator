package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefiLlamaClientFetch exercises the full DefiLlama parsing path against a
// mock server that returns a canned ETH price and a small pool set including
// one matching LRT (weETH) and one non-matching pool.
func TestDefiLlamaClientFetch(t *testing.T) {
	priceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"coins": map[string]interface{}{
				"coingecko:ethereum": map[string]interface{}{"price": 2500.0, "symbol": "ETH"},
			},
		})
	}))
	defer priceSrv.Close()

	yieldsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": []map[string]interface{}{
				{"symbol": "weETH", "project": "EtherFi", "chain": "Ethereum", "tvlUsd": 1_000_000.0, "apy": 3.45},
				{"symbol": "USDC", "project": "Aave", "chain": "Ethereum", "tvlUsd": 500_000_000.0, "apy": 5.0},
				{"symbol": "ezETH", "project": "Renzo", "chain": "Ethereum", "tvlUsd": 500_000.0, "apy": 0.0, "apyBase": 2.0, "apyReward": 1.5},
			},
		})
	}))
	defer yieldsSrv.Close()

	c := &DefiLlamaClient{
		pricesURL:  priceSrv.URL,
		yieldsURL:  yieldsSrv.URL,
		httpClient: standardHTTPClient(newRetryClient()),
		symbols:    symbolSet(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	metrics, err := c.Fetch(ctx)
	require.NoError(t, err)
	require.Len(t, metrics, 2, "only weETH and ezETH should match")

	// weETH: TVL 1_000_000 USD / 2500 = 400 ETH, APY 3.45% -> 0.0345
	assert.Equal(t, "defillama", metrics[0].Provider)
	assert.InDelta(t, 0.0345, metrics[0].APY, 1e-9)
	assert.InDelta(t, 400.0, metrics[0].TVL, 1e-6)
	assert.Equal(t, "WEETH", metrics[0].Symbol)

	// ezETH: APY falls back to apyBase+apyReward = 3.5% -> 0.035
	assert.InDelta(t, 0.035, metrics[1].APY, 1e-9)
}

// TestDefiLlamaClientNoMatches verifies the error path when no LRT pools match.
func TestDefiLlamaClientNoMatches(t *testing.T) {
	priceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"coins": map[string]interface{}{
				"coingecko:ethereum": map[string]interface{}{"price": 2500.0},
			},
		})
	}))
	defer priceSrv.Close()

	yieldsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": []map[string]interface{}{
				{"symbol": "USDC", "project": "Aave", "chain": "Ethereum", "tvlUsd": 1.0, "apy": 5.0},
			},
		})
	}))
	defer yieldsSrv.Close()

	c := &DefiLlamaClient{
		pricesURL:  priceSrv.URL,
		yieldsURL:  yieldsSrv.URL,
		httpClient: standardHTTPClient(newRetryClient()),
		symbols:    symbolSet(),
	}

	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no matching LRT pools")
}

// TestUnconfiguredProviders verifies that providers without an API URL
// still work (they default to DefiLlama's protocol endpoint). These tests
// just verify the clients can be constructed with empty config.
func TestUnconfiguredProviders(t *testing.T) {
	// With the new DefiLlama-backed implementation, providers are always
	// active (keyless). Empty config means "use default DefiLlama endpoint".
	_ = NewEigenLayerClient(config.Config{})
	_ = NewKarakClient(config.Config{})
	_ = NewSymbioticClient(config.Config{})
}

func symbolSet() map[string]struct{} {
	set := make(map[string]struct{}, len(defaultLRTSymbols))
	for _, s := range defaultLRTSymbols {
		set[strings.ToUpper(s)] = struct{}{}
	}
	return set
}

// TestRestClientRejectsOversizedResponse verifies that the response body size
// limit is enforced: a provider returning a payload larger than maxResponseBytes
// must fail with a decode error rather than consuming unbounded memory.
func TestRestClientRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write a valid JSON prefix then a huge padding inside a string value,
		// exceeding maxResponseBytes (16 MiB). Use streaming to avoid
		// allocating 16 MiB in the test itself.
		_, _ = w.Write([]byte(`{"name":"eigenlayer","chain":"Ethereum","tvl":[{"date":1,"totalLiquidityUSD":1,"pad":"`))
		chunk := make([]byte, 4096)
		for i := range chunk {
			chunk[i] = 'a'
		}
		// Write ~17 MiB of padding.
		for i := 0; i < (17<<20)/len(chunk); i++ {
			_, _ = w.Write(chunk)
		}
		_, _ = w.Write([]byte(`"}]}`))
	}))
	defer srv.Close()

	c := NewEigenLayerClient(config.Config{EigenURL: srv.URL})
	c.fetchETHPriceFn = func(_ context.Context) (float64, error) { return 2500, nil }
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	// The error should be a decode error (truncated by the limit reader).
	assert.Contains(t, err.Error(), "decode")
}

// TestRestClientHandlesLargeButValidResponse verifies that a response just
// under the limit is accepted, so the limit doesn't break legitimate use.
func TestRestClientHandlesLargeButValidResponse(t *testing.T) {
	// Build a protocol TVL response with many TVL entries but well under 16 MiB.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"eigenlayer","chain":"Ethereum","tvl":[`))
		for i := 0; i < 1000; i++ {
			if i > 0 {
				_, _ = w.Write([]byte(","))
			}
			_, _ = w.Write([]byte(`{"date":1700000000,"totalLiquidityUSD":4000000000}`))
		}
		_, _ = w.Write([]byte(`]}`))
	}))
	defer srv.Close()

	c := NewEigenLayerClient(config.Config{EigenURL: srv.URL})
	c.fetchETHPriceFn = func(_ context.Context) (float64, error) { return 2500, nil }
	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	// The new protocol client returns a single aggregated metric (last TVL entry).
	assert.Len(t, metrics, 1)
}
