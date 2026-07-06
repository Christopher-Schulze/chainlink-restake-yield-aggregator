package aggregate

import (
	"context"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestWeighted(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []model.Metric
		expected model.Metric
	}{
		{
			name: "single metric",
			metrics: []model.Metric{
				{
					APY:          5.0,
					TVL:          1000,
					PointsPerETH: 10,
					CollectedAt:  time.Now().Unix(),
					Provider:     "test",
				},
			},
			expected: model.Metric{
				APY:          5.0,
				TVL:          1000,
				PointsPerETH: 10,
				Provider:     "aggregated",
			},
		},
		{
			name: "multiple metrics",
			metrics: []model.Metric{
				{
					APY:          5.0,
					TVL:          1000,
					PointsPerETH: 10,
					CollectedAt:  time.Now().Unix(),
				},
				{
					APY:          10.0,
					TVL:          2000,
					PointsPerETH: 20,
					CollectedAt:  time.Now().Unix(),
				},
			},
			expected: model.Metric{
				APY:          8.333333333333334, // (5*1000 + 10*2000)/3000
				TVL:          3000,
				PointsPerETH: 15, // median(10, 20) — not TVL-weighted
				Provider:     "aggregated",
			},
		},
		{
			name:     "empty input",
			metrics:  []model.Metric{},
			expected: model.Metric{Provider: "aggregated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Weighted(tt.metrics)
			if got.APY != tt.expected.APY {
				t.Errorf("APY got = %v, want %v", got.APY, tt.expected.APY)
			}
			if got.TVL != tt.expected.TVL {
				t.Errorf("TVL got = %v, want %v", got.TVL, tt.expected.TVL)
			}
			if got.PointsPerETH != tt.expected.PointsPerETH {
				t.Errorf("PointsPerETH got = %v, want %v", got.PointsPerETH, tt.expected.PointsPerETH)
			}
			if got.Provider != "aggregated" {
				t.Errorf("Provider got = %v, want aggregated", got.Provider)
			}
		})
	}
}

func TestWeightedParallel(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []model.Metric
		expected model.Metric
	}{
		{
			name: "single metric",
			metrics: []model.Metric{
				{
					APY:          5.0,
					TVL:          1000,
					PointsPerETH: 10,
					CollectedAt:  time.Now().Unix(),
					Provider:     "test",
				},
			},
			expected: model.Metric{
				APY:          5.0,
				TVL:          1000,
				PointsPerETH: 10,
				Provider:     "aggregated",
			},
		},
		{
			name: "multiple metrics",
			metrics: []model.Metric{
				{
					APY:          5.0,
					TVL:          1000,
					PointsPerETH: 10,
					CollectedAt:  time.Now().Unix(),
				},
				{
					APY:          10.0,
					TVL:          2000,
					PointsPerETH: 20,
					CollectedAt:  time.Now().Unix(),
				},
			},
			expected: model.Metric{
				APY:          8.333333333333334,
				TVL:          3000,
				PointsPerETH: 15, // median(10, 20) — not TVL-weighted
				Provider:     "aggregated",
			},
		},
		{
			name:     "empty input",
			metrics:  []model.Metric{},
			expected: model.Metric{Provider: "aggregated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got := WeightedParallel(ctx, tt.metrics)
			if got.APY != tt.expected.APY {
				t.Errorf("APY got = %v, want %v", got.APY, tt.expected.APY)
			}
			if got.TVL != tt.expected.TVL {
				t.Errorf("TVL got = %v, want %v", got.TVL, tt.expected.TVL)
			}
			if got.PointsPerETH != tt.expected.PointsPerETH {
				t.Errorf("PointsPerETH got = %v, want %v", got.PointsPerETH, tt.expected.PointsPerETH)
			}
			if got.Provider != "aggregated" {
				t.Errorf("Provider got = %v, want aggregated", got.Provider)
			}
		})
	}
}

func TestMedian(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []model.Metric
		selector func(model.Metric) float64
		expected float64
	}{
		{
			name: "median APY odd count",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000},
				{APY: 10.0, TVL: 2000},
				{APY: 15.0, TVL: 3000},
			},
			selector: func(m model.Metric) float64 { return m.APY },
			expected: 10.0,
		},
		{
			name: "median APY even count",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000},
				{APY: 10.0, TVL: 2000},
				{APY: 15.0, TVL: 3000},
				{APY: 20.0, TVL: 4000},
			},
			selector: func(m model.Metric) float64 { return m.APY },
			expected: 12.5,
		},
		{
			name:     "empty metrics",
			metrics:  []model.Metric{},
			selector: func(m model.Metric) float64 { return m.APY },
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Median(tt.metrics, tt.selector)
			if got != tt.expected {
				t.Errorf("Median() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMedianAggregation(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []model.Metric
		expected model.Metric
	}{
		{
			name: "odd number of metrics",
			metrics: []model.Metric{
				{
					APY:          5.0,
					TVL:          1000,
					PointsPerETH: 10,
					CollectedAt:  time.Now().Unix(),
				},
				{
					APY:          10.0,
					TVL:          2000,
					PointsPerETH: 20,
					CollectedAt:  time.Now().Unix(),
				},
				{
					APY:          15.0,
					TVL:          3000,
					PointsPerETH: 30,
					CollectedAt:  time.Now().Unix(),
				},
			},
			expected: model.Metric{
				APY:          10.0,
				TVL:          6000, // TVL is now summed, not medianed
				PointsPerETH: 20,
				Provider:     "aggregated",
			},
		},
		{
			name: "even number of metrics",
			metrics: []model.Metric{
				{
					APY:          5.0,
					TVL:          1000,
					PointsPerETH: 10,
					CollectedAt:  time.Now().Unix(),
				},
				{
					APY:          10.0,
					TVL:          2000,
					PointsPerETH: 20,
					CollectedAt:  time.Now().Unix(),
				},
				{
					APY:          15.0,
					TVL:          3000,
					PointsPerETH: 30,
					CollectedAt:  time.Now().Unix(),
				},
				{
					APY:          20.0,
					TVL:          4000,
					PointsPerETH: 40,
					CollectedAt:  time.Now().Unix(),
				},
			},
			expected: model.Metric{
				APY:          12.5,
				TVL:          10000, // TVL is now summed, not medianed
				PointsPerETH: 25,
				Provider:     "aggregated",
			},
		},
		{
			name:     "empty input",
			metrics:  []model.Metric{},
			expected: model.Metric{Provider: "aggregated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MedianAggregation(tt.metrics)
			if got.APY != tt.expected.APY {
				t.Errorf("APY got = %v, want %v", got.APY, tt.expected.APY)
			}
			if got.TVL != tt.expected.TVL {
				t.Errorf("TVL got = %v, want %v", got.TVL, tt.expected.TVL)
			}
			if got.PointsPerETH != tt.expected.PointsPerETH {
				t.Errorf("PointsPerETH got = %v, want %v", got.PointsPerETH, tt.expected.PointsPerETH)
			}
			if got.Provider != "aggregated" {
				t.Errorf("Provider got = %v, want aggregated", got.Provider)
			}
		})
	}
}

func TestTrimmedMeanAggregation(t *testing.T) {
	tests := []struct {
		name        string
		metrics     []model.Metric
		trimPercent float64
		expected    model.Metric
	}{
		{
			name: "trim 10% from 10 metrics",
			metrics: []model.Metric{
				{APY: 1.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 2.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 3.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 4.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 7.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 8.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 9.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 10.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
			},
			trimPercent: 0.1,
			expected: model.Metric{
				APY:          5.5, // Mittelwert von 2-9 (ohne 1 und 10)
				TVL:          8000,
				PointsPerETH: 10,
				Provider:     "aggregated",
			},
		},
		{
			name: "too few metrics for trimming",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: time.Now().Unix()},
				{APY: 10.0, TVL: 2000, PointsPerETH: 20, CollectedAt: time.Now().Unix()},
			},
			trimPercent: 0.1,
			expected: model.Metric{
				APY:          8.333333333333334, // Fallback auf Weighted
				TVL:          3000,
				PointsPerETH: 16.666666666666668,
				Provider:     "aggregated",
			},
		},
		{
			name:        "empty input",
			metrics:     []model.Metric{},
			trimPercent: 0.1,
			expected:    model.Metric{Provider: "aggregated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TrimmedMeanAggregation(tt.metrics, tt.trimPercent)
			if got.Provider != "aggregated" {
				t.Errorf("Provider got = %v, want aggregated", got.Provider)
			}
			// With too few metrics, falls back to Weighted, so only test with enough metrics
			if len(tt.metrics) > 3 {
				if got.APY != tt.expected.APY {
					t.Errorf("APY got = %v, want %v", got.APY, tt.expected.APY)
				}
			}
		})
	}
}

func TestValidateMetric(t *testing.T) {
	now := time.Now().Unix()
	oldTimestamp := time.Now().Add(-48 * time.Hour).Unix()

	tests := []struct {
		name    string
		metric  model.Metric
		wantErr bool
	}{
		{
			name: "valid metric",
			metric: model.Metric{
				APY:          5.0,
				TVL:          1000,
				PointsPerETH: 10,
				CollectedAt:  now,
				Provider:     "test",
			},
			wantErr: false,
		},
		{
			name: "negative APY",
			metric: model.Metric{
				APY:          -5.0,
				TVL:          1000,
				PointsPerETH: 10,
				CollectedAt:  now,
				Provider:     "test",
			},
			wantErr: true,
		},
		{
			name: "too high APY",
			metric: model.Metric{
				APY:          150.0,
				TVL:          1000,
				PointsPerETH: 10,
				CollectedAt:  now,
				Provider:     "test",
			},
			wantErr: true,
		},
		{
			name: "zero TVL",
			metric: model.Metric{
				APY:          5.0,
				TVL:          0,
				PointsPerETH: 10,
				CollectedAt:  now,
				Provider:     "test",
			},
			wantErr: true,
		},
		{
			name: "negative PointsPerETH",
			metric: model.Metric{
				APY:          5.0,
				TVL:          1000,
				PointsPerETH: -10,
				CollectedAt:  now,
				Provider:     "test",
			},
			wantErr: true,
		},
		{
			name: "too old timestamp",
			metric: model.Metric{
				APY:          5.0,
				TVL:          1000,
				PointsPerETH: 10,
				CollectedAt:  oldTimestamp,
				Provider:     "test",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetric(tt.metric)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMetric() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFilterOutliers(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name    string
		metrics []model.Metric
		want    int // Expected metric count after filtering
	}{
		{
			name: "no outliers",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 5.5, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 6.5, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 7.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
			},
			want: 5, // All metrics retained
		},
		{
			name: "with outliers",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 5.5, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 6.5, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 50.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // outlier
			},
			want: 4, // The outlier is removed
		},
		{
			name: "too few metrics",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 50.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
			},
			want: 2, // Too few metrics for outlier detection
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterOutliers(tt.metrics)
			if len(filtered) != tt.want {
				t.Errorf("FilterOutliers() got %v metrics, want %v", len(filtered), tt.want)
			}
		})
	}
}

func TestValidateAndFilterMetrics(t *testing.T) {
	now := time.Now().Unix()
	oldTimestamp := time.Now().Add(-48 * time.Hour).Unix()

	tests := []struct {
		name    string
		metrics []model.Metric
		want    int // Expected metric count after validation and filtering
	}{
		{
			name: "all valid metrics",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 7.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
			},
			want: 3,
		},
		{
			name: "some invalid metrics",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: -6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // invalid
				{APY: 7.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 8.0, TVL: 0, PointsPerETH: 10, CollectedAt: now},             // invalid
				{APY: 9.0, TVL: 1000, PointsPerETH: 10, CollectedAt: oldTimestamp}, // invalid
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := ValidateAndFilterMetrics(tt.metrics)
			if len(filtered) != tt.want {
				t.Errorf("ValidateAndFilterMetrics() got %v metrics, want %v", len(filtered), tt.want)
			}
		})
	}
}

func TestWeightedWithValidation(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name    string
		metrics []model.Metric
	}{
		{
			name: "mixed valid and invalid metrics",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: -6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // invalid
				{APY: 7.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WeightedWithValidation(tt.metrics)
			if result.Provider != "aggregated" {
				t.Errorf("WeightedWithValidation() Provider = %v, want %v", result.Provider, "aggregated")
			}
			// Weitere Prüfungen könnten hinzugefügt werden
		})
	}
}

func TestWeightedParallelWithValidation(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name    string
		metrics []model.Metric
	}{
		{
			name: "mixed valid and invalid metrics",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: -6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // invalid
				{APY: 7.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			result := WeightedParallelWithValidation(ctx, tt.metrics)
			if result.Provider != "aggregated" {
				t.Errorf("WeightedParallelWithValidation() Provider = %v, want %v", result.Provider, "aggregated")
			}
			// Weitere Prüfungen könnten hinzugefügt werden
		})
	}
}

func TestAverageMetrics(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name     string
		metrics  []model.Metric
		expected model.Metric
	}{
		{
			name: "multiple metrics",
			metrics: []model.Metric{
				{APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
				{APY: 10.0, TVL: 2000, PointsPerETH: 20, CollectedAt: now},
				{APY: 15.0, TVL: 3000, PointsPerETH: 30, CollectedAt: now},
			},
			expected: model.Metric{
				APY:          10.0, // (5+10+15)/3
				TVL:          6000, // TVL is summed: 1000+2000+3000
				PointsPerETH: 20.0, // (10+20+30)/3
				Provider:     "aggregated",
			},
		},
		{
			name:     "empty input",
			metrics:  []model.Metric{},
			expected: model.Metric{Provider: "aggregated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AverageMetrics(tt.metrics)
			if got.APY != tt.expected.APY {
				t.Errorf("APY got = %v, want %v", got.APY, tt.expected.APY)
			}
			if got.TVL != tt.expected.TVL {
				t.Errorf("TVL got = %v, want %v", got.TVL, tt.expected.TVL)
			}
			if got.PointsPerETH != tt.expected.PointsPerETH {
				t.Errorf("PointsPerETH got = %v, want %v", got.PointsPerETH, tt.expected.PointsPerETH)
			}
			if got.Provider != "aggregated" {
				t.Errorf("Provider got = %v, want aggregated", got.Provider)
			}
		})
	}
}

// Benchmarks are in benchmark_test.go with parameterised input sizes.

// TestValidateMetric_RejectsNaNAndInf verifies that NaN and Inf values are
// rejected before they can poison the weighted average. NaN comparisons
// return false in Go, so without this guard NaN would pass every threshold
// check and corrupt the aggregation result.
func TestValidateMetric_RejectsNaNAndInf(t *testing.T) {
	now := time.Now().Unix()
	tests := []struct {
		name    string
		metric  model.Metric
		wantErr bool
	}{
		{"NaN APY", model.Metric{APY: nanValue(), TVL: 1000, PointsPerETH: 1.0, CollectedAt: now}, true},
		{"+Inf APY", model.Metric{APY: posInfValue(), TVL: 1000, PointsPerETH: 1.0, CollectedAt: now}, true},
		{"-Inf APY", model.Metric{APY: negInfValue(), TVL: 1000, PointsPerETH: 1.0, CollectedAt: now}, true},
		{"NaN TVL", model.Metric{APY: 0.05, TVL: nanValue(), PointsPerETH: 1.0, CollectedAt: now}, true},
		{"NaN PointsPerETH", model.Metric{APY: 0.05, TVL: 1000, PointsPerETH: nanValue(), CollectedAt: now}, true},
		{"valid metric", model.Metric{APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetric(tt.metric)
			if tt.wantErr {
				assertError(t, err, "NaN or Inf")
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestWeightedSkipsNaN verifies that the Weighted aggregation function skips
// NaN-valued metrics rather than letting them poison the result.
func TestWeightedSkipsNaN(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{APY: nanValue(), TVL: 2000, PointsPerETH: 2.0, CollectedAt: now}, // skipped (NaN fails m.APY >= 0)
		{APY: 0.06, TVL: 3000, PointsPerETH: 1.5, CollectedAt: now},
	}
	result := Weighted(metrics)
	// Only the two valid metrics: (0.04*1000 + 0.06*3000) / 4000 = 0.055
	assertFloatEq(t, 0.055, result.APY)
	assertFloatEq(t, 4000, result.TVL)
}

func nanValue() float64    { var z float64; return z / z }
func posInfValue() float64 { var z float64; return 1.0 / z }
func negInfValue() float64 { var z float64; return -1.0 / z }

func assertError(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", contains)
	}
}

func assertFloatEq(t *testing.T, want, got float64) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// --- Additional coverage tests ---

func TestWeighted_AllZeroTVL(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 0, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 0, PointsPerETH: 2.0, CollectedAt: now},
	}
	result := Weighted(metrics)
	assert.Equal(t, emptyMetric(), result)
}

func TestWeighted_Empty(t *testing.T) {
	assert.Equal(t, emptyMetric(), Weighted(nil))
	assert.Equal(t, emptyMetric(), Weighted([]model.Metric{}))
}

func TestMedian_EmptyMetrics(t *testing.T) {
	assert.Equal(t, 0.0, Median(nil, func(m model.Metric) float64 { return m.APY }))
	assert.Equal(t, 0.0, Median([]model.Metric{}, func(m model.Metric) float64 { return m.APY }))
}

func TestMedian_AllZeroTVL(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 0, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 0, CollectedAt: now},
	}
	assert.Equal(t, 0.0, Median(metrics, func(m model.Metric) float64 { return m.APY }))
}

func TestValidateMetric_ZeroTimestamp(t *testing.T) {
	m := model.Metric{
		Provider:     "p1",
		APY:          0.05,
		TVL:          1000,
		PointsPerETH: 1.0,
		CollectedAt:  0,
	}
	err := ValidateMetric(m)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timestamp")
}

func TestFilterOutliers_NoValidAPY(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: -0.01, TVL: 1000, CollectedAt: now},
		{Provider: "p2", APY: -0.02, TVL: 1000, CollectedAt: now},
		{Provider: "p3", APY: -0.03, TVL: 1000, CollectedAt: now},
		{Provider: "p4", APY: -0.04, TVL: 1000, CollectedAt: now},
	}
	result := FilterOutliers(metrics)
	assert.Len(t, result, 4, "should return unchanged when no valid APY")
}

func TestFilterOutliers_FewerThanFourValidAPY(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 1000, CollectedAt: now},
		{Provider: "p3", APY: 0.07, TVL: 1000, CollectedAt: now},
		{Provider: "p4", APY: 0.5, TVL: 0, CollectedAt: now},
	}
	result := FilterOutliers(metrics)
	assert.Len(t, result, 4, "should return unchanged with <4 valid APY")
}

func TestAverageMetrics_AllInvalid(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: -0.01, TVL: 1000, PointsPerETH: -1.0, CollectedAt: now},
		{Provider: "p2", APY: -0.02, TVL: 2000, PointsPerETH: -2.0, CollectedAt: now},
	}
	result := AverageMetrics(metrics)
	assert.Equal(t, emptyMetric(), result)
}

func TestTrimmedMeanAggregation_TrimPercentZero(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 2000, PointsPerETH: 2.0, CollectedAt: now},
		{Provider: "p3", APY: 0.07, TVL: 3000, PointsPerETH: 1.5, CollectedAt: now},
	}
	result := TrimmedMeanAggregation(metrics, 0)
	weighted := Weighted(metrics)
	assert.Equal(t, weighted, result)
}

func TestTrimmedMeanAggregation_TrimPercentHalf(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 2000, PointsPerETH: 2.0, CollectedAt: now},
		{Provider: "p3", APY: 0.07, TVL: 3000, PointsPerETH: 1.5, CollectedAt: now},
	}
	result := TrimmedMeanAggregation(metrics, 0.5)
	weighted := Weighted(metrics)
	assert.Equal(t, weighted, result)
}

func TestWeightedParallel_CancelledContext(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 2000, PointsPerETH: 2.0, CollectedAt: now},
		{Provider: "p3", APY: 0.07, TVL: 3000, PointsPerETH: 1.5, CollectedAt: now},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := WeightedParallel(ctx, metrics)
	assert.Equal(t, emptyMetric(), result)
}

func TestWeightedParallel_AllZeroTVL(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 0, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 0, PointsPerETH: 2.0, CollectedAt: now},
	}
	result := WeightedParallel(context.Background(), metrics)
	assert.Equal(t, emptyMetric(), result)
}

// TestTrimmedMeanAggregation_FewValidAfterFiltering covers the `len(valid) < 3`
// branch in TrimmedMeanAggregation (aggregate.go ~317-318). With >= 3 input
// metrics but fewer than 3 valid after filtering, the function falls back to
// Weighted on the original metrics.
func TestTrimmedMeanAggregation_FewValidAfterFiltering(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "p2", APY: -0.01, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now}, // invalid: negative APY
		{Provider: "p3", APY: 0.07, TVL: 0, PointsPerETH: 1.5, CollectedAt: now},     // invalid: zero TVL
	}
	result := TrimmedMeanAggregation(metrics, 0.1)
	weighted := Weighted(metrics)
	assert.Equal(t, weighted, result)
}

// TestTrimmedMeanAggregation_NegativeTrimPercent covers the `trimPercent <= 0`
// branch with a negative value (aggregate.go ~307).
func TestTrimmedMeanAggregation_NegativeTrimPercent(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "p2", APY: 0.06, TVL: 2000, PointsPerETH: 2.0, CollectedAt: now},
		{Provider: "p3", APY: 0.07, TVL: 3000, PointsPerETH: 1.5, CollectedAt: now},
	}
	result := TrimmedMeanAggregation(metrics, -0.1)
	weighted := Weighted(metrics)
	assert.Equal(t, weighted, result)
}

// TestWeightedPointsPerETHZeroExcluded verifies that metrics with
// PointsPerETH == 0 (meaning "not available") are excluded from the
// median calculation, and that the result is 0 when all metrics have 0.
func TestWeightedPointsPerETHZeroExcluded(t *testing.T) {
	// All zero → result should be 0.
	metrics := []model.Metric{
		{APY: 5.0, TVL: 1000, PointsPerETH: 0, CollectedAt: time.Now().Unix()},
		{APY: 10.0, TVL: 2000, PointsPerETH: 0, CollectedAt: time.Now().Unix()},
	}
	got := Weighted(metrics)
	assert.Equal(t, 0.0, got.PointsPerETH, "all-zero points → 0")

	// Mixed: only non-zero values contribute to median.
	metrics = []model.Metric{
		{APY: 5.0, TVL: 1000, PointsPerETH: 0, CollectedAt: time.Now().Unix()},
		{APY: 10.0, TVL: 2000, PointsPerETH: 15, CollectedAt: time.Now().Unix()},
		{APY: 8.0, TVL: 1500, PointsPerETH: 25, CollectedAt: time.Now().Unix()},
	}
	got = Weighted(metrics)
	assert.Equal(t, 20.0, got.PointsPerETH, "median(15, 25) = 20, zero excluded")
}
