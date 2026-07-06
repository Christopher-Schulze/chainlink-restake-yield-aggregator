package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/christopher/restake-yield-ea/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider returns a fixed set of metrics, satisfying provider.Provider.
type mockProvider struct {
	name    string
	metrics []model.Metric
	err     error
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Fetch(_ context.Context) ([]model.Metric, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.metrics, nil
}

func newTestServer(t *testing.T, providers []provider.Provider, opts ServerConfig) *Server {
	t.Helper()
	if opts.Port == "" {
		opts.Port = "0"
	}
	if opts.Timeout == 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.AggregationMode == "" {
		opts.AggregationMode = "weighted"
	}
	srv, err := NewServer(opts, config.Config{}, providers)
	require.NoError(t, err)
	return srv
}

func TestEAEndpointSuccess(t *testing.T) {
	now := time.Now().Unix()
	providers := []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{
			{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		}},
		&mockProvider{name: "p2", metrics: []model.Metric{
			{Provider: "p2", APY: 0.05, TVL: 2000, PointsPerETH: 1.1, CollectedAt: now},
		}},
		&mockProvider{name: "p3", metrics: []model.Metric{
			{Provider: "p3", APY: 0.045, TVL: 1500, PointsPerETH: 1.05, CollectedAt: now},
		}},
	}

	srv := newTestServer(t, providers, ServerConfig{
		EnableCircuitBreaker: true,
		EnableValidation:     true,
		EnableMetrics:        false,
		MinProviders:         2,
		MaxAPYThreshold:      10.0,
	})

	body := `{"id":"0xabc","jobRunID":"42","data":{}}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp ChainlinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "0xabc", resp.JobRunID) // "id" is echoed as jobRunID
	assert.Equal(t, "completed", resp.Status)

	// Weighted APY: (0.04*1000 + 0.05*2000 + 0.045*1500) / 4500 = 0.04611...
	assert.InDelta(t, 0.04611, resp.Data["apy"], 1e-4)
	assert.Equal(t, "aggregated-weighted", resp.Data["provider"])

	meta, ok := resp.Data["meta"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, meta, "latencyMs")
	assert.Equal(t, "weighted", meta["aggregationMode"])
}

func TestEAEndpointAllProvidersFail(t *testing.T) {
	providers := []provider.Provider{
		&mockProvider{name: "p1", err: context.DeadlineExceeded},
		&mockProvider{name: "p2", err: context.DeadlineExceeded},
	}
	srv := newTestServer(t, providers, ServerConfig{EnableValidation: false, EnableCircuitBreaker: false})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"jobRunID":"1"}`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	var resp ChainlinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "errored", resp.Status)
	errObj, ok := resp.Error.(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, errObj["message"], "fetch failed")
}

func TestHealthAndStatus(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: true, MinProviders: 1})

	for _, path := range []string{"/health", "/status"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.routes().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, path)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// TestEASpecCompliance verifies the minimal Chainlink EA response format:
// the response must contain data.result and echo back jobRunId.
func TestEASpecCompliance(t *testing.T) {
	now := time.Now().Unix()
	providers := []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{
			{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		}},
	}
	srv := newTestServer(t, providers, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	body := `{"id":"req-1","jobRunID":"job-42","data":{"symbol":"weETH"}}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp ChainlinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	// Minimal EA spec: data.result must be present.
	result, ok := resp.Data["result"]
	require.True(t, ok, "data.result must be present")
	assert.NotNil(t, result)

	// jobRunID must be echoed back (from "id" per Chainlink EA spec).
	assert.Equal(t, "req-1", resp.JobRunID)

	// Request data fields should be echoed back (except id/jobRunId).
	assert.Equal(t, "weETH", resp.Data["symbol"])
}

// TestEAEndpointMalformedJSON verifies the error response for bad input.
func TestEAEndpointMalformedJSON(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{invalid json`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp ChainlinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "errored", resp.Status)
	errObj, ok := resp.Error.(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, errObj["message"], "invalid request body")
}

// TestEAEndpointNoValidMetrics verifies the response when validation filters everything.
func TestEAEndpointNoValidMetrics(t *testing.T) {
	providers := []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{
			{Provider: "p1", APY: -1, TVL: 0, PointsPerETH: -1, CollectedAt: time.Now().Unix()},
		}},
	}
	srv := newTestServer(t, providers, ServerConfig{
		EnableCircuitBreaker: false,
		EnableValidation:     true,
	})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"jobRunID":"1"}`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var resp ChainlinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	errObj, ok := resp.Error.(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, errObj["message"], "no valid metrics")
}

// TestAggregationModes tests all aggregation modes through the EA endpoint.
func TestAggregationModes(t *testing.T) {
	now := time.Now().Unix()
	providers := []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{
			{Provider: "p1", APY: 0.03, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
			{Provider: "p2", APY: 0.05, TVL: 2000, PointsPerETH: 1.1, CollectedAt: now},
			{Provider: "p3", APY: 0.04, TVL: 1500, PointsPerETH: 1.05, CollectedAt: now},
		}},
	}

	modes := []string{"weighted", "median", "trimmed", "consensus"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			srv := newTestServer(t, providers, ServerConfig{
				AggregationMode:      mode,
				EnableCircuitBreaker: false,
				EnableValidation:     false,
			})
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"jobRunID":"1"}`))
			rec := httptest.NewRecorder()
			srv.routes().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, "mode=%s body=%s", mode, rec.Body.String())
			var resp ChainlinkResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			assert.Equal(t, "aggregated-"+mode, resp.Data["provider"])
			// TVL should be summed (4500) for all modes.
			assert.InDelta(t, 4500.0, resp.Data["tvl"], 1e-6)
		})
	}
}

// TestTVLIsSummedNotAveraged verifies the critical fix: TVL must be summed.
func TestTVLIsSummedNotAveraged(t *testing.T) {
	now := time.Now().Unix()
	providers := []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{
			{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
			{Provider: "p2", APY: 0.05, TVL: 2000, PointsPerETH: 1.1, CollectedAt: now},
			{Provider: "p3", APY: 0.045, TVL: 3000, PointsPerETH: 1.05, CollectedAt: now},
		}},
	}
	srv := newTestServer(t, providers, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"jobRunID":"1"}`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp ChainlinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	// TVL = 1000 + 2000 + 3000 = 6000 (summed), NOT 2000 (averaged).
	assert.InDelta(t, 6000.0, resp.Data["tvl"], 1e-6)
}
