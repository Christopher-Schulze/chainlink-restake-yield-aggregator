package validation

import (
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/stretchr/testify/assert"
)

// TestFilterOutliersSmallDatasetMAD locks in the MAD-based fix: with only 4
// points where the outlier is also the Q3 value, the IQR method alone fails to
// reject it. The MAD test must catch it.
func TestFilterOutliersSmallDatasetMAD(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name    string
		metrics []model.Metric
		want    int
	}{
		{
			name: "single extreme outlier among 4 (regression for IQR-only bug)",
			metrics: []model.Metric{
				{Provider: "p1", APY: 0.05, TVL: 1000, CollectedAt: now},
				{Provider: "p2", APY: 0.052, TVL: 1200, CollectedAt: now},
				{Provider: "p3", APY: 0.048, TVL: 900, CollectedAt: now},
				{Provider: "p4", APY: 0.3, TVL: 1100, CollectedAt: now},
			},
			want: 3,
		},
		{
			name: "no outliers among 4 keeps all",
			metrics: []model.Metric{
				{Provider: "p1", APY: 0.05, TVL: 1000, CollectedAt: now},
				{Provider: "p2", APY: 0.055, TVL: 1200, CollectedAt: now},
				{Provider: "p3", APY: 0.048, TVL: 900, CollectedAt: now},
				{Provider: "p4", APY: 0.052, TVL: 1100, CollectedAt: now},
			},
			want: 4,
		},
		{
			name: "low outlier among 4",
			metrics: []model.Metric{
				{Provider: "p1", APY: 0.05, TVL: 1000, CollectedAt: now},
				{Provider: "p2", APY: 0.052, TVL: 1200, CollectedAt: now},
				{Provider: "p3", APY: 0.048, TVL: 900, CollectedAt: now},
				{Provider: "p4", APY: -0.2, TVL: 1100, CollectedAt: now},
			},
			want: 3,
		},
		{
			name: "three identical + one outlier (scaledMAD==0 fallback)",
			metrics: []model.Metric{
				{Provider: "p1", APY: 0.05, TVL: 1000, CollectedAt: now},
				{Provider: "p2", APY: 0.05, TVL: 1200, CollectedAt: now},
				{Provider: "p3", APY: 0.05, TVL: 900, CollectedAt: now},
				{Provider: "p4", APY: 0.5, TVL: 1100, CollectedAt: now},
			},
			want: 3,
		},
	}

	opts := DefaultValidationOptions()
	opts.EnableOutlierDetection = true

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterInvalidWithOptions(tt.metrics, opts)
			assert.Len(t, filtered, tt.want)
		})
	}
}
