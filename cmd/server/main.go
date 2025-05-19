// Package main is the entry point for the Restake Yield External Adapter, an ideal solution
// for Chainlink nodes to gather reliable, robust yield data from multiple providers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/aggregate"
	"github.com/yourorg/restake-yield-ea/internal/circuitbreaker"
	"github.com/yourorg/restake-yield-ea/internal/config"
	"github.com/yourorg/restake-yield-ea/internal/enterprise"
	"github.com/yourorg/restake-yield-ea/internal/fetch"
	"github.com/yourorg/restake-yield-ea/internal/model"
	"github.com/yourorg/restake-yield-ea/internal/security"
	"github.com/yourorg/restake-yield-ea/internal/types"
	"github.com/yourorg/restake-yield-ea/internal/validation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// startTime records when the service was initialized for uptime reporting
var startTime = time.Now()

// ServerConfig holds the configuration for the External Adapter server
type ServerConfig struct {
	// HTTP port to listen on
	Port string

	// Aggregation strategy to use (weighted, median, trimmed)
	AggregationMode string

	// Request timeout for fetching metrics
	Timeout time.Duration

	// Whether to enable the circuit breaker for protection
	EnableCircuitBreaker bool

	// Whether to validate metrics before aggregation
	EnableValidation bool

	// Whether to enable Prometheus metrics
	EnableMetrics bool
}

// Server represents the External Adapter server instance
type Server struct {
	// Configuration for the server
	config ServerConfig

	// List of data providers
	providers []Provider

	// HTTP server instance
	server *http.Server

	// Circuit breaker for fault detection
	breaker *circuitbreaker.CircuitBreaker

	// Metrics registry
	metrics *serverMetrics

	// Validation options
	validationOpts validation.ValidationOptions

	// Enterprise features
	multiChainClient *fetch.MultiChainClient
	metricsExporter  *enterprise.MetricsExporter
	dataIntegrity    *security.DataIntegrityService
	rateLimit        *rate.Limiter
	enableEnterprise bool
}

// Provider defines the interface for any yield data source
type Provider interface {
	Fetch(ctx context.Context) ([]model.Metric, error)
}

// serverMetrics holds Prometheus metrics for the server
type serverMetrics struct {
	requestCounter   *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	providerErrors   *prometheus.CounterVec
	circuitBreaker   *prometheus.GaugeVec
	aggregateTVL     prometheus.Gauge
	aggregateAPY     prometheus.Gauge
	metricCount      prometheus.Gauge
}

// registerMetrics sets up Prometheus metrics collection
func registerMetrics() *serverMetrics {
	m := &serverMetrics{
		requestCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "restake_requests_total",
				Help: "Total number of requests processed",
			},
			[]string{"status", "aggregation"},
		),
		requestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "restake_request_duration_seconds",
				Help: "Request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"status"},
		),
		providerErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "restake_provider_errors_total",
				Help: "Total number of provider errors",
			},
			[]string{"provider"},
		),
		circuitBreaker: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "restake_circuit_breaker_state",
				Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
			},
			[]string{"reason"},
		),
		aggregateTVL: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "restake_aggregate_tvl",
				Help: "Aggregated TVL value",
			},
		),
		aggregateAPY: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "restake_aggregate_apy",
				Help: "Aggregated APY value",
			},
		),
		metricCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "restake_metric_count",
				Help: "Number of metrics processed",
			},
		),
	}

	// Register all metrics
	prometheus.MustRegister(
		m.requestCounter,
		m.requestDuration,
		m.providerErrors,
		m.circuitBreaker,
		m.aggregateTVL,
		m.aggregateAPY,
		m.metricCount,
	)

	return m
}

// main is the entry point for the application
func main() {
	// Configure logging
	setupLogging()

	// Load configuration
	cfg := loadConfig()

	// Create data providers
	providers := createProviders()

	// Create and start server
	server := NewServer(cfg, providers)
	server.Start()
}

// setupLogging configures the logging for the application
func setupLogging() {
	logFormat := strings.ToLower(os.Getenv("LOG_FORMAT"))
	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))

	// Set log formatter based on environment
	switch logFormat {
	case "json":
		logrus.SetFormatter(&logrus.JSONFormatter{})
	default:
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
	}

	// Set log level based on environment
	switch logLevel {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "info":
		logrus.SetLevel(logrus.InfoLevel)
	case "warn", "warning":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}

	logrus.Info("Logging configured")
}

// loadConfig loads server configuration from environment variables
func loadConfig() ServerConfig {
	// Load from environment or use defaults
	return ServerConfig{
		Port:                 getEnvOrDefault("PORT", "8080"),
		AggregationMode:      getEnvOrDefault("AGGREGATION_MODE", "weighted"),
		Timeout:              getDurationOrDefault("TIMEOUT", 10*time.Second),
		EnableCircuitBreaker: getBoolOrDefault("ENABLE_CIRCUIT_BREAKER", true),
		EnableValidation:     getBoolOrDefault("ENABLE_VALIDATION", true),
		EnableMetrics:        getBoolOrDefault("ENABLE_METRICS", true),
	}
}

// createProviders creates the list of data providers
func createProviders() []Provider {
	return []Provider{
		fetch.NewEigenLayerClient(),
		fetch.NewKarakClient(),
		fetch.NewSymbioticClient(),
	}
}

// NewServer creates a new server instance with providers and circuit breaker
func NewServer(config ServerConfig, providers []Provider) *Server {
	// Ensure we have at least one provider
	if len(providers) == 0 {
		logrus.Fatal("No providers configured")
	}

	// Create circuit breaker if enabled
	var circuitBreaker *circuitbreaker.CircuitBreaker
	if config.EnableCircuitBreaker {
		// Configure with sensible defaults for yield data
		circuitBreaker = circuitbreaker.NewCircuitBreaker(circuitbreaker.Options{
			MaxAPY:          100.0, // 10000% maximum APY threshold
			MaxTVLChange:    50.0,  // 50% maximum TVL change threshold
			MinProviders:    2,     // Minimum 2 providers required
			CooldownPeriod:  5 * time.Minute,
			HealthThreshold: 3,     // Number of successful checks to return to closed state
			OnTrip: func(reason string) {
				logrus.Warnf("Circuit breaker tripped: %s", reason)
			},
		})
	}

	// Initialize metrics if enabled
	var metricsRegistry *serverMetrics
	if config.EnableMetrics {
		metricsRegistry = registerMetrics()
	}
	
	// Check for enterprise mode
	enableEnterprise := getEnvBool("ENABLE_ENTERPRISE_FEATURES", false)
	
	// Initialize server with basic features
	server := &Server{
		config:           config,
		providers:        providers,
		breaker:          circuitBreaker,
		metrics:          metricsRegistry,
		validationOpts:   validation.DefaultValidationOptions(),
		enableEnterprise: enableEnterprise,
	}
	
	// Initialize enterprise features if enabled
	if enableEnterprise {
		logrus.Info("Initializing enterprise features...")
		
		// Initialize rate limiter
		requestsPerSecond := getEnvFloat("RATE_LIMIT_RPS", 10.0) // Default: 10 requests per second
		burstSize := getEnvInt("RATE_LIMIT_BURST", 20)          // Default: burst of 20 requests
		server.rateLimit = rate.NewLimiter(rate.Limit(requestsPerSecond), burstSize)
		logrus.Infof("Rate limiting initialized: %v req/s, burst: %d", requestsPerSecond, burstSize)
		
		// Initialize multi-chain client
		if multiChainEnabled := getEnvBool("MULTICHAIN_ENABLED", false); multiChainEnabled {
			// Create basic Ethereum-only chain config for demo
			chains := map[fetch.SupportedChain]fetch.ChainConfig{
				fetch.ChainEthereum: {
					Enabled:     true,
					RPCEndpoint: getEnvOrDefault("ETH_RPC_ENDPOINT", "https://mainnet.infura.io/v3/"),
					APIEndpoint: getEnvOrDefault("ETH_API_ENDPOINT", "https://api.eigenlayer.xyz"),
					APIKey:      os.Getenv("ETH_API_KEY"),
					Weight:      1.0,
				},
			}
			
			// Add Polygon if enabled
			if polygonEnabled := getEnvBool("POLYGON_ENABLED", false); polygonEnabled {
				chains[fetch.ChainPolygon] = fetch.ChainConfig{
					Enabled:     true,
					RPCEndpoint: getEnvOrDefault("POLYGON_RPC_ENDPOINT", "https://polygon-rpc.com"),
					APIEndpoint: getEnvOrDefault("POLYGON_API_ENDPOINT", "https://api.polygonscan.com/api"),
					APIKey:      os.Getenv("POLYGON_API_KEY"),
					Weight:      0.8,
				}
			}
			
			server.multiChainClient = fetch.NewMultiChainClient(chains)
			logrus.Info("Multi-chain client initialized")
		}
		
		// Initialize data integrity service if enabled
		if dataIntegrityEnabled := getEnvBool("DATA_INTEGRITY_ENABLED", false); dataIntegrityEnabled {
			signatureValidity := getDurationOrDefault("SIGNATURE_VALIDITY", 24*time.Hour)
			
			dataIntegrity, err := security.NewDataIntegrityService(security.VerificationOptions{
				SignatureEnabled:     true,
				VerificationRequired: getEnvBool("VERIFICATION_REQUIRED", false),
				SignatureValidity:    signatureValidity,
				StrictMode:           getEnvBool("STRICT_MODE", false),
			})
			
			if err != nil {
				logrus.Warnf("Failed to initialize data integrity service: %v", err)
			} else {
				server.dataIntegrity = dataIntegrity
				logrus.Info("Data integrity service initialized")
			}
		}
		
		// Initialize metrics exporter if enabled
		if metricsExportEnabled := getEnvBool("METRICS_EXPORT_ENABLED", false); metricsExportEnabled {
			exportInterval := getEnvOrDefault("METRICS_EXPORT_INTERVAL", "1m")
			
			exporter, err := enterprise.NewMetricsExporter(enterprise.ExporterConfig{
				Enabled:        true,
				BatchSize:      getEnvInt("METRICS_EXPORT_BATCH_SIZE", 100),
				ExportInterval: exportInterval,
				WebhookEnabled: getEnvBool("WEBHOOK_ENABLED", false),
				WebhookURL:     os.Getenv("WEBHOOK_URL"),
				WebhookAPIKey:  os.Getenv("WEBHOOK_API_KEY"),
			})
			
			if err != nil {
				logrus.Warnf("Failed to initialize metrics exporter: %v", err)
			} else {
				server.metricsExporter = exporter
				logrus.Info("Metrics exporter initialized")
			}
		}
	}
	
	logrus.WithFields(logrus.Fields{
		"port":              config.Port,
		"aggregation_mode":  config.AggregationMode,
		"timeout":           config.Timeout,
		"circuit_breaker":   config.EnableCircuitBreaker,
		"validation":        config.EnableValidation,
		"metrics":           config.EnableMetrics,
		"provider_count":    len(providers),
	}).Info("Server initialized")

	return s

}

// Start begins the HTTP server and sets up graceful shutdown
func (s *Server) Start() {
	// Create a new router
	mux := http.NewServeMux()
	
	// Register API endpoints
	mux.HandleFunc("/", s.handleRequest)             // Main Chainlink EA endpoint
	mux.HandleFunc("/health", s.handleHealth)         // Health check endpoint
	mux.HandleFunc("/metrics", s.handleMetrics)       // Prometheus metrics endpoint
	mux.HandleFunc("/status", s.handleStatus)         // Service status endpoint
	mux.HandleFunc("/circuit", s.handleCircuitStatus) // Circuit breaker status/control

	// Configure server with timeouts
	s.server = &http.Server{
		Addr:         ":" + s.config.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start the server in a goroutine
	go func() {
		logrus.Infof("Server starting on port %s", s.config.Port)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Fatalf("Error starting server: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shut down the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logrus.Info("Server shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		logrus.Fatalf("Server shutdown failed: %v", err)
	}

	logrus.Info("Server stopped")
}

// handleHealth is a simple health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "OK",
		"version": "1.0.0",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleMetrics exposes Prometheus metrics
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.config.EnableMetrics {
		http.Error(w, "Metrics disabled", http.StatusServiceUnavailable)
		return
	}

	promhttp.Handler().ServeHTTP(w, r)
}

// handleStatus provides detailed service status information
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"status": "operational",
		"uptime": time.Since(startTime).String(),
		"version": "1.0.0",
		"providers": len(s.providers),
		"configuration": map[string]interface{}{
			"aggregation_mode": s.config.AggregationMode,
			"circuit_breaker": s.config.EnableCircuitBreaker,
			"validation": s.config.EnableValidation,
		},
	}

	// Add circuit breaker state if enabled
	if s.config.EnableCircuitBreaker && s.breaker != nil {
		status["circuit_state"] = s.breaker.GetState()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleCircuitStatus allows viewing and controlling the circuit breaker
func (s *Server) handleCircuitStatus(w http.ResponseWriter, r *http.Request) {
	if !s.config.EnableCircuitBreaker || s.breaker == nil {
		http.Error(w, "Circuit breaker not enabled", http.StatusServiceUnavailable)
		return
	}

	response := map[string]interface{}{
		"state": s.breaker.GetState(),
	}

	// Allow reset operation via POST
	if r.Method == http.MethodPost {
		action := r.URL.Query().Get("action")
		if action == "reset" {
			s.breaker.Reset()
			response["message"] = "Circuit breaker reset"
		}
	}

	metrics := s.breaker.LastGoodMetrics()
	if metrics != nil {
		response["last_good_metrics_count"] = len(metrics)
		if len(metrics) > 0 {
			response["last_good_timestamp"] = time.Unix(metrics[0].CollectedAt, 0).Format(time.RFC3339)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ChainlinkRequest matches the standard Chainlink External Adapter request format
type ChainlinkRequest struct {
	ID       string                 `json:"id"`
	JobRunID string                 `json:"jobRunId"`
	Data     map[string]interface{} `json:"data"`
	Meta     map[string]interface{} `json:"meta,omitempty"`
}

// ChainlinkResponse matches the standard Chainlink External Adapter response format
type ChainlinkResponse struct {
	JobRunID   string                 `json:"jobRunId,omitempty"`
	StatusCode int                    `json:"statusCode"`
	Status     string                 `json:"status"`
	Data       map[string]interface{} `json:"data"`
	Error      string                 `json:"error,omitempty"`
}

// handleRequest processes the Chainlink External Adapter request
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	// Apply rate limiting if enabled in enterprise mode
	if s.enableEnterprise && s.rateLimit != nil {
		if !s.rateLimit.Allow() {
			s.errorResponse(w, http.StatusTooManyRequests, "Rate limit exceeded")
			return
		}
	}

	// Parse the Chainlink request
	var request ChainlinkRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Increase request counter in metrics
	if s.metrics != nil {
		s.metrics.requestCounter.WithLabelValues("started", s.config.AggregationMode).Inc()
	}

	// Set up context with timeout from config
	ctx, cancel := context.WithTimeout(r.Context(), s.config.Timeout)
	defer cancel()
	
	// Variable to hold metrics from providers
	var metrics []model.Metric
	var err error
	
	// Choose data source based on enterprise mode
	if s.enableEnterprise && s.multiChainClient != nil {
		// Use multi-chain client for enterprise mode
		logrus.Info("Using multi-chain client for data fetching")
		metrics, err = s.multiChainClient.Fetch(ctx)
	} else {
		// Use standard providers for normal mode
		metrics, err = s.fetchAllMetrics(ctx)
	}
	
	if err != nil {
		s.errorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error fetching metrics: %v", err))
		return
	}

	// If validation is enabled, filter invalid metrics
	if s.config.EnableValidation {
		metrics = validation.FilterInvalid(metrics)
		if len(metrics) == 0 {
			s.errorResponse(w, http.StatusServiceUnavailable, "No valid metrics available after validation")
			return
		}
	}

	// Apply circuit breaker check if enabled
	if s.config.EnableCircuitBreaker && s.breaker != nil {
		if err := s.breaker.Check(metrics); err != nil {
			logrus.Warnf("Circuit breaker tripped: %v", err)

			// Attempt to use last known good metrics
			lastGood := s.breaker.LastGoodMetrics()
			if lastGood != nil && len(lastGood) > 0 {
				logrus.Info("Using last known good metrics")
				metrics = lastGood
			} else {
				s.errorResponse(w, http.StatusServiceUnavailable, fmt.Sprintf("Circuit breaker open: %v", err))
				return
			}
		}
	}

	// Track metric count in Prometheus
	if s.metrics != nil {
		s.metrics.metricCount.Set(float64(len(metrics)))
	}

	// Aggregate metrics based on configuration
	result := s.aggregateMetrics(metrics)

	// Track aggregated values in Prometheus
	if s.metrics != nil {
		s.metrics.aggregateAPY.Set(result.APY)
		s.metrics.aggregateTVL.Set(result.TVL)
	}

	// Format the Chainlink EA response
	response := ChainlinkResponse{
		JobRunID:   request.JobRunID,
		StatusCode: http.StatusOK,
		Status:     "success",
		Data: map[string]interface{}{
			"result":       result.APY,
			"apy":          result.APY,
			"tvl":          result.TVL,
			"pointsPerETH": result.PointsPerETH,
			"provider":     result.Provider,
			"collectedAt":  result.CollectedAt,
			"timestamp":    time.Now().Unix(),
		},
	}

	// Add request ID if provided
	if request.ID != "" {
		response.Data["id"] = request.ID
	}

	// Add any additional parameters from the request
	for k, v := range request.Data {
		if k != "id" && k != "jobRunId" {
			response.Data[k] = v
		}
	}

	// Add performance metadata
	if request.Meta == nil {
		request.Meta = make(map[string]interface{})
	}
	request.Meta["latencyMs"] = time.Since(start).Milliseconds()
	request.Meta["metricCount"] = len(metrics)
	request.Meta["aggregationMode"] = s.config.AggregationMode
	
	// Add enterprise-specific metadata if enabled
	if s.enableEnterprise {
		request.Meta["enterprise"] = true
		if s.multiChainClient != nil {
			request.Meta["multichain"] = true
		}
		if s.dataIntegrity != nil {
			request.Meta["signed"] = true
			request.Meta["publicKey"] = s.dataIntegrity.GetPublicKey()
		}
	}
	
	response.Data["meta"] = request.Meta

	// Finish request timing in Prometheus
	if s.metrics != nil {
		s.metrics.requestDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
		s.metrics.requestCounter.WithLabelValues("success", s.config.AggregationMode).Inc()
	}
	
	// Apply data integrity signing if enabled
	var responseData interface{} = response
	if s.enableEnterprise && s.dataIntegrity != nil {
		// Add tamper-proofing to the response
		tamperProofData, err := s.dataIntegrity.CreateTamperProofWrapper(response, map[string]interface{}{
			"timestamp":     time.Now().Unix(),
			"source":        "restake-yield-ea",
			"version":       "1.0.0",
			"request_id":    request.ID,
			"job_run_id":    request.JobRunID,
			"chain_support": s.multiChainClient != nil,
		})
		
		if err != nil {
			logrus.Warnf("Failed to create tamper-proof data: %v", err)
		} else {
			responseData = tamperProofData
		}
	}
	
	// Export metrics if enabled
	if s.enableEnterprise && s.metricsExporter != nil {
		s.metricsExporter.AddMetricBatch([]interface{}{result})
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseData)
}

// errorResponse returns a formatted error response for Chainlink nodes
func (s *Server) errorResponse(w http.ResponseWriter, statusCode int, errorMsg string) {
	logrus.Warn(errorMsg)

	// Track errors in metrics
	if s.metrics != nil {
		s.metrics.requestCounter.WithLabelValues("error", s.config.AggregationMode).Inc()
	}

	response := ChainlinkResponse{
		StatusCode: statusCode,
		Status:     "error",
		Error:      errorMsg,
		Data:       map[string]interface{}{"error": errorMsg},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

// aggregateMetrics combines metrics using the configured strategy
func (s *Server) aggregateMetrics(metrics []model.Metric) model.Metric {
	var result model.Metric
	
	switch s.config.AggregationMode {
	case "weighted":
		result = aggregate.Weighted(metrics)
	case "median":
		result = aggregate.MedianAggregation(metrics)
	case "trimmed":
		result = aggregate.TrimmedMeanAggregation(metrics, 0.1) // 10% trimming
	case "consensus":
		// Apply confidence scoring
		scored := validation.CalculateConfidenceScores(metrics)
		// Filter to only include high confidence metrics (above 0.7)
		var highConfidence []model.Metric
		for _, m := range scored {
			if m.Confidence > 0.7 {
				highConfidence = append(highConfidence, m)
			}
		}
		// Fall back to all metrics if filtering was too aggressive
		if len(highConfidence) < 2 {
			highConfidence = scored
		}
		// Use weighted average on the filtered set
		result = aggregate.Weighted(highConfidence)
	default:
		// Default to weighted aggregation
		result = aggregate.Weighted(metrics)
	}
	
	// Add aggregator as provider name for transparency
	result.Provider = "aggregated-" + s.config.AggregationMode
	
	// Ensure timestamp is current
	if result.CollectedAt == 0 {
		result.CollectedAt = time.Now().Unix()
	}
	
	return result
}

func (s *Server) fetchAllMetrics(ctx context.Context) ([]model.Metric, error) {
    var (
        wg      sync.WaitGroup
        mu      sync.Mutex
        metrics []model.Metric
        errs    []error
    )

    for _, provider := range s.providers {
        wg.Add(1)
        go func(p Provider) {
            defer wg.Done()

            providerMetrics, err := p.Fetch(ctx)
            mu.Lock()
            defer mu.Unlock()

            if err != nil {
                errs = append(errs, err)
                return
            }

            metrics = append(metrics, providerMetrics...)
        }(provider)
    }

    wg.Wait()

    if len(metrics) == 0 && len(errs) > 0 {
        return nil, fmt.Errorf("all providers failed: %v", errs[0])
    }

    return metrics, nil
}

func getEnvOrDefault(key, defaultValue string) string {
    value := os.Getenv(key)
    if value == "" {
        return defaultValue
    }
    return value
}

func getDurationOrDefault(key string, defaultValue time.Duration) time.Duration {
    value := os.Getenv(key)
    if value == "" {
        return defaultValue
    }

    duration, err := time.ParseDuration(value)
    if err != nil {
        logrus.Printf("Warning: Invalid duration for %s, using default", key)
        return defaultValue
    }

    return duration
}
