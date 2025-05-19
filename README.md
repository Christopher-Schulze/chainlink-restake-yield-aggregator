# Restake Yield Aggregator

A Chainlink External Adapter for aggregating and validating ETH restaking yield data across multiple providers and blockchains.

![Build Status](https://img.shields.io/badge/build-passing-brightgreen)
![Go Version](https://img.shields.io/badge/go-1.22+-blue)
![License](https://img.shields.io/badge/license-MIT-green)

## Overview

The Restake Yield Aggregator solves the challenge of obtaining reliable yield data in DeFi by combining multiple data sources, applying statistical validation, and implementing protective measures to ensure data integrity even in extreme market conditions.

Built as a Chainlink External Adapter, it seamlessly integrates with Chainlink nodes to provide validated yield metrics for on-chain consumption.

## Key Features

- **Multi-Provider Aggregation**: Collects data from multiple sources with configurable weighting for increased reliability
- **Statistical Validation**: Identifies and filters outliers using configurable statistical methods
- **Circuit Breaker Pattern**: Protects against extreme market conditions with automatic fallback mechanisms
- **Enterprise Security**: ECDSA data signing for cryptographic verification and tamper-proof delivery
- **Multi-Chain Support**: Collects data across 7+ blockchain networks in parallel
- **Extensible Architecture**: Modular design for easy addition of new providers and chains
- **Performance Optimized**: Concurrent data collection with intelligent caching for maximum throughput

## Quick Start

```bash
# Clone the repository
git clone https://github.com/yourusername/restake-yield-ea.git
cd restake-yield-ea

# Build the application
go build -o bin/server cmd/server/main.go

# Run with minimal configuration
PORT=8080 ./bin/server

# Or with Docker
docker build -t restake-yield-ea .
docker run -p 8080:8080 restake-yield-ea
```

## Configuration

The adapter can be configured using environment variables:

```bash
# Basic configuration
PORT=8080                         # HTTP server port
ENABLE_VALIDATION=true            # Enable statistical validation
ENABLE_CIRCUIT_BREAKER=true       # Enable circuit breaker protection
AGGREGATION_STRATEGY=weighted     # weighted, median, trimmed, consensus

# Enterprise features
ENABLE_ENTERPRISE_FEATURES=false  # Enable enterprise features
DATA_INTEGRITY_ENABLED=false      # Enable cryptographic data signing
MULTICHAIN_ENABLED=false          # Enable multi-chain support

# Advanced configuration
TIMEOUT_MS=5000                   # Request timeout in milliseconds
MAX_APY_THRESHOLD=100.0           # Maximum valid APY (0-100)
MIN_PROVIDERS=2                   # Minimum number of valid providers
```

See the [DOCUMENTATION.md](./DOCUMENTATION.md) for comprehensive configuration options.

## API Reference

### Chainlink External Adapter Endpoint

**URL**: `/`  
**Method**: POST  
**Content-Type**: application/json

**Request Body**:
```json
{
  "id": "0x123...",
  "jobRunId": "1234567890"
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
    "timestamp": 1715003457
  }
}
```

## Architecture

```
┌──────────────────────────────────────────────────┐
│                 HTTP API Layer                   │
├──────────────────────────────────────────────────┤
│               Business Logic Layer               │
├───────────────┬───────────────┬─────────────────┤
│ Data          │ Validation &  │ Security        │
│ Collection    │ Aggregation   │ Services        │
└───────────────┴───────────────┴─────────────────┘
```

The system is designed with modularity and performance in mind:

1. **Request Processing**: Incoming requests are validated and rate limited
2. **Data Collection**: Multiple providers are queried in parallel
3. **Validation**: Statistical methods filter out invalid or outlier data
4. **Circuit Breaker**: Protects the system during extreme conditions
5. **Aggregation**: Different strategies combine valid metrics into a single result
6. **Security Processing**: Cryptographic signatures ensure data integrity
7. **Response**: Formatted to Chainlink EA specifications for node consumption

## Development

### Prerequisites

- Go 1.22+
- Docker (optional for containerized development)

### Building

```bash
# Build the application
go build -o bin/server cmd/server/main.go

# Run tests
go test -v ./...
```

### Testing

The project includes comprehensive test suites:

```bash
# Run all tests
go test ./...

# Run specific tests
go test ./internal/validation/...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Documentation

For complete technical details, see [DOCUMENTATION.md](./DOCUMENTATION.md).

## License

MIT License
