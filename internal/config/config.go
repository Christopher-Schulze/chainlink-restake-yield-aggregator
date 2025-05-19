// Package config provides configuration loading and management for the application.
package config

import (
	"encoding/json"
	"os"
	"strings"
	"time"
	"strconv"
)

// Config holds all application configuration
type Config struct {
	// HTTP server port
	Port string

	// The main provider to use when others fail
	PrimaryProvider string

	// Base URLs for different data providers
	EigenURL     string
	KarakURL     string
	SymbioticURL string

	// OpenTelemetry endpoint for observability
	OtelEndpoint string

	// API keys for various services
	APIKeys map[string]string

	// Timeouts and circuit breaker settings
	RequestTimeout    time.Duration
	MaxAPY            float64
	MaxTVLChange      float64
	MinProviderCount  int
	CircuitResetDelay time.Duration
}

// Load creates a new Config from environment variables
func Load() Config {
	apiKeys := map[string]string{}
	if raw := os.Getenv("API_KEYS"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &apiKeys)
	}

	return Config{
		Port:             GetEnvOrDefault("PORT", "8080"),
		PrimaryProvider:  strings.ToLower(GetEnvOrDefault("PRIMARY_PROVIDER", "eigenlayer")),
		EigenURL:         GetEnvOrDefault("EIGEN_URL", "https://api.eigenlayer.xyz/yield"),
		KarakURL:         GetEnvOrDefault("KARAK_URL", "https://karak.network/graphql"),
		SymbioticURL:     GetEnvOrDefault("SYMBIOTIC_URL", "https://api.symbiotic.finance/yield"),
		OtelEndpoint:     GetEnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		APIKeys:          apiKeys,
		RequestTimeout:   GetEnvAsDuration("REQUEST_TIMEOUT", 10*time.Second),
		MaxAPY:           GetEnvAsFloat("MAX_APY", 10.0), // 1000% max APY
		MaxTVLChange:     GetEnvAsFloat("MAX_TVL_CHANGE", 0.5), // 50% max TVL change
		MinProviderCount: GetEnvAsInt("MIN_PROVIDER_COUNT", 2),
		CircuitResetDelay: GetEnvAsDuration("CIRCUIT_RESET_DELAY", 5*time.Minute),
	}
}

// GetEnv retrieves an environment variable and whether it exists
func GetEnv(key string) (string, bool) {
	value, exists := os.LookupEnv(key)
	return value, exists
}

// GetEnvOrDefault retrieves an environment variable or returns the default value if not set
func GetEnvOrDefault(key, defaultValue string) string {
	if value, exists := GetEnv(key); exists {
		return value
	}
	return defaultValue
}

// GetEnvAsInt retrieves an environment variable as an integer with a default value
func GetEnvAsInt(key string, defaultValue int) int {
	if value, exists := GetEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// GetEnvAsFloat retrieves an environment variable as a float with a default value
func GetEnvAsFloat(key string, defaultValue float64) float64 {
	if value, exists := GetEnv(key); exists {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

// GetEnvAsDuration retrieves an environment variable as a duration with a default value
func GetEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if value, exists := GetEnv(key); exists {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
