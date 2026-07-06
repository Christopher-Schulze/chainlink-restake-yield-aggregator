package validation

import (
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterInvalid_BasicCriteria(t *testing.T) {
	now := time.Now().Unix()
	yesterdayTs := time.Now().Add(-23 * time.Hour).Unix()
	oldTs := time.Now().Add(-48 * time.Hour).Unix()

	tests := []struct {
		name    string
		metrics []model.Metric
		want    int // expected count of valid metrics
	}{
		{
			name: "all valid metrics",
			metrics: []model.Metric{
				{Provider: "provider1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
				{Provider: "provider2", APY: 0.08, TVL: 2000, PointsPerETH: 2.0, CollectedAt: now},
				{Provider: "provider3", APY: 0.03, TVL: 3000, PointsPerETH: 1.5, CollectedAt: yesterdayTs},
			},
			want: 3,
		},
		{
			name: "some invalid metrics",
			metrics: []model.Metric{
				{Provider: "provider1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
				{Provider: "provider2", APY: -0.01, TVL: 2000, PointsPerETH: 2.0, CollectedAt: now},  // negative APY
				{Provider: "provider3", APY: 0.03, TVL: 0, PointsPerETH: 1.5, CollectedAt: now},      // zero TVL
				{Provider: "provider4", APY: 0.07, TVL: 5000, PointsPerETH: -1.0, CollectedAt: now},  // negative PPE
				{Provider: "provider5", APY: 0.02, TVL: 1500, PointsPerETH: 0.5, CollectedAt: oldTs}, // too old
				{Provider: "", APY: 0.04, TVL: 2500, PointsPerETH: 1.2, CollectedAt: now},            // empty provider
			},
			want: 1,
		},
		{
			name:    "empty input",
			metrics: []model.Metric{},
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterInvalid(tt.metrics)
			assert.Len(t, filtered, tt.want)
		})
	}
}

func TestFilterInvalidWithOptions_CustomSettings(t *testing.T) {
	now := time.Now().Unix()

	// Create custom validation options
	customOpts := ValidationOptions{
		MaxAge:                      12 * time.Hour,
		MinTVL:                      2000, // higher minimum TVL
		MaxAPY:                      2.0,  // 200% max APY
		RequirePositivePointsPerETH: true,
		EnableOutlierDetection:      false, // disable outlier detection
		OutlierIQRMultiplier:        1.5,
	}

	metrics := []model.Metric{
		{Provider: "provider1", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},         // fails MinTVL
		{Provider: "provider2", APY: 0.08, TVL: 2500, PointsPerETH: 2.0, CollectedAt: now},         // valid
		{Provider: "provider3", APY: 1.5, TVL: 3000, PointsPerETH: 1.5, CollectedAt: now},          // valid
		{Provider: "provider4", APY: 3.0, TVL: 4000, PointsPerETH: 1.0, CollectedAt: now},          // exceeds MaxAPY
		{Provider: "provider5", APY: 0.10, TVL: 5000, PointsPerETH: 0.5, CollectedAt: now - 46000}, // too old (13 hours)
	}

	filtered := FilterInvalidWithOptions(metrics, customOpts)
	assert.Len(t, filtered, 2)

	// Verify correct metrics were kept
	providers := make(map[string]bool)
	for _, m := range filtered {
		providers[m.Provider] = true
	}
	assert.True(t, providers["provider2"])
	assert.True(t, providers["provider3"])
}

func TestFilterOutliers(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name    string
		metrics []model.Metric
		want    int // expected count after filtering
	}{
		{
			name: "no outliers",
			metrics: []model.Metric{
				{Provider: "provider1", APY: 0.05, TVL: 1000, CollectedAt: now},
				{Provider: "provider2", APY: 0.055, TVL: 1200, CollectedAt: now},
				{Provider: "provider3", APY: 0.048, TVL: 900, CollectedAt: now},
				{Provider: "provider4", APY: 0.052, TVL: 1100, CollectedAt: now},
			},
			want: 4, // all should pass
		},
		{
			name: "with outliers",
			metrics: []model.Metric{
				{Provider: "provider1", APY: 0.05, TVL: 1000, CollectedAt: now},
				{Provider: "provider2", APY: 0.052, TVL: 1200, CollectedAt: now},
				{Provider: "provider3", APY: 0.048, TVL: 900, CollectedAt: now},
				{Provider: "provider4", APY: 0.3, TVL: 1100, CollectedAt: now}, // extreme outlier
			},
			want: 3, // outlier should be filtered
		},
		{
			name: "too few for outlier detection",
			metrics: []model.Metric{
				{Provider: "provider1", APY: 0.05, TVL: 1000, CollectedAt: now},
				{Provider: "provider2", APY: 0.2, TVL: 1200, CollectedAt: now}, // would be outlier in larger dataset
			},
			want: 2, // not enough data points for outlier detection
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultValidationOptions()
			opts.EnableOutlierDetection = true

			filtered := FilterInvalidWithOptions(tt.metrics, opts)
			assert.Len(t, filtered, tt.want)
		})
	}
}

func TestFilterInvalidConcurrently(t *testing.T) {
	// Generate a large dataset to test concurrent filtering
	now := time.Now().Unix()
	var metrics []model.Metric

	// 200 valid metrics
	for i := 0; i < 200; i++ {
		metrics = append(metrics, model.Metric{
			Provider:     "provider" + string(rune(i%5+'1')),
			APY:          0.05 + float64(i%10)*0.01,
			TVL:          1000 + float64(i)*10,
			PointsPerETH: 1.0,
			CollectedAt:  now,
		})
	}

	// 50 invalid metrics
	for i := 0; i < 50; i++ {
		// Alternating invalid characteristics
		switch i % 5 {
		case 0:
			metrics = append(metrics, model.Metric{ // negative APY
				Provider:     "bad_provider",
				APY:          -0.01,
				TVL:          2000,
				PointsPerETH: 1.0,
				CollectedAt:  now,
			})
		case 1:
			metrics = append(metrics, model.Metric{ // zero TVL
				Provider:     "bad_provider",
				APY:          0.05,
				TVL:          0,
				PointsPerETH: 1.0,
				CollectedAt:  now,
			})
		case 2:
			metrics = append(metrics, model.Metric{ // too old
				Provider:     "bad_provider",
				APY:          0.05,
				TVL:          2000,
				PointsPerETH: 1.0,
				CollectedAt:  now - 90000, // 25 hours old
			})
		case 3:
			metrics = append(metrics, model.Metric{ // empty provider
				Provider:     "",
				APY:          0.05,
				TVL:          2000,
				PointsPerETH: 1.0,
				CollectedAt:  now,
			})
		case 4:
			metrics = append(metrics, model.Metric{ // negative PPE
				Provider:     "bad_provider",
				APY:          0.05,
				TVL:          2000,
				PointsPerETH: -1.0,
				CollectedAt:  now,
			})
		}
	}

	// Also add some outliers
	metrics = append(metrics, model.Metric{
		Provider:     "outlier1",
		APY:          1.5, // 150% APY
		TVL:          2000,
		PointsPerETH: 1.0,
		CollectedAt:  now,
	})
	metrics = append(metrics, model.Metric{
		Provider:     "outlier2",
		APY:          2.0, // 200% APY
		TVL:          2000,
		PointsPerETH: 1.0,
		CollectedAt:  now,
	})

	opts := DefaultValidationOptions()
	filtered := FilterInvalidConcurrently(metrics, opts)

	// We should get around 200 valid metrics, but some might be filtered as outliers
	assert.Greater(t, len(filtered), 180)
	assert.Less(t, len(filtered), 202) // Accounting for possible inclusion of some outliers

	// Verify no invalid metrics made it through
	for _, m := range filtered {
		assert.GreaterOrEqual(t, m.APY, 0.0)
		assert.Greater(t, m.TVL, 0.0)
		assert.GreaterOrEqual(t, m.PointsPerETH, 0.0)
		assert.NotEmpty(t, m.Provider)
		assert.True(t, time.Since(time.Unix(m.CollectedAt, 0)) <= 24*time.Hour)
	}
}

func TestCalculateConfidenceScores(t *testing.T) {
	now := time.Now().Unix()

	metrics := []model.Metric{
		{Provider: "provider1", APY: 0.05, TVL: 1000, CollectedAt: now},
		{Provider: "provider2", APY: 0.06, TVL: 2000, CollectedAt: now},
		{Provider: "provider3", APY: 0.055, TVL: 1500, CollectedAt: now},
	}

	result := CalculateConfidenceScores(metrics)
	require.Len(t, result, 3)

	// Verify all metrics have confidence scores
	for _, m := range result {
		assert.Greater(t, m.Confidence, 0.0)
		assert.LessOrEqual(t, m.Confidence, 1.0)
	}

	// Verify closest to the weighted average has highest confidence
	// Weighted average: (0.05*1000 + 0.06*2000 + 0.055*1500) / (1000 + 2000 + 1500) = 0.05555...
	var highestConfidence float64
	var highestProvider string
	for _, m := range result {
		if m.Confidence > highestConfidence {
			highestConfidence = m.Confidence
			highestProvider = m.Provider
		}
	}

	// Provider3 has APY (0.055) closest to weighted average (0.05555...)
	assert.Equal(t, "provider3", highestProvider)
}

func TestCalculateConfidenceScores_SingleMetric(t *testing.T) {
	now := time.Now().Unix()

	metrics := []model.Metric{
		{Provider: "provider1", APY: 0.05, TVL: 1000, CollectedAt: now},
	}

	result := CalculateConfidenceScores(metrics)
	require.Len(t, result, 1)

	// Single metric should be returned as-is (confidence = 0)
	assert.Equal(t, 0.0, result[0].Confidence)
}

func TestCalculateConfidenceScores_ZeroValues(t *testing.T) {
	now := time.Now().Unix()

	// Test edge case where all APYs are zero
	metrics := []model.Metric{
		{Provider: "provider1", APY: 0.0, TVL: 1000, CollectedAt: now},
		{Provider: "provider2", APY: 0.0, TVL: 2000, CollectedAt: now},
	}

	result := CalculateConfidenceScores(metrics)
	require.Len(t, result, 2)

	// All should have perfect agreement (confidence = 1.0)
	for _, m := range result {
		assert.Equal(t, 1.0, m.Confidence)
	}
}

// TestFilterInvalid_RejectsNaNAndInf locks in the NaN/Inf guard: a provider
// returning NaN or Inf values must be filtered out before aggregation, because
// NaN poisons every downstream computation (NaN comparisons return false, so
// without this guard NaN would slip past the APY < 0 and APY > MaxAPY checks).
func TestFilterInvalid_RejectsNaNAndInf(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "good", APY: 0.05, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "nan-apy", APY: nan(), TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "inf-apy", APY: posInf(), TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "nan-tvl", APY: 0.05, TVL: nan(), PointsPerETH: 1.0, CollectedAt: now},
		{Provider: "nan-ppe", APY: 0.05, TVL: 1000, PointsPerETH: nan(), CollectedAt: now},
		{Provider: "neg-inf", APY: negInf(), TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
	}
	filtered := FilterInvalid(metrics)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "good", filtered[0].Provider)
}

func nan() float64    { var z float64; return z / z }
func posInf() float64 { var z float64; return 1.0 / z }
func negInf() float64 { var z float64; return -1.0 / z }

func TestCalculateMean(t *testing.T) {
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	mean := calculateMean(values)
	assert.InDelta(t, 3.0, mean, 1e-9)
}

func TestCalculateMeanEmpty(t *testing.T) {
	assert.Equal(t, 0.0, calculateMean(nil))
	assert.Equal(t, 0.0, calculateMean([]float64{}))
}

func TestCalculateMeanSingle(t *testing.T) {
	assert.InDelta(t, 42.0, calculateMean([]float64{42.0}), 1e-9)
}

// TestMedianOfEmptySlice covers the empty-slice guard in medianOf.
func TestMedianOfEmptySlice(t *testing.T) {
	assert.Equal(t, 0.0, medianOf([]float64{}))
	assert.Equal(t, 0.0, medianOf(nil))
}

// TestMedianOfEvenCount covers the even-count branch of medianOf, which
// averages the two middle elements.
func TestMedianOfEvenCount(t *testing.T) {
	// Already-sorted input: medianOf expects a sorted slice.
	assert.InDelta(t, 2.5, medianOf([]float64{1, 2, 3, 4}), 1e-9)
}

// TestMedianOfOddCount covers the odd-count branch for completeness.
func TestMedianOfOddCount(t *testing.T) {
	assert.InDelta(t, 3.0, medianOf([]float64{1, 2, 3, 4, 5}), 1e-9)
}

// TestFilterOutliers_ThreeOrFewer covers the early return when there are
// not enough points for meaningful outlier detection.
func TestFilterOutliers_ThreeOrFewer(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, CollectedAt: now},
		{Provider: "p2", APY: 0.9, TVL: 1000, CollectedAt: now},
		{Provider: "p3", APY: 0.05, TVL: 1000, CollectedAt: now},
	}
	// filterOutliers is unexported; call it directly within the package.
	result := filterOutliers(metrics, 1.5)
	assert.Len(t, result, 3, "should return unchanged with <=3 metrics")
}

// TestFilterOutliers_SmallAPYRange covers the branch where the IQR-derived
// bounds are too tight (range < 0.005) and are widened using the mean.
func TestFilterOutliers_SmallAPYRange(t *testing.T) {
	now := time.Now().Unix()
	// All APYs within a tiny range so IQR < 0.005.
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.0500, TVL: 1000, CollectedAt: now},
		{Provider: "p2", APY: 0.0501, TVL: 1000, CollectedAt: now},
		{Provider: "p3", APY: 0.0502, TVL: 1000, CollectedAt: now},
		{Provider: "p4", APY: 0.0503, TVL: 1000, CollectedAt: now},
		{Provider: "p5", APY: 0.0504, TVL: 1000, CollectedAt: now},
	}
	result := filterOutliers(metrics, 1.5)
	// With such a tight cluster, none should be filtered as outliers.
	assert.Len(t, result, 5)
}

// TestFilterOutliers_ScaledMADZero covers the branch where all APY values
// are identical except one, making scaledMAD == 0. In that case any point
// differing from the median is treated as an outlier via the MAD fallback.
// The outlier is chosen small enough to pass the IQR bounds so it is
// rejected specifically by the `median != m.APY` MAD-fallback branch.
func TestFilterOutliers_ScaledMADZero(t *testing.T) {
	now := time.Now().Unix()
	metrics := []model.Metric{
		{Provider: "p1", APY: 0.05, TVL: 1000, CollectedAt: now},
		{Provider: "p2", APY: 0.05, TVL: 1000, CollectedAt: now},
		{Provider: "p3", APY: 0.05, TVL: 1000, CollectedAt: now},
		{Provider: "p4", APY: 0.05, TVL: 1000, CollectedAt: now},
		{Provider: "p5", APY: 0.12, TVL: 1000, CollectedAt: now},
	}
	result := filterOutliers(metrics, 1.5)
	// The four identical values are kept; the differing one is rejected by
	// the MAD fallback (scaledMAD == 0, median != m.APY).
	assert.Len(t, result, 4)
	for _, m := range result {
		assert.InDelta(t, 0.05, m.APY, 1e-9)
	}
}

// TestFilterInvalidConcurrently_SmallDataset covers the <100 metrics branch,
// which should delegate to FilterInvalidWithOptions instead of sharding.
func TestFilterInvalidConcurrently_SmallDataset(t *testing.T) {
	now := time.Now().Unix()
	metrics := make([]model.Metric, 0, 50)
	for i := 0; i < 50; i++ {
		metrics = append(metrics, model.Metric{
			Provider:     "provider",
			APY:          0.05,
			TVL:          1000,
			PointsPerETH: 1.0,
			CollectedAt:  now,
		})
	}
	opts := DefaultValidationOptions()
	result := FilterInvalidConcurrently(metrics, opts)
	// All 50 are valid and tightly clustered; none should be filtered.
	assert.Len(t, result, 50)
}

// TestFilterInvalidConcurrently_NoOutlierDetection covers the branch where
// outlier detection is disabled (or validMetrics <= 3), so the function
// returns validMetrics directly without calling filterOutliers.
func TestFilterInvalidConcurrently_NoOutlierDetection(t *testing.T) {
	now := time.Now().Unix()
	metrics := make([]model.Metric, 0, 100)
	for i := 0; i < 100; i++ {
		metrics = append(metrics, model.Metric{
			Provider:     "provider",
			APY:          0.05,
			TVL:          1000,
			PointsPerETH: 1.0,
			CollectedAt:  now,
		})
	}
	opts := DefaultValidationOptions()
	opts.EnableOutlierDetection = false
	result := FilterInvalidConcurrently(metrics, opts)
	assert.Len(t, result, 100)
}

// TestFilterInvalidConcurrently_FewValidMetrics covers the branch where
// after filtering, validMetrics <= 3, so outlier detection is skipped.
func TestFilterInvalidConcurrently_FewValidMetrics(t *testing.T) {
	now := time.Now().Unix()
	metrics := make([]model.Metric, 0, 100)
	// 3 valid metrics + 97 invalid (negative APY).
	for i := 0; i < 3; i++ {
		metrics = append(metrics, model.Metric{
			Provider:     "provider",
			APY:          0.05,
			TVL:          1000,
			PointsPerETH: 1.0,
			CollectedAt:  now,
		})
	}
	for i := 0; i < 97; i++ {
		metrics = append(metrics, model.Metric{
			Provider:     "bad",
			APY:          -0.01,
			TVL:          1000,
			PointsPerETH: 1.0,
			CollectedAt:  now,
		})
	}
	opts := DefaultValidationOptions()
	result := FilterInvalidConcurrently(metrics, opts)
	assert.Len(t, result, 3)
}

// TestFilterInvalidConcurrently_MoreWorkersThanChunks covers the `break`
// branch in FilterInvalidConcurrently (filters.go ~89-90). By temporarily
// increasing workerCount beyond the number of chunks, the last worker(s)
// hit `start >= len(metrics)` and break out of the loop.
func TestFilterInvalidConcurrently_MoreWorkersThanChunks(t *testing.T) {
	now := time.Now().Unix()
	// 100 metrics with 8 workers: chunkSize = ceil(100/8) = 13.
	// Workers 0-7: starts at 0,13,26,39,52,65,78,91. Worker 7: start=91 < 100.
	// With 10 workers: chunkSize = ceil(100/10) = 10. All 10 workers have work.
	// With 12 workers: chunkSize = ceil(100/12) = 9. Worker 11: start=99 < 100.
	// With 101 workers: chunkSize = ceil(100/101) = 1. Worker 100: start=100 >= 100 -> break!
	orig := workerCount
	workerCount = 101
	t.Cleanup(func() { workerCount = orig })

	metrics := make([]model.Metric, 100)
	for i := range metrics {
		metrics[i] = model.Metric{
			Provider:     "provider",
			APY:          0.05,
			TVL:          1000,
			PointsPerETH: 1.0,
			CollectedAt:  now,
		}
	}
	opts := DefaultValidationOptions()
	opts.EnableOutlierDetection = false
	result := FilterInvalidConcurrently(metrics, opts)
	assert.Len(t, result, 100)
}
