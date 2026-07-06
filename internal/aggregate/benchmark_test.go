package aggregate

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
)

// generateBenchmarkMetrics produces n synthetic metrics with realistic
// distributions: APY around 3-5%, TVL 100-10000 ETH, with a few outliers.
func generateBenchmarkMetrics(n int) []model.Metric {
	rng := rand.New(rand.NewSource(42))
	metrics := make([]model.Metric, n)
	now := time.Now().Unix()
	for i := range metrics {
		apy := 0.04 + rng.NormFloat64()*0.005 // 4% ± 0.5%
		tvl := 100 + rng.Float64()*9900       // 100-10000 ETH
		if i%20 == 0 {
			apy = 0.50 // 5% outlier
		}
		metrics[i] = model.Metric{
			Provider:     "bench",
			APY:          apy,
			TVL:          tvl,
			PointsPerETH: 1.0 + rng.Float64()*0.2,
			CollectedAt:  now,
		}
	}
	return metrics
}

func BenchmarkWeighted(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		metrics := generateBenchmarkMetrics(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Weighted(metrics)
			}
		})
	}
}

func BenchmarkMedianAggregation(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		metrics := generateBenchmarkMetrics(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MedianAggregation(metrics)
			}
		})
	}
}

func BenchmarkTrimmedMeanAggregation(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		metrics := generateBenchmarkMetrics(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				TrimmedMeanAggregation(metrics, 0.1)
			}
		})
	}
}

func BenchmarkWeightedParallel(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{10, 100, 1000} {
		metrics := generateBenchmarkMetrics(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				WeightedParallel(ctx, metrics)
			}
		})
	}
}
