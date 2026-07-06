// Package main is the entry point for the Restake Yield External Adapter.
//
// The adapter is a Chainlink External Adapter: it exposes a single POST
// endpoint that aggregates ETH restaking yield data from multiple providers,
// validates and filters it, runs it through a circuit breaker, aggregates it
// into a single result, optionally signs it for on-chain verification, and
// returns it in the Chainlink EA response format.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/christopher/restake-yield-ea/internal/aggregate"
	"github.com/christopher/restake-yield-ea/internal/circuitbreaker"
	"github.com/christopher/restake-yield-ea/internal/config"
	"github.com/christopher/restake-yield-ea/internal/enterprise"
	"github.com/christopher/restake-yield-ea/internal/envx"
	"github.com/christopher/restake-yield-ea/internal/fetch"
	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/christopher/restake-yield-ea/internal/otel"
	"github.com/christopher/restake-yield-ea/internal/provider"
	"github.com/christopher/restake-yield-ea/internal/security"
	"github.com/christopher/restake-yield-ea/internal/types"
	"github.com/christopher/restake-yield-ea/internal/validation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// Injectable function variables for testing. They default to the real
// implementations so production behaviour is unchanged.
var (
	initTracerFn         = otel.InitTracer
	newMetricsExporterFn = enterprise.NewMetricsExporter
	// initEnterpriseFn is set in NewServer to s.initEnterprise. Tests can
	// swap it to return an error and exercise the warning path without
	// polluting production code with test-only env vars.
	initEnterpriseFn = (*Server).initEnterprise
)

// dataIntegritySigner is the subset of DataIntegrityService used by Server.
// Tests can provide a mock implementation to exercise signing failure paths.
type dataIntegritySigner interface {
	CreateTamperProofWrapper(payload interface{}, metadata map[string]interface{}) (map[string]interface{}, error)
	Address() string
}

// version is the adapter version, overridable at build time via -ldflags.
var version = "1.0.0"

// startTime records when the service was initialized for uptime reporting.
var startTime = time.Now()

// ServerConfig holds the runtime configuration for the External Adapter.
type ServerConfig struct {
	Port                 string
	AggregationMode      string
	Timeout              time.Duration
	EnableCircuitBreaker bool
	EnableValidation     bool
	EnableMetrics        bool
	MaxAPYThreshold      float64
	MinProviders         int
}

// Server is the External Adapter HTTP server instance.
type Server struct {
	mu               sync.Mutex
	cfg              ServerConfig
	appCfg           config.Config
	providers        []provider.Provider
	httpServer       *http.Server
	breaker          *circuitbreaker.CircuitBreaker
	metrics          *serverMetrics
	validationOpts   validation.ValidationOptions
	multiChainClient *fetch.MultiChainClient
	metricsExporter  *enterprise.MetricsExporter
	dataIntegrity    dataIntegritySigner
	rateLimit        *rate.Limiter
	enableEnterprise bool
	tracerShutdown   func(context.Context) error
	shutdownFn       func(ctx context.Context) error // overrides httpServer.Shutdown if set
	adminToken       string // optional bearer token for admin endpoints; empty = no auth
	trustedProxy     string // optional trusted proxy IP for X-Forwarded-For; empty = never trust XFF
}

// serverMetrics holds Prometheus metrics for the server.
type serverMetrics struct {
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	providerErrors  *prometheus.CounterVec
	providerLatency *prometheus.HistogramVec
	circuitBreaker  *prometheus.GaugeVec
	aggregateTVL    prometheus.Gauge
	aggregateAPY    prometheus.Gauge
	metricCount     prometheus.Gauge
	providerCount   prometheus.Gauge
}

var (
	metricsOnce     sync.Once
	globalMetrics   *serverMetrics
)

func registerMetrics() *serverMetrics {
	metricsOnce.Do(func() {
		globalMetrics = &serverMetrics{
			requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "restake_requests_total",
				Help: "Total number of EA requests processed",
			}, []string{"status", "aggregation"}),
			requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "restake_request_duration_seconds",
				Help:    "EA request duration in seconds",
				Buckets: prometheus.DefBuckets,
			}, []string{"status"}),
			providerErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "restake_provider_errors_total",
				Help: "Total number of provider fetch errors",
			}, []string{"provider"}),
			providerLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "restake_provider_latency_seconds",
				Help:    "Per-provider fetch latency",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			}, []string{"provider"}),
			circuitBreaker: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "restake_circuit_breaker_state",
				Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
			}, []string{"reason"}),
			aggregateTVL: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "restake_aggregate_tvl",
				Help: "Aggregated TVL value (ETH)",
			}),
			aggregateAPY: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "restake_aggregate_apy",
				Help: "Aggregated APY value (decimal)",
			}),
			metricCount: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "restake_metric_count",
				Help: "Number of metrics processed per request",
			}),
			providerCount: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "restake_provider_count",
				Help: "Number of configured providers",
			}),
		}
		prometheus.MustRegister(
			globalMetrics.requestCounter, globalMetrics.requestDuration,
			globalMetrics.providerErrors, globalMetrics.providerLatency,
			globalMetrics.circuitBreaker, globalMetrics.aggregateTVL,
			globalMetrics.aggregateAPY, globalMetrics.metricCount,
			globalMetrics.providerCount,
		)
	})
	return globalMetrics
}

func main() {
	os.Exit(run(nil))
}

// run contains the server lifecycle, extracted from main so it is unit-testable.
// If quitCh is nil, it listens for SIGINT/SIGTERM. Otherwise it waits for a
// value on quitCh (used by tests). Returns the process exit code.
func run(quitCh chan struct{}) int {
	setupLogging()

	appCfg := config.Load()
	logrus.Infof("config: %s", appCfg)

	shutdown, err := initTracerFn(appCfg.OtelEndpoint)
	if err != nil {
		logrus.Warnf("failed to init tracer: %v", err)
		shutdown = func(context.Context) error { return nil }
	}

	srvCfg := loadServerConfig(appCfg)
	providers := createProviders(appCfg, srvCfg)

	srv, err := NewServer(srvCfg, appCfg, providers)
	if err != nil {
		logrus.Errorf("failed to create server: %v", err)
		return 1
	}
	srv.tracerShutdown = shutdown

	if quitCh == nil {
		quitCh = make(chan struct{})
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() { <-sigCh; close(quitCh) }()
	}

	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start() }()

	// Wait for either a shutdown signal or a fatal listen error.
	select {
	case <-quitCh:
		logrus.Info("shutdown signal received")
	case err := <-startErr:
		if err != nil {
			logrus.Errorf("listen error: %v", err)
			return 1
		}
	}
	srv.Stop()
	return 0
}

func setupLogging() {
	switch strings.ToLower(os.Getenv("LOG_FORMAT")) {
	case "json":
		logrus.SetFormatter(&logrus.JSONFormatter{})
	default:
		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	}
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "warn", "warning":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}
}

func loadServerConfig(appCfg config.Config) ServerConfig {
	return ServerConfig{
		Port:                 envx.String("PORT", appCfg.Port),
		AggregationMode:      envx.String("AGGREGATION_MODE", "weighted"),
		Timeout:              envx.Duration("TIMEOUT", appCfg.RequestTimeout),
		EnableCircuitBreaker: envx.Bool("ENABLE_CIRCUIT_BREAKER", true),
		EnableValidation:     envx.Bool("ENABLE_VALIDATION", true),
		EnableMetrics:        envx.Bool("ENABLE_METRICS", true),
		MaxAPYThreshold:      envx.Float64("MAX_APY_THRESHOLD", appCfg.MaxAPY),
		MinProviders:         envx.Int("MIN_PROVIDERS", appCfg.MinProviderCount),
	}
}

// createProviders builds the provider list from the app config, honouring the
// ENABLED_PROVIDERS override (comma-separated). By default only DefiLlama is
// enabled because it is the only keyless, always-on source. EigenLayer, Karak,
// and Symbiotic require their respective *_API env vars to be set.
func createProviders(appCfg config.Config, _ ServerConfig) []provider.Provider {
	all := map[string]func() provider.Provider{
		"defillama":  func() provider.Provider { return fetch.NewDefiLlamaClient(appCfg) },
		"lido":       func() provider.Provider { return fetch.NewLidoClient(appCfg) },
		"eigenlayer": func() provider.Provider { return fetch.NewEigenLayerClient(appCfg) },
		"karak":      func() provider.Provider { return fetch.NewKarakClient(appCfg) },
		"symbiotic":  func() provider.Provider { return fetch.NewSymbioticClient(appCfg) },
	}

	names := appCfg.EnabledProviders
	if len(names) == 0 {
		// Default: DefiLlama + Lido (both keyless, real data). Others activate when configured.
		names = []string{"defillama", "lido"}
		if appCfg.EigenURL != "" {
			names = append(names, "eigenlayer")
		}
		if appCfg.KarakURL != "" {
			names = append(names, "karak")
		}
		if appCfg.SymbioticURL != "" {
			names = append(names, "symbiotic")
		}
	}

	providers := make([]provider.Provider, 0, len(names))
	for _, n := range names {
		fn, ok := all[n]
		if !ok {
			logrus.Warnf("unknown provider %q skipped", n)
			continue
		}
		providers = append(providers, fn())
	}
	if len(providers) == 0 {
		logrus.Warn("no providers configured")
	}
	return providers
}

// NewServer creates a new server instance with providers and circuit breaker.
func NewServer(srvCfg ServerConfig, appCfg config.Config, providers []provider.Provider) (*Server, error) {
	if len(providers) == 0 {
		return nil, errors.New("no providers configured")
	}

	s := &Server{
		cfg:            srvCfg,
		appCfg:         appCfg,
		providers:      providers,
		validationOpts: validation.DefaultValidationOptions(),
	}

	if srvCfg.EnableCircuitBreaker {
		s.breaker = circuitbreaker.New(circuitbreaker.Thresholds{
			MaxAPY:       srvCfg.MaxAPYThreshold,
			MaxTVLChange: appCfg.MaxTVLChange,
			MinProviders: srvCfg.MinProviders,
		}).
			WithResetDelay(appCfg.CircuitResetDelay).
			WithSuccessThreshold(3).
			WithMaxStaleness(appCfg.MaxStaleSeconds).
			WithTripCallback(func(reason string, _ []model.Metric) {
				logrus.Warnf("circuit breaker tripped: %s", reason)
			})
	}

	if srvCfg.EnableMetrics {
		s.metrics = registerMetrics()
		s.metrics.providerCount.Set(float64(len(providers)))
	}

	s.enableEnterprise = envx.Bool("ENABLE_ENTERPRISE_FEATURES", false)
	s.adminToken = envx.String("ADMIN_TOKEN", "")
	s.trustedProxy = envx.String("TRUSTED_PROXY", "")
	if s.adminToken == "" {
		logrus.Warn("ADMIN_TOKEN not set — admin endpoints (/circuit, /status) are unauthenticated")
	}
	if s.enableEnterprise {
		if err := initEnterpriseFn(s); err != nil {
			logrus.Warnf("enterprise init error: %v", err)
		}
	}

	logrus.WithFields(logrus.Fields{
		"port":             srvCfg.Port,
		"aggregation_mode": srvCfg.AggregationMode,
		"timeout":          srvCfg.Timeout,
		"providers":        providerNames(providers),
		"circuit_breaker":  srvCfg.EnableCircuitBreaker,
		"validation":       srvCfg.EnableValidation,
		"metrics":          srvCfg.EnableMetrics,
		"enterprise":       s.enableEnterprise,
	}).Info("server initialised")

	return s, nil
}

func (s *Server) initEnterprise() error {
	rps := envx.Float64("RATE_LIMIT_RPS", 10.0)
	burst := envx.Int("RATE_LIMIT_BURST", 20)
	s.rateLimit = rate.NewLimiter(rate.Limit(rps), burst)
	logrus.Infof("rate limiting: %v req/s, burst %d", rps, burst)

	if envx.Bool("MULTICHAIN_ENABLED", false) {
		chains := map[types.SupportedChain]types.ChainConfig{
			fetch.ChainEthereum: {
				Enabled:     true,
				RPCEndpoint: envx.String("ETH_RPC_ENDPOINT", "https://rpc.ankr.com/eth"),
				APIEndpoint: s.appCfg.EigenURL,
				APIKey:      os.Getenv("ETH_API_KEY"),
				Weight:      1.0,
			},
		}
		if envx.Bool("POLYGON_ENABLED", false) {
			chains[fetch.ChainPolygon] = types.ChainConfig{
				Enabled:     true,
				RPCEndpoint: envx.String("POLYGON_RPC_ENDPOINT", "https://polygon-rpc.com"),
				APIEndpoint: envx.String("POLYGON_API_ENDPOINT", "https://api.polygonscan.com/api"),
				APIKey:      os.Getenv("POLYGON_API_KEY"),
				Weight:      0.8,
			}
		}
		s.multiChainClient = fetch.NewMultiChainClient(s.appCfg, chains)
		logrus.Info("multi-chain client initialised")
	}

	if envx.Bool("DATA_INTEGRITY_ENABLED", false) {
		opts := security.VerificationOptions{
			SignatureEnabled:     true,
			VerificationRequired: envx.Bool("VERIFICATION_REQUIRED", false),
			SignatureValidity:    envx.Duration("SIGNATURE_VALIDITY", 24*time.Hour),
			StrictMode:           envx.Bool("STRICT_MODE", false),
		}
		var di *security.DataIntegrityService
		var err error
		if key := os.Getenv("SIGNING_PRIVATE_KEY"); key != "" {
			di, err = security.NewDataIntegrityServiceFromKey(key, opts)
		} else {
			di, err = security.NewDataIntegrityService(opts)
		}
		if err != nil {
			logrus.Warnf("data integrity init failed: %v", err)
		} else {
			s.dataIntegrity = di
			logrus.WithField("signer", di.Address()).Info("data integrity service initialised")
		}
	}

	if envx.Bool("METRICS_EXPORT_ENABLED", false) {
		exp, err := newMetricsExporterFn(enterprise.ExporterConfig{
			Enabled:        true,
			BatchSize:      envx.Int("METRICS_EXPORT_BATCH_SIZE", 100),
			ExportInterval: envx.String("METRICS_EXPORT_INTERVAL", "1m"),
			WebhookEnabled: envx.Bool("WEBHOOK_ENABLED", false),
			WebhookURL:     os.Getenv("WEBHOOK_URL"),
			WebhookAPIKey:  os.Getenv("WEBHOOK_API_KEY"),
		})
		if err != nil {
			logrus.Warnf("metrics exporter init failed: %v", err)
		} else {
			s.metricsExporter = exp
		}
	}
	return nil
}

// Start begins the HTTP server. It blocks until the server stops listening.
// Returns an error if the server fails to listen; http.ErrServerClosed is
// treated as a clean shutdown and returns nil.
func (s *Server) Start() error {
	s.mu.Lock()
	s.httpServer = &http.Server{
		Addr:         ":" + s.cfg.Port,
		Handler:      s.routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	srv := s.httpServer
	s.mu.Unlock()
	logrus.Infof("server listening on :%s", s.cfg.Port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// routes returns the HTTP handler used by the server. Extracted for testing.
// The mux is wrapped in the production middleware stack (security headers,
// request-ID, body-size limit, access logging) so every endpoint benefits.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/metrics", s.handleMetrics)
	// Admin endpoints are protected by optional bearer-token auth.
	mux.Handle("/status", s.adminAuth(http.HandlerFunc(s.handleStatus)))
	mux.Handle("/circuit", s.adminAuth(http.HandlerFunc(s.handleCircuitStatus)))
	return s.withMiddleware(mux)
}

// Stop gracefully shuts down the HTTP server and background services.
func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s.mu.Lock()
	httpServer := s.httpServer
	shutdownFn := s.shutdownFn
	s.mu.Unlock()
	if httpServer != nil {
		if shutdownFn == nil {
			shutdownFn = httpServer.Shutdown
		}
		if err := shutdownFn(ctx); err != nil {
			logrus.Errorf("http shutdown: %v", err)
		}
	}
	if s.metricsExporter != nil {
		s.metricsExporter.Stop()
	}
	if s.tracerShutdown != nil {
		if err := s.tracerShutdown(ctx); err != nil {
			logrus.Errorf("tracer shutdown: %v", err)
		}
	}
	logrus.Info("server stopped")
}

// --- HTTP handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "OK",
		"version":   version,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleReadyz is a Kubernetes-style readiness probe. Unlike /health (which
// only reports process liveness), /readyz returns 503 when the server has no
// configured providers or the circuit breaker is permanently open, signalling
// to the load balancer that the instance should not receive traffic.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if len(s.providers) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":  "not ready",
			"reason":  "no providers configured",
			"version": version,
		})
		return
	}
	resp := map[string]interface{}{
		"status":    "ready",
		"version":   version,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if s.breaker != nil {
		resp["circuit_state"] = s.breaker.GetState().String()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.EnableMetrics {
		http.Error(w, "metrics disabled", http.StatusServiceUnavailable)
		return
	}
	promhttp.Handler().ServeHTTP(w, r)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status := map[string]interface{}{
		"status":    "operational",
		"uptime":    time.Since(startTime).String(),
		"version":   version,
		"providers": providerNames(s.providers),
		"configuration": map[string]interface{}{
			"aggregation_mode": s.cfg.AggregationMode,
			"circuit_breaker":  s.cfg.EnableCircuitBreaker,
			"validation":       s.cfg.EnableValidation,
			"enterprise":       s.enableEnterprise,
		},
	}
	if s.breaker != nil {
		status["circuit_state"] = s.breaker.GetState().String()
	}
	if s.dataIntegrity != nil {
		status["signer"] = s.dataIntegrity.Address()
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCircuitStatus(w http.ResponseWriter, r *http.Request) {
	if s.breaker == nil {
		http.Error(w, "circuit breaker not enabled", http.StatusServiceUnavailable)
		return
	}
	resp := map[string]interface{}{"state": s.breaker.GetState().String()}
	if r.Method == http.MethodPost && r.URL.Query().Get("action") == "reset" {
		s.breaker.Reset()
		resp["message"] = "circuit breaker reset"
	}
	if last := s.breaker.LastGoodMetrics(); len(last) > 0 {
		resp["last_good_metrics_count"] = len(last)
		resp["last_good_timestamp"] = time.Unix(last[0].CollectedAt, 0).Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ChainlinkRequest matches the standard Chainlink External Adapter request format.
// The Chainlink node sends: {"id":"...","data":{},"meta":{...}}
// The "id" is the job run ID that must be echoed back in the response.
type ChainlinkRequest struct {
	ID       string                 `json:"id"`
	JobRunID string                 `json:"jobRunID"`
	Data     map[string]interface{} `json:"data"`
	Meta     map[string]interface{} `json:"meta,omitempty"`
}

// ChainlinkResponse matches the Chainlink External Adapter response format.
// The required fields are jobRunID (echoed from request id) and data.result.
// status is "completed" on success, "errored" on failure. The top-level
// result field is included for OCR job compatibility.
type ChainlinkResponse struct {
	JobRunID   string                 `json:"jobRunID"`
	Status     string                 `json:"status"`
	StatusCode int                    `json:"statusCode,omitempty"`
	Data       map[string]interface{} `json:"data"`
	Result     interface{}            `json:"result,omitempty"`
	Error      interface{}            `json:"error"`
	Pending    bool                   `json:"pending"`
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodPost {
		s.errorResponse(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if s.enableEnterprise && s.rateLimit != nil && !s.rateLimit.Allow() {
		s.errorResponse(w, r, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	var req ChainlinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.errorResponse(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	// Determine the jobRunID: the Chainlink node sends it as "id" in the
	// request. Some EAs also accept "jobRunID" directly. Prefer "id".
	jobRunID := req.ID
	if jobRunID == "" {
		jobRunID = req.JobRunID
	}

	if s.metrics != nil {
		s.metrics.requestCounter.WithLabelValues("started", s.cfg.AggregationMode).Inc()
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()

	metrics, err := s.collectMetrics(ctx)
	if err != nil {
		s.errorResponseWithJobRunID(w, r, http.StatusInternalServerError, fmt.Sprintf("fetch failed: %v", err), jobRunID)
		return
	}

	if s.cfg.EnableValidation {
		metrics = validation.FilterInvalidWithOptions(metrics, s.validationOpts)
		if len(metrics) == 0 {
			s.errorResponseWithJobRunID(w, r, http.StatusServiceUnavailable, "no valid metrics after validation", jobRunID)
			return
		}
	}

	stale := false
	if s.breaker != nil {
		if err := s.breaker.Check(metrics); err != nil {
			logrus.Warnf("circuit breaker: %v", err)
			if last := s.breaker.LastGoodMetrics(); len(last) > 0 {
				metrics = last
				stale = true
			} else {
				s.errorResponseWithJobRunID(w, r, http.StatusServiceUnavailable, fmt.Sprintf("circuit open: %v", err), jobRunID)
				return
			}
		}
	}

	result := s.aggregateMetrics(metrics)

	if s.metrics != nil {
		s.metrics.metricCount.Set(float64(len(metrics)))
		s.metrics.aggregateAPY.Set(result.APY)
		s.metrics.aggregateTVL.Set(result.TVL)
		s.metrics.requestDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
		s.metrics.requestCounter.WithLabelValues("success", s.cfg.AggregationMode).Inc()
	}

	data := map[string]interface{}{
		"result":       result.APY,
		"apy":          result.APY,
		"tvl":          result.TVL,
		"pointsPerETH": result.PointsPerETH,
		"provider":     result.Provider,
		"collectedAt":  result.CollectedAt,
		"timestamp":    time.Now().Unix(),
	}
	if stale {
		data["stale"] = true
	}
	if req.ID != "" {
		data["id"] = req.ID
	}
	// Echo back request data fields using a whitelist of known-safe keys.
	// This prevents arbitrary client-supplied keys from overwriting our
	// response fields (e.g. a client sending {"data": {"result": 999}}
	// could otherwise replace the aggregated APY).
	allowedDataKeys := map[string]struct{}{
		"chain":    {},
		"symbol":   {},
		"mode":     {},
		"provider": {},
	}
	for k, v := range req.Data {
		if _, ok := allowedDataKeys[k]; ok {
			data[k] = v
		}
	}

	meta := req.Meta
	if meta == nil {
		meta = make(map[string]interface{})
	}
	meta["latencyMs"] = time.Since(start).Milliseconds()
	meta["metricCount"] = len(metrics)
	meta["aggregationMode"] = s.cfg.AggregationMode
	if rid := requestIDFromContext(r.Context()); rid != "" {
		meta["requestId"] = rid
	}
	if stale {
		meta["stale"] = true
	}
	if s.enableEnterprise {
		meta["enterprise"] = true
		if s.multiChainClient != nil {
			meta["multichain"] = true
		}
		if s.dataIntegrity != nil {
			meta["signed"] = true
			meta["signer"] = s.dataIntegrity.Address()
		}
	}
	data["meta"] = meta

	resp := ChainlinkResponse{
		JobRunID: jobRunID,
		Status:   "completed",
		Data:     data,
		Result:   result.APY, // top-level result for OCR job compatibility
		Error:    nil,
	}

	var payload interface{} = resp
	if s.dataIntegrity != nil {
		wrapped, err := s.dataIntegrity.CreateTamperProofWrapper(resp, map[string]interface{}{
			"timestamp":  time.Now().Unix(),
			"source":     "restake-yield-ea",
			"version":    version,
			"request_id": req.ID,
			"job_run_id": jobRunID,
		})
		if err != nil {
			logrus.Errorf("signing failed, sending unsigned response: %v", err)
		} else {
			payload = wrapped
		}
	}

	if s.metricsExporter != nil {
		s.metricsExporter.AddMetricBatch([]interface{}{result})
	}

	writeJSON(w, http.StatusOK, payload)
}

// collectMetrics fans out across all providers concurrently and merges results.
func (s *Server) collectMetrics(ctx context.Context) ([]model.Metric, error) {
	if s.enableEnterprise && s.multiChainClient != nil {
		return s.multiChainClient.Fetch(ctx)
	}
	return s.fetchAllProviders(ctx)
}

func (s *Server) fetchAllProviders(ctx context.Context) ([]model.Metric, error) {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		metrics []model.Metric
		errs    []error
	)

	for _, p := range s.providers {
		wg.Add(1)
		go func(p provider.Provider) {
			defer wg.Done()
			pStart := time.Now()
			pm, err := p.Fetch(ctx)
			if s.metrics != nil {
				s.metrics.providerLatency.WithLabelValues(p.Name()).Observe(time.Since(pStart).Seconds())
			}
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
				if s.metrics != nil {
					s.metrics.providerErrors.WithLabelValues(p.Name()).Inc()
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			metrics = append(metrics, pm...)
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	if len(metrics) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("all providers failed: %v", errs)
	}
	return metrics, nil
}

func (s *Server) aggregateMetrics(metrics []model.Metric) model.Metric {
	var result model.Metric
	switch s.cfg.AggregationMode {
	case "median":
		result = aggregate.MedianAggregation(metrics)
	case "trimmed":
		result = aggregate.TrimmedMeanAggregation(metrics, 0.1)
	case "consensus":
		scored := validation.CalculateConfidenceScores(metrics)
		var high []model.Metric
		for _, m := range scored {
			if m.Confidence > 0.7 {
				high = append(high, m)
			}
		}
		if len(high) < 2 {
			high = scored
		}
		result = aggregate.Weighted(high)
	default: // "weighted"
		result = aggregate.Weighted(metrics)
	}
	result.Provider = "aggregated-" + s.cfg.AggregationMode
	if result.CollectedAt == 0 {
		result.CollectedAt = time.Now().Unix()
	}
	return result
}

func (s *Server) errorResponse(w http.ResponseWriter, r *http.Request, code int, msg string) {
	s.errorResponseWithJobRunID(w, r, code, msg, "")
}

// errorResponseWithJobRunID is like errorResponse but includes the jobRunID
// from the parsed request, allowing error responses to comply with the
// Chainlink EA spec (jobRunID must be echoed in all responses).
func (s *Server) errorResponseWithJobRunID(w http.ResponseWriter, r *http.Request, code int, msg, jobRunID string) {
	logrus.WithFields(logrus.Fields{
		"status":     code,
		"error":      msg,
		"request_id": requestIDFromContext(r.Context()),
	}).Warn("request error")
	if s.metrics != nil {
		s.metrics.requestCounter.WithLabelValues("error", s.cfg.AggregationMode).Inc()
	}

	resp := ChainlinkResponse{
		JobRunID:   jobRunID,
		Status:     "errored",
		StatusCode: code,
		Error: map[string]interface{}{
			"name":    "EAError",
			"message": msg,
		},
		Data: map[string]interface{}{},
	}
	if rid := requestIDFromContext(r.Context()); rid != "" {
		resp.Data["requestId"] = rid
	}
	writeJSON(w, code, resp)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logrus.Errorf("write json: %v", err)
	}
}

func providerNames(ps []provider.Provider) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}
