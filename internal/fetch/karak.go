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

type KarakClient struct {
	cfg config.Config
}

func (c *KarakClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	client := newRetryClient()
	
	graphqlQuery := `{"query":"{ vaults { apy tvl pointsPerETH } }"}`
	req, err := retryablehttp.NewRequest("POST", c.cfg.KarakURL, []byte(graphqlQuery))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if k := getAPIKey(c.cfg, "karak"); k != "" {
		req.Header.Set("Authorization", k)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var response struct {
		Data struct {
			Vaults []struct {
				APY          float64 `json:"apy"`
				TVL          float64 `json:"tvl"`
				PointsPerETH float64 `json:"pointsPerETH"`
			} `json:"vaults"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Data.Vaults) == 0 {
		return nil, fmt.Errorf("no vaults found in response")
	}

	// Use first vault in array as specified
	vault := response.Data.Vaults[0]
	return []model.Metric{
		{
			APY:          vault.APY,
			TVL:          vault.TVL,
			PointsPerETH: vault.PointsPerETH,
			CollectedAt:  time.Now().Unix(),
			Provider:     "karak",
		},
	}, nil
}