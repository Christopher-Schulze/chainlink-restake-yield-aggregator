// Package fetch provides provider-specific clients for retrieving yield metrics from various DeFi protocols.
package fetch

import (
	"context"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/yourorg/restake-yield-ea/internal/config"
	"github.com/yourorg/restake-yield-ea/internal/model"
)

// Client defines the interface that all provider clients must implement
type Client interface {
	// Fetch retrieves yield metrics from a specific provider
	Fetch(ctx context.Context) ([]model.Metric, error)
}

// NewClient creates a new provider client based on the provided configuration and provider name
func NewClient(cfg config.Config, provider string) Client {
	switch provider {
	case "eigenlayer":
		return NewEigenLayerClient()
	case "karak":
		return NewKarakClient()
	case "symbiotic":
		return NewSymbioticClient()
	default:
		return NewEigenLayerClient()
	}
}

// newRetryClient creates a new HTTP client with retry capabilities
func newRetryClient() *retryablehttp.Client {
	c := retryablehttp.NewClient()
	c.RetryMax = 3
	c.RetryWaitMin = 500 * time.Millisecond
	c.RetryWaitMax = 3 * time.Second
	c.Logger = nil
	return c
}

// StandardClient converts a retryablehttp.Client to a standard http.Client
func StandardClient(retryClient *retryablehttp.Client) *http.Client {
	return retryClient.StandardClient()
}

// getAPIKey retrieves an API key for a specific provider from configuration
func getAPIKey(cfg config.Config, provider string) string {
	if k, ok := cfg.APIKeys[provider]; ok {
		return k
	}
	return ""
}

// getEnvOrDefault retrieves an environment variable or returns the default value if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value, exists := config.GetEnv(key); exists {
		return value
	}
	return defaultValue
}