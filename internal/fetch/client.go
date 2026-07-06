// Package fetch provides provider-specific clients for retrieving yield
// metrics from various DeFi protocols. Each provider implements the shared
// provider.Provider interface so the server can fan out across them
// concurrently and aggregate the results.
package fetch

import (
	"context"
	"net/http"
	"time"

	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/christopher/restake-yield-ea/internal/provider"
	"github.com/hashicorp/go-retryablehttp"
)

// Compile-time assertions that each client satisfies provider.Provider.
var (
	_ provider.Provider = (*EigenLayerClient)(nil)
	_ provider.Provider = (*KarakClient)(nil)
	_ provider.Provider = (*SymbioticClient)(nil)
	_ provider.Provider = (*DefiLlamaClient)(nil)
)

// Client is the legacy alias kept for backwards-compatible factory code.
type Client = provider.Provider

// NewClient creates a new provider client based on the provided configuration
// and provider name. Unknown providers return nil so the caller can skip them
// rather than silently substituting a different provider.
func NewClient(cfg config.Config, name string) provider.Provider {
	switch name {
	case "eigenlayer":
		return NewEigenLayerClient(cfg)
	case "karak":
		return NewKarakClient(cfg)
	case "symbiotic":
		return NewSymbioticClient(cfg)
	case "defillama":
		return NewDefiLlamaClient(cfg)
	default:
		return nil
	}
}

// newRetryClient creates a retryablehttp.Client with sensible defaults for
// talking to external yield APIs: up to 3 retries with jittered backoff.
func newRetryClient() *retryablehttp.Client {
	c := retryablehttp.NewClient()
	c.RetryMax = 3
	c.RetryWaitMin = 500 * time.Millisecond
	c.RetryWaitMax = 3 * time.Second
	c.Logger = nil
	return c
}

// standardHTTPClient wraps a retryablehttp.Client into a standard *http.Client
// so providers can use the familiar net/http API surface.
func standardHTTPClient(rc *retryablehttp.Client) *http.Client {
	return rc.StandardClient()
}

// getAPIKey looks up the API key for a provider from the loaded configuration.
func getAPIKey(cfg config.Config, provider string) string {
	if k, ok := cfg.APIKeys[provider]; ok {
		return k
	}
	return ""
}

// maxResponseBytes caps the size of a successful provider response body
// before JSON decoding. 16 MiB is far above any legitimate yield-API payload
// and prevents a malicious or buggy endpoint from exhausting server memory.
const maxResponseBytes = 16 << 20 // 16 MiB

// maxErrorBodyBytes caps how much of an error response body is read for the
// error message. 4 KiB is enough for a useful diagnostic without unbounded
// reads on a 5xx page.
const maxErrorBodyBytes = 4 << 10 // 4 KiB

// Fetch is a tiny helper used by the multi-chain client to satisfy the
// provider.Provider interface via a function value.
type funcProvider struct {
	fn   func(ctx context.Context) ([]model.Metric, error)
	name string
}

func (f *funcProvider) Fetch(ctx context.Context) ([]model.Metric, error) { return f.fn(ctx) }
func (f *funcProvider) Name() string                                      { return f.name }

// FuncProvider wraps a fetch function into a provider.Provider.
func FuncProvider(name string, fn func(ctx context.Context) ([]model.Metric, error)) provider.Provider {
	return &funcProvider{name: name, fn: fn}
}
