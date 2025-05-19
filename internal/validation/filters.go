// Package validation provides filtering and validation mechanisms for yield metrics.
package validation

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// ValidationOptions holds configuration for the validation process
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
		MaxAge:                   24 * time.Hour,
		MinTVL:                   1.0,
		MaxAPY:                   10.0, // 1000% as decimal
		RequirePositivePointsPerETH: true,
		EnableOutlierDetection:   true,
		OutlierIQRMultiplier:     1.5,
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

	workerCount := 4
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

// filterOutliers removes statistical outliers using the IQR method
func filterOutliers(metrics []model.Metric, iqrMultiplier float64) []model.Metric {
	if len(metrics) <= 3 {
		return metrics // Need at least 4 points for meaningful outlier detection
	}

	// Extract APY values
	apys := make([]float64, len(metrics))
	for i, m := range metrics {
		apys[i] = m.APY
	}

	// Calculate Q1, Q3, and IQR
	sort.Float64s(apys)
	q1Idx := len(apys) / 4
	q3Idx := len(apys) * 3 / 4
	q1 := apys[q1Idx]
	q3 := apys[q3Idx]
	iqr := q3 - q1

	// Calculate bounds
	lowerBound := q1 - iqrMultiplier*iqr
	upperBound := q3 + iqrMultiplier*iqr

	// If bounds are too strict, adjust them to ensure we don't filter too aggressively
	if upperBound - lowerBound < 0.005 { // Very small range
		mean := calculateMean(apys)
		lowerBound = mean * 0.5 // Allow down to 50% of mean
		upperBound = mean * 2.0 // Allow up to 200% of mean
	}

	// Filter outliers
	valid := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		if m.APY >= lowerBound && m.APY <= upperBound {
			valid = append(valid, m)
		} else {
			logrus.WithFields(logrus.Fields{
				"provider": m.Provider,
				"apy":      m.APY,
				"bounds":   []float64{lowerBound, upperBound},
			}).Info("Filtered outlier metric")
		}
	}

	// Log summary
	logrus.WithFields(logrus.Fields{
		"total":    len(metrics),
		"filtered": len(metrics) - len(valid),
		"bounds":   []float64{lowerBound, upperBound},
	}).Debug("Outlier filtering complete")

	return valid
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
		copy := m
		
		// Calculate relative distance from consensus
		relativeDist := math.Abs(m.APY - refAPY) / refAPY
		if refAPY == 0 {
			relativeDist = math.Abs(m.APY)
		}
		
		// Convert to confidence score (1 = perfect agreement, 0 = no confidence)
		confidence := 1.0 / (1.0 + relativeDist*5)
		copy.Confidence = confidence
		
		result[i] = copy
	}

	return result
}
