package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupportedChainConstants(t *testing.T) {
	assert.Equal(t, SupportedChain("ethereum"), ChainEthereum)
	assert.Equal(t, SupportedChain("polygon"), ChainPolygon)
	assert.Equal(t, SupportedChain("arbitrum"), ChainArbitrum)
	assert.Equal(t, SupportedChain("optimism"), ChainOptimism)
	assert.Equal(t, SupportedChain("avalanche"), ChainAvalanche)
	assert.Equal(t, SupportedChain("binance"), ChainBSC)
	assert.Equal(t, SupportedChain("base"), ChainBase)
}

func TestChainConfigJSONRoundtrip(t *testing.T) {
	cfg := ChainConfig{
		Enabled:       true,
		RPCEndpoint:   "https://rpc.example.com",
		APIEndpoint:   "https://api.example.com",
		APIKey:        "secret-key",
		Weight:        1.5,
		GasMultiplier: 2.0,
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var decoded ChainConfig
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, cfg.Enabled, decoded.Enabled)
	assert.Equal(t, cfg.RPCEndpoint, decoded.RPCEndpoint)
	assert.Equal(t, cfg.APIEndpoint, decoded.APIEndpoint)
	assert.Equal(t, cfg.Weight, decoded.Weight)
	assert.Equal(t, cfg.GasMultiplier, decoded.GasMultiplier)
}

func TestChainConfigDefaults(t *testing.T) {
	var cfg ChainConfig
	assert.False(t, cfg.Enabled)
	assert.Equal(t, "", cfg.RPCEndpoint)
	assert.Equal(t, 0.0, cfg.Weight)
}

func TestSupportedChainStringConversion(t *testing.T) {
	chain := ChainEthereum
	assert.Equal(t, "ethereum", string(chain))
}
