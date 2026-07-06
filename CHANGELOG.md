# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2025-07-06

### Security
- Fixed oracle digest decoupling vulnerability in `RestakeYieldOracle.submitReport` — digest now reconstructed on-chain via EIP-712 from report parameters
- Fixed EA↔Oracle signing scheme mismatch — Go EA now uses EIP-712 typed data compatible with Solidity `ecrecover`
- Fixed vault flash-loan yield theft — share-based accounting prevents same-block deposit+withdraw yield extraction
- Added on-chain replay prevention via monotonic timestamp check in `submitReport`
- Fixed circuit breaker race condition — state recheck after write lock acquisition
- Fixed `setSigner` role hygiene — old signer's `UPDATER_ROLE` now revoked on rotation
- Added admin endpoint authentication (`ADMIN_TOKEN` bearer token, constant-time compare)
- Added request data echo whitelist (chain, symbol, mode, provider only)
- Fixed `clientIP` trusting `X-Forwarded-For` unconditionally — now honours XFF only from `TRUSTED_PROXY`
- Fixed `verifyYield` return value not checked against `authorisedSigner` — explicit comparison added

### Added
- EIP-712 typed-data signing module (`internal/security/eip712.go`)
- Cross-language EIP-712 proof — Go-signed fixture verified by Solidity `ecrecover` on-chain
- Foundry invariant tests (5 invariants, 256 runs × 128k calls)
- Go fuzzing tests (3 fuzz targets, 700K+ executions)
- `SECURITY.md` with threat model, attack-surface table, trust-boundary diagram
- `AUDIT_REPORT.md` — professional audit deliverable with methodology and finding details
- Pre-commit hook with security checks (`scripts/pre-commit.sh`)
- `internal/envx` package — typed env-var helpers
- Metrics exporter retry queue with exponential backoff and maxRetries=3
- `golangci-lint` CI job with 10 linters
- `LIDO_FALLBACK_TVL_ETH` env var for configurable TVL fallback
- Security badges in README

### Changed
- Lido TVL now fetched from DefiLlama instead of hardcoded value
- `PointsPerETH` — 0 now means "not available" (excluded from median calculation)
- `weightedPoints` uses median instead of mean for points calculation
- `WeightedParallel` uses worker pool instead of goroutine-per-metric
- Invariant tests restricted to `targetContract(address(vault))` — 93% → 67% revert rate
- Stub providers reframed as "extension points" in README

### Fixed
- Metrics exporter retry queue: `maxRetries` now actually enforced (batches dropped after 3 failed attempts)
- Metrics exporter: `drainRetryQueue` now called even when `batchMetrics` is empty
- 17 golangci-lint issues (errcheck, deprecated API, ineffectual assignment, redefined builtin)
- Stale `coverage.out` removed from working tree
- All stale documentation numbers corrected (55→74 tests, 12→13 packages, 100%→≥98% coverage)

### Removed
- Stale `coverage.out` file
- Duplicated env helpers (consolidated into `internal/envx`)
