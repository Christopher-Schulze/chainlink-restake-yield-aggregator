package config

import (
	"os"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/envx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withEnv sets an env var for the duration of the test and restores the
// original value on cleanup.
func withEnv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t,
		"PORT", "EIGENLAYER_API", "KARAK_API", "SYMBIOTIC_API",
		"DEFILLAMA_API", "DEFILLAMA_YIELDS_API", "DEFILLAMA_PRICES_API",
		"OTEL_ENDPOINT", "API_KEYS", "TIMEOUT", "MAX_APY_THRESHOLD",
		"MAX_TVL_CHANGE", "MIN_PROVIDERS", "CIRCUIT_RESET_DELAY",
		"ENABLED_PROVIDERS",
	)

	cfg := Load()

	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, "", cfg.EigenURL)
	assert.Equal(t, "", cfg.KarakURL)
	assert.Equal(t, "", cfg.SymbioticURL)
	assert.Equal(t, "https://api.llama.fi", cfg.DefiLlamaURL)
	assert.Equal(t, "https://yields.llama.fi", cfg.DefiLlamaYields)
	assert.Equal(t, "https://coins.llama.fi", cfg.DefiLlamaPrices)
	assert.Equal(t, "", cfg.OtelEndpoint)
	assert.Equal(t, 10*time.Second, cfg.RequestTimeout)
	assert.InDelta(t, 1.0, cfg.MaxAPY, 1e-9)
	assert.InDelta(t, 0.5, cfg.MaxTVLChange, 1e-9)
	assert.Equal(t, 2, cfg.MinProviderCount)
	assert.Equal(t, 5*time.Minute, cfg.CircuitResetDelay)
	assert.Equal(t, int64(300), cfg.MaxStaleSeconds)
	assert.Nil(t, cfg.EnabledProviders)
}

func TestLoadOverrides(t *testing.T) {
	withEnv(t, "PORT", "9090")
	withEnv(t, "EIGENLAYER_API", "https://api.eigenlayer.xyz")
	withEnv(t, "KARAK_API", "https://api.karak.xyz")
	withEnv(t, "SYMBIOTIC_API", "https://api.symbiotic.xyz")
	withEnv(t, "DEFILLAMA_API", "https://custom.llama.fi")
	withEnv(t, "DEFILLAMA_YIELDS_API", "https://custom-yields.llama.fi")
	withEnv(t, "DEFILLAMA_PRICES_API", "https://custom-coins.llama.fi")
	withEnv(t, "OTEL_ENDPOINT", "http://collector:4318")
	withEnv(t, "TIMEOUT", "30s")
	withEnv(t, "MAX_APY_THRESHOLD", "0.5")
	withEnv(t, "MAX_TVL_CHANGE", "0.25")
	withEnv(t, "MIN_PROVIDERS", "3")
	withEnv(t, "CIRCUIT_RESET_DELAY", "10m")
	withEnv(t, "MAX_STALE_SECONDS", "600")
	withEnv(t, "ENABLED_PROVIDERS", "defillama, eigenlayer , karak")

	cfg := Load()

	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, "https://api.eigenlayer.xyz", cfg.EigenURL)
	assert.Equal(t, "https://api.karak.xyz", cfg.KarakURL)
	assert.Equal(t, "https://api.symbiotic.xyz", cfg.SymbioticURL)
	assert.Equal(t, "https://custom.llama.fi", cfg.DefiLlamaURL)
	assert.Equal(t, "https://custom-yields.llama.fi", cfg.DefiLlamaYields)
	assert.Equal(t, "https://custom-coins.llama.fi", cfg.DefiLlamaPrices)
	assert.Equal(t, "http://collector:4318", cfg.OtelEndpoint)
	assert.Equal(t, 30*time.Second, cfg.RequestTimeout)
	assert.InDelta(t, 0.5, cfg.MaxAPY, 1e-9)
	assert.InDelta(t, 0.25, cfg.MaxTVLChange, 1e-9)
	assert.Equal(t, 3, cfg.MinProviderCount)
	assert.Equal(t, 10*time.Minute, cfg.CircuitResetDelay)
	assert.Equal(t, int64(600), cfg.MaxStaleSeconds)
	assert.Equal(t, []string{"defillama", "eigenlayer", "karak"}, cfg.EnabledProviders)
}

func TestLoadAPIKeys(t *testing.T) {
	withEnv(t, "API_KEYS", `{"eigenlayer":"key1","karak":"key2"}`)

	cfg := Load()

	require.Len(t, cfg.APIKeys, 2)
	assert.Equal(t, "key1", cfg.APIKeys["eigenlayer"])
	assert.Equal(t, "key2", cfg.APIKeys["karak"])
}

func TestLoadAPIKeysMalformed(t *testing.T) {
	withEnv(t, "API_KEYS", `{invalid json}`)

	cfg := Load()

	// Malformed JSON should result in an empty (but non-nil) map.
	assert.NotNil(t, cfg.APIKeys)
	assert.Empty(t, cfg.APIKeys)
}

func TestLoadInvalidInt64(t *testing.T) {
	withEnv(t, "MAX_STALE_SECONDS", "not-a-number")

	cfg := Load()

	// Invalid int64 should fall back to the default (300).
	assert.Equal(t, int64(300), cfg.MaxStaleSeconds)
}

func TestLoadEnabledProvidersTrimmed(t *testing.T) {
	withEnv(t, "ENABLED_PROVIDERS", "  defillama  ,  eigenlayer  ,,  karak  ")

	cfg := Load()

	assert.Equal(t, []string{"defillama", "eigenlayer", "karak"}, cfg.EnabledProviders)
}

func TestLoadEnabledProvidersEmpty(t *testing.T) {
	withEnv(t, "ENABLED_PROVIDERS", "")

	cfg := Load()

	assert.Nil(t, cfg.EnabledProviders)
}

func TestConfigString(t *testing.T) {
	cfg := Config{
		Port:             "8080",
		RequestTimeout:   10 * time.Second,
		MaxAPY:           1.0,
		MinProviderCount: 2,
		OtelEndpoint:     "http://otel:4318",
	}
	s := cfg.String()
	assert.Contains(t, s, "port=8080")
	assert.Contains(t, s, "timeout=10s")
	assert.Contains(t, s, "providers=all")
	assert.Contains(t, s, "otel=true")
}

func TestConfigStringWithProviders(t *testing.T) {
	cfg := Config{
		Port:             "8080",
		RequestTimeout:   10 * time.Second,
		MaxAPY:           1.0,
		MinProviderCount: 2,
		EnabledProviders: []string{"defillama", "eigenlayer"},
	}
	s := cfg.String()
	assert.Contains(t, s, "providers=defillama,eigenlayer")
	assert.Contains(t, s, "otel=false")
}

func TestGetEnvIntInvalid(t *testing.T) {
	withEnv(t, "MIN_PROVIDERS", "not-a-number")
	// Should fall back to default with a warning.
	cfg := Load()
	assert.Equal(t, 2, cfg.MinProviderCount)
}

func TestGetEnvFloatInvalid(t *testing.T) {
	withEnv(t, "MAX_APY_THRESHOLD", "not-a-float")
	cfg := Load()
	assert.InDelta(t, 1.0, cfg.MaxAPY, 1e-9)
}

func TestGetEnvDurationInvalid(t *testing.T) {
	withEnv(t, "TIMEOUT", "not-a-duration")
	cfg := Load()
	assert.Equal(t, 10*time.Second, cfg.RequestTimeout)
}

func TestGetEnvBoolValid(t *testing.T) {
	withEnv(t, "SOME_BOOL", "true")
	assert.True(t, envx.Bool("SOME_BOOL", false))
	withEnv(t, "SOME_BOOL", "false")
	assert.False(t, envx.Bool("SOME_BOOL", true))
	withEnv(t, "SOME_BOOL", "1")
	assert.True(t, envx.Bool("SOME_BOOL", false))
	withEnv(t, "SOME_BOOL", "0")
	assert.False(t, envx.Bool("SOME_BOOL", true))
}

func TestGetEnvBoolInvalid(t *testing.T) {
	withEnv(t, "SOME_BOOL", "not-a-bool")
	assert.True(t, envx.Bool("SOME_BOOL", true))
}

func TestGetEnvBoolMissing(t *testing.T) {
	clearEnv(t, "SOME_BOOL")
	assert.True(t, envx.Bool("SOME_BOOL", true))
	assert.False(t, envx.Bool("SOME_BOOL", false))
}
