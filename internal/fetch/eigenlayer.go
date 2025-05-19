// Package fetch provides API clients for various yield data providers
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/config"
	"github.com/yourorg/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// EigenLayerClient implements a client for the EigenLayer API
type EigenLayerClient struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
}

// NewEigenLayerClient creates a new EigenLayer API client
func NewEigenLayerClient() *EigenLayerClient {
	cfg := config.Load()
	retryClient := newRetryClient()
	return &EigenLayerClient{
		baseURL:    cfg.EigenURL,
		httpClient: StandardClient(retryClient),
		apiKey:     getAPIKey(cfg, "eigenlayer"),
	}
}

// Fetch retrieves yield data from the EigenLayer API.
func (c *EigenLayerClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/metrics", nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	logrus.Debugf("Fetching metrics from EigenLayer: %s", c.baseURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching data from EigenLayer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("EigenLayer API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Define the structure matching the EigenLayer API response
	var response struct {
		Data []struct {
			APY          float64 `json:"apy"`
			TVL          float64 `json:"tvl"`
			PointsPerETH float64 `json:"points_per_eth"`
			CollectedAt  int64   `json:"collected_at"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no data returned from EigenLayer")
	}

	// Convert API response to standard metric format
	var metrics []model.Metric
	for _, data := range response.Data {
		metrics = append(metrics, model.Metric{
			Provider:     "eigenlayer",
			APY:          data.APY,
			TVL:          data.TVL,
			PointsPerETH: data.PointsPerETH,
			CollectedAt:  data.CollectedAt,
		})
	}

	logrus.Debugf("Received %d metrics from EigenLayer", len(metrics))
	return metrics, nil
}

// newRetryClient creates an HTTP client with retry logic.
func newRetryClient() *http.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 1 * time.Second
	retryClient.RetryWaitMax = 5 * time.Second
	return retryClient.StandardClient()
}

// getAPIKey retrieves the API key from the environment variable.
func getAPIKey(cfg *config.Config, key string) string {
	apiKey := os.Getenv(key)
	if apiKey == "" {
		fmt.Printf("Warning: %s not set\n", key)
	}
	return apiKey
}

// getEnvOrDefault retrieves a value from the environment variable or returns the default value.
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}