// Package model defines the core data structures for the restake-yield-ea.
package model

import (
	"time"
)

// Metric represents a single yield metric data point from a provider.
// This is the core data structure that flows through the entire application.
type Metric struct {
	// Provider is the unique identifier of the data source
	Provider string `json:"provider"`
	
	// APY is the Annual Percentage Yield, expressed as a decimal
	// e.g., 0.05 for 5% APY
	APY float64 `json:"apy"`
	
	// TVL is the Total Value Locked in the protocol in ETH
	TVL float64 `json:"tvl"`
	
	// PointsPerETH represents the conversion rate of ETH to protocol-specific points
	PointsPerETH float64 `json:"points_per_eth"`
	
	// CollectedAt is the Unix timestamp when this metric was collected
	CollectedAt int64 `json:"collected_at"`
	
	// Additional metadata like confidence interval or data quality score
	Confidence float64 `json:"confidence,omitempty"`
	
	// Risk score for the protocol (higher is riskier)
	RiskScore float64 `json:"risk_score,omitempty"`
	
	// Protocol name if different from provider
	Protocol string `json:"protocol,omitempty"`
	
	// Network or blockchain this metric is for
	Chain string `json:"chain,omitempty"`
	
	// Weight for cross-chain aggregation
	Weight float64 `json:"weight,omitempty"`
	
	// Any error message associated with this metric
	Error string `json:"error,omitempty"`
	
	// Version indicates the data schema version
	Version string `json:"version,omitempty"`
}

// NewMetric creates a new metric with current timestamp
func NewMetric(provider string, apy, tvl, pointsPerETH float64) Metric {
	return Metric{
		Provider:     provider,
		APY:          apy,
		TVL:          tvl,
		PointsPerETH: pointsPerETH,
		CollectedAt:  time.Now().Unix(),
		Version:      "1.0",
	}
}

// IsValid performs basic validation on this metric
func (m Metric) IsValid() bool {
	return m.APY >= 0 && 
	       m.TVL > 0 &&
	       m.PointsPerETH >= 0 &&
	       time.Since(time.Unix(m.CollectedAt, 0)) < 24*time.Hour &&
	       m.Provider != ""
}

// WithConfidence adds a confidence score to the metric
func (m Metric) WithConfidence(confidence float64) Metric {
	m.Confidence = confidence
	return m
}
