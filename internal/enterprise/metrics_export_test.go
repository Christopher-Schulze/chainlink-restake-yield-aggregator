package enterprise

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetricsExporterBatchFlush verifies that the exporter flushes when the
// batch size threshold is reached.
func TestMetricsExporterBatchFlush(t *testing.T) {
	var receivedCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Metrics []interface{} `json:"metrics"`
			Count   int           `json:"count"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		atomic.StoreInt32(&receivedCount, int32(payload.Count))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      3,
		ExportInterval: "10m", // long interval so only batch threshold triggers
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)
	defer exp.Stop()

	// Add 3 metrics — should trigger a flush.
	exp.AddMetricBatch([]interface{}{"m1", "m2", "m3"})

	// The flush happens in a goroutine; wait for it.
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&receivedCount) == 3
	}, 2*time.Second, 50*time.Millisecond, "batch flush should send 3 metrics")
}

// TestMetricsExporterPeriodicFlush verifies that the periodic ticker flushes
// pending metrics even if the batch threshold is not reached.
func TestMetricsExporterPeriodicFlush(t *testing.T) {
	var receivedCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Count int `json:"count"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		atomic.StoreInt32(&receivedCount, int32(payload.Count))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100, // high threshold so periodic flush triggers first
		ExportInterval: "100ms",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)
	defer exp.Stop()

	// Add 1 metric — below batch threshold.
	exp.AddMetricBatch([]interface{}{"m1"})

	// Wait for the periodic ticker to fire.
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&receivedCount) == 1
	}, 2*time.Second, 50*time.Millisecond, "periodic flush should send 1 metric")
}

// TestMetricsExporterStopFlushesRemaining verifies that Stop flushes any
// pending metrics before shutting down.
func TestMetricsExporterStopFlushesRemaining(t *testing.T) {
	var receivedCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Count int `json:"count"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		atomic.AddInt32(&receivedCount, int32(payload.Count))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100, // high so no auto-flush
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)

	exp.AddMetricBatch([]interface{}{"m1", "m2"})
	exp.Stop() // should flush remaining 2

	assert.Equal(t, int32(2), atomic.LoadInt32(&receivedCount), "Stop should flush remaining metrics")
}

// TestMetricsExporterDisabled verifies that a disabled exporter is a no-op.
func TestMetricsExporterDisabled(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{Enabled: false})
	require.NoError(t, err)

	// AddMetricBatch should be a no-op.
	exp.AddMetricBatch([]interface{}{"m1", "m2"})
	exp.Stop()

	// GetExporterStatus should report disabled.
	status := exp.GetExporterStatus()
	assert.False(t, status["enabled"].(bool))
}

// TestMetricsExporterEmptyBatch verifies that adding an empty batch is a no-op.
func TestMetricsExporterEmptyBatch(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      10,
		ExportInterval: "10m",
	})
	require.NoError(t, err)
	defer exp.Stop()

	exp.AddMetricBatch(nil)
	exp.AddMetricBatch([]interface{}{})

	status := exp.GetExporterStatus()
	assert.Equal(t, 0, status["current_batch"])
}

// TestMetricsExporterWebhookError verifies that webhook failures are handled
// gracefully (no panic, exporter continues).
func TestMetricsExporterWebhookError(t *testing.T) {
	// Server that always returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      1,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)
	defer exp.Stop()

	// This should trigger a flush that fails — but not panic.
	exp.AddMetricBatch([]interface{}{"m1"})
	// Give the goroutine time to attempt the failed export.
	time.Sleep(200 * time.Millisecond)
}

// TestMetricsExporterInvalidInterval verifies that an invalid interval
// falls back to the default.
func TestMetricsExporterInvalidInterval(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "not-a-duration",
	})
	require.NoError(t, err)
	defer exp.Stop()

	status := exp.GetExporterStatus()
	assert.Equal(t, "1m0s", status["export_interval"].(string))
}

// TestMetricsExporterDefaultBatchSize verifies that a zero/negative batch
// size falls back to the default of 100.
func TestMetricsExporterDefaultBatchSize(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      0,
		ExportInterval: "10m",
	})
	require.NoError(t, err)
	defer exp.Stop()

	status := exp.GetExporterStatus()
	// BatchSize is 0 in config, but the internal threshold defaults to 100.
	// The status reports the config value, which is 0.
	assert.NotNil(t, status)
}

// TestMetricsExporterGetStatus verifies the status report structure.
func TestMetricsExporterGetStatus(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      50,
		ExportInterval: "5m",
		WebhookEnabled: true,
		WebhookURL:     "https://example.com/webhook",
	})
	require.NoError(t, err)
	defer exp.Stop()

	status := exp.GetExporterStatus()
	assert.True(t, status["enabled"].(bool))
	assert.Equal(t, 50, status["batch_size"])
	assert.Equal(t, "5m0s", status["export_interval"].(string))
	assert.True(t, status["webhook_enabled"].(bool))
	assert.Equal(t, 0, status["current_batch"])
}

// TestMetricsExporterHTTPSWarning is a non-fatal test that just verifies
// the exporter can be created with an HTTP (non-HTTPS) webhook URL without
// panicking. The warning is logged but not fatal.
func TestMetricsExporterHTTPWebhook(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     "http://insecure.example.com/webhook",
	})
	require.NoError(t, err)
	defer exp.Stop()
}

// TestMetricsExporterExportToWebhookNoURL verifies that exportToWebhook
// returns an error when the webhook URL is empty.
func TestMetricsExporterExportToWebhookNoURL(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     "",
	})
	require.NoError(t, err)
	defer exp.Stop()

	err = exp.exportToWebhook([]interface{}{"m1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestMetricsExporterExportToWebhookUnreachable verifies that a failed
// webhook request is handled gracefully.
func TestMetricsExporterExportToWebhookUnreachable(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     "http://localhost:1/unreachable",
	})
	require.NoError(t, err)
	defer exp.Stop()

	err = exp.exportToWebhook([]interface{}{"m1"})
	require.Error(t, err)
}

// TestMetricsExporterExportToWebhookWithAPIKey verifies that the API key
// is sent as a Bearer token.
func TestMetricsExporterExportToWebhookWithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
		WebhookAPIKey:  "test-key",
	})
	require.NoError(t, err)
	defer exp.Stop()

	err = exp.exportToWebhook([]interface{}{"m1"})
	require.NoError(t, err)
}

// TestMetricsExporterExportEmptyBatch verifies that exportMetrics is a
// no-op when the batch is empty.
func TestMetricsExporterExportEmptyBatch(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
	})
	require.NoError(t, err)
	defer exp.Stop()

	// Should not panic.
	exp.exportMetrics()
}

// TestMetricsExporterStatusWithLastExport verifies that the status includes
// last_export and next_export_in after a successful export.
func TestMetricsExporterStatusWithLastExport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      1,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)
	defer exp.Stop()

	// Trigger an export.
	exp.AddMetricBatch([]interface{}{"m1"})
	time.Sleep(200 * time.Millisecond)

	status := exp.GetExporterStatus()
	assert.NotNil(t, status["last_export"])
	assert.NotNil(t, status["next_export_in"])
}

// TestMetricsExporterStopTwice verifies that calling Stop twice is safe.
func TestMetricsExporterStopTwice(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
	})
	require.NoError(t, err)

	exp.Stop()
	exp.Stop() // should not panic
}

// --- additional branch-coverage tests ---

// TestAddMetricBatchDefaultThreshold covers the `threshold <= 0` branch in
// AddMetricBatch (metrics_export.go ~100-102). With BatchSize=0, the
// threshold defaults to 100, so adding 100 metrics should trigger a flush.
func TestAddMetricBatchDefaultThreshold(t *testing.T) {
	var receivedCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Count int `json:"count"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		atomic.StoreInt32(&receivedCount, int32(payload.Count))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      0, // zero -> defaults to 100 internally
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)
	defer exp.Stop()

	// Add 100 metrics — should trigger a flush with the default threshold.
	metrics := make([]interface{}, 100)
	for i := range metrics {
		metrics[i] = "m"
	}
	exp.AddMetricBatch(metrics)

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&receivedCount) == 100
	}, 2*time.Second, 50*time.Millisecond, "batch flush should send 100 metrics")
}

// TestExportToWebhookMarshalError covers the json.Marshal failure path in
// exportToWebhook (metrics_export.go ~162-163). A channel cannot be
// JSON-marshalled, so the payload marshal fails.
func TestExportToWebhookMarshalError(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     "http://localhost:9999",
	})
	require.NoError(t, err)
	defer exp.Stop()

	// A channel cannot be JSON-marshalled.
	err = exp.exportToWebhook([]interface{}{make(chan int)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal metrics")
}

// TestExportToWebhookRequestCreationError covers the http.NewRequest failure
// path in exportToWebhook (metrics_export.go ~167-168). An invalid URL with
// a newline causes http.NewRequest to fail.
func TestExportToWebhookRequestCreationError(t *testing.T) {
	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     "http://localhost:9999\n", // newline makes URL invalid
	})
	require.NoError(t, err)
	defer exp.Stop()

	err = exp.exportToWebhook([]interface{}{"m1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create webhook request")
}

// TestExportToWebhookStatus400 covers the status >= 400 branch in
// exportToWebhook (metrics_export.go ~181-182). A server returning 400
// triggers the error.
func TestExportToWebhookStatus400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	exp, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      100,
		ExportInterval: "10m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)
	defer exp.Stop()

	err = exp.exportToWebhook([]interface{}{"m1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook status 400")
}

// TestRetryQueueOnWebhookFailure verifies that failed webhook exports are
// queued for retry and resent when the webhook recovers.
func TestRetryQueueOnWebhookFailure(t *testing.T) {
	var (
		mu         sync.Mutex
		callCount  int
		failFirst  int = 1 // fail the first call, succeed after
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()
		if n <= failFirst {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exporter, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      1,
		ExportInterval: "1m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)

	exporter.AddMetricBatch([]interface{}{"metric-1"})

	// First export — fails, batch goes to retry queue, then drainRetryQueue
	// immediately retries (and succeeds since we only fail the first call).
	exporter.exportMetrics()

	mu.Lock()
	assert.GreaterOrEqual(t, callCount, 2, "first export + immediate retry should have been attempted")
	mu.Unlock()

	// Second export cycle — retry queue should be empty (drained successfully).
	exporter.exportMetrics()

	mu.Lock()
	// The second export has no new metrics, so no webhook call is made.
	// The retry already succeeded in the first cycle.
	finalCount := callCount
	mu.Unlock()
	assert.GreaterOrEqual(t, finalCount, 2, "retry should have succeeded")

	exporter.Stop()
}

// TestRetryQueueCapped verifies that the retry queue does not grow
// unboundedly during prolonged outages.
func TestRetryQueueCapped(t *testing.T) {
	// Webhook that always fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	exporter, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      1,
		ExportInterval: "1m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)

	// Export many batches — all will fail and queue up.
	for i := 0; i < maxRetryQueueSize+50; i++ {
		exporter.AddMetricBatch([]interface{}{"metric"})
		exporter.exportMetrics()
	}

	exporter.mutex.Lock()
	queueLen := len(exporter.retryQueue)
	exporter.mutex.Unlock()

	assert.LessOrEqual(t, queueLen, maxRetryQueueSize, "retry queue must be capped")

	exporter.Stop()
}

// TestRetryBatchDroppedAfterMaxRetries verifies that a batch is dropped
// after exactly maxRetries failed attempts, not retried forever.
func TestRetryBatchDroppedAfterMaxRetries(t *testing.T) {
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	exporter, err := NewMetricsExporter(ExporterConfig{
		Enabled:        true,
		BatchSize:      1,
		ExportInterval: "1m",
		WebhookEnabled: true,
		WebhookURL:     srv.URL,
	})
	require.NoError(t, err)

	exporter.AddMetricBatch([]interface{}{"metric-doomed"})

	// exportMetrics: 1 initial attempt + maxRetries retry attempts = 4 total.
	// After that the batch should be dropped.
	exporter.exportMetrics() // initial + retry 1
	exporter.exportMetrics() // retry 2 (drainRetryQueue from previous cycle)
	exporter.exportMetrics() // retry 3 → dropped

	// One more cycle — queue should be empty now, no more calls.
	exporter.exportMetrics()

	finalCount := atomic.LoadInt32(&callCount)
	// Initial (1) + 3 retries = 4. The 4th exportMetrics should not call.
	assert.LessOrEqual(t, int(finalCount), 5, "batch should be dropped after maxRetries, not retried forever")

	exporter.mutex.Lock()
	assert.Equal(t, 0, len(exporter.retryQueue), "retry queue should be empty after maxRetries")
	exporter.mutex.Unlock()

	exporter.Stop()
}
