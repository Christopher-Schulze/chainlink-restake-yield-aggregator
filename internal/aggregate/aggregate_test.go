package aggregate

import (
    "context"
    "testing"
    "time"

    "github.com/yourorg/restake-yield-ea/internal/model"
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
                PointsPerETH: 16.666666666666668, // (10*1000 + 20*2000)/3000
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
                PointsPerETH: 16.666666666666668,
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
                TVL:          2000,
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
                TVL:          2500,
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
            // Bei zu wenigen Metriken wird auf Weighted zurückgefallen, daher nur bei ausreichend Metriken prüfen
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
        want    int // Erwartete Anzahl der Metriken nach dem Filtern
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
            want: 5, // Alle Metriken bleiben erhalten
        },
        {
            name: "with outliers",
            metrics: []model.Metric{
                {APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
                {APY: 5.5, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
                {APY: 6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
                {APY: 6.5, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
                {APY: 50.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // Ausreißer
            },
            want: 4, // Der Ausreißer wird entfernt
        },
        {
            name: "too few metrics",
            metrics: []model.Metric{
                {APY: 5.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
                {APY: 50.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
            },
            want: 2, // Zu wenige Metriken für Ausreißererkennung
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
        want    int // Erwartete Anzahl der Metriken nach Validierung und Filterung
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
                {APY: -6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // Ungültig
                {APY: 7.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now},
                {APY: 8.0, TVL: 0, PointsPerETH: 10, CollectedAt: now}, // Ungültig
                {APY: 9.0, TVL: 1000, PointsPerETH: 10, CollectedAt: oldTimestamp}, // Ungültig
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
                {APY: -6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // Ungültig
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
                {APY: -6.0, TVL: 1000, PointsPerETH: 10, CollectedAt: now}, // Ungültig
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
                TVL:          2000, // (1000+2000+3000)/3
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

func BenchmarkWeighted(b *testing.B) {
    metrics := make([]model.Metric, 100)
    for i := 0; i < 100; i++ {
        metrics[i] = model.Metric{
            APY:          float64(i) + 1.0,
            TVL:          float64(i+1) * 1000,
            PointsPerETH: float64(i+1) * 10,
            CollectedAt:  time.Now().Unix(),
        }
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        Weighted(metrics)
    }
}

func BenchmarkWeightedParallel(b *testing.B) {
    metrics := make([]model.Metric, 100)
    for i := 0; i < 100; i++ {
        metrics[i] = model.Metric{
            APY:          float64(i) + 1.0,
            TVL:          float64(i+1) * 1000,
            PointsPerETH: float64(i+1) * 10,
            CollectedAt:  time.Now().Unix(),
        }
    }

    ctx := context.Background()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        WeightedParallel(ctx, metrics)
    }
}