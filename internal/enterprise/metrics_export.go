// Package enterprise provides optional features beyond the core EA pipeline:
// webhook-based metrics export with batching and graceful shutdown.
package enterprise

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// MetricsExporter batches aggregated metrics and forwards them to a configured
// webhook endpoint. Export is triggered by batch-size threshold or a periodic
// ticker, whichever comes first.
type MetricsExporter struct {
	config         ExporterConfig
	httpClient     *http.Client
	mutex          sync.Mutex
	batchMetrics   []interface{}
	retryQueue     []retryBatch // failed batches pending retry
	lastExport     time.Time
	exportInterval time.Duration
	exportCancel   context.CancelFunc
}

// retryBatch wraps a failed export batch with a retry counter.
// After maxRetries attempts, the batch is dropped to prevent
// unbounded retry loops during prolonged outages.
type retryBatch struct {
	metrics     []interface{}
	retryCount  int
}

// ExporterConfig holds configuration for the webhook metrics exporter.
type ExporterConfig struct {
	Enabled        bool   `json:"enabled"`
	BatchSize      int    `json:"batch_size"`
	ExportInterval string `json:"export_interval"`

	WebhookEnabled bool   `json:"webhook_enabled"`
	WebhookURL     string `json:"webhook_url"`
	WebhookAPIKey  string `json:"webhook_api_key,omitempty"`
}

// NewMetricsExporter creates a webhook-based metrics exporter.
func NewMetricsExporter(config ExporterConfig) (*MetricsExporter, error) {
	if !config.Enabled {
		return &MetricsExporter{config: config}, nil
	}

	// Warn if the webhook URL is not HTTPS — API keys are sent as Bearer tokens.
	if config.WebhookEnabled && config.WebhookURL != "" {
		if u, err := url.Parse(config.WebhookURL); err != nil || !strings.EqualFold(u.Scheme, "https") {
			logrus.Warnf("metrics exporter: webhook URL is not HTTPS (%s); API key may be exposed", config.WebhookURL)
		}
	}

	exportInterval, err := time.ParseDuration(config.ExportInterval)
	if err != nil {
		logrus.Warnf("invalid export_interval %q, defaulting to 1m: %v", config.ExportInterval, err)
		exportInterval = 1 * time.Minute
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			IdleConnTimeout: 90 * time.Second,
		},
	}

	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	exporter := &MetricsExporter{
		config:         config,
		httpClient:     httpClient,
		batchMetrics:   make([]interface{}, 0, batchSize),
		exportInterval: exportInterval,
	}

	ctx, cancel := context.WithCancel(context.Background())
	exporter.exportCancel = cancel
	go exporter.periodicExport(ctx)

	logrus.Info("metrics exporter initialized (webhook)")
	return exporter, nil
}

// AddMetricBatch adds metrics to the batch for export.
func (e *MetricsExporter) AddMetricBatch(metrics []interface{}) {
	if !e.config.Enabled || len(metrics) == 0 {
		return
	}

	e.mutex.Lock()
	e.batchMetrics = append(e.batchMetrics, metrics...)
	threshold := e.config.BatchSize
	if threshold <= 0 {
		threshold = 100
	}
	shouldFlush := len(e.batchMetrics) >= threshold
	e.mutex.Unlock()

	if shouldFlush {
		go e.exportMetrics()
	}
}

// periodicExport runs until ctx is cancelled, flushing on each tick.
func (e *MetricsExporter) periodicExport(ctx context.Context) {
	ticker := time.NewTicker(e.exportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.exportMetrics()
		case <-ctx.Done():
			return
		}
	}
}

// exportMetrics flushes the current batch to the webhook and drains any
// pending retry batches. Even when there are no new metrics, the retry
// queue is drained so that failed batches get resent on each export cycle.
func (e *MetricsExporter) exportMetrics() {
	e.mutex.Lock()
	if len(e.batchMetrics) == 0 {
		e.mutex.Unlock()
		// Still drain the retry queue — failed batches from previous
		// cycles should be retried even when no new metrics arrived.
		e.drainRetryQueue()
		return
	}
	metrics := make([]interface{}, len(e.batchMetrics))
	copy(metrics, e.batchMetrics)
	e.batchMetrics = make([]interface{}, 0, cap(e.batchMetrics))
	e.lastExport = time.Now()
	e.mutex.Unlock()

	if err := e.exportToWebhook(metrics); err != nil {
		logrus.Errorf("webhook export failed: %v", err)
		// Queue the failed batch for retry.
		// The retry queue is capped to prevent unbounded memory growth
		// during prolonged outages.
		e.mutex.Lock()
		if len(e.retryQueue) < maxRetryQueueSize {
			e.retryQueue = append(e.retryQueue, retryBatch{metrics: metrics, retryCount: 0})
		} else {
			logrus.Warnf("metrics retry queue full (%d), dropping batch", len(e.retryQueue))
		}
		e.mutex.Unlock()
	} else {
		logrus.Debugf("exported %d metrics to webhook", len(metrics))
	}

	// Attempt to drain the retry queue.
	e.drainRetryQueue()
}

// maxRetryQueueSize caps the number of pending retry batches. Each batch
// can contain up to ExporterConfig.BatchSize metrics.
const maxRetryQueueSize = 100

// maxRetries is the maximum number of retry attempts per batch before it
// is dropped. This prevents unbounded retry loops during prolonged outages.
const maxRetries = 3

func (e *MetricsExporter) drainRetryQueue() {
	e.mutex.Lock()
	if len(e.retryQueue) == 0 {
		e.mutex.Unlock()
		return
	}
	queue := e.retryQueue
	e.retryQueue = nil
	e.mutex.Unlock()

	var remaining []retryBatch
	for _, rb := range queue {
		if err := e.exportToWebhook(rb.metrics); err != nil {
			rb.retryCount++
			if rb.retryCount >= maxRetries {
				logrus.Errorf("webhook retry exhausted after %d attempts, dropping %d metrics",
					rb.retryCount, len(rb.metrics))
				continue
			}
			logrus.Warnf("webhook retry %d/%d failed: %v", rb.retryCount, maxRetries, err)
			remaining = append(remaining, rb)
		} else {
			logrus.Debugf("retry: exported %d metrics to webhook", len(rb.metrics))
		}
	}

	if len(remaining) > 0 {
		e.mutex.Lock()
		// Prepend remaining batches to the front of the queue so they
		// get retried first on the next export cycle.
		e.retryQueue = append(remaining, e.retryQueue...)
		if len(e.retryQueue) > maxRetryQueueSize {
			e.retryQueue = e.retryQueue[:maxRetryQueueSize]
		}
		e.mutex.Unlock()
	}
}

// exportToWebhook sends a JSON POST to the configured webhook endpoint.
func (e *MetricsExporter) exportToWebhook(metrics []interface{}) error {
	if e.config.WebhookURL == "" {
		return fmt.Errorf("webhook URL not configured")
	}

	payload := struct {
		Metrics    []interface{} `json:"metrics"`
		ExportTime string        `json:"export_time"`
		Count      int           `json:"count"`
	}{
		Metrics:    metrics,
		ExportTime: time.Now().UTC().Format(time.RFC3339),
		Count:      len(metrics),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, e.config.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.config.WebhookAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.config.WebhookAPIKey)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

// Stop cancels the periodic export and flushes remaining metrics.
func (e *MetricsExporter) Stop() {
	if e.exportCancel != nil {
		e.exportCancel()
	}
	e.exportMetrics()
}

// GetExporterStatus returns the current exporter state for /status.
func (e *MetricsExporter) GetExporterStatus() map[string]interface{} {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	status := map[string]interface{}{
		"enabled":         e.config.Enabled,
		"batch_size":      e.config.BatchSize,
		"export_interval": e.exportInterval.String(),
		"current_batch":   len(e.batchMetrics),
		"webhook_enabled": e.config.WebhookEnabled,
	}
	if !e.lastExport.IsZero() {
		status["last_export"] = e.lastExport.Format(time.RFC3339)
		status["next_export_in"] = e.exportInterval - time.Since(e.lastExport)
	}
	return status
}
