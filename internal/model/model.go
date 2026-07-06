// Package model defines the core data structures for the restake-yield-ea.
package model

import "time"

// schemaVersion is the data schema version stamped on every metric.
const schemaVersion = "1.0"

// Metric represents a single yield metric data point from a provider.
// This is the core data structure that flows through the entire application.
type Metric struct {
	// Provider is the unique identifier of the data source (e.g. "defillama").
	Provider string `json:"provider"`

	// APY is the Annual Percentage Yield as a decimal (0.05 = 5%).
	APY float64 `json:"apy"`

	// TVL is the Total Value Locked in the protocol, in ETH.
	TVL float64 `json:"tvl"`

	// PointsPerETH is the conversion rate of ETH to protocol-specific points.
	PointsPerETH float64 `json:"points_per_eth"`

	// CollectedAt is the Unix timestamp when this metric was collected.
	CollectedAt int64 `json:"collected_at"`

	// Confidence is a 0..1 data quality score assigned by the validation layer.
	Confidence float64 `json:"confidence,omitempty"`

	// Chain is the blockchain this metric is for (e.g. "ethereum").
	Chain string `json:"chain,omitempty"`

	// Symbol is the LRT token symbol (e.g. "weETH", "ezETH") when applicable.
	Symbol string `json:"symbol,omitempty"`

	// Weight is the cross-chain aggregation weight (default 1.0).
	Weight float64 `json:"weight,omitempty"`

	// Version is the data schema version.
	Version string `json:"version,omitempty"`
}

// NewMetric creates a new metric with the current timestamp.
func NewMetric(provider string, apy, tvl, pointsPerETH float64) Metric {
	return Metric{
		Provider:     provider,
		APY:          apy,
		TVL:          tvl,
		PointsPerETH: pointsPerETH,
		CollectedAt:  time.Now().Unix(),
		Version:      schemaVersion,
	}
}

// IsValid performs basic plausibility validation on this metric.
func (m Metric) IsValid() bool {
	return m.APY >= 0 &&
		m.TVL > 0 &&
		m.PointsPerETH >= 0 &&
		time.Since(time.Unix(m.CollectedAt, 0)) < 24*time.Hour &&
		m.Provider != ""
}

// WithConfidence returns a copy of m with the confidence score set.
func (m Metric) WithConfidence(confidence float64) Metric {
	m.Confidence = confidence
	return m
}
