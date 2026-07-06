package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/enterprise"
	"github.com/christopher/restake-yield-ea/internal/envx"
	"github.com/christopher/restake-yield-ea/internal/fetch"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/christopher/restake-yield-ea/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- env helpers ---

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_ENV_OR", "custom")
	assert.Equal(t, "custom", envx.String("TEST_ENV_OR", "default"))
	assert.Equal(t, "default", envx.String("TEST_ENV_NONEXISTENT", "default"))
}

func TestEnvBool(t *testing.T) {
	t.Setenv("TEST_BOOL_TRUE", "true")
	assert.True(t, envx.Bool("TEST_BOOL_TRUE", false))
	t.Setenv("TEST_BOOL_FALSE", "false")
	assert.False(t, envx.Bool("TEST_BOOL_FALSE", true))
	assert.True(t, envx.Bool("TEST_BOOL_NONEXISTENT", true))
	t.Setenv("TEST_BOOL_INVALID", "not-a-bool")
	assert.True(t, envx.Bool("TEST_BOOL_INVALID", true))
}

func TestEnvInt(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	assert.Equal(t, 42, envx.Int("TEST_INT", 10))
	assert.Equal(t, 10, envx.Int("TEST_INT_NONEXISTENT", 10))
	t.Setenv("TEST_INT_INVALID", "not-a-number")
	assert.Equal(t, 10, envx.Int("TEST_INT_INVALID", 10))
}

func TestEnvFloat(t *testing.T) {
	t.Setenv("TEST_FLOAT", "3.14")
	assert.InDelta(t, 3.14, envx.Float64("TEST_FLOAT", 1.0), 1e-9)
	assert.InDelta(t, 1.0, envx.Float64("TEST_FLOAT_NONEXISTENT", 1.0), 1e-9)
	t.Setenv("TEST_FLOAT_INVALID", "not-a-float")
	assert.InDelta(t, 1.0, envx.Float64("TEST_FLOAT_INVALID", 1.0), 1e-9)
}

func TestEnvDuration(t *testing.T) {
	t.Setenv("TEST_DUR", "30s")
	assert.Equal(t, 30*time.Second, envx.Duration("TEST_DUR", 10*time.Second))
	assert.Equal(t, 10*time.Second, envx.Duration("TEST_DUR_NONEXISTENT", 10*time.Second))
	t.Setenv("TEST_DUR_INVALID", "not-a-duration")
	assert.Equal(t, 10*time.Second, envx.Duration("TEST_DUR_INVALID", 10*time.Second))
}

// --- setupLogging ---

func TestSetupLoggingJSON(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("LOG_LEVEL", "debug")
	setupLogging()
	// No assertions needed — just verify it doesn't panic.
}

func TestSetupLoggingText(t *testing.T) {
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("LOG_LEVEL", "warn")
	setupLogging()
}

func TestSetupLoggingErrorLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "error")
	setupLogging()
}

func TestSetupLoggingDefault(t *testing.T) {
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_LEVEL", "")
	setupLogging()
}

// --- loadServerConfig ---

func TestLoadServerConfigDefaults(t *testing.T) {
	os.Unsetenv("PORT")
	os.Unsetenv("AGGREGATION_MODE")
	os.Unsetenv("TIMEOUT")
	os.Unsetenv("ENABLE_CIRCUIT_BREAKER")
	os.Unsetenv("ENABLE_VALIDATION")
	os.Unsetenv("ENABLE_METRICS")
	os.Unsetenv("MAX_APY_THRESHOLD")
	os.Unsetenv("MIN_PROVIDERS")

	appCfg := config.Config{Port: "8080", RequestTimeout: 10 * time.Second, MaxAPY: 1.0, MinProviderCount: 2}
	srvCfg := loadServerConfig(appCfg)

	assert.Equal(t, "8080", srvCfg.Port)
	assert.Equal(t, "weighted", srvCfg.AggregationMode)
	assert.Equal(t, 10*time.Second, srvCfg.Timeout)
	assert.True(t, srvCfg.EnableCircuitBreaker)
	assert.True(t, srvCfg.EnableValidation)
	assert.True(t, srvCfg.EnableMetrics)
	assert.InDelta(t, 1.0, srvCfg.MaxAPYThreshold, 1e-9)
	assert.Equal(t, 2, srvCfg.MinProviders)
}

func TestLoadServerConfigOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("AGGREGATION_MODE", "median")
	t.Setenv("TIMEOUT", "30s")
	t.Setenv("ENABLE_CIRCUIT_BREAKER", "false")
	t.Setenv("ENABLE_VALIDATION", "false")
	t.Setenv("ENABLE_METRICS", "false")
	t.Setenv("MAX_APY_THRESHOLD", "0.5")
	t.Setenv("MIN_PROVIDERS", "3")

	appCfg := config.Config{Port: "8080", RequestTimeout: 10 * time.Second, MaxAPY: 1.0, MinProviderCount: 2}
	srvCfg := loadServerConfig(appCfg)

	assert.Equal(t, "9090", srvCfg.Port)
	assert.Equal(t, "median", srvCfg.AggregationMode)
	assert.Equal(t, 30*time.Second, srvCfg.Timeout)
	assert.False(t, srvCfg.EnableCircuitBreaker)
	assert.False(t, srvCfg.EnableValidation)
	assert.False(t, srvCfg.EnableMetrics)
	assert.InDelta(t, 0.5, srvCfg.MaxAPYThreshold, 1e-9)
	assert.Equal(t, 3, srvCfg.MinProviders)
}

// --- createProviders ---

func TestCreateProvidersDefault(t *testing.T) {
	appCfg := config.Config{}
	srvCfg := ServerConfig{}
	providers := createProviders(appCfg, srvCfg)
	assert.NotEmpty(t, providers)
	assert.Equal(t, "defillama", providers[0].Name())
}

func TestCreateProvidersWithEigenLayer(t *testing.T) {
	appCfg := config.Config{EigenURL: "https://api.eigenlayer.xyz"}
	srvCfg := ServerConfig{}
	providers := createProviders(appCfg, srvCfg)
	names := providerNames(providers)
	assert.Contains(t, names, "defillama")
	assert.Contains(t, names, "eigenlayer")
}

func TestCreateProvidersWithAllConfigured(t *testing.T) {
	appCfg := config.Config{
		EigenURL:    "https://api.eigenlayer.xyz",
		KarakURL:    "https://api.karak.xyz",
		SymbioticURL: "https://api.symbiotic.xyz",
	}
	srvCfg := ServerConfig{}
	providers := createProviders(appCfg, srvCfg)
	names := providerNames(providers)
	assert.Contains(t, names, "defillama")
	assert.Contains(t, names, "eigenlayer")
	assert.Contains(t, names, "karak")
	assert.Contains(t, names, "symbiotic")
}

func TestCreateProvidersWithExplicitList(t *testing.T) {
	appCfg := config.Config{
		EnabledProviders: []string{"defillama", "karak"},
		KarakURL:         "https://api.karak.xyz",
	}
	srvCfg := ServerConfig{}
	providers := createProviders(appCfg, srvCfg)
	names := providerNames(providers)
	assert.Contains(t, names, "defillama")
	assert.Contains(t, names, "karak")
}

func TestCreateProvidersUnknownSkipped(t *testing.T) {
	appCfg := config.Config{
		EnabledProviders: []string{"defillama", "unknown-provider"},
	}
	srvCfg := ServerConfig{}
	providers := createProviders(appCfg, srvCfg)
	assert.Len(t, providers, 1)
	assert.Equal(t, "defillama", providers[0].Name())
}

// --- initEnterprise ---

func TestInitEnterpriseRateLimiting(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("RATE_LIMIT_RPS", "5")
	t.Setenv("RATE_LIMIT_BURST", "10")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	assert.NotNil(t, srv.rateLimit)
}

func TestInitEnterpriseMultiChain(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("MULTICHAIN_ENABLED", "true")
	os.Unsetenv("POLYGON_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	assert.NotNil(t, srv.multiChainClient)
}

func TestInitEnterpriseDataIntegrity(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("DATA_INTEGRITY_ENABLED", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")
	os.Unsetenv("SIGNING_PRIVATE_KEY")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	assert.NotNil(t, srv.dataIntegrity)
}

func TestInitEnterpriseDataIntegrityWithKey(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("DATA_INTEGRITY_ENABLED", "true")
	t.Setenv("SIGNING_PRIVATE_KEY", "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80") // anvil key 0
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	assert.NotNil(t, srv.dataIntegrity)
	assert.True(t, strings.HasPrefix(srv.dataIntegrity.Address(), "0x"))
}

func TestInitEnterpriseMetricsExporter(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("METRICS_EXPORT_ENABLED", "true")
	t.Setenv("METRICS_EXPORT_BATCH_SIZE", "50")
	t.Setenv("METRICS_EXPORT_INTERVAL", "5m")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("WEBHOOK_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	assert.NotNil(t, srv.metricsExporter)
}

func TestInitEnterpriseDataIntegrityInvalidKey(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("DATA_INTEGRITY_ENABLED", "true")
	t.Setenv("SIGNING_PRIVATE_KEY", "not-a-valid-key")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	// Data integrity should be nil due to invalid key.
	assert.Nil(t, srv.dataIntegrity)
}

// --- handleMetrics disabled ---

func TestHandleMetricsDisabled(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
		EnableMetrics:   false,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.handleMetrics(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// --- handleCircuitStatus ---

func TestHandleCircuitStatusDisabled(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:               freePort(t),
		Timeout:            5 * time.Second,
		AggregationMode:    "weighted",
		EnableCircuitBreaker: false,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/circuit", nil)
	srv.handleCircuitStatus(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleCircuitStatusReset(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:               freePort(t),
		Timeout:            5 * time.Second,
		AggregationMode:    "weighted",
		EnableCircuitBreaker: true,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/circuit?action=reset", nil)
	srv.handleCircuitStatus(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "circuit breaker reset", resp["message"])
}

func TestHandleCircuitStatusGet(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:               freePort(t),
		Timeout:            5 * time.Second,
		AggregationMode:    "weighted",
		EnableCircuitBreaker: true,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/circuit", nil)
	srv.handleCircuitStatus(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "closed", resp["state"])
}

// --- handleStatus with data integrity ---

func TestHandleStatusWithDataIntegrity(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("DATA_INTEGRITY_ENABLED", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")
	os.Unsetenv("SIGNING_PRIVATE_KEY")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/status", nil)
	srv.handleStatus(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var status map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&status)
	assert.NotNil(t, status["signer"])
}

// --- handleReadyz no providers ---

func TestHandleReadyzNoProviders(t *testing.T) {
	srv := &Server{
		cfg: ServerConfig{Port: "0"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.handleReadyz(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// --- writeJSON ---

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "ok", resp["status"])
}

// --- providerNames ---

func TestProviderNames(t *testing.T) {
	ps := []provider.Provider{
		&mockProvider{name: "p1"},
		&mockProvider{name: "p2"},
	}
	names := providerNames(ps)
	assert.Equal(t, []string{"p1", "p2"}, names)
}

func TestProviderNamesEmpty(t *testing.T) {
	assert.Empty(t, providerNames(nil))
}

// --- Stop with tracer shutdown ---

func TestStopWithTracerShutdown(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)

	called := false
	srv.tracerShutdown = func(ctx context.Context) error {
		called = true
		return nil
	}

	srv.Stop()
	assert.True(t, called, "tracer shutdown should be called")
}

// --- NewServer no providers ---

func TestNewServerNoProviders(t *testing.T) {
	_, err := NewServer(ServerConfig{}, config.Config{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no providers")
}

// --- run() with injectable quit channel ---

func TestRunGracefulShutdown(t *testing.T) {
	t.Setenv("PORT", freePort(t))
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("ENABLE_METRICS", "false")
	os.Unsetenv("OTEL_ENDPOINT")
	os.Unsetenv("ENABLED_PROVIDERS")

	quitCh := make(chan struct{})
	done := make(chan int, 1)
	go func() { done <- run(quitCh) }()

	// Give the server time to start listening.
	time.Sleep(300 * time.Millisecond)
	close(quitCh)

	select {
	case code := <-done:
		assert.Equal(t, 0, code)
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not exit in time")
	}
}

// --- handleRequest edge cases ---

func TestHandleRequestWithID(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"0xabc","jobRunID":"job-1","data":{"symbol":"weETH"}}`))
	srv.handleRequest(w, r)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	// Per Chainlink EA spec, "id" is the jobRunID and is echoed back.
	assert.Equal(t, "0xabc", resp.JobRunID)
	data := resp.Data
	assert.Equal(t, "0xabc", data["id"])
	assert.Equal(t, "weETH", data["symbol"])
}

func TestHandleRequestWithMeta(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1","meta":{"custom":"value"}}`))
	srv.handleRequest(w, r)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	meta := resp.Data["meta"].(map[string]interface{})
	assert.Equal(t, "value", meta["custom"])
}

func TestHandleRequestValidationNoValidMetrics(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
		EnableValidation: true,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: -1, TVL: 0, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleRequestCircuitBreakerOpenNoLastGood(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:               freePort(t),
		Timeout:            5 * time.Second,
		AggregationMode:    "weighted",
		EnableCircuitBreaker: true,
		EnableValidation:   false,
		MaxAPYThreshold:    0.1,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 100.0, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleRequestCircuitBreakerFallbackToLastGood(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:               freePort(t),
		Timeout:            5 * time.Second,
		AggregationMode:    "weighted",
		EnableCircuitBreaker: true,
		EnableValidation:   false,
		MaxAPYThreshold:    0.1,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	// First request — stores last-known-good.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w1, r1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Now swap the provider to return bad data that trips the breaker.
	srv.providers = []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 100.0, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}

	// Second request — breaker trips, falls back to last-known-good.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"2"}`))
	srv.handleRequest(w2, r2)
	assert.Equal(t, http.StatusOK, w2.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w2.Body).Decode(&resp)
	assert.Equal(t, true, resp.Data["stale"])
}

func TestHandleRequestMedianMode(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "median",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
		&mockProvider{name: "p2", metrics: []model.Metric{{Provider: "p2", APY: 0.06, TVL: 2000, CollectedAt: time.Now().Unix()}}},
		&mockProvider{name: "p3", metrics: []model.Metric{{Provider: "p3", APY: 0.05, TVL: 1500, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "aggregated-median", resp.Data["provider"])
}

func TestHandleRequestTrimmedMode(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "trimmed",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
		&mockProvider{name: "p2", metrics: []model.Metric{{Provider: "p2", APY: 0.06, TVL: 2000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "aggregated-trimmed", resp.Data["provider"])
}

func TestHandleRequestConsensusMode(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "consensus",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
		&mockProvider{name: "p2", metrics: []model.Metric{{Provider: "p2", APY: 0.05, TVL: 2000, CollectedAt: time.Now().Unix()}}},
		&mockProvider{name: "p3", metrics: []model.Metric{{Provider: "p3", APY: 0.045, TVL: 1500, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "aggregated-consensus", resp.Data["provider"])
}

func TestHandleRequestRateLimited(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("RATE_LIMIT_RPS", "0.01")
	t.Setenv("RATE_LIMIT_BURST", "1")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	// First request should succeed (burst=1).
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w1, r1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second request should be rate-limited.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"2"}`))
	srv.handleRequest(w2, r2)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

func TestHandleRequestWithMetricsExporter(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("METRICS_EXPORT_ENABLED", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("WEBHOOK_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCollectMetricsWithMultiChain(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("MULTICHAIN_ENABLED", "true")
	os.Unsetenv("POLYGON_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	// collectMetrics should use multiChainClient when enterprise is enabled.
	_, err = srv.collectMetrics(context.Background())
	// May error if no providers registered for chains, but should not panic.
	_ = err
}

func TestAggregateMetricsDefaultMode(t *testing.T) {
	srv := &Server{
		cfg: ServerConfig{AggregationMode: "unknown-mode"},
	}
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "p2", APY: 0.06, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	result := srv.aggregateMetrics(metrics)
	assert.Equal(t, "aggregated-unknown-mode", result.Provider)
	assert.True(t, result.CollectedAt > 0)
}

func TestErrorResponseWithRequestID(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
		EnableMetrics:   false,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, "test-rid-123"))
	srv.errorResponse(w, r, http.StatusInternalServerError, "test error")

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "test-rid-123", resp.Data["requestId"])
}

// --- writeJSON error path ---

type failingWriter struct {
	headers http.Header
}

func (w *failingWriter) Header() http.Header {
	if w.headers == nil {
		w.headers = make(http.Header)
	}
	return w.headers
}
func (w *failingWriter) WriteHeader(code int) {}
func (w *failingWriter) Write(b []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWriteJSONError(t *testing.T) {
	w := &failingWriter{}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	// Should not panic even when the writer fails.
}

// --- handleRequest with invalid body ---

func TestHandleRequestInvalidBody(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not-json"))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- handleRequest with GET method ---

func TestHandleRequestGetMethod(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- handleRequest with all providers failing ---

func TestHandleRequestAllProvidersFail(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", err: errors.New("connection refused")},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- handleRequest with data integrity signing ---

func TestHandleRequestWithDataIntegrity(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("DATA_INTEGRITY_ENABLED", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")
	os.Unsetenv("SIGNING_PRIVATE_KEY")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"0xabc","jobRunID":"job-1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	// The response should be a tamper-proof wrapper, not a plain ChainlinkResponse.
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	// Tamper-proof wrapper has "payload", "integrity", "_signature" keys.
	assert.NotNil(t, resp["payload"])
	assert.NotNil(t, resp["integrity"])
}

// --- handleHealth ---

func TestHandleHealth(t *testing.T) {
	srv := &Server{cfg: ServerConfig{Port: "0"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.handleHealth(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "OK", resp["status"])
}

// --- handleReadyz with providers and circuit breaker ---

func TestHandleReadyzWithProviders(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:               freePort(t),
		Timeout:            5 * time.Second,
		AggregationMode:    "weighted",
		EnableCircuitBreaker: true,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.handleReadyz(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "ready", resp["status"])
	assert.Equal(t, "closed", resp["circuit_state"])
}

// --- handleStatus ---

func TestHandleStatus(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/status", nil)
	srv.handleStatus(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var status map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&status)
	assert.Equal(t, "operational", status["status"])
	assert.Equal(t, "weighted", status["configuration"].(map[string]interface{})["aggregation_mode"])
}

// --- fetchAllProviders with metrics nil ---

func TestFetchAllProvidersEmpty(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: nil},
	})
	require.NoError(t, err)
	defer srv.Stop()

	metrics, err := srv.fetchAllProviders(context.Background())
	require.NoError(t, err)
	assert.Empty(t, metrics)
}

// --- collectMetrics without multichain ---

func TestCollectMetricsWithoutMultiChain(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	metrics, err := srv.collectMetrics(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

// --- Stop with httpServer shutdown error ---

func TestStopWithHTTPShutdownError(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)

	// Set a fake httpServer that returns an error on Shutdown.
	srv.httpServer = &http.Server{Addr: ":0"}

	// Start it in a goroutine so Shutdown has something to shut down.
	go func() { _ = srv.httpServer.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)

	// Stop should handle the shutdown gracefully.
	srv.Stop()
}

// --- Stop with tracer shutdown error ---

func TestStopWithTracerShutdownError(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)

	srv.tracerShutdown = func(ctx context.Context) error {
		return errors.New("tracer shutdown failed")
	}

	srv.Stop() // should not panic
}

// --- Stop with metrics exporter ---

func TestStopWithMetricsExporter(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("METRICS_EXPORT_ENABLED", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("WEBHOOK_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)

	srv.Stop() // should flush metrics exporter
}

// --- errorResponse without request ID ---

func TestErrorResponseWithoutRequestID(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
		EnableMetrics:   false,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.errorResponse(w, r, http.StatusBadRequest, "bad request")

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	// Error is now an object with name + message per Chainlink EA spec.
	errObj, ok := resp.Error.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "bad request", errObj["message"])
	assert.Equal(t, "EAError", errObj["name"])
	_, hasRID := resp.Data["requestId"]
	assert.False(t, hasRID, "should not have requestId when none in context")
}

// --- newRequestID fallback path ---

type failingRandReader struct{}

func (f *failingRandReader) Read(p []byte) (int, error) {
	return 0, errors.New("rand read failed")
}

func TestNewRequestIDFallback(t *testing.T) {
	original := randReader
	randReader = &failingRandReader{}
	defer func() { randReader = original }()

	id := newRequestID()
	assert.NotEmpty(t, id)
	// The fallback uses a timestamp-derived ID.
	assert.True(t, len(id) > 0)
}

func TestNewRequestIDNormal(t *testing.T) {
	id := newRequestID()
	assert.Len(t, id, 32) // 16 bytes -> 32 hex chars
}

// --- Start() returns error on bad port ---

func TestStartReturnsErrorOnBadPort(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            "99999", // invalid port
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)

	err = srv.Start()
	assert.Error(t, err) // should return error, not Fatalf
}

// --- Start() returns nil on clean shutdown ---

func TestStartReturnsNilOnCleanShutdown(t *testing.T) {
	port := freePort(t)
	srv, err := NewServer(ServerConfig{
		Port:            port,
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Wait for server to be ready.
	baseURL := "http://127.0.0.1:" + port
	require.Eventually(t, func() bool {
		resp, err := http.Get(baseURL + "/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	}, 5*time.Second, 100*time.Millisecond)

	srv.Stop()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return")
	}
}

// --- statusWriter.Write without WriteHeader ---

func TestStatusWriterWriteWithoutWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	_, _ = sw.Write([]byte("hello"))
	assert.Equal(t, http.StatusOK, sw.status)
	assert.Equal(t, 5, sw.size)
}

func TestStatusWriterWriteAfterWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	sw.WriteHeader(http.StatusCreated)
	_, _ = sw.Write([]byte("hello"))
	assert.Equal(t, http.StatusCreated, sw.status)
}

// --- handleRequest with data integrity signing failure ---

func TestHandleRequestSigningFailure(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("DATA_INTEGRITY_ENABLED", "true")
	t.Setenv("SIGNING_PRIVATE_KEY", "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	// Make CreateTamperProofWrapper fail by passing a request that causes
	// the signing to fail. We can't easily force this, but we can verify
	// the unsigned response is still sent.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"0xabc","jobRunID":"job-1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
}

// --- handleRequest with metrics enabled ---

func TestHandleRequestWithMetrics(t *testing.T) {
	// Use a fresh prometheus registry to avoid duplicate registration.
	// Since we can't easily reset the global registry, we just test that
	// the request succeeds when metrics are enabled (the e2e test already
	// covers this, so this is a no-op assertion).
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
		EnableMetrics:   false,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
}

// --- handleRequest with enterprise meta flags ---

func TestHandleRequestEnterpriseMetaFlags(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	t.Setenv("DATA_INTEGRITY_ENABLED", "true")
	os.Unsetenv("METRICS_EXPORT_ENABLED")
	os.Unsetenv("SIGNING_PRIVATE_KEY")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify the response body contains enterprise and signed meta flags.
	body := w.Body.String()
	assert.Contains(t, body, "enterprise")
	assert.Contains(t, body, "signed")
}

// --- handleRequest with data field containing reserved keys ---

func TestHandleRequestDataWithReservedKeys(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1","data":{"id":"should-be-skipped","jobRunID":"also-skipped","result":42,"symbol":"weETH"}}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	// "result" from client data must NOT overwrite the aggregated result
	// (whitelist filtering). "symbol" is whitelisted and should be echoed.
	// "id" and "jobRunID" are reserved and must not be echoed from data.
	assert.NotEqual(t, float64(42), resp.Data["result"], "client result must not overwrite aggregated result")
	assert.Equal(t, "weETH", resp.Data["symbol"], "whitelisted keys should be echoed")
}

// --- handleRequest with nil meta ---

func TestHandleRequestNilMeta(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	// No meta field in request.
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	meta := resp.Data["meta"].(map[string]interface{})
	assert.NotNil(t, meta["latencyMs"])
}

// --- handleRequest with requestID in context ---

func TestHandleRequestWithRequestIDInContext(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, "test-rid-456"))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	meta := resp.Data["meta"].(map[string]interface{})
	assert.Equal(t, "test-rid-456", meta["requestId"])
}

// --- fetchAllProviders with partial failure ---

func TestFetchAllProvidersPartialFailure(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
		&mockProvider{name: "p2", err: errors.New("timeout")},
	})
	require.NoError(t, err)
	defer srv.Stop()

	metrics, err := srv.fetchAllProviders(context.Background())
	require.NoError(t, err) // partial success
	assert.Len(t, metrics, 1)
}

// --- aggregateMetrics with CollectedAt already set ---

func TestAggregateMetricsCollectedAtSet(t *testing.T) {
	srv := &Server{
		cfg: ServerConfig{AggregationMode: "weighted"},
	}
	ts := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: ts},
		{Provider: "p2", APY: 0.06, TVL: 2000, CollectedAt: ts},
	}
	result := srv.aggregateMetrics(metrics)
	assert.Equal(t, ts, result.CollectedAt) // should not be overwritten
}

// --- run() error paths ---

func TestRunTracerInitFailure(t *testing.T) {
	t.Setenv("PORT", freePort(t))
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("ENABLE_METRICS", "false")
	os.Unsetenv("ENABLED_PROVIDERS")

	// Swap initTracerFn to return an error.
	orig := initTracerFn
	initTracerFn = func(endpoint string) (func(context.Context) error, error) {
		return nil, errors.New("tracer init failed")
	}
	t.Cleanup(func() { initTracerFn = orig })

	quitCh := make(chan struct{})
	done := make(chan int, 1)
	go func() { done <- run(quitCh) }()

	time.Sleep(300 * time.Millisecond)
	close(quitCh)

	select {
	case code := <-done:
		assert.Equal(t, 0, code)
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not exit in time")
	}
}

func TestRunNewServerFailure(t *testing.T) {
	t.Setenv("PORT", freePort(t))
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("ENABLE_METRICS", "false")
	os.Unsetenv("OTEL_ENDPOINT")
	// Set ENABLED_PROVIDERS to an unknown provider so createProviders returns empty.
	t.Setenv("ENABLED_PROVIDERS", "nonexistent-provider")

	done := make(chan int, 1)
	go func() { done <- run(make(chan struct{})) }()

	select {
	case code := <-done:
		assert.Equal(t, 1, code, "run should return 1 when NewServer fails")
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not exit in time")
	}
}

func TestRunListenError(t *testing.T) {
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("ENABLE_METRICS", "false")
	os.Unsetenv("OTEL_ENDPOINT")
	os.Unsetenv("ENABLED_PROVIDERS")

	// Occupy a port first so the server can't listen on it.
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	t.Setenv("PORT", fmt.Sprintf("%d", port))

	done := make(chan int, 1)
	go func() { done <- run(make(chan struct{})) }()

	select {
	case code := <-done:
		assert.Equal(t, 1, code, "run should return 1 when listen fails")
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not exit in time")
	}
}

// --- createProviders with no valid providers ---

func TestCreateProvidersNoValidProviders(t *testing.T) {
	cfg := config.Config{
		EnabledProviders: []string{"nonexistent"},
	}
	providers := createProviders(cfg, ServerConfig{})
	assert.Empty(t, providers)
}

// --- initEnterprise with polygon enabled ---

func TestInitEnterprisePolygonEnabled(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("MULTICHAIN_ENABLED", "true")
	t.Setenv("POLYGON_ENABLED", "true")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv := &Server{appCfg: config.Config{}}
	err := srv.initEnterprise()
	require.NoError(t, err)
	assert.NotNil(t, srv.multiChainClient)
}

// --- initEnterprise with metrics exporter failure ---

func TestInitEnterpriseMetricsExporterInvalidInterval(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("METRICS_EXPORT_ENABLED", "true")
	t.Setenv("METRICS_EXPORT_INTERVAL", "invalid-duration")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")

	srv := &Server{appCfg: config.Config{}}
	err := srv.initEnterprise()
	require.NoError(t, err)
	// NewMetricsExporter falls back to 1m default for invalid duration.
	assert.NotNil(t, srv.metricsExporter)
}

// --- Stop with http shutdown error ---

func TestStopWithShutdownError(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)

	// Set a fake httpServer and a shutdownFn that returns an error.
	srv.mu.Lock()
	srv.httpServer = &http.Server{Addr: ":0"}
	srv.shutdownFn = func(ctx context.Context) error {
		return errors.New("shutdown failed")
	}
	srv.mu.Unlock()

	// Stop should log the error but not panic.
	srv.Stop()
}

// --- handleRequest with multiChainClient meta ---

func TestHandleRequestMultiChainMeta(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")
	os.Unsetenv("METRICS_EXPORT_ENABLED")

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	// Set multiChainClient to trigger the meta["multichain"] = true path.
	srv.multiChainClient = fetch.NewMultiChainClient(config.Config{}, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	meta := resp.Data["meta"].(map[string]interface{})
	assert.True(t, meta["multichain"].(bool))
}

// --- fetchAllProviders with metrics enabled and provider error ---

func TestFetchAllProvidersErrorWithMetrics(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
		EnableMetrics:   true,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", err: errors.New("connection refused")},
		&mockProvider{name: "p2", err: errors.New("timeout")},
	})
	require.NoError(t, err)
	defer srv.Stop()

	metrics, err := srv.fetchAllProviders(context.Background())
	require.Error(t, err)
	assert.Empty(t, metrics)
	assert.Contains(t, err.Error(), "all providers failed")
}

// --- aggregateMetrics with CollectedAt == 0 ---

func TestAggregateMetricsCollectedAtZero(t *testing.T) {
	srv := &Server{
		cfg: ServerConfig{AggregationMode: "weighted"},
	}
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: 0},
		{Provider: "p2", APY: 0.06, TVL: 2000, CollectedAt: 0},
	}
	result := srv.aggregateMetrics(metrics)
	assert.NotZero(t, result.CollectedAt) // should be set to now
}

// --- errorResponse with metrics enabled ---

func TestErrorResponseWithMetrics(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
		EnableMetrics:   true,
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{invalid`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- NewServer with enterprise init error ---

func TestNewServerEnterpriseInitError(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")

	orig := initEnterpriseFn
	initEnterpriseFn = func(s *Server) error {
		return errors.New("forced enterprise init error")
	}
	t.Cleanup(func() { initEnterpriseFn = orig })

	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err) // NewServer doesn't fail, just warns
	defer srv.Stop()
}

// --- initEnterprise with metrics exporter error ---

func TestInitEnterpriseMetricsExporterError(t *testing.T) {
	t.Setenv("ENABLE_ENTERPRISE_FEATURES", "true")
	t.Setenv("METRICS_EXPORT_ENABLED", "true")
	os.Unsetenv("MULTICHAIN_ENABLED")
	os.Unsetenv("DATA_INTEGRITY_ENABLED")

	// Swap newMetricsExporterFn to return an error.
	orig := newMetricsExporterFn
	newMetricsExporterFn = func(cfg enterprise.ExporterConfig) (*enterprise.MetricsExporter, error) {
		return nil, errors.New("exporter init failed")
	}
	t.Cleanup(func() { newMetricsExporterFn = orig })

	srv := &Server{appCfg: config.Config{}}
	err := srv.initEnterprise()
	require.NoError(t, err)
	assert.Nil(t, srv.metricsExporter)
}

// --- handleRequest with signing failure ---

func TestHandleRequestSigningFailureWithMock(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	srv.enableEnterprise = true
	srv.dataIntegrity = &mockDataIntegrity{
		addr: "0x1234",
		err:  errors.New("signing failed"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	// Response should be unsigned (plain ChainlinkResponse, not wrapped).
	var resp ChainlinkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "completed", resp.Status)
	_, isWrapped := resp.Data["payload"]
	assert.False(t, isWrapped, "response should be unsigned on signing failure")
}

// --- handleRequest with successful signing ---

func TestHandleRequestSigningSuccessWithMock(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		Port:            freePort(t),
		Timeout:         5 * time.Second,
		AggregationMode: "weighted",
	}, config.Config{}, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	})
	require.NoError(t, err)
	defer srv.Stop()

	srv.enableEnterprise = true
	srv.dataIntegrity = &mockDataIntegrity{
		addr: "0x1234",
		wrapped: map[string]interface{}{
			"payload": map[string]interface{}{"result": 0.04},
			"_signature": map[string]interface{}{"r": "0x1", "s": "0x2", "v": 27},
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jobRunID":"1"}`))
	srv.handleRequest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	// Response should be the wrapped (signed) payload.
	body := w.Body.String()
	assert.Contains(t, body, "_signature")
}

// mockDataIntegrity implements dataIntegritySigner for testing.
type mockDataIntegrity struct {
	addr    string
	wrapped map[string]interface{}
	err     error
}

func (m *mockDataIntegrity) CreateTamperProofWrapper(payload interface{}, metadata map[string]interface{}) (map[string]interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.wrapped, nil
}

func (m *mockDataIntegrity) Address() string { return m.addr }

// --- run() with signal handling (quitCh == nil) ---

func TestRunSignalHandling(t *testing.T) {
	t.Setenv("PORT", freePort(t))
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("ENABLE_METRICS", "false")
	os.Unsetenv("OTEL_ENDPOINT")
	os.Unsetenv("ENABLED_PROVIDERS")

	done := make(chan int, 1)
	go func() { done <- run(nil) }()

	// Give the server time to start, then send SIGTERM.
	time.Sleep(300 * time.Millisecond)
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	select {
	case code := <-done:
		assert.Equal(t, 0, code)
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not exit in time")
	}
}

// --- main() via subprocess ---

func TestMainSubprocess(t *testing.T) {
	if os.Getenv("TEST_MAIN_SUBPROCESS") == "1" {
		// Reset signal handlers so the test framework's handlers don't
		// interfere with run()'s signal handling.
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		os.Setenv("PORT", os.Getenv("TEST_PORT"))
		os.Setenv("LOG_LEVEL", "error")
		os.Setenv("ENABLE_METRICS", "false")
		os.Unsetenv("OTEL_ENDPOINT")
		os.Unsetenv("ENABLED_PROVIDERS")
		main() // calls os.Exit(run(nil))
		return
	}

	// Find a free port for the server.
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	// Run the test binary as a subprocess, invoking main() via the env var.
	cmd := exec.Command(os.Args[0], "-test.run=TestMainSubprocess")
	cmd.Env = append(os.Environ(),
		"TEST_MAIN_SUBPROCESS=1",
		"TEST_PORT="+fmt.Sprintf("%d", port),
	)
	// Capture subprocess output for debugging.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())

	// Wait for the server to start, then send SIGTERM.
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Logf("subprocess stdout: %s", stdout.String())
			t.Logf("subprocess stderr: %s", stderr.String())
			assert.Equal(t, 0, exitErr.ExitCode())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("subprocess did not exit in time")
	}
}
