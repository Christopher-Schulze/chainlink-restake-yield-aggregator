// Package config provides environment-driven configuration for the adapter.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/christopher/restake-yield-ea/internal/envx"
	"github.com/sirupsen/logrus"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// HTTP server port.
	Port string

	// Base URLs for data providers.
	EigenURL         string
	KarakURL         string
	SymbioticURL     string
	DefiLlamaURL     string
	DefiLlamaYields  string
	DefiLlamaPrices  string

	// Lido API for real stETH APR data.
	LidoAPIURL string

	// OpenTelemetry OTLP/HTTP endpoint (empty = no-op tracer).
	OtelEndpoint string

	// API keys for providers that require auth.
	APIKeys map[string]string

	// Timeouts and circuit breaker thresholds.
	RequestTimeout    time.Duration
	MaxAPY            float64
	MaxTVLChange      float64
	MinProviderCount  int
	CircuitResetDelay time.Duration
	// MaxStaleSeconds is the maximum age (in seconds) for circuit breaker
	// last-good fallback data. 0 disables the staleness check.
	MaxStaleSeconds int64

	// EnabledProviders controls which providers the server fans out to.
	// Empty means "all known providers".
	EnabledProviders []string
}

// Load reads configuration from environment variables.
func Load() Config {
	apiKeys := map[string]string{}
	if raw := os.Getenv("API_KEYS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &apiKeys); err != nil {
			logrus.Warnf("ignoring malformed API_KEYS env var: %v", err)
		}
	}

	cfg := Config{
		Port:             envx.String("PORT", "8080"),
		EigenURL:         envx.String("EIGENLAYER_API", ""),
		KarakURL:         envx.String("KARAK_API", ""),
		SymbioticURL:     envx.String("SYMBIOTIC_API", ""),
		DefiLlamaURL:     envx.String("DEFILLAMA_API", "https://api.llama.fi"),
		DefiLlamaYields:  envx.String("DEFILLAMA_YIELDS_API", "https://yields.llama.fi"),
		DefiLlamaPrices:  envx.String("DEFILLAMA_PRICES_API", "https://coins.llama.fi"),
		LidoAPIURL:       envx.String("LIDO_API_URL", "https://eth-api.lido.fi/v1/protocol/steth/apr/sma"),
		OtelEndpoint:     envx.String("OTEL_ENDPOINT", ""),
		APIKeys:          apiKeys,
		RequestTimeout:   envx.Duration("TIMEOUT", 10*time.Second),
		MaxAPY:           envx.Float64("MAX_APY_THRESHOLD", 1.0), // 100% max APY
		MaxTVLChange:     envx.Float64("MAX_TVL_CHANGE", 0.5),    // 50% max TVL change
		MinProviderCount: envx.Int("MIN_PROVIDERS", 2),
		CircuitResetDelay: envx.Duration("CIRCUIT_RESET_DELAY", 5*time.Minute),
		MaxStaleSeconds:   envx.Int64("MAX_STALE_SECONDS", 300), // 5 min default
	}

	if raw := os.Getenv("ENABLED_PROVIDERS"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(strings.ToLower(p)); p != "" {
				cfg.EnabledProviders = append(cfg.EnabledProviders, p)
			}
		}
	}

	return cfg
}

// String returns a human-readable summary of the config for logging at startup.
func (c Config) String() string {
	providers := "all"
	if len(c.EnabledProviders) > 0 {
		providers = strings.Join(c.EnabledProviders, ",")
	}
	return fmt.Sprintf("port=%s timeout=%s maxAPY=%f minProviders=%d providers=%s otel=%t",
		c.Port, c.RequestTimeout, c.MaxAPY, c.MinProviderCount, providers, c.OtelEndpoint != "")
}
