// Package validation provides filtering and validation mechanisms for yield metrics.
package validation

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// workerCount is the number of goroutines used by FilterInvalidConcurrently.
// It is a package-level variable so tests can override it to exercise edge
// cases (e.g. more workers than chunks).
var workerCount = 4

// ValidationOptions holds configuration for the validation process
//
//nolint:revive // API: stable public name
type ValidationOptions struct {
	// MaxAge defines how recent metrics must be to be considered valid
	MaxAge time.Duration

	// MinTVL defines the minimum TVL required for a metric to be valid
	MinTVL float64

	// MaxAPY defines the maximum reasonable APY value
	MaxAPY float64

	// RequirePositivePointsPerETH determines if we require PointsPerETH > 0
	RequirePositivePointsPerETH bool

	// EnableOutlierDetection enables statistical outlier detection
	EnableOutlierDetection bool

	// OutlierIQRMultiplier defines sensitivity for outlier detection (1.5 is standard)
	OutlierIQRMultiplier float64
}

// DefaultValidationOptions returns sensible defaults for validation
func DefaultValidationOptions() ValidationOptions {
	return ValidationOptions{
		MaxAge:                      24 * time.Hour,
		MinTVL:                      1.0,
		MaxAPY:                      10.0, // 1000% as decimal
		RequirePositivePointsPerETH: true,
		EnableOutlierDetection:      true,
		OutlierIQRMultiplier:        1.5,
	}
}

// FilterInvalid removes metrics that fail basic validation criteria.
// This is the main entrypoint for the validation package.
func FilterInvalid(metrics []model.Metric) []model.Metric {
	return FilterInvalidWithOptions(metrics, DefaultValidationOptions())
}

// FilterInvalidWithOptions removes metrics with custom validation options.
func FilterInvalidWithOptions(metrics []model.Metric, opts ValidationOptions) []model.Metric {
	// First apply basic filters
	valid := filterBasicCriteria(metrics, opts)

	// Then apply statistical filters if enabled
	if opts.EnableOutlierDetection && len(valid) > 3 {
		return filterOutliers(valid, opts.OutlierIQRMultiplier)
	}

	return valid
}

// FilterInvalidConcurrently performs validation in parallel for large datasets.
func FilterInvalidConcurrently(metrics []model.Metric, opts ValidationOptions) []model.Metric {
	if len(metrics) < 100 {
		// For small datasets, parallel processing overhead isn't worth it
		return FilterInvalidWithOptions(metrics, opts)
	}

	chunkSize := (len(metrics) + workerCount - 1) / workerCount
	wg := sync.WaitGroup{}
	resultChan := make(chan []model.Metric, workerCount)

	// Process in chunks
	for i := 0; i < workerCount; i++ {
		start := i * chunkSize
		end := (i + 1) * chunkSize
		if end > len(metrics) {
			end = len(metrics)
		}
		if start >= len(metrics) {
			break
		}

		chunk := metrics[start:end]
		wg.Add(1)
		go func(chunk []model.Metric) {
			defer wg.Done()
			validChunk := filterBasicCriteria(chunk, opts)
			resultChan <- validChunk
		}(chunk)
	}

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	var validMetrics []model.Metric
	for chunk := range resultChan {
		validMetrics = append(validMetrics, chunk...)
	}

	// Apply outlier detection on the combined result
	if opts.EnableOutlierDetection && len(validMetrics) > 3 {
		return filterOutliers(validMetrics, opts.OutlierIQRMultiplier)
	}

	return validMetrics
}

// filterBasicCriteria applies fundamental validation rules to each metric
func filterBasicCriteria(metrics []model.Metric, opts ValidationOptions) []model.Metric {
	valid := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		if isValidMetric(m, opts) {
			valid = append(valid, m)
		} else {
			logrus.WithFields(logrus.Fields{
				"provider": m.Provider,
				"apy":      m.APY,
				"tvl":      m.TVL,
			}).Debug("Filtered invalid metric")
		}
	}
	return valid
}

// isValidMetric checks if a single metric meets all validation criteria
func isValidMetric(m model.Metric, opts ValidationOptions) bool {
	// Reject NaN and Inf values first. NaN comparisons always return false in
	// Go, so a NaN APY would silently slip past every threshold check below
	// and poison the aggregation (any arithmetic with NaN yields NaN). Inf
	// values are equally invalid for financial data.
	if isNaNOrInf(m.APY) || isNaNOrInf(m.TVL) || isNaNOrInf(m.PointsPerETH) {
		return false
	}

	// Check for non-negative APY (negative yields don't make sense)
	if m.APY < 0 {
		return false
	}

	// Check for unreasonably high APY
	if m.APY > opts.MaxAPY {
		return false
	}

	// Check for sufficient TVL (protects against manipulation of low-liquidity pools)
	if m.TVL <= opts.MinTVL {
		return false
	}

	// Check if the metric is recent enough
	collectedAt := time.Unix(m.CollectedAt, 0)
	if time.Since(collectedAt) > opts.MaxAge {
		return false
	}

	// Check for valid provider identifier
	if m.Provider == "" {
		return false
	}

	// Optionally check for positive points per ETH ratio
	if opts.RequirePositivePointsPerETH && m.PointsPerETH < 0 {
		return false
	}

	return true
}

// filterOutliers removes statistical outliers using a combination of the IQR
// method and a robust MAD (median absolute deviation) test. The MAD test is
// essential for small samples: with only 4 points the IQR can be dominated by
// the outlier itself (the outlier becomes Q3), producing bounds that fail to
// reject it. MAD is robust to up to 50% contamination and catches such cases.
//
// A metric is treated as an outlier if it falls outside the IQR bounds OR its
// distance from the median exceeds madMultiplier * scaledMAD.
func filterOutliers(metrics []model.Metric, iqrMultiplier float64) []model.Metric {
	if len(metrics) <= 3 {
		return metrics // Need at least 4 points for meaningful outlier detection
	}

	// Extract APY values
	apys := make([]float64, len(metrics))
	for i, m := range metrics {
		apys[i] = m.APY
	}

	// --- IQR bounds ---
	sort.Float64s(apys)
	q1Idx := len(apys) / 4
	q3Idx := len(apys) * 3 / 4
	q1 := apys[q1Idx]
	q3 := apys[q3Idx]
	iqr := q3 - q1
	lowerBound := q1 - iqrMultiplier*iqr
	upperBound := q3 + iqrMultiplier*iqr

	// If bounds are too strict, adjust them to ensure we don't filter too aggressively
	if upperBound-lowerBound < 0.005 { // Very small range
		mean := calculateMean(apys)
		lowerBound = mean * 0.5 // Allow down to 50% of mean
		upperBound = mean * 2.0 // Allow up to 200% of mean
	}

	// --- MAD (median absolute deviation) bounds ---
	median := medianOf(apys)
	absDevs := make([]float64, len(apys))
	for i, v := range apys {
		absDevs[i] = math.Abs(v - median)
	}
	sort.Float64s(absDevs)
	mad := medianOf(absDevs)
	// Scale MAD to be comparable to the standard deviation (1.4826 factor for
	// consistency with the normal distribution).
	scaledMAD := 1.4826 * mad
	const madMultiplier = 3.0
	var madLower, madUpper float64
	if scaledMAD > 0 {
		madLower = median - madMultiplier*scaledMAD
		madUpper = median + madMultiplier*scaledMAD
	}

	// Filter outliers: a point is rejected if it violates EITHER bound set.
	// When scaledMAD is 0 (all values identical except the outlier), fall back
	// to rejecting any point that differs from the median at all.
	valid := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		outsideIQR := m.APY < lowerBound || m.APY > upperBound
		var outsideMAD bool
		if scaledMAD > 0 {
			outsideMAD = m.APY < madLower || m.APY > madUpper
		} else if median != m.APY {
			outsideMAD = true
		}
		if outsideIQR || outsideMAD {
			logrus.WithFields(logrus.Fields{
				"provider":  m.Provider,
				"apy":       m.APY,
				"iqrBounds": []float64{lowerBound, upperBound},
				"madBounds": []float64{madLower, madUpper},
				"median":    median,
			}).Info("Filtered outlier metric")
			continue
		}
		valid = append(valid, m)
	}

	// Log summary
	logrus.WithFields(logrus.Fields{
		"total":    len(metrics),
		"filtered": len(metrics) - len(valid),
		"bounds":   []float64{lowerBound, upperBound},
	}).Debug("Outlier filtering complete")

	return valid
}

// medianOf returns the median of a sorted float64 slice.
func medianOf(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

// calculateMean computes the arithmetic mean of a slice of float64
func calculateMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// CalculateConfidenceScores assigns a confidence score (0-1) to each metric
// based on its agreement with other providers
func CalculateConfidenceScores(metrics []model.Metric) []model.Metric {
	if len(metrics) <= 1 {
		return metrics // Can't calculate confidence with fewer than 2 metrics
	}

	// Calculate weighted average as our reference point
	var totalAPY, totalTVL float64
	for _, m := range metrics {
		totalAPY += m.APY * m.TVL
		totalTVL += m.TVL
	}
	refAPY := totalAPY / totalTVL

	// Calculate score based on distance from reference
	result := make([]model.Metric, len(metrics))
	for i, m := range metrics {
		mc := m

		// Calculate relative distance from consensus
		relativeDist := math.Abs(m.APY-refAPY) / refAPY
		if refAPY == 0 {
			relativeDist = math.Abs(m.APY)
		}

		// Convert to confidence score (1 = perfect agreement, 0 = no confidence)
		confidence := 1.0 / (1.0 + relativeDist*5)
		mc.Confidence = confidence

		result[i] = mc
	}

	return result
}

// isNaNOrInf reports whether v is NaN or +/-Inf. These values are never valid
// for financial metrics: NaN poisons every downstream computation (any
// comparison with NaN returns false, any arithmetic yields NaN), and Inf
// indicates an overflow or corrupt source. Rejecting them at the validation
// gate prevents them from reaching aggregation and the on-chain signature.
func isNaNOrInf(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
}
