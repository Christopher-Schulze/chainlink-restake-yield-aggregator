// Package enterprise provides advanced features for enterprise-grade Chainlink integrations
package enterprise

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// MetricsExporter provides enterprise-grade metrics export capabilities
type MetricsExporter struct {
	config           ExporterConfig
	httpClient       *http.Client
	mutex            sync.RWMutex
	batchMetrics     []interface{}
	lastExport       time.Time
	exportInterval   time.Duration
	exportContext    context.Context
	exportCancel     context.CancelFunc
}

// ExporterConfig holds configuration for metrics exporting
type ExporterConfig struct {
	// General settings
	Enabled         bool   `json:"enabled"`
	BatchSize       int    `json:"batch_size"`
	ExportInterval  string `json:"export_interval"`
	DashboardURL    string `json:"dashboard_url"`
	
	// AWS settings
	AWSEnabled      bool   `json:"aws_enabled"`
	AWSRegion       string `json:"aws_region"`
	AWSAccessKey    string `json:"aws_access_key,omitempty"`
	AWSSecretKey    string `json:"aws_secret_key,omitempty"`
	CloudwatchGroup string `json:"cloudwatch_group"`
	S3Bucket        string `json:"s3_bucket"`
	S3KeyPrefix     string `json:"s3_key_prefix"`
	
	// Webhook settings
	WebhookEnabled  bool   `json:"webhook_enabled"`
	WebhookURL      string `json:"webhook_url"`
	WebhookAPIKey   string `json:"webhook_api_key,omitempty"`
	WebhookFormat   string `json:"webhook_format"`
	
	// Kafka settings
	KafkaEnabled    bool     `json:"kafka_enabled"`
	KafkaBrokers    []string `json:"kafka_brokers"`
	KafkaTopic      string   `json:"kafka_topic"`
	KafkaUsername   string   `json:"kafka_username,omitempty"`
	KafkaPassword   string   `json:"kafka_password,omitempty"`
}

// NewMetricsExporter creates a new metrics exporter
func NewMetricsExporter(config ExporterConfig) (*MetricsExporter, error) {
	if !config.Enabled {
		return &MetricsExporter{config: config}, nil
	}
	
	// Set up export interval
	exportInterval, err := time.ParseDuration(config.ExportInterval)
	if err != nil {
		exportInterval = 1 * time.Minute // Default
	}
	
	// Set up HTTP client for webhooks
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			IdleConnTimeout: 90 * time.Second,
		},
	}
	
	exporter := &MetricsExporter{
		config:         config,
		httpClient:     httpClient,
		batchMetrics:   make([]interface{}, 0, config.BatchSize),
		exportInterval: exportInterval,
	}
	
	// Start background task for periodic exports
	exporter.exportContext, exporter.exportCancel = context.WithCancel(context.Background())
	go exporter.periodicExport()
	
	logrus.Info("Enterprise metrics exporter initialized")
	return exporter, nil
}

// AddMetricBatch adds metrics to the batch for export
func (e *MetricsExporter) AddMetricBatch(metrics []interface{}) {
	if !e.config.Enabled || len(metrics) == 0 {
		return
	}
	
	e.mutex.Lock()
	defer e.mutex.Unlock()
	
	e.batchMetrics = append(e.batchMetrics, metrics...)
	
	// If we've reached batch size, export immediately
	if len(e.batchMetrics) >= e.config.BatchSize {
		go e.exportMetrics()
	}
}

// periodicExport runs a background task to periodically export metrics
func (e *MetricsExporter) periodicExport() {
	ticker := time.NewTicker(e.exportInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			e.exportMetrics()
		case <-e.exportContext.Done():
			return
		}
	}
}

// exportMetrics exports the current batch of metrics
func (e *MetricsExporter) exportMetrics() {
	e.mutex.Lock()
	
	// If there are no metrics to export, return
	if len(e.batchMetrics) == 0 {
		e.mutex.Unlock()
		return
	}
	
	// Copy the metrics batch and reset for new inputs
	metrics := make([]interface{}, len(e.batchMetrics))
	copy(metrics, e.batchMetrics)
	e.batchMetrics = make([]interface{}, 0, e.config.BatchSize)
	e.lastExport = time.Now()
	
	e.mutex.Unlock()
	
	// Perform exports in parallel
	var wg sync.WaitGroup
	
	if e.config.AWSEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.exportToAWS(metrics); err != nil {
				logrus.Errorf("Failed to export to AWS: %v", err)
			}
		}()
	}
	
	if e.config.WebhookEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.exportToWebhook(metrics); err != nil {
				logrus.Errorf("Failed to export to webhook: %v", err)
			}
		}()
	}
	
	if e.config.KafkaEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.exportToKafka(metrics); err != nil {
				logrus.Errorf("Failed to export to Kafka: %v", err)
			}
		}()
	}
	
	wg.Wait()
	logrus.Infof("Exported %d metrics to enterprise endpoints", len(metrics))
}

// exportToAWS exports metrics to AWS CloudWatch and S3
func (e *MetricsExporter) exportToAWS(metrics []interface{}) error {
	// In a real implementation, this would use the AWS SDK to export metrics
	// to CloudWatch and S3. For this example, we'll just log the operation.
	logrus.Infof("Would export %d metrics to AWS CloudWatch and S3", len(metrics))
	return nil
}

// exportToWebhook exports metrics to a webhook endpoint
func (e *MetricsExporter) exportToWebhook(metrics []interface{}) error {
	if e.config.WebhookURL == "" {
		return fmt.Errorf("webhook URL not configured")
	}
	
	// Prepare export data
	exportData := struct {
		Metrics    []interface{} `json:"metrics"`
		ExportTime string        `json:"export_time"`
		Count      int           `json:"count"`
	}{
		Metrics:    metrics,
		ExportTime: time.Now().UTC().Format(time.RFC3339),
		Count:      len(metrics),
	}
	
	// Convert to JSON
	jsonData, err := json.Marshal(exportData)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics: %w", err)
	}
	
	// Create request
	req, err := http.NewRequest("POST", e.config.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}
	
	// Add headers
	req.Header.Set("Content-Type", "application/json")
	if e.config.WebhookAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.config.WebhookAPIKey)
	}
	
	// Send request
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned error status: %d", resp.StatusCode)
	}
	
	return nil
}

// exportToKafka exports metrics to a Kafka topic
func (e *MetricsExporter) exportToKafka(metrics []interface{}) error {
	if !e.config.KafkaEnabled || len(e.config.KafkaBrokers) == 0 {
		return fmt.Errorf("Kafka not configured")
	}
	
	// Log the data that would be sent to Kafka
	logrus.Infof("Would export %d metrics to Kafka topic %s at brokers %s", 
		len(metrics), e.config.KafkaTopic, strings.Join(e.config.KafkaBrokers, ","))
	
	return nil
}

// Stop cleanly stops the exporter
func (e *MetricsExporter) Stop() {
	if e.exportCancel != nil {
		e.exportCancel()
	}
	
	// Export any remaining metrics
	e.exportMetrics()
}

// GetExporterStatus returns the current status of the exporter
func (e *MetricsExporter) GetExporterStatus() map[string]interface{} {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	
	status := map[string]interface{}{
		"enabled":          e.config.Enabled,
		"batch_size":       e.config.BatchSize,
		"export_interval":  e.exportInterval.String(),
		"current_batch":    len(e.batchMetrics),
		"aws_enabled":      e.config.AWSEnabled,
		"webhook_enabled":  e.config.WebhookEnabled,
		"kafka_enabled":    e.config.KafkaEnabled,
	}
	
	if !e.lastExport.IsZero() {
		status["last_export"] = e.lastExport.Format(time.RFC3339)
		status["next_export_in"] = e.exportInterval - time.Since(e.lastExport)
	}
	
	return status
}
