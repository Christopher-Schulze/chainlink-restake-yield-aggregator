package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLidoClientName(t *testing.T) {
	c := NewLidoClient(config.Config{})
	assert.Equal(t, "lido", c.Name())
}

func TestLidoClientFetchSuccess(t *testing.T) {
	// Mock Lido API server returning a realistic response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := lidoAPRResponse{}
		resp.Data.SMAApr = 3.27
		resp.Data.APRs = []struct {
			TimeUnix int64   `json:"timeUnix"`
			APR      float64 `json:"apr"`
		}{
			{TimeUnix: 1700000000, APR: 3.18},
			{TimeUnix: 1700000100, APR: 3.36},
		}
		resp.Meta.Symbol = "stETH"
		resp.Meta.Address = "0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84"
		resp.Meta.ChainID = 1
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLidoClient(config.Config{LidoAPIURL: srv.URL})
	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)

	m := metrics[0]
	assert.Equal(t, "lido", m.Provider)
	assert.Equal(t, "stETH", m.Symbol)
	assert.InDelta(t, 0.0327, m.APY, 1e-6) // 3.27% → 0.0327
	assert.Equal(t, int64(1700000100), m.CollectedAt)
	assert.Equal(t, "ethereum", m.Chain)
	assert.Greater(t, m.TVL, 0.0)
}

func TestLidoClientFetchSuccessNoAPRs(t *testing.T) {
	// When the aprs array is empty, CollectedAt should fall back to now.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := lidoAPRResponse{}
		resp.Data.SMAApr = 2.5
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLidoClient(config.Config{LidoAPIURL: srv.URL})
	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	assert.Greater(t, metrics[0].CollectedAt, int64(0))
}

func TestLidoClientFetchInvalidAPR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := lidoAPRResponse{}
		resp.Data.SMAApr = 0 // invalid
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLidoClient(config.Config{LidoAPIURL: srv.URL})
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid APR")
}

func TestLidoClientFetchNegativeAPR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := lidoAPRResponse{}
		resp.Data.SMAApr = -1.0
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLidoClient(config.Config{LidoAPIURL: srv.URL})
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid APR")
}

func TestLidoClientFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	// Test doFetchAPR directly (bypasses retry client) for deterministic error.
	c := &LidoClient{
		aprURL:     srv.URL,
		httpClient: http.DefaultClient,
	}
	_, err := c.doFetchAPR(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestLidoClientFetchDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	// Test doFetchAPR directly to bypass retry client.
	c := &LidoClient{
		aprURL:     srv.URL,
		httpClient: http.DefaultClient,
	}
	_, err := c.doFetchAPR(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestLidoClientFetchRequestError(t *testing.T) {
	// Use an invalid URL to trigger a request error.
	c := NewLidoClient(config.Config{LidoAPIURL: "http://127.0.0.1:0/v1/protocol/steth/apr/sma"})
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

func TestLidoClientFetchWithInjectableFn(t *testing.T) {
	c := NewLidoClient(config.Config{})
	c.fetchAPRFn = func(ctx context.Context) (lidoAPRResponse, error) {
		resp := lidoAPRResponse{}
		resp.Data.SMAApr = 4.2
		resp.Data.APRs = []struct {
			TimeUnix int64   `json:"timeUnix"`
			APR      float64 `json:"apr"`
		}{
			{TimeUnix: 1700000000, APR: 4.2},
		}
		return resp, nil
	}

	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	assert.InDelta(t, 0.042, metrics[0].APY, 1e-6)
}

func TestLidoClientFetchInjectableFnError(t *testing.T) {
	c := NewLidoClient(config.Config{})
	c.fetchAPRFn = func(ctx context.Context) (lidoAPRResponse, error) {
		return lidoAPRResponse{}, errors.New("injected error")
	}

	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "injected error")
}

func TestLidoClientDoFetchEmptyURL(t *testing.T) {
	c := &LidoClient{aprURL: ""}
	_, err := c.doFetchAPR(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestLidoClientDoFetchInvalidURL(t *testing.T) {
	// A URL with a space causes NewRequest to fail (invalid URL).
	c := &LidoClient{
		aprURL:     "http://exa mple.com/bad",
		httpClient: http.DefaultClient,
	}
	_, err := c.doFetchAPR(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create request")
}

func TestLidoClientDefaultURL(t *testing.T) {
	c := NewLidoClient(config.Config{})
	assert.Equal(t, "https://eth-api.lido.fi/v1/protocol/steth/apr/sma", c.aprURL)
}

func TestLidoClientFetchWithDynamicTVL(t *testing.T) {
	c := NewLidoClient(config.Config{})
	c.fetchAPRFn = func(ctx context.Context) (lidoAPRResponse, error) {
		resp := lidoAPRResponse{}
		resp.Data.SMAApr = 4.2
		resp.Data.APRs = []struct {
			TimeUnix int64   `json:"timeUnix"`
			APR      float64 `json:"apr"`
		}{
			{TimeUnix: 1700000000, APR: 4.2},
		}
		return resp, nil
	}
	c.fetchTVLFn = func(ctx context.Context) (float64, error) {
		return 9_500_000, nil // 9.5M ETH from DefiLlama
	}

	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	assert.InDelta(t, 0.042, metrics[0].APY, 1e-6)
	assert.InDelta(t, 9_500_000, metrics[0].TVL, 1e-6, "TVL should come from dynamic fetch")
	assert.Equal(t, 0.0, metrics[0].PointsPerETH, "Lido does not use points")
}

func TestLidoClientFetchTVLFallbackOnError(t *testing.T) {
	c := NewLidoClient(config.Config{})
	c.fetchAPRFn = func(ctx context.Context) (lidoAPRResponse, error) {
		resp := lidoAPRResponse{}
		resp.Data.SMAApr = 4.2
		return resp, nil
	}
	c.fetchTVLFn = func(ctx context.Context) (float64, error) {
		return 0, fmt.Errorf("defillama unavailable")
	}

	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	// Should fall back to the conservative estimate.
	assert.InDelta(t, 10_000_000, metrics[0].TVL, 1e-6, "TVL should fall back to conservative estimate")
}
