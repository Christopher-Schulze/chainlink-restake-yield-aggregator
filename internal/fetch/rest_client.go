package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// restClient is a generic REST client for yield data providers that expose a
// simple JSON API returning a "data" array of metric objects. It is shared by
// the EigenLayer, Karak, and Symbiotic clients, which differ only in name,
// configured URL, and API key.
//
// The client only becomes active when an API URL is supplied via configuration.
// When the URL is empty, Fetch returns a configuration error so the aggregator
// can skip the provider rather than silently hitting a fake endpoint.
type restClient struct {
	name       string
	apiURL     string
	apiKey     string
	httpClient *http.Client
}

// Name returns the provider identifier.
func (c *restClient) Name() string { return c.name }

// Fetch retrieves yield data from the configured API endpoint.
//
// The expected response format is:
//
//	{
//	  "data": [
//	    {
//	      "protocol": "<name>",
//	      "apy": 0.04,
//	      "tvl": 1000,
//	      "points_per_eth": 1.1,
//	      "collected_at": 1700000000
//	    }
//	  ]
//	}
//
// If no API URL has been configured, Fetch returns an error instructing the
// operator to set the corresponding environment variable.
func (c *restClient) Fetch(ctx context.Context) ([]model.Metric, error) {
	if c.apiURL == "" {
		return nil, fmt.Errorf("%s: API endpoint not configured (set %s_API)",
			c.name, strings.ToUpper(c.name))
	}

	// Warn if an API key is being sent over a non-HTTPS connection.
	if c.apiKey != "" && !isHTTPS(c.apiURL) {
		logrus.Warnf("%s: API key sent over non-HTTPS connection to %s", c.name, c.apiURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: create request: %w", c.name, err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	logrus.Debugf("%s: GET %s", c.name, c.apiURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", c.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, fmt.Errorf("%s: status %d: %s", c.name, resp.StatusCode, string(body))
	}

	// Cap the response body before decoding to prevent a malicious or
	// misconfigured endpoint from exhausting server memory with a huge
	// payload. 16 MiB is far above any legitimate yield-API response.
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))

	var response struct {
		Data []struct {
			Protocol     string  `json:"protocol"`
			APY          float64 `json:"apy"`
			TVL          float64 `json:"tvl"`
			PointsPerETH float64 `json:"points_per_eth"`
			CollectedAt  int64   `json:"collected_at"`
		} `json:"data"`
	}
	if err := dec.Decode(&response); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", c.name, err)
	}
	if len(response.Data) == 0 {
		return nil, fmt.Errorf("%s: no data returned", c.name)
	}

	now := time.Now().Unix()
	metrics := make([]model.Metric, 0, len(response.Data))
	for _, d := range response.Data {
		ts := d.CollectedAt
		if ts == 0 {
			ts = now
		}
		metrics = append(metrics, model.Metric{
			Provider:     c.name,
			APY:          d.APY,
			TVL:          d.TVL,
			PointsPerETH: d.PointsPerETH,
			CollectedAt:  ts,
			Chain:        "ethereum",
		})
	}
	return metrics, nil
}

// isHTTPS reports whether the given URL uses the https scheme.
func isHTTPS(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https")
}
