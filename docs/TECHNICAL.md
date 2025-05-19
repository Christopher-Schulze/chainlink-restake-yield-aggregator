# Restake Yield Aggregator - Technical Documentation

## Table of Contents

1. [Introduction](#introduction)
2. [Architecture Overview](#architecture-overview)
3. [Core Components](#core-components)
4. [Enterprise Features](#enterprise-features)
5. [Data Flow](#data-flow)
6. [Technical Implementation Details](#technical-implementation-details)
7. [Security Considerations](#security-considerations)
8. [API Reference](#api-reference)
9. [Performance Specifications](#performance-specifications)
10. [Testing Methodology](#testing-methodology)

## Introduction

The Restake Yield Aggregator is an enterprise-grade External Adapter (EA) for Chainlink nodes, designed to provide reliable, statistically validated yield metrics from multiple sources. This document provides an in-depth technical explanation of its implementation, architecture, and features.

### Purpose

This EA addresses the challenge of obtaining reliable yield data in DeFi by:

1. Fetching metrics from multiple providers
2. Applying statistical validation to identify and filter outliers
3. Aggregating data using multiple strategies
4. Implementing protective measures like circuit breakers
5. Supporting multi-chain data collection and integration
6. Adding cryptographic verification for data integrity

### Technology Stack

- **Language**: Go 1.22+
- **HTTP Framework**: Standard library net/http
- **Metrics**: Prometheus metrics collection
- **Cryptography**: ECDSA with P-256 for data signatures
- **Concurrency**: Go's goroutines and channels for parallel processing
- **Configuration**: Environment variables with intelligent defaults

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

### Key Design Principles

1. **Modularity**: Each component is independent and can be replaced or modified without affecting others
2. **Concurrency**: Parallel execution where possible for maximum performance
3. **Defensive Programming**: Comprehensive error handling and fallbacks
4. **Statelessness**: Minimized state for horizontal scaling capability
5. **Observability**: Extensive logging and metrics collection

## Core Components

### Server Component

**Location**: `cmd/server/main.go`

The server component is the entry point that:
- Initializes all services and components
- Sets up HTTP endpoints
- Configures middleware
- Establishes health checks
- Manages graceful shutdown

The `Server` struct encapsulates all dependencies:

```go
type Server struct {
    config           ServerConfig
    providers        []Provider
    server           *http.Server
    breaker          *circuitbreaker.CircuitBreaker
    metrics          *serverMetrics
    validationOpts   validation.ValidationOptions
    multiChainClient *fetch.MultiChainClient
    metricsExporter  *enterprise.MetricsExporter
    dataIntegrity    *security.DataIntegrityService
    rateLimit        *rate.Limiter
    enableEnterprise bool
}
```

### Data Model

**Location**: `internal/model/model.go`

The unified data model for yield metrics:

```go
type Metric struct {
    Provider     string  `json:"provider"`
    APY          float64 `json:"apy"`
    TVL          float64 `json:"tvl"`
    PointsPerETH float64 `json:"points_per_eth"`
    CollectedAt  int64   `json:"collected_at"`
    Confidence   float64 `json:"confidence,omitempty"`
    RiskScore    float64 `json:"risk_score,omitempty"`
    Protocol     string  `json:"protocol,omitempty"`
    Chain        string  `json:"chain,omitempty"`
    Weight       float64 `json:"weight,omitempty"`
    Error        string  `json:"error,omitempty"`
    Version      string  `json:"version,omitempty"`
}
```

### Provider Interface

**Location**: `cmd/server/main.go`

Each data provider must implement:

```go
type Provider interface {
    Fetch(ctx context.Context) ([]model.Metric, error)
}
```

### Circuit Breaker Pattern

**Location**: `internal/circuitbreaker/breaker.go`

The circuit breaker follows the standard pattern with three states:
- **Closed**: Normal operation
- **Open**: Service unavailable, return fallback
- **Half-Open**: Testing if conditions normalized

Its implementation includes:
- Configurable thresholds
- Automatic state transitions
- Callback interface for monitoring
- Cache of last valid metrics

### Validation Pipeline

**Location**: `internal/validation/filters.go`

The validation system provides several filtering mechanisms:
1. Basic validation (non-negative values, recency)
2. Statistical outlier detection (IQR-based)
3. Confidence scoring
4. Provider reputation-based filtering

### Aggregation Strategies

**Location**: `internal/aggregate/`

Four distinct aggregation strategies:
1. **Weighted**: TVL-weighted average (default)
2. **Median**: Median value for resistance to outliers
3. **Trimmed**: Trimmed mean, removing extreme values
4. **Consensus**: Confidence-scored weighted average

## Enterprise Features

### Multi-Chain Support

**Location**: `internal/fetch/multichain.go`, `internal/types/chain_types.go`

The multi-chain system:
- Supports 7+ blockchain networks
- Provides unified interface for chain-specific clients
- Implements parallel data collection
- Applies chain-specific weighting for aggregation
- Includes caching for performance optimization

Supported chains:
```go
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

### Data Integrity Service

**Location**: `internal/security/data_integrity.go`

Cryptographic data integrity implements:
- ECDSA signatures using P-256 curve
- Double hash verification (SHA-256 and Keccak-256)
- Timestamped validity periods
- Blockchain-compatible verification formats
- Tamper-evident wrappers

### Enterprise Metrics Export

**Location**: `internal/enterprise/metrics_export.go`

The export system enables:
- Batched metrics collection
- Multiple export destinations
- Scheduled background exports
- Error handling and retry logic
- Monitoring of export operations
