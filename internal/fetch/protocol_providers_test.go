package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/christopher/restake-yield-ea/internal/config"
)

// helper: create a mock DefiLlama protocol TVL response server
func mockProtocolTVLServer(t *testing.T, tvlUSD float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name":  "test-protocol",
			"chain": "Ethereum",
			"tvl": []map[string]interface{}{
				{"date": 1700000000, "totalLiquidityUSD": tvlUSD / 2},
				{"date": 1700000100, "totalLiquidityUSD": tvlUSD},
			},
		})
	}))
}

func TestEigenLayerClientFetch(t *testing.T) {
	protoSrv := mockProtocolTVLServer(t, 4_000_000_000) // $4B TVL
	defer protoSrv.Close()

	cfg := config.Config{EigenURL: protoSrv.URL}
	c := NewEigenLayerClient(cfg)
	// Inject mock price: ETH = $2500
	c.fetchETHPriceFn = func(_ context.Context) (float64, error) { return 2500, nil }

	metrics, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("EigenLayer Fetch failed: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	m := metrics[0]
	if m.Provider != "eigenlayer" {
		t.Errorf("expected provider=eigenlayer, got %s", m.Provider)
	}
	if m.APY != 0 {
		t.Errorf("expected APY=0 (restaking has no simple yield), got %f", m.APY)
	}
	// $4B / $2500 = 1.6M ETH
	expectedTVL := 4_000_000_000.0 / 2500.0
	if m.TVL < expectedTVL*0.99 || m.TVL > expectedTVL*1.01 {
		t.Errorf("expected TVL~%.0f ETH, got %.0f", expectedTVL, m.TVL)
	}
	if m.Chain != "ethereum" {
		t.Errorf("expected chain=ethereum, got %s", m.Chain)
	}
}

func TestEigenLayerClientEmptyTVL(t *testing.T) {
	protoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "test",
			"tvl":  []interface{}{},
		})
	}))
	defer protoSrv.Close()

	cfg := config.Config{EigenURL: protoSrv.URL}
	c := NewEigenLayerClient(cfg)
	c.fetchETHPriceFn = func(_ context.Context) (float64, error) { return 2500, nil }

	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error for empty TVL, got nil")
	}
}

func TestEigenLayerClientErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	c := NewEigenLayerClient(config.Config{EigenURL: srv.URL})
	c.fetchETHPriceFn = func(_ context.Context) (float64, error) { return 2500, nil }
	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 status, got nil")
	}
}

func TestKarakClientFetch(t *testing.T) {
	protoSrv := mockProtocolTVLServer(t, 6_000_000) // $6M TVL
	defer protoSrv.Close()

	cfg := config.Config{KarakURL: protoSrv.URL}
	c := NewKarakClient(cfg)
	c.fetchETHPriceFn = func(_ context.Context) (float64, error) { return 2500, nil }

	metrics, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Karak Fetch failed: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	m := metrics[0]
	if m.Provider != "karak" {
		t.Errorf("expected provider=karak, got %s", m.Provider)
	}
	if m.APY != 0 {
		t.Errorf("expected APY=0, got %f", m.APY)
	}
	expectedTVL := 6_000_000.0 / 2500.0
	if m.TVL < expectedTVL*0.99 || m.TVL > expectedTVL*1.01 {
		t.Errorf("expected TVL~%.0f ETH, got %.0f", expectedTVL, m.TVL)
	}
}

func TestSymbioticClientFetch(t *testing.T) {
	protoSrv := mockProtocolTVLServer(t, 300_000_000) // $300M TVL
	defer protoSrv.Close()

	cfg := config.Config{SymbioticURL: protoSrv.URL}
	c := NewSymbioticClient(cfg)
	c.fetchETHPriceFn = func(_ context.Context) (float64, error) { return 2500, nil }

	metrics, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Symbiotic Fetch failed: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	m := metrics[0]
	if m.Provider != "symbiotic" {
		t.Errorf("expected provider=symbiotic, got %s", m.Provider)
	}
	if m.APY != 0 {
		t.Errorf("expected APY=0, got %f", m.APY)
	}
	expectedTVL := 300_000_000.0 / 2500.0
	if m.TVL < expectedTVL*0.99 || m.TVL > expectedTVL*1.01 {
		t.Errorf("expected TVL~%.0f ETH, got %.0f", expectedTVL, m.TVL)
	}
}

func TestEigenLayerClientDefaultURL(t *testing.T) {
	cfg := config.Config{}
	c := NewEigenLayerClient(cfg)
	if c.apiURL != defaultEigenLayerURL {
		t.Errorf("expected default URL %s, got %s", defaultEigenLayerURL, c.apiURL)
	}
}

func TestKarakClientDefaultURL(t *testing.T) {
	cfg := config.Config{}
	c := NewKarakClient(cfg)
	if c.apiURL != defaultKarakURL {
		t.Errorf("expected default URL %s, got %s", defaultKarakURL, c.apiURL)
	}
}

func TestSymbioticClientDefaultURL(t *testing.T) {
	cfg := config.Config{}
	c := NewSymbioticClient(cfg)
	if c.apiURL != defaultSymbioticURL {
		t.Errorf("expected default URL %s, got %s", defaultSymbioticURL, c.apiURL)
	}
}
