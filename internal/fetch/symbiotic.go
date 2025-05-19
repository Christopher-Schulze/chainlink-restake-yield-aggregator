package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/config"
	"github.com/yourorg/restake-yield-ea/internal/model"
)

type SymbioticClient struct {
	cfg config.Config
}

func (c *SymbioticClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	client := newRetryClient()
	req, err := retryablehttp.NewRequest("GET", c.cfg.SymbioticURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if k := getAPIKey(c.cfg, "symbiotic"); k != "" {
		req.Header.Set("Authorization", k)
	}

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var data struct {
		APY float64 `json:"apy"`
		TVL float64 `json:"tvl"`
		// PointsPerETH may be absent in response
		PointsPerETH float64 `json:"pointsPerETH,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return []model.Metric{
		{
			APY:          data.APY,
			TVL:          data.TVL,
			PointsPerETH: data.PointsPerETH, // Will be 0 if field was absent
			CollectedAt:  time.Now().Unix(),
			Provider:     "symbiotic",
		},
	}, nil
}