# Restake Yield External Adapter — Technical Documentation

Single source of truth for the restake-yield-ea project. Covers architecture, packages, contracts, configuration, deployment, and CI.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Request Lifecycle](#3-request-lifecycle)
4. [Go Packages](#4-go-packages)
5. [Solidity Contracts](#5-solidity-contracts)
6. [Configuration](#6-configuration)
7. [HTTP API](#7-http-api)
8. [Observability](#8-observability)
9. [Deployment](#9-deployment)
10. [CI Pipeline](#10-ci-pipeline)
11. [Security](#11-security)
12. [Testing](#12-testing)

---

## 1. Overview

A Chainlink External Adapter (EA) that aggregates ETH restaking yield data from multiple off-chain providers, validates and filters it, runs it through a circuit breaker, aggregates it into a single result, optionally signs it via EIP-712 for on-chain verification, and returns it in the Chainlink EA response format.

**Stack:** Go 1.25+ (EA), Solidity 0.8.24 (on-chain oracle), Foundry (tests), Prometheus/Grafana (metrics), OpenTelemetry (tracing), Docker/Kubernetes (deployment).

**Data flow:**

```
Providers (DefiLlama, Lido, EigenLayer, Karak, Symbiotic)
  → concurrent fetch (per-provider goroutines)
  → validation (basic filters + IQR/MAD outlier detection)
  → circuit breaker (threshold checks + last-good fallback)
  → aggregation (weighted / median / trimmed / consensus)
  → optional EIP-712 signing (secp256k1)
  → Chainlink EA JSON response
  → optional on-chain submission to RestakeYieldOracle
```

---

## 2. Architecture

```
cmd/server/
  main.go          — entry point, Server struct, HTTP handlers, request lifecycle
  middleware.go    — security headers, request-ID, body-size limit, access log, admin auth

internal/
  model/           — Metric struct (core data type)
  provider/        — Provider interface (Fetch + Name)
  config/          — Config struct, env-var loading
  envx/            — typed env-var helpers (String, Int, Float64, Duration, Bool)
  types/           — SupportedChain constants, ChainConfig
  fetch/           — HTTP clients for each provider, multi-chain client
  validation/      — filters, outlier detection, confidence scoring
  aggregate/       — weighted/median/trimmed/consensus aggregation
  circuitbreaker/  — circuit breaker with last-good fallback
  security/        — EIP-712 signing, data integrity service
  enterprise/      — webhook metrics exporter
  otel/            — OpenTelemetry tracer init

contracts/
  src/             — YieldVerifier, RestakeYieldOracle, YieldConsumerExample
  test/            — Foundry test suite
  script/          — genfixture.go (cross-language signature compatibility proof)

deploy/
  k8s/             — Kubernetes Deployment, Service, ServiceMonitor
  prometheus/      — prometheus.yml
  grafana/         — dashboard.json + provisioning
```

---

## 3. Request Lifecycle

`POST /` is the main Chainlink EA endpoint. Processing order:

1. **Middleware chain** (outermost → innermost):
   - `securityHeaders` — `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`
   - `requestID` — injects `X-Request-ID` (16-byte hex from crypto/rand, or client-supplied if ≤64 chars)
   - `limitBody` — `http.MaxBytesReader` at 1 MiB
   - `accessLog` — structured log with method, path, status, latency, bytes, request_id, remote

2. **Rate limiting** (enterprise only): `rate.Limiter` from `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST`. Returns 429 if exceeded.

3. **Request parsing**: JSON-decode `ChainlinkRequest{ID, JobRunID, Data, Meta}`. `jobRunID` = `req.ID` (falls back to `req.JobRunID`).

4. **Context timeout**: `context.WithTimeout(r.Context(), cfg.Timeout)`.

5. **Metric collection** (`collectMetrics`):
   - If enterprise + multi-chain enabled: `MultiChainClient.Fetch` (fans out across chains, each chain fans out across providers, per-chain TTL cache)
   - Otherwise: `fetchAllProviders` — one goroutine per provider, merges results under a mutex, records per-provider latency and error Prometheus metrics

6. **Validation** (if `ENABLE_VALIDATION`): `validation.FilterInvalidWithOptions` — basic criteria (age, TVL, APY, PointsPerETH, NaN/Inf rejection) then IQR + MAD outlier detection. Returns 503 if zero valid metrics.

7. **Circuit breaker** (if `ENABLE_CIRCUIT_BREAKER`): `breaker.Check(metrics)`:
   - If open and reset delay not elapsed → returns error
   - If open and delay elapsed → transitions to half-open, allows test request
   - Checks: min provider count, max APY, TVL change vs history, APY std-dev
   - On trip: fires callback, returns error
   - On error: if `LastGoodMetrics` available and not stale → uses fallback, sets `stale=true` in response. Otherwise returns 503.

8. **Aggregation** (`aggregateMetrics`): mode selected by `AGGREGATION_MODE`:
   - `weighted` (default) — TVL-weighted APY, summed TVL, median PointsPerETH
   - `median` — median APY, summed TVL, median PointsPerETH
   - `trimmed` — 10% trimmed mean (by APY), then weighted
   - `consensus` — confidence scoring, filter >0.7, then weighted

9. **Response construction**: `ChainlinkResponse{JobRunID, Status, Data, Result, Error, Pending}`. Data includes `result` (APY), `apy`, `tvl`, `pointsPerETH`, `provider`, `collectedAt`, `timestamp`, plus whitelisted echo keys (`chain`, `symbol`, `mode`, `provider`). Meta includes `latencyMs`, `metricCount`, `aggregationMode`, `requestId`, and enterprise flags.

10. **Optional signing** (enterprise + `DATA_INTEGRITY_ENABLED`): `CreateTamperProofWrapper` wraps the response with a Keccak256 integrity hash and secp256k1 signature envelope.

11. **Optional webhook export** (enterprise + `METRICS_EXPORT_ENABLED`): `AddMetricBatch` queues the result for batched webhook delivery.

---

## 4. Go Packages

### 4.1 `internal/model`

Core data structure.

```go
type Metric struct {
    Provider      string  `json:"provider"`
    APY           float64 `json:"apy"`           // decimal, 0.05 = 5%
    TVL           float64 `json:"tvl"`           // ETH
    PointsPerETH  float64 `json:"points_per_eth"`
    CollectedAt   int64   `json:"collected_at"`  // unix timestamp
    Confidence    float64 `json:"confidence,omitempty"`  // 0..1
    Chain         string  `json:"chain,omitempty"`
    Symbol        string  `json:"symbol,omitempty"`
    Weight        float64 `json:"weight,omitempty"`
    Version       string  `json:"version,omitempty"`
}
```

- `NewMetric(provider, apy, tvl, pointsPerETH) Metric` — stamps `CollectedAt` with `time.Now().Unix()`, sets `Version` to `"1.0"`
- `Metric.IsValid() bool` — basic plausibility (non-negative, non-NaN, non-Inf, positive TVL, recent timestamp)
- `Metric.WithConfidence(c) Metric` — returns copy with confidence set

### 4.2 `internal/provider`

```go
type Provider interface {
    Fetch(ctx context.Context) ([]model.Metric, error)
    Name() string  // stable lowercase identifier
}
```

- `Adapter` — struct wrapping a function + name, implements `Provider` for tests/mocks

### 4.3 `internal/config`

```go
type Config struct {
    Port              string
    EigenURL          string
    KarakURL          string
    SymbioticURL      string
    DefiLlamaURL      string
    DefiLlamaYields   string
    DefiLlamaPrices   string
    LidoAPIURL        string
    OtelEndpoint      string
    APIKeys           map[string]string
    RequestTimeout    time.Duration
    MaxAPY            float64
    MaxTVLChange      float64
    MinProviderCount  int
    CircuitResetDelay time.Duration
    MaxStaleSeconds   int64
    EnabledProviders  []string
}
```

- `Load() Config` — reads from env vars via `envx` helpers
- `Config.String() string` — human-readable summary for startup logging

### 4.4 `internal/envx`

Typed env-var helpers with defaults. All return the default if the variable is unset or unparseable.

- `String(key, def) string`
- `Int(key, def) int`
- `Int64(key, def) int64`
- `Float64(key, def) float64`
- `Duration(key, def) time.Duration`
- `Bool(key, def) bool`

### 4.5 `internal/types`

```go
type SupportedChain string

const (
    ChainEthereum  SupportedChain = "ethereum"
    ChainPolygon   SupportedChain = "polygon"
    ChainArbitrum  SupportedChain = "arbitrum"
    ChainOptimism  SupportedChain = "optimism"
    ChainAvalanche SupportedChain = "avalanche"
    ChainBSC       SupportedChain = "binance"
    ChainBase      SupportedChain = "base"
)

type ChainConfig struct {
    Enabled       bool    `json:"enabled"`
    RPCEndpoint   string  `json:"rpc_endpoint"`
    APIEndpoint   string  `json:"api_endpoint"`
    APIKey        string  `json:"api_key,omitempty"`
    Weight        float64 `json:"weight"`
    GasMultiplier float64 `json:"gas_multiple"`
}
```

### 4.6 `internal/fetch`

HTTP clients for each provider. All implement `provider.Provider`.

**Shared infrastructure:**
- `maxResponseBytes = 16 MiB` — caps successful response body before JSON decode
- `maxErrorBodyBytes = 4 KiB` — caps error response body for diagnostics
- `newRetryClient()` — `retryablehttp.Client` with retry policy
- `fetchETHPriceFromDefiLlama(ctx, hc) (float64, error)` — shared ETH price fetcher from `coins.llama.fi/prices/current/coingecko:ethereum`, used by DefiLlama, EigenLayer, Karak, Symbiotic clients
- `NewClient(cfg, name) provider.Provider` — factory; returns nil for unknown providers
- `FuncProvider(name, fn) provider.Provider` — wraps a function into a Provider

**Compile-time interface assertions:**
```go
var _ provider.Provider = (*EigenLayerClient)(nil)
var _ provider.Provider = (*KarakClient)(nil)
var _ provider.Provider = (*SymbioticClient)(nil)
var _ provider.Provider = (*DefiLlamaClient)(nil)
```

#### DefiLlamaClient

Keyless, always-on. Fetches LRT pool yields from `yields.llama.fi/pools` and ETH price from `coins.llama.fi/prices/current/coingecko:ethereum`. Normalizes TVL from USD to ETH.

Default LRT symbols tracked: `weETH, eETH, ezETH, rsETH, rswETH, ETHx, pufETH, rstETH, jsETH, unshETH, wBETH, cbETH`

- `NewDefiLlamaClient(cfg) *DefiLlamaClient`
- `Fetch(ctx) ([]model.Metric, error)` — concurrent ETH price + yields fetch, emits one metric per matching LRT pool
- Injectable: `fetchETHPriceFn`, `fetchYieldsFn` for testing

#### LidoClient

Fetches real stETH staking APR from Lido's official API (`eth-api.lido.fi/v1/protocol/steth/apr/sma`). Returns 7-day SMA APR. Converts from percentage (3.27 = 3.27%) to decimal (0.0327). TVL fetched from DefiLlama protocols API, with configurable fallback (`LIDO_FALLBACK_TVL_ETH`, default 10M ETH).

- `NewLidoClient(cfg) *LidoClient`
- `Fetch(ctx) ([]model.Metric, error)`
- Injectable: `fetchAPRFn`, `fetchTVLFn`

#### EigenLayerClient

Fetches EigenLayer restaking TVL from DefiLlama protocols API (`api.llama.fi/protocol/eigenlayer`). APY reported as 0 (restaking protocols don't have a simple yield). TVL converted from USD to ETH. Keyless, always active. URL overridable via `EIGENLAYER_API`.

- `NewEigenLayerClient(cfg) *EigenLayerClient`
- `Fetch(ctx) ([]model.Metric, error)`

#### KarakClient

Same pattern as EigenLayer, targeting `api.llama.fi/protocol/karak`. URL overridable via `KARAK_API`.

#### SymbioticClient

Same pattern as EigenLayer, targeting `api.llama.fi/protocol/symbiotic`. URL overridable via `SYMBIOTIC_API`.

#### restClient (unexported)

Generic REST client for providers exposing a `{"data": [...]}` JSON format. Shared by EigenLayer/Karak/Symbiotic when a custom API URL is configured. Returns a configuration error when URL is empty (so the aggregator skips the provider).

#### MultiChainClient

Fans out across multiple blockchains concurrently with per-chain TTL caching.

```go
type MultiChainClient struct {
    cfg           config.Config
    httpClient    *http.Client
    chains        map[SupportedChain]ChainConfig
    dataProviders map[SupportedChain][]provider.Provider
    mutex         sync.RWMutex
    cacheTTL      time.Duration
    cachedData    map[SupportedChain][]model.Metric
    cacheTime     map[SupportedChain]time.Time
}
```

- `NewMultiChainClient(cfg, chains) *MultiChainClient`
- `RegisterProvider(chain, p)` — adds a provider for a chain
- `Fetch(ctx) ([]model.Metric, error)` — concurrent fan-out across enabled chains
- `GenericChainProvider`, `PolygonProvider`, `ArbitrumProvider` — chain-specific wrappers

### 4.7 `internal/validation`

```go
type ValidationOptions struct {
    MaxAge                      time.Duration
    MinTVL                      float64
    MaxAPY                      float64
    RequirePositivePointsPerETH bool
    EnableOutlierDetection      bool
    OutlierIQRMultiplier        float64
}
```

Defaults: `MaxAge=24h`, `MinTVL=1.0`, `MaxAPY=10.0` (1000%), `RequirePositivePointsPerETH=true`, `EnableOutlierDetection=true`, `OutlierIQRMultiplier=1.5`

**Functions:**
- `DefaultValidationOptions() ValidationOptions`
- `FilterInvalid(metrics) []Metric` — uses defaults
- `FilterInvalidWithOptions(metrics, opts) []Metric` — basic filters then outlier detection (if >3 metrics)
- `FilterInvalidConcurrently(metrics, opts) []Metric` — parallel for ≥100 metrics (4 workers), serial for smaller sets
- `CalculateConfidenceScores(metrics) []Metric` — assigns 0..1 confidence based on distance from TVL-weighted consensus APY

**Outlier detection** (`filterOutliers`): combines IQR method with MAD (median absolute deviation). MAD is essential for small samples — with 4 points the IQR can be dominated by the outlier itself. A metric is an outlier if it falls outside IQR bounds OR its distance from the median exceeds `madMultiplier * scaledMAD`.

**Basic criteria** (`filterBasicCriteria` → `isValidMetric`):
- Age ≤ MaxAge
- TVL ≥ MinTVL
- APY ≤ MaxAPY
- APY ≥ 0
- PointsPerETH ≥ 0 (and >0 if required)
- Rejects NaN and Inf (these poison downstream computation)

### 4.8 `internal/aggregate`

- `Weighted(metrics) Metric` — TVL-weighted APY, summed TVL, median PointsPerETH (excludes zero values). Returns `emptyMetric()` if no valid metrics.
- `WeightedParallel(ctx, metrics) Metric` — concurrent variant, one goroutine per CPU core, chunk-local computation then mutex merge. Honours context cancellation.
- `Median(metrics, selector) float64` — median of a selected field across valid metrics
- `ValidateMetric(m) error` — checks NaN/Inf, negative APY, APY >1000%, TVL ≤0, negative PointsPerETH, timestamp validity, age <24h
- `FilterOutliers(metrics) []Metric` — standalone IQR filter (requires ≥4 metrics)
- `ValidateAndFilterMetrics(metrics) []Metric` — validate then filter outliers
- `WeightedWithValidation(metrics) Metric` — validate + filter + weighted
- `WeightedParallelWithValidation(ctx, metrics) Metric` — concurrent variant
- `AverageMetrics(metrics) Metric` — simple unweighted average (TVL still summed)
- `MedianAggregation(metrics) Metric` — median APY, summed TVL, median PointsPerETH
- `TrimmedMeanAggregation(metrics, trimPercent) Metric` — sorts by APY, trims top/bottom `trimPercent`, then weighted

### 4.9 `internal/circuitbreaker

```go
type State int
const (
    StateClosed   State = iota  // normal operation
    StateOpen                   // tripped, blocked
    StateHalfOpen               // testing recovery
)

type Thresholds struct {
    MaxAPY             float64 `json:"max_apy"`
    MaxTVLChange       float64 `json:"max_tvl_change"`
    MinProviders       int     `json:"min_providers"`
    MaxStdDevMultiple  float64 `json:"max_std_dev_multiple,omitempty"`
}

type CircuitBreaker struct {
    thresholds       Thresholds
    state            State
    lastTrip         time.Time
    resetDelay       time.Duration
    mu               sync.RWMutex
    metricsHistory   []model.Metric
    lastGoodMetrics  []model.Metric
    lastGoodAt       int64
    maxStaleness     int64
    successCount     int
    successThreshold int
    onTripCallback   func(reason string, metrics []model.Metric)
}
```

**Builder methods (chainable):**
- `New(thresholds) *CircuitBreaker` — defaults: `resetDelay=5m`, `successThreshold=3`
- `WithResetDelay(d) *CircuitBreaker`
- `WithSuccessThreshold(n) *CircuitBreaker`
- `WithTripCallback(fn) *CircuitBreaker`
- `WithMaxStaleness(seconds) *CircuitBreaker` — 0 = no limit

**Operations:**
- `Check(metrics) error` — main entry point. If open and reset delay elapsed → half-open. Checks: min providers, max APY per metric, TVL change vs history, APY std-dev. On pass: records history, stores last-good, increments success count in half-open (closes after threshold). On fail: trips, fires callback after lock release.
- `GetState() State`
- `Reset()` — forcibly closes
- `LastGoodMetrics() []Metric` — defensive copy, returns nil if stale (age > maxStaleness)

**Concurrency:** Uses RLock for state check, then upgrades to Lock for validation. Rechecks state after acquiring write lock to handle concurrent trips. Callback fires outside the lock to prevent deadlock if callback calls back into the breaker.

### 4.10 `internal/security`

#### EIP-712

```go
type EIP712Domain struct {
    Name              string
    Version           string
    ChainID           *big.Int
    VerifyingContract common.Address
}

type YieldReport struct {
    APYBps          *big.Int  // uint96, basis points (450 = 4.5%)
    TVLMilliETH     *big.Int  // uint96, milli-ETH (1_000_000 = 1000 ETH)
    PointsPerETHppm *big.Int  // uint64, ppm (1_100_000 = 1.1)
    Timestamp       *big.Int  // uint32, unix seconds
}
```

Type hashes (MUST match Solidity constants):
- `EIP712DomainTypeHash` = keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
- `YieldReportTypeHash` = keccak256("YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)")

**Methods:**
- `EIP712Domain.Separator() common.Hash` — keccak256(typeHash || keccak256(name) || keccak256(version) || chainId || verifyingContract)
- `YieldReport.StructHash() common.Hash` — keccak256(typeHash || apyBps || tvlMilliETH || pointsPerETHppm || timestamp)
- `YieldReport.Digest(domain) common.Hash` — keccak256("\x19\x01" || domainSeparator || structHash)
- `NewYieldReport(apyBps, tvlMilliETH, pointsPerETHppm, timestamp) (YieldReport, error)`
- `EIP712DomainFromHex(name, version, chainIDStr, verifyingContractHex) (EIP712Domain, error)`

#### DataIntegrityService

```go
type DataIntegrityService struct {
    privateKey       *ecdsa.PrivateKey
    address          string
    publicKeyEncoded string  // base64 of uncompressed 65-byte public key
    verificationOpts VerificationOptions
}

type VerificationOptions struct {
    SignatureEnabled     bool
    VerificationRequired bool
    SignatureValidity    time.Duration
    StrictMode           bool
}
```

**Constructors:**
- `NewDataIntegrityService(opts) (*DataIntegrityService, error)` — generates ephemeral secp256k1 key
- `NewDataIntegrityServiceFromKey(hexKey, opts) (*DataIntegrityService, error)` — from existing hex private key (0x-prefixed or bare)

**Methods:**
- `Address() string` — 0x-prefixed Ethereum address
- `GetPublicKey() string` — base64-encoded 65-byte public key
- `Sign(data) ([]byte, error)` — 65-byte r||s||v signature; hashes with keccak256 if not already 32 bytes
- `SignDigest(payload) ([]byte, error)` — keccak256-hashes payload first
- `Verify(data, signature) bool` — constant-time comparison via `subtle.ConstantTimeCompare`
- `SignPayload(payload) (map[string]interface{}, error)` — adds `_signature` envelope (signature, publicKey, address, algorithm, timestamp, validUntil, nonce) over canonical JSON
- `VerifyPayload(signedPayload) (bool, error)` — checks expiry, re-canonicalises, constant-time pubkey compare
- `OnChainVerificationData(payload) (map[string]interface{}, error)` — produces bundle for on-chain ecrecover
- `CreateTamperProofWrapper(payload, metadata) (map[string]interface{}, error)` — wraps with Keccak256 integrity hash + signature
- `VerifyIntegrity(wrappedData) (bool, map[string]interface{}, error)` — signature + hash verification
- `SignOnChainReport(report, domain) (digest, signature, signer, error)` — signs EIP-712 YieldReport digest

**Canonical JSON:** Structs are normalised to `map[string]interface{}` via marshal→unmarshal round-trip so key sorting applies uniformly. Without this, struct field order vs map sorted keys would produce different bytes and break signatures.

**Replay protection:** Nonces are 63-bit positive integers from `crypto/rand`. Verifiers should track seen nonces within the validity window.

### 4.11 `internal/enterprise`

```go
type ExporterConfig struct {
    Enabled        bool   `json:"enabled"`
    BatchSize      int    `json:"batch_size"`
    ExportInterval string `json:"export_interval"`
    WebhookEnabled bool   `json:"webhook_enabled"`
    WebhookURL     string `json:"webhook_url"`
    WebhookAPIKey  string `json:"webhook_api_key,omitempty"`
}
```

- `NewMetricsExporter(config) (*MetricsExporter, error)` — starts periodic export goroutine. Warns if webhook URL is not HTTPS (API key sent as Bearer token). Enforces TLS 1.2+.
- `AddMetricBatch(metrics)` — queues metrics; flushes when batch size reached
- `Stop()` — cancels periodic export, flushes remaining
- `GetExporterStatus() map[string]interface{}` — for `/status`

Retry queue: max 100 batches, max 3 retries per batch before drop. Prevents unbounded retry loops during prolonged outages.

### 4.12 `internal/otel`

- `InitTracer(endpoint) (shutdown func(ctx) error, error)`:
  - empty endpoint → no-op TracerProvider
  - `"stdout"` → stdout exporter with pretty print
  - URL → OTLP/HTTP exporter (auto-detects insecure for `http://` scheme or plain `host:port`)
- `Tracer() trace.Tracer` — returns `otel.Tracer("restake-yield-ea")`
- `RecordError(ctx, err)` — records error on active span
- `Shutdown(ctx) error` — flushes and shuts down global TracerProvider
- Sampler: `TraceIDRatioBased` from `OTEL_TRACE_SAMPLE_RATIO` (default 0.1 = 10%)
- Resource: `service.name=restake-yield-ea`, `service.version=1.0.0`

### 4.13 `cmd/server`

**Server struct:**
```go
type Server struct {
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
    shutdownFn       func(context.Context) error
    adminToken       string
    trustedProxy     string
}
```

**Lifecycle:**
- `main()` → `run(nil)` → `setupLogging()`, `config.Load()`, `InitTracer()`, `loadServerConfig()`, `createProviders()`, `NewServer()`, `srv.Start()` in goroutine, waits for SIGINT/SIGTERM or listen error, `srv.Stop()`
- `Start()` — `http.Server` with 15s read/write timeout, 60s idle timeout
- `Stop()` — 30s graceful shutdown, stops metrics exporter, shuts down tracer

**Provider selection** (`createProviders`):
- Default: `defillama` + `lido` (both keyless, real data)
- `eigenlayer` added if `EIGENLAYER_API` set
- `karak` added if `KARAK_API` set
- `symbiotic` added if `SYMBIOTIC_API` set
- `ENABLED_PROVIDERS` override (comma-separated) takes precedence

**Enterprise init** (`initEnterprise`):
- Rate limiting: `RATE_LIMIT_RPS` (default 10), `RATE_LIMIT_BURST` (default 20)
- Multi-chain: if `MULTICHAIN_ENABLED`, creates `MultiChainClient` with Ethereum (+ Polygon if `POLYGON_ENABLED`)
- Data integrity: if `DATA_INTEGRITY_ENABLED`, creates `DataIntegrityService` from `SIGNING_PRIVATE_KEY` (or ephemeral)
- Metrics export: if `METRICS_EXPORT_ENABLED`, creates `MetricsExporter` with webhook config

---

## 5. Solidity Contracts

### 5.1 YieldVerifier

On-chain signature verifier. No state (pure functions only).

**Inheritance:** None (imports OpenZeppelin `ECDSA`)

**Constants:**
- `SECP256K1N_HALF` (bytes32, private) = `0x7fffffffffffffffffffffffffffffff5d576e7357a4501ddfe92f46681b20a0` — half curve order for low-s enforcement

**Events:**
- `YieldVerified(address indexed signer, bytes32 indexed digest)`

**Errors:**
- `InvalidSignatureLength(uint256 length)`
- `InvalidSignatureV(uint8 v)`
- `InvalidSignatureS(bytes32 s)`
- `SignerMismatch(address expected, address recovered)`

**Functions:**
- `verifyYield(bytes32 digest, bytes calldata signature, address expectedSigner) external pure returns (address recovered)` — verifies 65-byte r||s||v signature
- `verifyAndLog(bytes32 digest, bytes calldata signature, address expectedSigner) external returns (address recovered)` — verifies + emits event
- `digestOf(bytes calldata payload) external pure returns (bytes32)` — `keccak256(payload)`
- `_verifyYield(...)` internal — parses signature via assembly, normalises v from {0,1} to {27,28}, enforces low-s, calls `ECDSA.recover`, checks recovered == expected

### 5.2 RestakeYieldOracle

On-chain oracle storing the latest verified yield report.

**Inheritance:** OpenZeppelin `AccessControl`, `Pausable`

**Struct:**
```solidity
struct YieldReport {
    uint96 apyBps;           // basis points, 450 = 4.5%
    uint96 tvlMilliETH;      // milli-ETH, 1_000_000 = 1000 ETH
    uint64 pointsPerETHppm;  // ppm, 1_100_000 = 1.1
    uint32 updatedAt;        // block timestamp
}
```

**Constants:**
- `EIP712_DOMAIN_TYPEHASH` = keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
- `YIELDREPORT_TYPEHASH` = keccak256("YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)")
- `EIP712_NAME` = "RestakeYieldOracle"
- `EIP712_VERSION` = "1"
- `ADMIN_ROLE` = keccak256("ADMIN_ROLE")
- `UPDATER_ROLE` = keccak256("UPDATER_ROLE")

**State:**
- `verifier` (YieldVerifier, immutable)
- `authorisedSigner` (address)
- `isUpdater` (mapping(address => bool))
- `s_latestReport` (YieldReport)
- `lastReportTimestamp` (uint256)
- `maxAPYBps` = 10_000 (100%)
- `minAPYBps` = 0
- `maxDeviationBps` = 500 (5%)
- `stalenessThreshold` = 1 hour

**Events:**
- `YieldUpdated(uint256 indexed apyBps, uint96 indexed tvlMilliETH, address indexed signer, uint256 timestamp)`
- `SignerRotated(address indexed oldSigner, address indexed newSigner)`
- `UpdaterSet(address indexed updater, bool authorised)`
- `MaxAPYBpsSet`, `MinAPYBpsSet`, `MaxDeviationBpsSet`, `StalenessThresholdSet`
- `OwnershipTransferred`

**Errors:**
- `Unauthorized(address caller)`
- `InvalidSignature()`
- `StaleReport(uint256 submittedAt, uint256 lastUpdate)`
- `ReplayDetected(uint256 submittedTimestamp, uint256 lastReportTimestamp)`
- `APYDeviationExceeded(int256 deviationBps, uint256 maxDeviationBps)`
- `InvalidAPYBounds(uint256 apyBps)`
- `ZeroAddress()`

**Constructor:** `constructor(YieldVerifier _verifier, address _signer)` — sets verifier, authorisedSigner, grants `DEFAULT_ADMIN_ROLE` + `ADMIN_ROLE` to deployer, grants `UPDATER_ROLE` to signer

**Functions:**
- `domainSeparator() public view returns (bytes32)` — recomputed on every call using `block.chainid` (correct after forks)
- `digestOf(uint96 apyBps, uint96 tvlMilliETH, uint64 pointsPerETHppm, uint32 timestamp) public view returns (bytes32)` — EIP-712 digest
- `submitReport(bytes calldata signature, uint96 apyBps, uint96 tvlMilliETH, uint64 pointsPerETHppm, uint32 timestamp) external whenNotPaused` — main submission path (see checks below)
- `latestYield() external view returns (YieldReport memory)`
- `latestAPYScaled() external view returns (uint256)` — `apyBps * 1e14` (for fixed-point math)
- `isStale() public view returns (bool)`
- `setSigner(address) external onlyRole(ADMIN_ROLE)` — rotates signer, revokes old, grants new
- `setUpdater(address, bool) external onlyRole(ADMIN_ROLE)`
- `setMaxAPYBps`, `setMinAPYBps`, `setMaxDeviationBps`, `setStalenessThreshold` — `onlyRole(ADMIN_ROLE)`
- `pause()`, `unpause()` — `onlyRole(ADMIN_ROLE)`

**submitReport checks (in order):**
1. Caller must be an updater (`isUpdater[msg.sender]`)
2. Reconstruct digest on-chain from report params + domain separator (binds signature to values)
3. Verify signature recovers to `authorisedSigner` via `verifier.verifyYield`
4. Replay prevention: `timestamp > lastReportTimestamp`
5. Staleness: `block.timestamp - timestamp ≤ stalenessThreshold` (underflow reverts on future timestamps)
6. APY bounds: `minAPYBps ≤ apyBps ≤ maxAPYBps`
7. Deviation: `|apyBps - prevAPY| ≤ maxDeviationBps` (skipped on first report or if maxDeviationBps=0)
8. Store report, update `lastReportTimestamp`, emit `YieldUpdated`

### 5.3 YieldConsumerExample

Minimal DeFi vault consuming the oracle. Demonstrates the consumer side.

**Inheritance:** OpenZeppelin `ReentrancyGuard`

**Constants:**
- `MAX_PLAUSIBLE_APY_BPS` = 10_000 (100%)
- `SECONDS_PER_YEAR` = 31_557_600 (365.25 days)

**State:**
- `oracle` (RestakeYieldOracle, immutable)
- `totalAssets` (uint256) — principal + accrued yield
- `totalShares` (uint256)
- `sharesOf` (mapping(address => uint256))
- `lastAccrualTime` (uint256)
- `lastAppliedAPYBps` (uint256)
- `circuitBreakerOpen` (bool)

**Functions:**
- `deposit() external payable nonReentrant` — accrues yield, mints shares at current price
- `withdraw(uint256 sharesToBurn) external nonReentrant` — accrues yield, burns shares, sends ETH. Blocked if circuit breaker open.
- `accrueYield() external` — anyone can call
- `totalValue() external view returns (uint256)`
- `balanceOf(address) external view returns (uint256)`
- `sharePrice() external view returns (uint256)`
- `_sharePrice() internal view returns (uint256)` — `totalShares == 0 ? 1e18 : totalAssets * 1e18 / totalShares`
- `_accrueYield() internal` — checks oracle staleness/implausibility, trips/clears circuit breaker, linear accrual: `totalAssets *= (1 + apyBps * 1e14 * elapsed / SECONDS_PER_YEAR)`
- `receive() external payable` — reverts (rejects direct ETH sends)

**Flash-loan protection:** Shares are minted AFTER accrual, so new deposits buy at the current price (which already reflects accrued yield). New depositors never receive retroactive yield.

---

## 6. Configuration

All configuration via environment variables. Defaults in parentheses.

### Core

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `LOG_FORMAT` | `text` | `text`/`json` |
| `TIMEOUT` | `10s` | Request timeout |
| `AGGREGATION_MODE` | `weighted` | `weighted`/`median`/`trimmed`/`consensus` |
| `ENABLE_CIRCUIT_BREAKER` | `true` | Enable circuit breaker |
| `ENABLE_VALIDATION` | `true` | Enable validation filters |
| `ENABLE_METRICS` | `true` | Enable Prometheus metrics |
| `MAX_APY_THRESHOLD` | `1.0` | Max APY (decimal) for circuit breaker |
| `MIN_PROVIDERS` | `2` | Min provider count for circuit breaker |
| `MAX_TVL_CHANGE` | `0.5` | Max TVL change ratio (50%) |
| `CIRCUIT_RESET_DELAY` | `5m` | Circuit breaker reset delay |
| `MAX_STALE_SECONDS` | `300` | Max age for last-good fallback (0=disabled) |
| `ENABLED_PROVIDERS` | `defillama,lido` | Comma-separated provider list |

### Provider URLs

| Variable | Default | Description |
|----------|---------|-------------|
| `DEFILLAMA_URL` | `https://defillama.com` | DefiLlama base |
| `DEFILLAMA_YIELDS` | `https://yields.llama.fi` | DefiLlama yields API |
| `DEFILLAMA_PRICES` | `https://coins.llama.fi` | DefiLlama prices API |
| `LIDO_API_URL` | `https://eth-api.lido.fi` | Lido APR API |
| `LIDO_FALLBACK_TVL_ETH` | `10000000` | Fallback TVL if DefiLlama fetch fails |
| `EIGENLAYER_API` | `https://api.llama.fi/protocol/eigenlayer` | EigenLayer TVL source |
| `KARAK_API` | `https://api.llama.fi/protocol/karak` | Karak TVL source |
| `SYMBIOTIC_API` | `https://api.llama.fi/protocol/symbiotic` | Symbiotic TVL source |

### Enterprise

| Variable | Default | Description |
|----------|---------|-------------|
| `ENABLE_ENTERPRISE_FEATURES` | `false` | Master switch |
| `ADMIN_TOKEN` | (empty) | Bearer token for `/circuit`, `/status` |
| `TRUSTED_PROXY` | (empty) | Trusted proxy IP for XFF |
| `RATE_LIMIT_RPS` | `10` | Rate limit requests/sec |
| `RATE_LIMIT_BURST` | `20` | Rate limit burst |
| `MULTICHAIN_ENABLED` | `false` | Enable multi-chain client |
| `DATA_INTEGRITY_ENABLED` | `false` | Enable signing |
| `SIGNING_PRIVATE_KEY` | (empty) | Hex private key (empty = ephemeral) |
| `VERIFICATION_REQUIRED` | `false` | Require signature verification |
| `SIGNATURE_VALIDITY` | `24h` | Signature validity window |
| `STRICT_MODE` | `false` | Strict verification mode |
| `METRICS_EXPORT_ENABLED` | `false` | Enable webhook export |
| `METRICS_EXPORT_BATCH_SIZE` | `100` | Export batch size |
| `METRICS_EXPORT_INTERVAL` | `1m` | Export interval |
| `WEBHOOK_ENABLED` | `false` | Enable webhook |
| `WEBHOOK_URL` | (empty) | Webhook endpoint (must be HTTPS) |
| `WEBHOOK_API_KEY` | (empty) | Webhook Bearer token |

### Observability

| Variable | Default | Description |
|----------|---------|-------------|
| `OTEL_ENDPOINT` | (empty) | OTLP/HTTP endpoint (empty=no-op, `stdout`=debug) |
| `OTEL_TRACE_SAMPLE_RATIO` | `0.1` | Trace sampling ratio (0.0–1.0) |

---

## 7. HTTP API

### `POST /`

Chainlink EA request/response format.

**Request:**
```json
{
  "id": "job-run-id",
  "data": {},
  "meta": {}
}
```

**Response (success):**
```json
{
  "jobRunID": "job-run-id",
  "status": "completed",
  "data": {
    "result": 0.045,
    "apy": 0.045,
    "tvl": 1000000.0,
    "pointsPerETH": 1.1,
    "provider": "aggregated-weighted",
    "collectedAt": 1700000000,
    "timestamp": 1700000001,
    "meta": {
      "latencyMs": 150,
      "metricCount": 5,
      "aggregationMode": "weighted",
      "requestId": "abc123..."
    }
  },
  "result": 0.045,
  "error": null,
  "pending": false
}
```

**Response (error):**
```json
{
  "jobRunID": "job-run-id",
  "status": "errored",
  "statusCode": 503,
  "error": {
    "name": "EAError",
    "message": "circuit open: ..."
  },
  "data": {},
  "pending": false
}
```

When `DATA_INTEGRITY_ENABLED`, the response is wrapped in a tamper-proof envelope with `_signature` and `integrity` fields.

### `GET /health`

Liveness probe. Returns `{"status":"OK","version":"...","timestamp":"..."}`

### `GET /readyz`

Readiness probe. Returns 503 if no providers configured. Includes `circuit_state` if breaker enabled.

### `GET /metrics`

Prometheus metrics endpoint (if `ENABLE_METRICS`).

### `GET /status` (admin)

Server status: uptime, version, providers, configuration, circuit state, signer address. Protected by `ADMIN_TOKEN` if set.

### `GET|POST /circuit` (admin)

Circuit breaker status. `POST ?action=reset` forcibly resets. Protected by `ADMIN_TOKEN` if set.

---

## 8. Observability

### Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `restake_requests_total` | Counter | status, aggregation | Total requests |
| `restake_request_duration_seconds` | Histogram | status | Request duration |
| `restake_provider_errors_total` | Counter | provider | Provider fetch errors |
| `restake_provider_latency_seconds` | Histogram | provider | Per-provider latency |
| `restake_circuit_breaker_state` | Gauge | reason | 0=closed, 1=open, 2=half-open |
| `restake_aggregate_tvl` | Gauge | — | Aggregated TVL (ETH) |
| `restake_aggregate_apy` | Gauge | — | Aggregated APY (decimal) |
| `restake_metric_count` | Gauge | — | Metrics per request |
| `restake_provider_count` | Gauge | — | Configured providers |

### Grafana

Dashboard auto-provisioned from `deploy/grafana/dashboard.json`. Datasource: Prometheus at `http://prometheus:9090`.

### OpenTelemetry

Traces exported via OTLP/HTTP. Service name: `restake-yield-ea`, version `1.0.0`. Default sampling 10%.

---

## 9. Deployment

### Docker

Multi-stage build:
- Builder: `golang:1.25-alpine`, static binary with `-ldflags="-s -w -X main.version=${VERSION}"`
- Runtime: `alpine:3.20`, non-root user (`appuser`, UID 10001), healthcheck on `/readyz`

### docker-compose

Brings up:
- `restake-ea` (port 8080) — the EA
- `prometheus` (port 9090) — scrapes `/metrics` every 10s
- `grafana` (port 3000, admin/admin) — visualises metrics

### Kubernetes

`deploy/k8s/deployment.yaml`:
- Namespace: `restake-yield-ea`
- ConfigMap: all non-secret env vars
- Deployment: 2 replicas, readiness probe `/readyz`, liveness probe `/health`, resource limits (500m CPU, 256Mi memory)
- Service: port 8080
- Secret: `signing-private-key` (created via kubectl)

`deploy/k8s/servicemonitor.yaml`:
- ServiceMonitor for Prometheus Operator, scrapes `/metrics` every 15s

---

## 10. CI Pipeline

`.github/workflows/go.yaml` — 7 jobs:

| Job | Description |
|-----|-------------|
| Go build, vet, test | `go vet`, `go build`, `go test -race`, coverage ≥70%, benchmarks |
| golangci-lint | `golangci-lint-action@v6`, `install-mode: goinstall` (Go 1.26 compat) |
| gosec | `gosec -severity medium -confidence medium` |
| govulncheck | `govulncheck ./...` |
| Foundry build + test | `forge build`, `forge test -vv`, generates signature fixture via `genfixture.go` |
| Slither | `slither . --config slither.config.json`, `fail_on: medium` |
| Docker build | `docker/build-push-action@v6`, depends on go + solidity |

`.github/workflows/gas-report.yaml`:
- Forge gas report, uploads as artifact, comments on PRs

### Linting Config

`.golangci.yml`: errcheck, ineffassign, staticcheck, unused, govet, gosec, misspell, unconvert, prealloc, gocritic, revive. Test files excluded from gosec/prealloc/revive/gocritic.

`slither.config.json`: `fail_on: medium`, excludes naming-convention/solc-version/external-function detectors, filters lib/test/script/mock paths.

---

## 11. Security

### Signing

- secp256k1 / keccak256 (Ethereum curve)
- EIP-712 typed data with domain separator (prevents cross-chain/cross-contract/cross-application replay)
- 65-byte r||s||v signatures, v in {0,1} (go-ethereum convention), normalised to {27,28} on-chain
- Low-s malleability enforcement on-chain
- Monotonic timestamp check on-chain (same-chain replay prevention)
- Digest reconstructed on-chain from report params (prevents value-substitution attacks)

### HTTP Hardening

- Body size limit: 1 MiB request, 16 MiB response, 4 KiB error body
- Security headers: `nosniff`, `DENY`, `no-referrer`, `no-store`
- Request ID propagation (crypto/rand, 16-byte hex)
- Admin endpoint auth: Bearer token, constant-time comparison
- Trusted proxy for XFF (only honoured from configured proxy IP)
- Rate limiting (enterprise)

### Data Integrity

- Canonical JSON for deterministic signing (sorted keys, struct→map normalisation)
- Nonce from crypto/rand (63-bit positive)
- Signature expiry via `validUntil`
- Constant-time public key comparison

### Operational

- Circuit breaker with last-good fallback and staleness check
- Outlier detection (IQR + MAD)
- NaN/Inf rejection at validation gate
- HTTPS enforcement for webhook export
- TLS 1.2+ minimum for webhook client
- Non-root Docker user (UID 10001)
- Pre-commit hook: go vet, go test -race, forge test, secret pattern scanning

---

## 12. Testing

### Go

- **13 packages**, 96% coverage
- `go test -race -count=1` in CI
- Benchmarks: `go test -bench=. -benchmem ./internal/aggregate/...`
- Integration tests: `go test -tags=integration ./internal/fetch/...` (requires network)
- Fuzz tests: `internal/security/eip712_fuzz_test.go`
- Injectable functions for failure-path testing: `randReader`, `generateKeyFunc`, `signFunc`, `jsonMarshalFunc`, `jsonUnmarshalFunc`, `initTracerFn`, `newMetricsExporterFn`, `newStdoutExporter`, `newOTLPHTTPExporter`, `resourceMergeFunc`

### Solidity

- **74 tests** (0 failed, 1 skipped)
- Unit tests: `RestakeYieldOracle.t.sol`, `YieldVerifier.t.sol`, `YieldConsumerExample.t.sol`
- Invariant tests: `YieldConsumerExample.invariant.t.sol`
- Mainnet fork tests: `MainnetFork.t.sol` (requires `FORK_URL`)
- Halmos formal verification: `halmos --function check_`
- Cross-language signature proof: `genfixture.go` generates `eip712_signature.json`, loaded by `test_OracleAcceptsGoGeneratedFixture`

### Verification Commands

```bash
make test          # Go tests
make test-race     # Go tests with race detector
make lint          # golangci-lint
make security      # gosec
make vulncheck     # govulncheck
make forge-test    # Foundry tests
make slither       # Slither
make halmos        # Halmos formal verification
make forge-coverage # Solidity coverage
make forge-gas     # Gas report
```
