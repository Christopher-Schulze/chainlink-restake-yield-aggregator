package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewMetric(t *testing.T) {
	m := NewMetric("defillama", 0.045, 1000.0, 1.1)
	assert.Equal(t, "defillama", m.Provider)
	assert.InDelta(t, 0.045, m.APY, 1e-9)
	assert.InDelta(t, 1000.0, m.TVL, 1e-9)
	assert.InDelta(t, 1.1, m.PointsPerETH, 1e-9)
	assert.True(t, m.CollectedAt > 0)
	assert.Equal(t, "1.0", m.Version)
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		name   string
		metric Metric
		valid  bool
	}{
		{
			name:   "valid metric",
			metric: Metric{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: time.Now().Unix()},
			valid:  true,
		},
		{
			name:   "negative APY",
			metric: Metric{Provider: "p1", APY: -0.01, TVL: 1000, PointsPerETH: 1.0, CollectedAt: time.Now().Unix()},
			valid:  false,
		},
		{
			name:   "zero TVL",
			metric: Metric{Provider: "p1", APY: 0.04, TVL: 0, PointsPerETH: 1.0, CollectedAt: time.Now().Unix()},
			valid:  false,
		},
		{
			name:   "negative PointsPerETH",
			metric: Metric{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: -1.0, CollectedAt: time.Now().Unix()},
			valid:  false,
		},
		{
			name:   "empty provider",
			metric: Metric{Provider: "", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: time.Now().Unix()},
			valid:  false,
		},
		{
			name:   "stale metric (old timestamp)",
			metric: Metric{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: time.Now().Add(-48 * time.Hour).Unix()},
			valid:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.metric.IsValid())
		})
	}
}

func TestWithConfidence(t *testing.T) {
	m := Metric{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: time.Now().Unix()}
	m2 := m.WithConfidence(0.85)
	assert.InDelta(t, 0.85, m2.Confidence, 1e-9)
	// Original should be unchanged.
	assert.InDelta(t, 0.0, m.Confidence, 1e-9)
}

func TestSchemaVersion(t *testing.T) {
	m := NewMetric("p", 0.01, 1, 1)
	assert.Equal(t, "1.0", m.Version)
}
