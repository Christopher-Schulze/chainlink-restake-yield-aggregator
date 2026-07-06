package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdapterFetch(t *testing.T) {
	expected := []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0}}
	adapter := Adapter{
		Name_: "test-adapter",
		Fn: func(_ context.Context) ([]model.Metric, error) {
			return expected, nil
		},
	}

	assert.Equal(t, "test-adapter", adapter.Name())

	metrics, err := adapter.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, expected, metrics)
}

func TestAdapterFetchError(t *testing.T) {
	adapter := Adapter{
		Name_: "err-adapter",
		Fn: func(_ context.Context) ([]model.Metric, error) {
			return nil, errors.New("boom")
		},
	}

	_, err := adapter.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestAdapterName(t *testing.T) {
	adapter := Adapter{Name_: "my-provider"}
	assert.Equal(t, "my-provider", adapter.Name())
}

func TestAdapterEmptyName(t *testing.T) {
	adapter := Adapter{}
	assert.Equal(t, "", adapter.Name())
}
