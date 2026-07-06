// Package aggregate provides metric aggregation strategies: TVL-weighted
// average, median, trimmed mean, and simple average. All strategies return a
// single model.Metric that represents the consensus across providers.
package aggregate

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
)

// emptyMetric returns a zero-value aggregated metric for edge cases.
func emptyMetric() model.Metric {
	return model.Metric{Provider: "aggregated"}
}

// Weighted computes the TVL-weighted average of APY and the total (summed)
// TVL across all valid metrics. PointsPerETH is computed as the median of
// non-zero values — it is a protocol conversion rate, not a yield metric
// that scales with liquidity, so TVL-weighting it is semantically incorrect.
// A PointsPerETH of 0 means "not available" and is excluded from the median.
func Weighted(metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return emptyMetric()
	}

	var totalTVL, weightedAPY float64
	validMetrics := 0
	latestTimestamp := int64(0)
	var pointsValues []float64

	for _, m := range metrics {
		if m.TVL > 0 && m.APY >= 0 && m.PointsPerETH >= 0 {
			totalTVL += m.TVL
			weightedAPY += m.APY * m.TVL
			validMetrics++
			if m.CollectedAt > latestTimestamp {
				latestTimestamp = m.CollectedAt
			}
			if m.PointsPerETH > 0 {
				pointsValues = append(pointsValues, m.PointsPerETH)
			}
		}
	}

	if validMetrics == 0 || totalTVL <= 0 {
		return emptyMetric()
	}

	return model.Metric{
		APY:          weightedAPY / totalTVL,
		TVL:          totalTVL, // TVL is summed, not averaged
		PointsPerETH: medianFloat64s(pointsValues),
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// medianFloat64s returns the median of a sorted slice of float64s.
// Returns 0 if the slice is empty.
func medianFloat64s(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	n := len(values)
	if n%2 == 0 {
		return (values[n/2-1] + values[n/2]) / 2
	}
	return values[n/2]
}

// WeightedParallel is a concurrent variant of Weighted for large input sets.
// It uses a fixed worker pool (one goroutine per CPU core, not one per metric)
// to avoid goroutine creation overhead and mutex contention for large inputs.
// Each worker processes a chunk locally (no contention), then merges under a
// mutex once. The context is honoured so callers can cancel mid-computation.
func WeightedParallel(ctx context.Context, metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return emptyMetric()
	}

	workers := runtime.NumCPU()
	if workers > len(metrics) {
		workers = len(metrics)
	}
	if workers < 1 {
		workers = 1
	}

	chunkSize := (len(metrics) + workers - 1) / workers

	var (
		mu              sync.Mutex
		wg              sync.WaitGroup
		totalTVL        float64
		weightedAPY     float64
		validMetrics    int
		latestTimestamp int64
		pointsValues    []float64
	)

	for i := 0; i < workers; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(metrics) {
			end = len(metrics)
		}
		if start >= len(metrics) {
			break
		}

		wg.Add(1)
		go func(chunk []model.Metric) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			default:
			}

			var localTVL, localAPY float64
			var localValid int
			var localLatest int64
			var localPoints []float64

			for _, m := range chunk {
				if m.TVL > 0 && m.APY >= 0 && m.PointsPerETH >= 0 {
					localTVL += m.TVL
					localAPY += m.APY * m.TVL
					localValid++
					if m.CollectedAt > localLatest {
						localLatest = m.CollectedAt
					}
					if m.PointsPerETH > 0 {
						localPoints = append(localPoints, m.PointsPerETH)
					}
				}
			}

			mu.Lock()
			totalTVL += localTVL
			weightedAPY += localAPY
			validMetrics += localValid
			if localLatest > latestTimestamp {
				latestTimestamp = localLatest
			}
			pointsValues = append(pointsValues, localPoints...)
			mu.Unlock()
		}(metrics[start:end])
	}

	wg.Wait()

	if validMetrics == 0 || totalTVL <= 0 {
		return emptyMetric()
	}

	return model.Metric{
		APY:          weightedAPY / totalTVL,
		TVL:          totalTVL,
		PointsPerETH: medianFloat64s(pointsValues),
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// Median computes the median of a selected field across valid metrics.
func Median(metrics []model.Metric, selector func(model.Metric) float64) float64 {
	if len(metrics) == 0 {
		return 0
	}

	values := make([]float64, 0, len(metrics))
	for _, m := range metrics {
		if m.TVL > 0 {
			values = append(values, selector(m))
		}
	}
	if len(values) == 0 {
		return 0
	}

	sort.Float64s(values)
	n := len(values)
	if n%2 == 0 {
		return (values[n/2-1] + values[n/2]) / 2
	}
	return values[n/2]
}

// ValidateMetric checks whether a metric contains plausible values.
func ValidateMetric(m model.Metric) error {
	// Reject NaN and Inf before any comparison — NaN comparisons return false
	// in Go, so these would silently pass every threshold check below and
	// poison the weighted average (NaN propagates through all arithmetic).
	if math.IsNaN(m.APY) || math.IsInf(m.APY, 0) {
		return fmt.Errorf("invalid APY (NaN or Inf): %f", m.APY)
	}
	if math.IsNaN(m.TVL) || math.IsInf(m.TVL, 0) {
		return fmt.Errorf("invalid TVL (NaN or Inf): %f", m.TVL)
	}
	if math.IsNaN(m.PointsPerETH) || math.IsInf(m.PointsPerETH, 0) {
		return fmt.Errorf("invalid PointsPerETH (NaN or Inf): %f", m.PointsPerETH)
	}
	if m.APY < 0 {
		return fmt.Errorf("negative APY: %f", m.APY)
	}
	// APY is a decimal; 1.0 = 100%. Anything above 10.0 (1000%) is implausible.
	if m.APY > 10.0 {
		return fmt.Errorf("implausible APY (>1000%%): %f", m.APY)
	}
	if m.TVL <= 0 {
		return fmt.Errorf("invalid TVL: %f", m.TVL)
	}
	if m.PointsPerETH < 0 {
		return fmt.Errorf("negative PointsPerETH: %f", m.PointsPerETH)
	}
	if m.CollectedAt <= 0 {
		return fmt.Errorf("invalid timestamp: %d", m.CollectedAt)
	}
	maxAge := time.Now().Add(-24 * time.Hour).Unix()
	if m.CollectedAt < maxAge {
		return fmt.Errorf("metric too old: %d", m.CollectedAt)
	}
	return nil
}

// FilterOutliers removes APY outliers using the IQR method. This is a simple
// standalone filter; the validation package provides a more robust IQR+MAD
// implementation that handles small sample sizes correctly.
func FilterOutliers(metrics []model.Metric) []model.Metric {
	if len(metrics) < 4 {
		return metrics
	}

	apyValues := make([]float64, 0, len(metrics))
	for _, m := range metrics {
		if m.TVL > 0 && m.APY >= 0 {
			apyValues = append(apyValues, m.APY)
		}
	}
	if len(apyValues) < 4 {
		return metrics
	}

	sort.Float64s(apyValues)
	n := len(apyValues)
	q1Index := n / 4
	q3Index := n * 3 / 4
	q1 := apyValues[q1Index]
	q3 := apyValues[q3Index]
	iqr := q3 - q1
	lowerBound := q1 - 1.5*iqr
	upperBound := q3 + 1.5*iqr

	filtered := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		if m.APY >= lowerBound && m.APY <= upperBound {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// ValidateAndFilterMetrics combines validation and outlier filtering.
func ValidateAndFilterMetrics(metrics []model.Metric) []model.Metric {
	valid := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		if err := ValidateMetric(m); err == nil {
			valid = append(valid, m)
		}
	}
	return FilterOutliers(valid)
}

// WeightedWithValidation combines validation, outlier filtering, and weighted
// aggregation into a single call.
func WeightedWithValidation(metrics []model.Metric) model.Metric {
	return Weighted(ValidateAndFilterMetrics(metrics))
}

// WeightedParallelWithValidation is the concurrent variant of
// WeightedWithValidation for large input sets.
func WeightedParallelWithValidation(ctx context.Context, metrics []model.Metric) model.Metric {
	return WeightedParallel(ctx, ValidateAndFilterMetrics(metrics))
}

// AverageMetrics computes a simple (unweighted) average of APY and
// PointsPerETH. TVL is summed because averaging TVL across providers is
// mathematically meaningless.
func AverageMetrics(metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return emptyMetric()
	}

	var totalAPY, totalTVL, totalPoints float64
	validMetrics := 0
	latestTimestamp := int64(0)

	for _, m := range metrics {
		if m.APY >= 0 && m.PointsPerETH >= 0 {
			totalAPY += m.APY
			totalTVL += m.TVL // sum, not average
			totalPoints += m.PointsPerETH
			validMetrics++
			if m.CollectedAt > latestTimestamp {
				latestTimestamp = m.CollectedAt
			}
		}
	}

	if validMetrics == 0 {
		return emptyMetric()
	}

	return model.Metric{
		APY:          totalAPY / float64(validMetrics),
		TVL:          totalTVL, // summed
		PointsPerETH: totalPoints / float64(validMetrics),
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// MedianAggregation computes the median of APY, PointsPerETH, and TVL across
// metrics. TVL median is less meaningful than sum but is included for
// completeness; callers that need summed TVL should use Weighted instead.
func MedianAggregation(metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return emptyMetric()
	}

	apyMedian := Median(metrics, func(m model.Metric) float64 { return m.APY })
	pointsMedian := Median(metrics, func(m model.Metric) float64 { return m.PointsPerETH })

	// For TVL, sum is more meaningful than median. We sum all valid TVLs.
	var totalTVL float64
	latestTimestamp := int64(0)
	for _, m := range metrics {
		if m.TVL > 0 {
			totalTVL += m.TVL
		}
		if m.CollectedAt > latestTimestamp {
			latestTimestamp = m.CollectedAt
		}
	}

	return model.Metric{
		APY:          apyMedian,
		TVL:          totalTVL, // summed, not median
		PointsPerETH: pointsMedian,
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// TrimmedMeanAggregation removes the top and bottom trimPercent of metrics
// (by APY) and then computes a TVL-weighted average of the remaining set.
func TrimmedMeanAggregation(metrics []model.Metric, trimPercent float64) model.Metric {
	if len(metrics) < 3 || trimPercent <= 0 || trimPercent >= 0.5 {
		return Weighted(metrics)
	}

	valid := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		if m.TVL > 0 && m.APY >= 0 && m.PointsPerETH >= 0 {
			valid = append(valid, m)
		}
	}
	if len(valid) < 3 {
		return Weighted(metrics)
	}

	sort.Slice(valid, func(i, j int) bool {
		return valid[i].APY < valid[j].APY
	})

	trimCount := int(float64(len(valid)) * trimPercent)
	trimmed := valid[trimCount : len(valid)-trimCount]
	return Weighted(trimmed)
}
