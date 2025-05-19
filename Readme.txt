
# Restake Yield Aggregator (restake-yield-ea)

## Overview

Restake Yield Aggregator is a robust, high-performance Go module designed to aggregate, validate, and analyze staking yield metrics from multiple decentralized finance (DeFi) providers. Built with extensibility, reliability, and statistical rigor in mind, this project is ideal for powering Chainlink External Adapters, DeFi dashboards, or any application that requires trustworthy, outlier-resistant yield data.

## Motivation

The DeFi ecosystem is rapidly evolving, with new staking protocols and yield sources emerging constantly. However, yield data from different providers can be inconsistent, noisy, or even manipulated. Outliers and invalid data can skew results, leading to poor decisions or even financial loss.

**Restake Yield Aggregator** was created to solve this problem by:
- Providing multiple aggregation strategies (weighted average, median, trimmed mean, etc.) to ensure robust, manipulation-resistant results.
- Validating and filtering metrics to guarantee only high-quality, recent, and plausible data is used.
- Offering parallelized, high-performance computation for real-time applications and large datasets.
- Enabling easy extension for new aggregation or validation strategies.

## Features

- **Weighted Aggregation:** TVL-weighted averages for APY, TVL, and PointsPerETH.
- **Median & Trimmed Mean:** Robust statistical methods to mitigate the impact of outliers.
- **Outlier Detection:** IQR-based filtering to automatically remove statistical anomalies.
- **Parallel Processing:** Goroutine-powered validation and aggregation for maximum throughput.
- **Comprehensive Validation:** Ensures metrics are recent, non-negative, and from trusted providers.
- **Extensive Testing & Benchmarking:** Includes unit tests and advanced benchmarks for all core functions.

## How It Works

1. **Data Collection:** Metrics are gathered from various staking providers.
2. **Validation:** Each metric is checked for plausibility (e.g., no negative APY, recent timestamp).
3. **Outlier Filtering:** Optional removal of statistical outliers using IQR or trimmed mean.
4. **Aggregation:** Multiple strategies available:
   - TVL-weighted average
   - Median
   - Trimmed mean (removes a configurable percentage of extreme values)
   - Simple average
5. **Result:** A single, robust, manipulation-resistant metric is produced for downstream use.

## Example Usage

```go
metrics := []model.Metric{ /* ... collected from providers ... */ }

// Weighted aggregation
result := aggregate.Weighted(metrics)

// Median aggregation
median := aggregate.MedianAggregation(metrics)

// Trimmed mean (removes 10% of extreme values)
trimmed := aggregate.TrimmedMeanAggregation(metrics, 0.1)

// Outlier filtering + aggregation
filtered := aggregate.FilterOutliers(metrics)
robust := aggregate.Weighted(filtered)
```

## Performance

The module is optimized for both small and large datasets. Parallelized functions leverage Go's concurrency model for rapid validation and aggregation, as demonstrated by included benchmarks.

## Extensibility

- **Add new aggregation strategies** by implementing a simple function signature.
- **Customize validation** to fit new protocols or data sources.
- **Plug into Chainlink External Adapters** or any Go-based DeFi backend.

## Why This Matters (and Why Chainlink)

Reliable, manipulation-resistant data is the backbone of secure DeFi protocols and oracles. By combining statistical rigor with high-performance Go code, this project demonstrates:
- Deep understanding of DeFi data challenges
- Advanced Go programming and concurrency skills
- Commitment to open-source quality and extensibility

**This project is a testament to my ability to deliver production-grade, secure, and innovative solutions for the Chainlink ecosystem and beyond.**

## Tests & Benchmarks

Run all tests:
```bash
go test ./internal/aggregate/...
```

Run benchmarks:
```bash
go test -bench=. -benchmem ./internal/aggregate
```

## License

MIT License

---

*Created with ❤️ for the Chainlink community and the future of decentralized finance.*