// Package provider defines the shared Provider interface used across the
// restake-yield-ea. It lives in its own package so that both the HTTP server
// (package main) and the multi-chain fetch layer (package fetch) can depend on
// it without creating an import cycle.
package provider

import (
	"context"

	"github.com/christopher/restake-yield-ea/internal/model"
)

// Provider is the contract every yield data source must implement.
//
// Implementations must be safe for concurrent use: the server calls Fetch from
// multiple goroutines (one per provider) and the multi-chain client fans out
// across chains, each of which may itself fan out across providers.
type Provider interface {
	// Fetch retrieves one or more yield metrics for the given chain/provider.
	// The context carries the request deadline; implementations must honour it.
	Fetch(ctx context.Context) ([]model.Metric, error)

	// Name returns a stable, lowercase identifier for the provider (e.g.
	// "defillama", "eigenlayer"). It is used for logging, Prometheus labels
	// and as the Provider field on emitted metrics.
	Name() string
}

// Adapter wraps a function into a Provider, useful for tests and mocks.
type Adapter struct {
	Fn   func(ctx context.Context) ([]model.Metric, error)
	Name_ string //nolint:revive // API: renamed to avoid collision with Provider.Name() method
}

func (a Adapter) Fetch(ctx context.Context) ([]model.Metric, error) { return a.Fn(ctx) }
func (a Adapter) Name() string                                       { return a.Name_ }
