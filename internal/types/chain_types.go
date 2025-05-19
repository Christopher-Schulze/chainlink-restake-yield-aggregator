// Package types contains shared type definitions used across multiple packages
package types

// SupportedChain represents a blockchain network supported by the adapter
type SupportedChain string

// Supported blockchain networks
const (
	ChainEthereum   SupportedChain = "ethereum"
	ChainPolygon    SupportedChain = "polygon"
	ChainArbitrum   SupportedChain = "arbitrum"
	ChainOptimism   SupportedChain = "optimism"
	ChainAvalanche  SupportedChain = "avalanche"
	ChainBSC        SupportedChain = "binance"
	ChainBase       SupportedChain = "base"
)

// ChainConfig holds configuration for a specific blockchain network
type ChainConfig struct {
	Enabled       bool    `json:"enabled"`
	RPCEndpoint   string  `json:"rpc_endpoint"`
	APIEndpoint   string  `json:"api_endpoint"`
	APIKey        string  `json:"api_key,omitempty"`
	Weight        float64 `json:"weight"`       // Weight for cross-chain aggregation
	GasMultiplier float64 `json:"gas_multiple"` // For gas cost normalization
}
