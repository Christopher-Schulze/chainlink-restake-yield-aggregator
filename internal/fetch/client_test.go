package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientEigenLayer(t *testing.T) {
	cfg := config.Config{EigenURL: "https://api.eigenlayer.xyz"}
	c := NewClient(cfg, "eigenlayer")
	require.NotNil(t, c)
	assert.Equal(t, "eigenlayer", c.Name())
}

func TestNewClientKarak(t *testing.T) {
	cfg := config.Config{KarakURL: "https://api.karak.xyz"}
	c := NewClient(cfg, "karak")
	require.NotNil(t, c)
	assert.Equal(t, "karak", c.Name())
}

func TestNewClientSymbiotic(t *testing.T) {
	cfg := config.Config{SymbioticURL: "https://api.symbiotic.xyz"}
	c := NewClient(cfg, "symbiotic")
	require.NotNil(t, c)
	assert.Equal(t, "symbiotic", c.Name())
}

func TestNewClientDefiLlama(t *testing.T) {
	cfg := config.Config{
		DefiLlamaYields: "https://yields.llama.fi",
		DefiLlamaPrices: "https://coins.llama.fi",
	}
	c := NewClient(cfg, "defillama")
	require.NotNil(t, c)
	assert.Equal(t, "defillama", c.Name())
}

func TestNewClientUnknown(t *testing.T) {
	c := NewClient(config.Config{}, "unknown")
	assert.Nil(t, c)
}

func TestNewRetryClient(t *testing.T) {
	rc := newRetryClient()
	assert.NotNil(t, rc)
	assert.Equal(t, 3, rc.RetryMax)
}

func TestStandardHTTPClient(t *testing.T) {
	rc := newRetryClient()
	hc := standardHTTPClient(rc)
	assert.NotNil(t, hc)
}

func TestGetAPIKeyFound(t *testing.T) {
	cfg := config.Config{
		APIKeys: map[string]string{"eigenlayer": "key123"},
	}
	assert.Equal(t, "key123", getAPIKey(cfg, "eigenlayer"))
}

func TestGetAPIKeyNotFound(t *testing.T) {
	assert.Equal(t, "", getAPIKey(config.Config{}, "nonexistent"))
}

func TestFuncProviderFetch(t *testing.T) {
	called := false
	fp := FuncProvider("test-func", func(ctx context.Context) ([]model.Metric, error) {
		called = true
		return []model.Metric{{Provider: "test-func", APY: 0.04, TVL: 1000}}, nil
	})
	assert.Equal(t, "test-func", fp.Name())

	metrics, err := fp.Fetch(context.Background())
	require.NoError(t, err)
	assert.True(t, called)
	assert.Len(t, metrics, 1)
}

func TestRestClientName(t *testing.T) {
	c := &restClient{name: "test-rest"}
	assert.Equal(t, "test-rest", c.Name())
}

func TestRestClientFetchEmptyURL(t *testing.T) {
	c := &restClient{name: "eigenlayer", apiURL: "", httpClient: standardHTTPClient(newRetryClient())}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestRestClientFetchWithAPIKeyOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"protocol": "test", "apy": 0.04, "tvl": 1000.0, "points_per_eth": 1.0, "collected_at": 1700000000},
			},
		})
	}))
	defer srv.Close()

	c := &restClient{
		name:       "test",
		apiURL:     srv.URL, // HTTP, not HTTPS
		apiKey:     "secret-key",
		httpClient: standardHTTPClient(newRetryClient()),
	}
	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestRestClientFetchWithCollectedAtZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"protocol": "test", "apy": 0.04, "tvl": 1000.0, "points_per_eth": 1.0, "collected_at": 0},
			},
		})
	}))
	defer srv.Close()

	c := &restClient{
		name:       "test",
		apiURL:     srv.URL,
		httpClient: standardHTTPClient(newRetryClient()),
	}
	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, metrics)
	assert.True(t, metrics[0].CollectedAt > 0, "collected_at=0 should default to now")
}

func TestRestClientFetchDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := &restClient{
		name:       "test",
		apiURL:     srv.URL,
		httpClient: standardHTTPClient(newRetryClient()),
	}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestRestClientFetchBadURL(t *testing.T) {
	c := &restClient{
		name:       "test",
		apiURL:     "http://[::1]:namedport", // invalid URL
		httpClient: standardHTTPClient(newRetryClient()),
	}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
}

func TestIsHTTPS(t *testing.T) {
	assert.True(t, isHTTPS("https://example.com"))
	assert.False(t, isHTTPS("http://example.com"))
	assert.False(t, isHTTPS("://invalid"))
}

func TestIsHTTPSEmpty(t *testing.T) {
	assert.False(t, isHTTPS(""))
}

func TestDefiLlamaClientName(t *testing.T) {
	c := NewDefiLlamaClient(config.Config{
		DefiLlamaYields: "https://yields.llama.fi",
		DefiLlamaPrices: "https://coins.llama.fi",
	})
	assert.Equal(t, "defillama", c.Name())
}

func TestDefiLlamaClientFetchError(t *testing.T) {
	c := NewDefiLlamaClient(config.Config{
		DefiLlamaYields: "http://localhost:1", // unreachable
		DefiLlamaPrices: "http://localhost:1",
	})
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
}

func TestCreateDefaultProviderEthereum(t *testing.T) {
	cfg := config.Config{}
	chains := map[SupportedChain]ChainConfig{
		ChainEthereum: {Enabled: true, Weight: 1.0},
	}
	client := NewMultiChainClient(cfg, chains)
	p, err := client.createDefaultProvider(ChainEthereum)
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestCreateDefaultProviderPolygon(t *testing.T) {
	cfg := config.Config{}
	chains := map[SupportedChain]ChainConfig{
		ChainPolygon: {Enabled: true, APIEndpoint: "http://localhost:9999", Weight: 0.8},
	}
	client := NewMultiChainClient(cfg, chains)
	p, err := client.createDefaultProvider(ChainPolygon)
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestCreateDefaultProviderArbitrum(t *testing.T) {
	cfg := config.Config{}
	chains := map[SupportedChain]ChainConfig{
		ChainArbitrum: {Enabled: true, APIEndpoint: "http://localhost:9999", Weight: 0.8},
	}
	client := NewMultiChainClient(cfg, chains)
	p, err := client.createDefaultProvider(ChainArbitrum)
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestCreateDefaultProviderGeneric(t *testing.T) {
	cfg := config.Config{}
	chains := map[SupportedChain]ChainConfig{
		ChainOptimism: {Enabled: true, APIEndpoint: "http://localhost:9999", Weight: 0.8},
	}
	client := NewMultiChainClient(cfg, chains)
	p, err := client.createDefaultProvider(ChainOptimism)
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestCreateDefaultProviderDisabled(t *testing.T) {
	cfg := config.Config{}
	chains := map[SupportedChain]ChainConfig{
		ChainEthereum: {Enabled: false, Weight: 1.0},
	}
	client := NewMultiChainClient(cfg, chains)
	_, err := client.createDefaultProvider(ChainEthereum)
	require.Error(t, err)
}

func TestCreateDefaultProviderNotConfigured(t *testing.T) {
	cfg := config.Config{}
	chains := map[SupportedChain]ChainConfig{}
	client := NewMultiChainClient(cfg, chains)
	_, err := client.createDefaultProvider(ChainEthereum)
	require.Error(t, err)
}

// --- DefiLlama tests ---

// newDefiLlamaClient builds a DefiLlamaClient pointing at the given mock
// prices and yields servers with a minimal LRT symbol set.
func newDefiLlamaClient(t *testing.T, pricesURL, yieldsURL string) *DefiLlamaClient {
	t.Helper()
	return &DefiLlamaClient{
		pricesURL:  pricesURL,
		yieldsURL:  yieldsURL,
		httpClient: standardHTTPClient(newRetryClient()),
		symbols:    map[string]struct{}{"WEETH": {}},
	}
}

// mockPricesServer returns a test server that responds with the given status
// code and body for the ETH prices endpoint.
func mockPricesServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// mockYieldsServer returns a test server that responds with the given status
// code and body for the yields endpoint.
func mockYieldsServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestDefiLlamaFetchInvalidETHPrice(t *testing.T) {
	prices := mockPricesServer(t, http.StatusOK, `{"coins":{"coingecko:ethereum":{"price":0,"symbol":"ETH"}}}`)
	defer prices.Close()
	yields := mockYieldsServer(t, http.StatusOK, `{"status":"success","data":[]}`)
	defer yields.Close()

	c := newDefiLlamaClient(t, prices.URL, yields.URL)
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	// fetchETHPrice guards price <= 0 and reports "eth price not found"; the
	// Fetch wrapper prefixes "defillama: eth price:". Either way the error
	// must mention the eth price problem.
	assert.Contains(t, err.Error(), "eth price")
}

func TestDefiLlamaFetchNegativeAPYZeroTVLFiltered(t *testing.T) {
	prices := mockPricesServer(t, http.StatusOK, `{"coins":{"coingecko:ethereum":{"price":2000,"symbol":"ETH"}}}`)
	defer prices.Close()
	// One valid weETH pool, one with negative APY, one with zero TVL.
	yieldsBody := `{"status":"success","data":[` +
		`{"symbol":"weETH","project":"test","chain":"Ethereum","tvlUsd":1000,"apy":5.0,"apyBase":5.0,"apyReward":0},` +
		`{"symbol":"weETH","project":"bad-apy","chain":"Ethereum","tvlUsd":500,"apy":-5.0,"apyBase":-5.0,"apyReward":0},` +
		`{"symbol":"weETH","project":"bad-tvl","chain":"Ethereum","tvlUsd":0,"apy":3.0,"apyBase":3.0,"apyReward":0}` +
		`]}`
	yields := mockYieldsServer(t, http.StatusOK, yieldsBody)
	defer yields.Close()

	c := newDefiLlamaClient(t, prices.URL, yields.URL)
	metrics, err := c.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1, "negative-APY and zero-TVL pools should be filtered out")
	assert.Equal(t, "WEETH", metrics[0].Symbol)
	assert.InDelta(t, 0.05, metrics[0].APY, 1e-9)
}

func TestDefiLlamaFetchETHPriceNon200(t *testing.T) {
	prices := mockPricesServer(t, http.StatusNotFound, "not found")
	defer prices.Close()
	yields := mockYieldsServer(t, http.StatusOK, `{"status":"success","data":[]}`)
	defer yields.Close()

	c := newDefiLlamaClient(t, prices.URL, yields.URL)
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

func TestDefiLlamaFetchETHPriceNotFound(t *testing.T) {
	prices := mockPricesServer(t, http.StatusOK, `{"coins":{"other:coin":{"price":100}}}`)
	defer prices.Close()
	yields := mockYieldsServer(t, http.StatusOK, `{"status":"success","data":[]}`)
	defer yields.Close()

	c := newDefiLlamaClient(t, prices.URL, yields.URL)
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eth price not found")
}

func TestDefiLlamaFetchYieldsNon200(t *testing.T) {
	prices := mockPricesServer(t, http.StatusOK, `{"coins":{"coingecko:ethereum":{"price":2000,"symbol":"ETH"}}}`)
	defer prices.Close()
	yields := mockYieldsServer(t, http.StatusInternalServerError, "server error")
	defer yields.Close()

	// Use a plain http.Client (no retry) so the 500 response is surfaced
	// directly to the status-check branch instead of being retried.
	c := &DefiLlamaClient{
		pricesURL:  prices.URL,
		yieldsURL:  yields.URL,
		httpClient: &http.Client{},
		symbols:    map[string]struct{}{"WEETH": {}},
	}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestDefiLlamaFetchYieldsAPIStatusNotSuccess(t *testing.T) {
	prices := mockPricesServer(t, http.StatusOK, `{"coins":{"coingecko:ethereum":{"price":2000,"symbol":"ETH"}}}`)
	defer prices.Close()
	yields := mockYieldsServer(t, http.StatusOK, `{"status":"error","data":[]}`)
	defer yields.Close()

	c := newDefiLlamaClient(t, prices.URL, yields.URL)
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api status")
}

// --- restClient tests ---

func TestRestClientFetchNon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer srv.Close()

	c := &restClient{
		name:       "test",
		apiURL:     srv.URL,
		httpClient: standardHTTPClient(newRetryClient()),
	}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

func TestRestClientFetchEmptyDataArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
	}))
	defer srv.Close()

	c := &restClient{
		name:       "test",
		apiURL:     srv.URL,
		httpClient: standardHTTPClient(newRetryClient()),
	}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no data returned")
}

// --- multichain tests ---

func TestMultiChainFetchChainDataNoProvidersCreateDefaultFails(t *testing.T) {
	cfg := config.Config{}
	// Empty chains map: createDefaultProvider will fail for any chain.
	client := NewMultiChainClient(cfg, map[SupportedChain]ChainConfig{})
	_, err := client.fetchChainData(context.Background(), ChainEthereum)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no providers for chain")
}

func TestGenericChainProviderFetchDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	p := NewGenericChainProvider("testchain", srv.URL, "")
	_, err := p.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestGenericChainProviderFetchNon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer srv.Close()

	// Use a plain http.Client (no retry) so the 500 response is surfaced
	// directly to the status-check branch instead of being retried.
	p := &GenericChainProvider{
		chain:      "testchain",
		apiURL:     srv.URL,
		httpClient: &http.Client{},
	}
	_, err := p.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

// --- additional branch-coverage tests ---

// TestDefiLlamaFetchZeroPriceNoError covers the `pr.price <= 0` defensive
// branch in DefiLlamaClient.Fetch (defillama.go ~126-127). The real
// fetchETHPrice never returns (0, nil), so we inject a function that does.
func TestDefiLlamaFetchZeroPriceNoError(t *testing.T) {
	c := &DefiLlamaClient{
		pricesURL:  "http://unused",
		yieldsURL:  "http://unused",
		httpClient: &http.Client{},
		symbols:    map[string]struct{}{"WEETH": {}},
		fetchETHPriceFn: func(ctx context.Context) (float64, error) {
			return 0, nil // defensive: price <= 0 with no error
		},
		fetchYieldsFn: func(ctx context.Context) (yieldsResponse, error) {
			return yieldsResponse{Status: "success", Data: nil}, nil
		},
	}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid eth price")
}

// TestDefiLlamaFetchETHPriceRequestError covers the http.NewRequestWithContext
// failure path in fetchETHPrice (defillama.go ~159-160). A URL with a newline
// control character causes request creation to fail.
func TestDefiLlamaFetchETHPriceRequestError(t *testing.T) {
	c := &DefiLlamaClient{
		pricesURL:  "http://localhost:9999\n", // newline makes URL invalid
		yieldsURL:  "http://localhost:9999",
		httpClient: &http.Client{},
		symbols:    map[string]struct{}{"WEETH": {}},
	}
	_, err := c.fetchETHPrice(context.Background())
	require.Error(t, err)
}

// TestDefiLlamaFetchETHPriceDecodeError covers the json.Decode failure path
// in fetchETHPrice (defillama.go ~172-173). A server returning invalid JSON
// with a 200 status triggers the decode error.
func TestDefiLlamaFetchETHPriceDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := &DefiLlamaClient{
		pricesURL:  srv.URL,
		yieldsURL:  "http://unused",
		httpClient: &http.Client{},
		symbols:    map[string]struct{}{"WEETH": {}},
	}
	_, err := c.fetchETHPrice(context.Background())
	require.Error(t, err)
}

// TestDefiLlamaFetchYieldsRequestError covers the http.NewRequestWithContext
// failure path in fetchYields (defillama.go ~188-189). A URL with a newline
// control character causes request creation to fail.
func TestDefiLlamaFetchYieldsRequestError(t *testing.T) {
	c := &DefiLlamaClient{
		pricesURL:  "http://localhost:9999",
		yieldsURL:  "http://localhost:9999\n", // newline makes URL invalid
		httpClient: &http.Client{},
		symbols:    map[string]struct{}{"WEETH": {}},
	}
	_, err := c.fetchYields(context.Background())
	require.Error(t, err)
}

// TestDefiLlamaFetchYieldsDecodeError covers the json.Decode failure path
// in fetchYields (defillama.go ~201-202). A server returning invalid JSON
// with a 200 status triggers the decode error.
func TestDefiLlamaFetchYieldsDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := &DefiLlamaClient{
		pricesURL:  "http://unused",
		yieldsURL:  srv.URL,
		httpClient: &http.Client{},
		symbols:    map[string]struct{}{"WEETH": {}},
	}
	_, err := c.fetchYields(context.Background())
	require.Error(t, err)
}

// TestGenericChainProviderFetchRequestError covers the http.NewRequestWithContext
// failure path in GenericChainProvider.Fetch (multichain.go ~258-259). A URL
// with a newline causes request creation to fail.
func TestGenericChainProviderFetchRequestError(t *testing.T) {
	p := &GenericChainProvider{
		chain:      "testchain",
		apiURL:     "http://localhost:9999\n", // newline makes URL invalid
		httpClient: &http.Client{},
	}
	_, err := p.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create request")
}

// TestGenericChainProviderFetchRequestFailed covers the httpClient.Do failure
// path in GenericChainProvider.Fetch (multichain.go ~267-268). An unreachable
// URL causes the request to fail.
func TestGenericChainProviderFetchRequestFailed(t *testing.T) {
	p := &GenericChainProvider{
		chain:      "testchain",
		apiURL:     "http://localhost:1", // unreachable port
		httpClient: &http.Client{},
	}
	_, err := p.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

// TestRestClientFetchRequestFailed covers the httpClient.Do failure path in
// restClient.Fetch (rest_client.go ~75-76). An unreachable URL causes the
// request to fail.
func TestRestClientFetchRequestFailed(t *testing.T) {
	c := &restClient{
		name:       "test",
		apiURL:     "http://localhost:1", // unreachable port
		httpClient: &http.Client{},
	}
	_, err := c.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}
