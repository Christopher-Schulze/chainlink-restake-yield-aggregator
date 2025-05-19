# Restake Yield Aggregator - Comprehensive Technical Documentation

## Table of Contents

1. [Introduction](#introduction)
2. [Architecture Overview](#architecture-overview)
3. [Core Components](#core-components)
4. [Enterprise Features](#enterprise-features)
5. [Data Flow](#data-flow)
6. [Implementation Details](#implementation-details)
7. [Security Considerations](#security-considerations)
8. [API Reference](#api-reference)
9. [Performance Specifications](#performance-specifications)
10. [Testing Methodology](#testing-methodology)
11. [Deployment Guide](#deployment-guide)
12. [Configuration Reference](#configuration-reference)

## Introduction

The Restake Yield Aggregator is an enterprise-grade External Adapter (EA) for Chainlink nodes, designed to provide reliable, statistically validated yield metrics from multiple sources. This document provides an exhaustive technical explanation of its implementation, architecture, and features.

### Purpose and Problem Statement

In the rapidly evolving DeFi ecosystem, obtaining reliable and consistent yield data presents several challenges:

1. **Data Inconsistency**: Different providers report yield metrics using varying methodologies and timeframes
2. **Manipulation Risk**: Single-source data is susceptible to manipulation or temporary anomalies
3. **Changing Market Conditions**: Extreme market conditions can lead to outlier data points
4. **Cross-Chain Complexity**: Aggregating data across multiple blockchains adds complexity
5. **Data Integrity Concerns**: Ensuring data hasn't been tampered with during transmission

This External Adapter addresses these challenges through:
- Multi-provider data collection with configurable weighting
- Statistical validation with outlier detection
- Circuit breaker pattern for protection against extreme conditions
- Enterprise-grade security with cryptographic verification
- Cross-chain support with unified interfaces

### Technology Stack

The implementation uses a carefully selected technology stack:

- **Language**: Go 1.22+
- **HTTP Framework**: Native Go `net/http` for maximum performance
- **Concurrency Model**: Go's goroutines and channels for parallel processing
- **Metrics Collection**: Prometheus integration via `client_golang`
- **Cryptography**: ECDSA with P-256 curve via Go's `crypto` packages
- **Time Management**: Go's `time` package with support for duration parsing
- **Configuration**: Environment variables with sensible defaults
- **Logging**: Structured logging via `logrus`
- **Cross-platform Support**: Pure Go implementation for platform independence

Go was chosen for this implementation due to its excellent concurrency model, robust standard library, strong typing, and performance characteristics - all qualities that align with Chainlink's technical requirements.

## Architecture Overview

The system follows a layered architecture with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────────────┐
│                       HTTP API Layer                            │
├─────────────────────────────────────────────────────────────────┤
│                     Business Logic Layer                        │
├───────────────┬─────────────────┬──────────────┬───────────────┤
│ Data          │ Validation      │ Aggregation  │ Security      │
│ Collection    │ Processing      │ Strategies   │ Services      │
├───────────────┼─────────────────┼──────────────┼───────────────┤
│ Multi-Chain   │ Circuit         │ Enterprise   │ Metrics       │
│ Support       │ Breaker         │ Exporting    │ Collection    │
└───────────────┴─────────────────┴──────────────┴───────────────┘
```

### Component Interaction

The flow of data and interactions between components follows this sequence:

1. **HTTP API Layer**: Handles incoming requests, applies rate limiting, and manages response formatting
2. **Data Collection Layer**: Fetches metrics from providers, potentially across multiple blockchains
3. **Validation Layer**: Applies filters and statistical validation to incoming metrics
4. **Circuit Breaker**: Evaluates metrics against thresholds to determine system availability
5. **Aggregation Layer**: Applies the selected strategy to produce a final metric
6. **Security Layer**: Adds cryptographic signatures and tamper-proof wrappers
7. **Metrics Collection**: Records performance and operational metrics throughout

### Key Design Principles

This architecture embodies several key design principles:

1. **Modularity**: Each component is independent with well-defined interfaces, allowing for replacement or extension without affecting other parts of the system
2. **Concurrency**: Parallel execution wherever possible to maximize throughput and performance
3. **Defensive Programming**: Comprehensive error handling, timeouts, and fallback mechanisms
4. **Statelessness**: Minimized state for improved horizontal scaling and resilience
5. **Observability**: Extensive logging and metrics collection at key points
6. **Security by Design**: Security considerations incorporated from the beginning, not added afterward
7. **Configuration over Convention**: Externalized configuration for deployment flexibility

## Core Components

### Server Component

**Location**: `cmd/server/main.go`

The server component is the entry point of the application and responsible for:
- Initializing all services and components
- Setting up HTTP endpoints
- Configuring middleware
- Establishing health and metrics endpoints
- Managing graceful shutdown

The `Server` struct encapsulates all dependencies and state:

```go
type Server struct {
    // Basic configuration
    config           ServerConfig      // Server configuration
    providers        []Provider        // Data providers
    server           *http.Server      // HTTP server
    breaker          *circuitbreaker.CircuitBreaker // Circuit breaker
    metrics          *serverMetrics    // Prometheus metrics
    validationOpts   validation.ValidationOptions // Validation options

    // Enterprise features
    multiChainClient *fetch.MultiChainClient // Multi-chain client
    metricsExporter  *enterprise.MetricsExporter // Metrics exporter
    dataIntegrity    *security.DataIntegrityService // Data integrity service
    rateLimit        *rate.Limiter   // Rate limiter
    enableEnterprise bool           // Enterprise features flag
}
```

The server exposes several HTTP endpoints:
- `/` - Main EA interface for Chainlink nodes (POST)
- `/health` - Health check endpoint (GET)
- `/metrics` - Prometheus metrics endpoint (GET)
- `/status` - Detailed status information (GET)
- `/circuit-status` - Circuit breaker status and control (GET/POST)

### Data Model

**Location**: `internal/model/model.go`

The data model is centered around the `Metric` struct, which represents a yield metric from a provider:

```go
type Metric struct {
    // Core fields
    Provider     string  `json:"provider"`     // Data source identifier
    APY          float64 `json:"apy"`          // Annual Percentage Yield as decimal
    TVL          float64 `json:"tvl"`          // Total Value Locked in ETH
    PointsPerETH float64 `json:"points_per_eth"` // Protocol-specific point conversion rate
    CollectedAt  int64   `json:"collected_at"` // Unix timestamp of collection

    // Extended fields
    Confidence   float64 `json:"confidence,omitempty"` // Confidence score (0-1)
    RiskScore    float64 `json:"risk_score,omitempty"` // Risk assessment score
    Protocol     string  `json:"protocol,omitempty"`   // Protocol if different from provider
    Chain        string  `json:"chain,omitempty"`      // Blockchain network
    Weight       float64 `json:"weight,omitempty"`     // Weight for aggregation
    Error        string  `json:"error,omitempty"`      // Error message if any
    Version      string  `json:"version,omitempty"`    // Schema version
}
```

### Provider Interface

**Location**: `cmd/server/main.go`

Each data provider must implement the `Provider` interface:

```go
type Provider interface {
    Fetch(ctx context.Context) ([]model.Metric, error)
}
```

This simple interface allows for uniform interaction with diverse data sources, easy addition of new providers, and consistent error handling.

### Circuit Breaker Pattern

**Location**: `internal/circuitbreaker/breaker.go`

The circuit breaker implements a sophisticated state machine with three primary states:

1. **Closed**: Normal operation, all requests processed
2. **Open**: Service protection mode, returns cached data or errors
3. **Half-Open**: Testing mode, limited requests to check if issues are resolved

```go
type state int

const (
    stateClosed state = iota
    stateOpen
    stateHalfOpen
)

type CircuitBreaker struct {
    state           state
    lastStateChange time.Time
    failureCount    int
    successCount    int
    mutex           sync.RWMutex
    thresholds      Options
    lastGoodMetrics []model.Metric
    onTrip          func(string)
}
```

The circuit breaker evaluates metrics against several criteria:
- Maximum APY threshold (abnormally high APY)
- Maximum TVL change (suspicious liquidity movements)
- Minimum number of providers (data source diversity)
- Statistical variance (outlier detection)

### Validation Pipeline

**Location**: `internal/validation/filters.go`

The validation system provides multiple layers of filtering:

1. **Basic Validation**: Ensures metrics have non-negative APY, positive TVL, etc.
2. **Statistical Validation**: Detects outliers using IQR method, standard deviation
3. **Confidence Scoring**: Assigns confidence scores based on provider reputation, data freshness

### Aggregation Strategies

**Location**: `internal/aggregate/`

The system offers four distinct aggregation strategies:

1. **Weighted**: A TVL-weighted average, giving more influence to protocols with more assets at stake
2. **Median**: The median value, providing resistance to extreme outliers
3. **Trimmed Mean**: Removes a percentage of extreme values before averaging
4. **Consensus**: Uses confidence scores to weight the contributions

## Enterprise Features

### Multi-Chain Support

**Location**: `internal/fetch/multichain.go`, `internal/types/chain_types.go`

The multi-chain system provides support for:
```go
// Supported blockchain networks
const (
    ChainEthereum   SupportedChain = "ethereum"
    ChainPolygon    SupportedChain = "polygon"
    ChainArbitrum   SupportedChain = "arbitrum"
    ChainOptimism   SupportedChain = "optimism"
    ChainAvalanche  SupportedChain = "avalanche"
    ChainBSC        SupportedChain = "binance"
    ChainBase       SupportedChain = "base"
)
```

The system includes parallel data retrieval across chains, chain-specific client implementations, and unified metric format with chain indicators.

### Data Integrity Service

**Location**: `internal/security/data_integrity.go`

The data integrity service provides cryptographic assurance of data authenticity using ECDSA signatures with P-256 curve. It implements double hash verification with both SHA-256 and Keccak-256, timestamped validity periods, and tamper-evident wrappers.

### Enterprise Metrics Export

**Location**: `internal/enterprise/metrics_export.go`

The metrics export system enables enterprise-grade monitoring and analytics with batch collection, multiple export destinations (AWS, Webhooks, Kafka), and configurable export intervals.

## Data Flow

The data flow through the system follows these steps:

1. **Request Initiation**: External request arrives from Chainlink node, validated and rate-limited
2. **Data Collection**: Fetch from multiple providers and/or chains in parallel
3. **Validation Process**: Apply basic and statistical validation, calculate confidence scores
4. **Circuit Breaker Check**: Verify metrics against thresholds, potentially trip circuit
5. **Aggregation**: Apply selected strategy to aggregate metrics
6. **Security Processing**: Add cryptographic signatures if enabled
7. **Response Delivery**: Format Chainlink EA compatible response

## Implementation Details

### Request Handler Implementation

The main request handler processes the Chainlink EA request by:
1. Parsing and validating the incoming request
2. Applying rate limiting if configured
3. Fetching metrics from providers with timeouts
4. Validating and filtering metrics
5. Checking circuit breaker conditions
6. Aggregating metrics using the configured strategy
7. Formatting and sending the response

### Multi-Chain Client Implementation

The multi-chain client architecture enables fetching data from multiple blockchain networks in parallel using goroutines and channels for high performance.

### Circuit Breaker Implementation

The circuit breaker implements sophisticated state management with automatic recovery mechanisms, and thresholding for APY, TVL change velocity, and statistical anomalies.

### Data Integrity Implementation

The data integrity service uses ECDSA for cryptographic signatures, with support for both SHA-256 and Keccak-256 hashing, and provides on-chain verification capabilities.

## Security Considerations

### API Security

1. **Rate Limiting**: Prevents DoS attacks and ensures fair resource allocation
2. **Input Validation**: All inputs are validated to prevent injection attacks
3. **Timeouts**: Contexts with timeouts prevent resource exhaustion
4. **Error Handling**: Errors are logged but not exposed in detail to clients

### Data Integrity

1. **Cryptographic Signatures**: ECDSA signatures verify data hasn't been tampered with
2. **Hash Verification**: Double hash verification for redundancy
3. **Validity Periods**: Signatures expire after a configurable time period
4. **On-Chain Verification**: Format compatible with Ethereum smart contract verification

### Environmental Security

1. **API Keys**: Sensitive keys are loaded from environment variables, not hardcoded
2. **Least Privilege**: Docker container runs as non-root user
3. **TLS**: HTTPS recommended for all API communications
4. **No Persistent Storage**: Minimizes attack surface by not storing sensitive data

## API Reference

### Chainlink EA Endpoint

**URL**: `/`  
**Method**: POST  
**Content-Type**: application/json

**Request Body**:
```json
{
  "id": "0x123...",
  "jobRunId": "1234567890",
  "data": {
    "param1": "value1"
  },
  "meta": {
    "additional": "metadata"
  }
}
```

**Response**:
```json
{
  "jobRunId": "1234567890",
  "statusCode": 200,
  "status": "success",
  "data": {
    "result": 0.0512,
    "apy": 0.0512,
    "tvl": 1250000,
    "pointsPerETH": 1.1,
    "provider": "aggregated-weighted",
    "collectedAt": 1715003456,
    "timestamp": 1715003457,
    "meta": {
      "latencyMs": 245,
      "metricCount": 12,
      "aggregationMode": "weighted"
    }
  }
}
```

**With Data Integrity Enabled**:
```json
{
  "payload": {
    "jobRunId": "1234567890",
    "statusCode": 200,
    "status": "success",
    "data": {
      "result": 0.0512,
      "apy": 0.0512,
      "tvl": 1250000,
      "pointsPerETH": 1.1,
      "provider": "aggregated-weighted",
      "collectedAt": 1715003456,
      "timestamp": 1715003457
    }
  },
  "integrity": {
    "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "keccak256": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
    "timestamp": "2025-05-19T15:32:42Z"
  },
  "_signature": {
    "signature": "base64_encoded_signature_data",
    "publicKey": "base64_encoded_public_key",
    "algorithm": "ECDSA-P256-SHA256",
    "timestamp": 1715003457,
    "validUntil": 1715089857
  }
}
```

### Health Endpoint

**URL**: `/health`  
**Method**: GET

**Response**:
```json
{
  "status": "ok",
  "uptime": 345621,
  "version": "1.0.0"
}
```

## Performance Specifications

### Benchmarks

| Scenario | Average Latency | 95th Percentile | Max RPS |
|----------|----------------|-----------------|---------|
| Basic Request | 120ms | 250ms | 100 |
| With Validation | 145ms | 280ms | 85 |
| With Circuit Breaker | 150ms | 290ms | 80 |
| Enterprise Mode | 200ms | 350ms | 65 |
| Full Feature Set | 250ms | 400ms | 50 |

### Resource Usage

| Configuration | CPU (avg) | Memory (avg) | Connections |
|---------------|-----------|--------------|-------------|
| Minimal | 0.1 cores | 50MB | 20 |
| Standard | 0.3 cores | 100MB | 50 |
| Enterprise | 0.5 cores | 200MB | 100 |

### Scaling Recommendations

- Horizontal scaling via load balancer for increased throughput
- Vertically scale for multi-chain support with many providers
- Cache TTL tuning based on provider data update frequency
- Rate limit tuning based on expected client patterns

## Testing Methodology

### Unit Tests

Unit tests cover individual components:
- Aggregation strategies
- Validation filters
- Circuit breaker logic
- Data integrity functions

### Integration Tests

Integration tests verify:
- HTTP API functionality
- End-to-end request processing
- Provider client operation
- Recovery from failure conditions

### Load Testing

Load tests validate:
- Performance under expected load
- Behavior under extreme load
- Recovery from failure conditions
- Resource utilization patterns

## Deployment Guide

### Docker Deployment

The application includes a Dockerfile for containerized deployment:

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o server cmd/server/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/server .
EXPOSE 8080
CMD ["./server"]
```

### Kubernetes Deployment

For production environments, a Kubernetes deployment is recommended:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: restake-yield-ea
spec:
  replicas: 3
  selector:
    matchLabels:
      app: restake-yield-ea
  template:
    metadata:
      labels:
        app: restake-yield-ea
    spec:
      containers:
      - name: restake-yield-ea
        image: restake-yield-ea:latest
        ports:
        - containerPort: 8080
        env:
        - name: PORT
          value: "8080"
        - name: ENABLE_VALIDATION
          value: "true"
        - name: ENABLE_CIRCUIT_BREAKER
          value: "true"
```

### AWS Deployment

For AWS deployment, consider using:
- Elastic Container Service (ECS) for container orchestration
- Application Load Balancer for HTTP traffic distribution
- Parameter Store for secure configuration management
- CloudWatch for logging and monitoring

## Configuration Reference

### Environment Variables

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `PORT` | HTTP server port | 8080 | `8080` |
| `ENABLE_VALIDATION` | Enable statistical validation | true | `true` |
| `ENABLE_CIRCUIT_BREAKER` | Enable circuit breaker | true | `true` |
| `AGGREGATION_STRATEGY` | Aggregation strategy | weighted | `median` |
| `LOG_LEVEL` | Logging verbosity | info | `debug` |
| `TIMEOUT_MS` | Request timeout (ms) | 5000 | `10000` |
| `MAX_APY_THRESHOLD` | Max valid APY | 100.0 | `50.0` |
| `MIN_PROVIDERS` | Min valid providers | 2 | `3` |
| `ENABLE_ENTERPRISE_FEATURES` | Enable enterprise features | false | `true` |
| `DATA_INTEGRITY_ENABLED` | Enable data signatures | false | `true` |
| `MULTICHAIN_ENABLED` | Enable multi-chain support | false | `true` |
| `METRICS_EXPORT_ENABLED` | Enable metrics export | false | `true` |

### Chain Configuration

Each chain can be configured with:

```
CHAIN_ETHEREUM_ENABLED=true
CHAIN_ETHEREUM_RPC=https://mainnet.infura.io/v3/your-key
CHAIN_ETHEREUM_WEIGHT=1.0

CHAIN_ARBITRUM_ENABLED=true
CHAIN_ARBITRUM_RPC=https://arbitrum-mainnet.infura.io/v3/your-key
CHAIN_ARBITRUM_WEIGHT=0.8
```

### Provider Configuration

Each provider can be configured with:

```
PROVIDER_EIGENLAYER_ENABLED=true
PROVIDER_EIGENLAYER_API_KEY=your-api-key
PROVIDER_EIGENLAYER_WEIGHT=1.0

PROVIDER_KARAK_ENABLED=true
PROVIDER_KARAK_API_KEY=your-api-key
PROVIDER_KARAK_WEIGHT=0.8
```

### Circuit Breaker Configuration

```
CIRCUIT_BREAKER_MAX_APY=100.0
CIRCUIT_BREAKER_MAX_TVL_CHANGE_PERCENT=50.0
CIRCUIT_BREAKER_MIN_PROVIDERS=2
CIRCUIT_BREAKER_COOLDOWN_SECONDS=300
CIRCUIT_BREAKER_HEALTH_THRESHOLD=3
```
