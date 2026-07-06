//go:build integration

// Integration tests that hit the real DefiLlama API. These are excluded from
// the normal test suite (go test ./...) and must be explicitly enabled:
//
//	go test -tags=integration -run TestIntegration ./internal/fetch/...
//
// They require network access and may be rate-limited or fail if DefiLlama
// changes its API. They are intentionally lenient: we only assert structural
// correctness, not specific yield values (which change constantly).
package fetch

import (
	"context"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegrationDefiLlamaFetch(t *testing.T) {
	client := NewDefiLlamaClient(config.Load())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	metrics, err := client.Fetch(ctx)
	require.NoError(t, err, "DefiLlama fetch should succeed with network access")

	assert.NotEmpty(t, metrics, "should return at least one LRT metric")

	for _, m := range metrics {
		assert.Equal(t, "defillama", m.Provider)
		assert.NotEmpty(t, m.Symbol, "metric should have a symbol")
		assert.Greater(t, m.TVL, 0.0, "TVL should be positive")
		assert.GreaterOrEqual(t, m.APY, 0.0, "APY should be non-negative")
		assert.Less(t, m.APY, 10.0, "APY should be < 1000% (sanity)")
		assert.Greater(t, m.CollectedAt, int64(0), "CollectedAt should be set")
	}

	t.Logf("DefiLlama returned %d LRT metrics", len(metrics))
	for i, m := range metrics {
		if i >= 5 {
			break
		}
		t.Logf("  %s: APY=%.4f TVL=%.0f ETH", m.Symbol, m.APY, m.TVL)
	}
}

func TestIntegrationDefiLlamaETHPrice(t *testing.T) {
	client := NewDefiLlamaClient(config.Load())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	price, err := client.fetchETHPrice(ctx)
	require.NoError(t, err)

	assert.Greater(t, price, 100.0, "ETH price should be > $100")
	assert.Less(t, price, 100000.0, "ETH price should be < $100,000")
	t.Logf("ETH spot price: $%.2f", price)
}

func TestIntegrationLidoFetch(t *testing.T) {
	client := NewLidoClient(config.Load())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	metrics, err := client.Fetch(ctx)
	require.NoError(t, err, "Lido fetch should succeed with network access")

	require.Len(t, metrics, 1, "Lido should return exactly one stETH metric")
	m := metrics[0]

	assert.Equal(t, "lido", m.Provider)
	assert.Equal(t, "stETH", m.Symbol)
	assert.Equal(t, "ethereum", m.Chain)
	assert.Greater(t, m.APY, 0.0, "stETH APR should be positive")
	assert.Less(t, m.APY, 1.0, "stETH APR should be < 100% (sanity)")
	assert.Greater(t, m.TVL, 0.0, "TVL should be positive")
	assert.Greater(t, m.CollectedAt, int64(0), "CollectedAt should be set")

	t.Logf("Lido stETH: APY=%.4f%% TVL=%.0f ETH", m.APY*100, m.TVL)
}
