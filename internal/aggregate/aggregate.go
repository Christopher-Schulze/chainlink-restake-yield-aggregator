package aggregate

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/model"
)

// Weighted berechnet TVL-gewichtete Durchschnittswerte für APY und PointsPerETH
// Gibt aggregierte Metrik mit aktuellem Timestamp zurück
func Weighted(metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return model.Metric{
			APY:          0,
			TVL:          0,
			PointsPerETH: 0,
			CollectedAt:  0,
			Provider:     "aggregated",
		}
	}

	var totalTVL, weightedAPY, weightedPoints float64
	validMetrics := 0
	latestTimestamp := int64(0)

	for _, m := range metrics {
		if m.TVL > 0 && m.APY >= 0 && m.PointsPerETH >= 0 {
			totalTVL += m.TVL
			weightedAPY += m.APY * m.TVL
			weightedPoints += m.PointsPerETH * m.TVL
			validMetrics++

			if m.CollectedAt > latestTimestamp {
				latestTimestamp = m.CollectedAt
			}
		}
	}

	if validMetrics == 0 || totalTVL <= 0 || math.IsNaN(weightedAPY) || math.IsNaN(weightedPoints) {
		return model.Metric{
			APY:          0,
			TVL:          0,
			PointsPerETH: 0,
			CollectedAt:  0,
			Provider:     "aggregated",
		}
	}

	return model.Metric{
		APY:          weightedAPY / totalTVL,
		TVL:          totalTVL,
		PointsPerETH: weightedPoints / totalTVL,
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// WeightedParallel berechnet TVL-gewichtete Durchschnittswerte mit paralleler Verarbeitung
// für bessere Performance bei großen Metrik-Sammlungen
func WeightedParallel(ctx context.Context, metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return model.Metric{
			APY:          0,
			TVL:          0,
			PointsPerETH: 0,
			CollectedAt:  0,
			Provider:     "aggregated",
		}
	}

	var (
		mu              sync.Mutex
		wg              sync.WaitGroup
		totalTVL        float64
		weightedAPY     float64
		weightedPoints  float64
		validMetrics    int
		latestTimestamp int64
	)

	// Verarbeite Metriken parallel für bessere Performance
	for i := range metrics {
		wg.Add(1)
		go func(m model.Metric) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
				if m.TVL > 0 && m.APY >= 0 && m.PointsPerETH >= 0 {
					mu.Lock()
					totalTVL += m.TVL
					weightedAPY += m.APY * m.TVL
					weightedPoints += m.PointsPerETH * m.TVL
					validMetrics++
					if m.CollectedAt > latestTimestamp {
						latestTimestamp = m.CollectedAt
					}
					mu.Unlock()
				}
			}
		}(metrics[i])
	}

	wg.Wait()

	if validMetrics == 0 || totalTVL <= 0 || math.IsNaN(weightedAPY) || math.IsNaN(weightedPoints) {
		return model.Metric{
			APY:          0,
			TVL:          0,
			PointsPerETH: 0,
			CollectedAt:  0,
			Provider:     "aggregated",
		}
	}

	return model.Metric{
		APY:          weightedAPY / totalTVL,
		TVL:          totalTVL,
		PointsPerETH: weightedPoints / totalTVL,
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// Median berechnet den Medianwert für eine bestimmte Metrik-Eigenschaft
// Nützlich für robuste Statistiken, die weniger anfällig für Ausreißer sind
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

// ValidateMetric prüft, ob eine Metrik plausible Werte enthält
// Gibt einen Fehler zurück, wenn die Metrik ungültige Werte enthält
func ValidateMetric(m model.Metric) error {
	if m.APY < 0 {
		return fmt.Errorf("negative APY: %f", m.APY)
	}

	if m.APY > 100 {
		return fmt.Errorf("unplausible APY value (>100%%): %f", m.APY)
	}

	if m.TVL <= 0 {
		return fmt.Errorf("invalid TVL value: %f", m.TVL)
	}

	if m.PointsPerETH < 0 {
		return fmt.Errorf("negative PointsPerETH: %f", m.PointsPerETH)
	}

	if m.CollectedAt <= 0 {
		return fmt.Errorf("invalid timestamp: %d", m.CollectedAt)
	}

	maxAge := time.Now().Add(-24 * time.Hour).Unix()
	if m.CollectedAt < maxAge {
		return fmt.Errorf("metric data too old: %d", m.CollectedAt)
	}

	return nil
}

// FilterOutliers entfernt Ausreißer aus den Metriken basierend auf statistischen Methoden
// Verwendet den IQR (Interquartile Range) zur Erkennung von Ausreißern
func FilterOutliers(metrics []model.Metric) []model.Metric {
	if len(metrics) < 4 {
		return metrics
	}

	// Extrahiere APY-Werte
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

	// Berechne Q1 (25. Perzentil) und Q3 (75. Perzentil)
	q1Index := n / 4
	q3Index := n * 3 / 4
	q1 := apyValues[q1Index]
	q3 := apyValues[q3Index]

	// Berechne IQR und Grenzen für Ausreißer
	iqr := q3 - q1
	lowerBound := q1 - 1.5*iqr
	upperBound := q3 + 1.5*iqr

	// Filtere Ausreißer
	filtered := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		if m.APY >= lowerBound && m.APY <= upperBound {
			filtered = append(filtered, m)
		}
	}

	return filtered
}

// ValidateAndFilterMetrics kombiniert Validierung und Ausreißererkennung
// Gibt nur gültige Metriken zurück, die keine Ausreißer sind
func ValidateAndFilterMetrics(metrics []model.Metric) []model.Metric {
	validMetrics := make([]model.Metric, 0, len(metrics))
	
	for _, m := range metrics {
		if err := ValidateMetric(m); err == nil {
			validMetrics = append(validMetrics, m)
		}
	}
	
	return FilterOutliers(validMetrics)
}

// WeightedWithValidation kombiniert Validierung, Ausreißererkennung und gewichtete Aggregation
// Bietet eine robuste Methode zur Aggregation von Metriken mit Qualitätssicherung
func WeightedWithValidation(metrics []model.Metric) model.Metric {
	validMetrics := ValidateAndFilterMetrics(metrics)
	return Weighted(validMetrics)
}

// WeightedParallelWithValidation kombiniert Validierung, Ausreißererkennung und parallele gewichtete Aggregation
// Optimiert für große Datensätze mit Qualitätssicherung
func WeightedParallelWithValidation(ctx context.Context, metrics []model.Metric) model.Metric {
	validMetrics := ValidateAndFilterMetrics(metrics)
	return WeightedParallel(ctx, validMetrics)
}

// AverageMetrics berechnet einfache (nicht gewichtete) Durchschnittswerte
// Nützlich, wenn TVL-Werte nicht zuverlässig sind oder gleich gewichtet werden sollen
func AverageMetrics(metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return model.Metric{
			APY:          0,
			TVL:          0,
			PointsPerETH: 0,
			CollectedAt:  0,
			Provider:     "aggregated",
		}
	}

	var totalAPY, totalTVL, totalPoints float64
	validMetrics := 0
	latestTimestamp := int64(0)

	for _, m := range metrics {
		if m.APY >= 0 && m.PointsPerETH >= 0 {
			totalAPY += m.APY
			totalTVL += m.TVL
			totalPoints += m.PointsPerETH
			validMetrics++

			if m.CollectedAt > latestTimestamp {
				latestTimestamp = m.CollectedAt
			}
		}
	}

	if validMetrics == 0 {
		return model.Metric{
			APY:          0,
			TVL:          0,
			PointsPerETH: 0,
			CollectedAt:  0,
			Provider:     "aggregated",
		}
	}

	return model.Metric{
		APY:          totalAPY / float64(validMetrics),
		TVL:          totalTVL / float64(validMetrics),
		PointsPerETH: totalPoints / float64(validMetrics),
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// MedianAggregation berechnet Medianwerte für alle Metrik-Eigenschaften
// Besonders robust gegen Ausreißer, ideal für unzuverlässige Datenquellen
func MedianAggregation(metrics []model.Metric) model.Metric {
	if len(metrics) == 0 {
		return model.Metric{
			APY:          0,
			TVL:          0,
			PointsPerETH: 0,
			CollectedAt:  0,
			Provider:     "aggregated",
		}
	}

	apyMedian := Median(metrics, func(m model.Metric) float64 { return m.APY })
	tvlMedian := Median(metrics, func(m model.Metric) float64 { return m.TVL })
	pointsMedian := Median(metrics, func(m model.Metric) float64 { return m.PointsPerETH })
	
	// Finde den neuesten Zeitstempel
	latestTimestamp := int64(0)
	for _, m := range metrics {
		if m.CollectedAt > latestTimestamp {
			latestTimestamp = m.CollectedAt
		}
	}

	return model.Metric{
		APY:          apyMedian,
		TVL:          tvlMedian,
		PointsPerETH: pointsMedian,
		CollectedAt:  latestTimestamp,
		Provider:     "aggregated",
	}
}

// TrimmedMeanAggregation berechnet getrimmte Mittelwerte (ohne extreme Werte)
// Entfernt einen bestimmten Prozentsatz der höchsten und niedrigsten Werte vor der Mittelwertbildung
func TrimmedMeanAggregation(metrics []model.Metric, trimPercent float64) model.Metric {
	if len(metrics) < 3 || trimPercent <= 0 || trimPercent >= 0.5 {
		return Weighted(metrics) // Fallback auf gewichteten Durchschnitt
	}

	// Sortiere Metriken nach APY
	validMetrics := make([]model.Metric, 0, len(metrics))
	for _, m := range metrics {
		if m.TVL > 0 && m.APY >= 0 && m.PointsPerETH >= 0 {
			validMetrics = append(validMetrics, m)
		}
	}
	
	if len(validMetrics) < 3 {
		return Weighted(metrics) // Fallback auf gewichteten Durchschnitt
	}

	sort.Slice(validMetrics, func(i, j int) bool {
		return validMetrics[i].APY < validMetrics[j].APY
	})

	// Berechne Anzahl der zu trimmenden Elemente
	trimCount := int(float64(len(validMetrics)) * trimPercent)
	
	// Trimme die Metriken
	trimmedMetrics := validMetrics[trimCount : len(validMetrics)-trimCount]
	
	// Berechne gewichteten Durchschnitt der getrimmten Metriken
	return Weighted(trimmedMetrics)
}
