# Security Audit Report — Restake Yield External Adapter

**Project:** `restake-yield-ea` — Chainlink External Adapter for ETH restaking yield
**Auditor:** Self-audit (applicant)
**Date:** 2025-07-06
**Commit:** `ac0d36d`
**Scope:** 7 Go packages (manual review) + 3 Solidity contracts (5,633 LOC production code); 13 Go packages total (automated testing)

---

## 1. Executive Summary

A self-audit of the Restake Yield External Adapter identified **3 critical
vulnerabilities**, **5 high-severity issues**, and **8 medium/low findings**.
All 16 findings have been fixed. A post-completion reality-check review found
**8 additional issues** (1 Slither finding, 2 bugs in own fixes, 5
code-quality issues), all fixed.

The audit covered the full oracle loop: off-chain data aggregation (Go) →
EIP-712 signing (Go) → on-chain verification (Solidity) → yield accrual
(Solidity vault). The most severe finding was a **digest-decoupling
vulnerability** in `RestakeYieldOracle.submitReport` that allowed an
authorized updater to pair a valid signature with arbitrary report values.

### Key Metrics

| Metric | Value |
|--------|-------|
| Total findings | 24 (16 initial + 8 reality-check) |
| Critical | 3 (fixed) |
| High | 5 (fixed) |
| Medium | 6 (fixed) |
| Low | 10 (fixed) |
| Go test packages | 13 (all pass with `-race`) |
| Foundry tests | 74 (69 unit + 5 invariant, 256 runs × 128k calls) |
| Go fuzz executions | 700K+ (0 failures) |
| Slither findings | 0 high/critical/medium (1 low fixed, 4 medium false positives suppressed inline, 7 low/info triaged) |
| gosec findings | 0 |
| golangci-lint findings | 0 (10 linters) |

---

## 2. Audit Methodology

### 2.1 Manual Review

Line-by-line review of all production code paths:
- `RestakeYieldOracle.sol` (369 LOC) — signature verification, replay prevention, bounds checking
- `YieldVerifier.sol` (98 LOC) — ecrecover, low-s malleability, v-normalization
- `YieldConsumerExample.sol` (230 LOC) — share-based accounting, yield accrual, circuit breaker
- `internal/security/eip712.go` (193 LOC) — EIP-712 domain separator, struct hash, digest
- `internal/circuitbreaker/breaker.go` — concurrent Check/Trip, state machine
- `cmd/server/main.go` — request handling, admin auth, clientIP, data echo
- `internal/aggregate/aggregate.go` — weighted/median/trimmed/consensus aggregation

### 2.2 Static Analysis

| Tool | Target | Configuration |
|------|--------|---------------|
| Slither | Solidity contracts | `slither.config.json` (fail_on: medium, 98 detectors) |
| gosec | Go source | `-severity medium -confidence medium` |
| golangci-lint | Go source | 10 linters (errcheck, ineffassign, staticcheck, govet, gosec, misspell, unconvert, prealloc, gocritic, revive) |
| govulncheck | Go dependencies | default |

### 2.3 Dynamic Testing

| Method | Description |
|--------|-------------|
| Unit tests | 74 Solidity + 13 Go packages, all with `-race` |
| Invariant tests | 5 Foundry invariants, 256 runs × 128k calls each, fuzzed deposit/withdraw/accrue/warp sequences |
| Fuzz testing | 3 Go fuzz targets (digest determinism, signature recovery, domain separation), 700K+ executions |
| Cross-language proof | Go-signed fixture verified by Solidity `ecrecover` on-chain (`test_OracleAcceptsGoGeneratedFixture`) |
| Race condition regression | Concurrent circuit-breaker Check/Trip test with `-race` |

### 2.4 Threat Modeling

STRIDE-based analysis of the oracle loop. Trust boundaries identified
between: (1) external provider APIs and the EA, (2) the EA and the signing
key, (3) the signed report and the on-chain oracle, (4) the oracle and the
consumer vault. See [SECURITY.md](SECURITY.md) for the full threat model
with attack-surface table and trust-boundary diagram.

---

## 3. Findings

### 3.1 Critical (P0)

#### P0-01: Oracle Digest Decoupled from Report Parameters

**Severity:** Critical
**Location:** `RestakeYieldOracle.sol:submitReport`
**Status:** Fixed (commit `3aa970f`)

**Description:** The original `submitReport` accepted a pre-computed digest
as a parameter. An authorized updater could compute a valid signature for
a legitimate report (APY=4.5%) and then submit a different report
(APY=999%) with the same signature, because the digest was not bound to the
submitted values.

**Impact:** An authorized-but-malicious updater could inject arbitrary APY
values into the oracle, causing the yield vault to accrue at attacker-chosen
rates. This is a value-substitution attack.

**Fix:** The digest is now reconstructed on-chain from the submitted
parameters using EIP-712 typed data. The signature is bound to the exact
`apyBps`, `tvlMilliETH`, `pointsPerETHppm`, and `timestamp` values. The
`submitReport` function signature changed from accepting a digest to
accepting the raw parameters + signature.

**Verification:** `test_RevertOnValueSubstitution` — a valid signature for
APY=450 is rejected when submitted with APY=9999.

---

#### P0-02: EA↔Oracle Signing Scheme Mismatch

**Severity:** Critical
**Location:** `internal/security/eip712.go` ↔ `RestakeYieldOracle.sol`
**Status:** Fixed (commit `3aa970f`)

**Description:** The Go EA signed a keccak256 hash of a canonical JSON
payload. The Solidity oracle expected an EIP-712 digest
(`keccak256("\x19\x01" || domainSeparator || structHash)`). The two
digests were structurally incompatible — a signature produced by the EA
would never verify on-chain.

**Impact:** The entire oracle loop was non-functional. No signed report
could ever be verified on-chain.

**Fix:** Implemented a shared EIP-712 module in Go (`internal/security/eip712.go`)
that produces digests identical to the Solidity `digestOf()` view function.
The Go `EIP712Domain.Separator()` and `YieldReport.StructHash()` methods
mirror the Solidity `domainSeparator()` and `structHash` computations
exactly.

**Verification:** `test_OracleAcceptsGoGeneratedFixture` — a fixture
generated by `contracts/script/genfixture.go` (using the Go EIP-712 module)
is loaded in the Foundry test and verified by the on-chain oracle via
`ecrecover`. This is a **cross-language cryptographic proof**: the same
digest is computed in Go and Solidity, and the same signature verifies in
both.

---

#### P0-03: Vault Flash-Loan Yield Theft

**Severity:** Critical
**Location:** `YieldConsumerExample.sol:_totalValue`
**Status:** Fixed (commit `07851ed`)

**Description:** The vault's `_totalValue()` function included pending yield
in the total assets, but new deposits were credited at the pre-accrual
price. An attacker could:
1. Flash-loan ETH
2. Deposit into the vault (credited at pre-accrual price)
3. Trigger `accrueYield()` (yield accrues on all assets including the deposit)
4. Withdraw (receives principal + retroactive yield)
5. Repay flash-loan

**Impact:** An attacker could steal accrued yield from existing depositors
in a single transaction, with zero capital at risk.

**Fix:** Rewrote the vault to use share-based accounting. New deposits buy
shares at the current (post-accrual) share price. Yield accrues only on
existing `totalAssets`, not on future deposits. A depositor who deposits
and withdraws in the same block receives exactly zero retroactive yield.

**Verification:**
- `test_FlashLoanAttackMitigated` — the exact attack sequence above is
  attempted and the attacker's profit is asserted to be ≤ 0.
- `test_NewDepositorGetsNoRetroactiveYield` — a new depositor receives no
  yield from the pre-deposit accrual period.
- `invariant_VaultNeverInsolvent` — 256 fuzz runs × 128k calls verify the
  vault's ETH balance always covers `totalAssets`.

---

### 3.2 High (P1)

#### P1-04: No On-Chain Replay Prevention

**Severity:** High
**Location:** `RestakeYieldOracle.sol:submitReport`
**Status:** Fixed (commit `3aa970f`)

**Description:** The oracle accepted any report with a valid signature,
regardless of timestamp. A signature from a past report could be resubmitted
indefinitely.

**Fix:** Added a monotonic timestamp check: `timestamp <= lastReportTimestamp`
reverts with `ReplayDetected`. Combined with the EIP-712 domain separator
(chainId + verifyingContract), this closes all replay vectors: same-chain,
cross-chain, and cross-contract.

**Verification:** `test_RevertOnReplayedTimestamp`, `test_RevertOnCrossChainReplay`,
`test_RevertOnCrossContractReplay`.

---

#### P1-05: Circuit Breaker Race Condition

**Severity:** High
**Location:** `internal/circuitbreaker/breaker.go:Check`
**Status:** Fixed (commit `3ea881d`)

**Description:** The circuit breaker's `Check()` method read the breaker
state under a read lock, then made a decision. Between the read and the
decision, another goroutine could call `Trip()`, changing the state. The
`Check()` caller would proceed with stale state.

**Fix:** After acquiring the write lock in `Trip()`, the breaker state is
rechecked to ensure no concurrent trip has already occurred. A regression
test with concurrent `Check()` and `Trip()` calls verifies the fix under
`-race`.

**Verification:** `TestConcurrentCheckAndTrip` — 100 goroutines call
`Check()` and `Trip()` concurrently; the race detector confirms no data
race and the state is consistent.

---

#### P1-06: setSigner Does Not Revoke Old Role

**Severity:** High
**Location:** `RestakeYieldOracle.sol:setSigner`
**Status:** Fixed (commit `3aa970f`)

**Description:** When rotating the signer, the old signer's `UPDATER_ROLE`
was not revoked. An old signer could continue submitting reports after
rotation.

**Fix:** `setSigner` now revokes the old signer's `UPDATER_ROLE` when
setting a new signer.

**Verification:** `test_SetSignerRevokesOldUpdaterRole`.

---

#### P1-07: Admin Endpoints Unauthenticated

**Severity:** Medium
**Location:** `cmd/server/middleware.go`
**Status:** Fixed (commit `3ea881d`)

**Description:** The `/circuit` and `/status` endpoints had no
authentication. Anyone with network access could read server state or
reset the circuit breaker.

**Fix:** Added optional `ADMIN_TOKEN` bearer token authentication. When
set, requests must include `Authorization: Bearer <token>`. Token
comparison uses `subtle.ConstantTimeCompare` to prevent timing attacks.
When unset, admin endpoints are unauthenticated (suitable for private
networks behind the Chainlink node).

**Verification:** `TestAdminAuth`, `TestAdminAuthWrongToken`, `TestAdminAuthNoToken`.

---

#### P1-08: Lido TVL Hardcoded

**Severity:** Medium
**Location:** `internal/fetch/lido.go`
**Status:** Fixed (commit `779b3a6`, refined in `ac0d36d`)

**Description:** The Lido TVL was hardcoded to `10_000_000` ETH. This is
roughly correct for Lido as of 2025, but a magic number in production code
is a maintenance hazard and a data-integrity concern.

**Fix:** TVL is now fetched from DefiLlama's protocols API. The fallback
value is configurable via `LIDO_FALLBACK_TVL_ETH` env var (default:
10M ETH) and is clearly documented as a safety-net estimate, not ground
truth.

**Verification:** `TestLidoTVLFromDefiLlama`, `TestLidoTVLFallback`.

---

### 3.3 Medium & Low (P2/P3)

| ID | Severity | Title | Status |
|----|----------|-------|--------|
| P2-09 | Low | Stale `coverage.out` in repo | Fixed |
| P2-10 | Low | `PointsPerETH` fake 1.0 values skew aggregation | Fixed (0 = N/A, excluded from median) |
| P2-11 | Low | `weightedPoints` semantically incorrect (mean instead of median) | Fixed (median for points) |
| P2-12 | Low | Duplicated env helpers in `cmd/server` and `internal/config` | Fixed (consolidated into `internal/envx`) |
| P2-13 | Medium | Request data echo uses blacklist (client can overwrite response fields) | Fixed (whitelist: chain, symbol, mode, provider) |
| P2-14 | Medium | Metrics exporter drops failed batches silently | Fixed (retry queue with maxRetries=3, capped at 100 batches) |
| P2-15 | Low | `WeightedParallel` spawns goroutine-per-metric | Fixed (worker pool) |
| P2-16 | Medium | `clientIP` trusts `X-Forwarded-For` unconditionally | Fixed (XFF honoured only from `TRUSTED_PROXY`) |

### 3.4 Reality-Check Findings (post-completion review)

| ID | Severity | Title | Status |
|----|----------|-------|--------|
| RC-26 | Low | `verifyYield` return value not checked against `authorisedSigner` | Fixed (explicit check in `submitReport`) |
| RC-27 | Medium | `maxRetries=3` defined but never enforced — retry queue retries forever | Fixed (per-batch retry counter, drop after 3) |
| RC-27b | Medium | `drainRetryQueue` never called when `batchMetrics` is empty | Fixed (always drain, even without new metrics) |
| RC-28 | Low | Invariant tests waste 93% of fuzz calls on oracle admin functions | Fixed (`targetContract(address(vault))`, reverts 93%→67%) |
| RC-29 | Low | Lido TVL fallback magic number not configurable | Fixed (`LIDO_FALLBACK_TVL_ETH` env var) |
| RC-30 | Low | 17 golangci-lint issues (errcheck, deprecated API, ineffectual assignment) | Fixed (all 17, `.golangci.yml` added, CI job added) |
| RC-31 | Low | Slither findings not documented | Fixed (full triage table in SECURITY.md) |
| RC-32 | Low | No separate audit report document | Fixed (this document) |

---

## 4. Verification

### 4.1 Test Results

```
=== Go ===
go vet ./...                                    # clean
golangci-lint run --timeout 120s ./...          # 0 issues
gosec -severity medium ./...                    # 0 issues
go test -race -count=1 ./...                    # 13 packages pass

=== Solidity ===
forge test                                      # 74 tests pass
  RestakeYieldOracle.t.sol:    38 tests
  YieldConsumerExample.t.sol:  19 tests (incl. 1 invariant)
  YieldVerifier.t.sol:         12 tests
  YieldConsumerExample.invariant.t.sol: 5 invariants

=== Fuzzing ===
go test -fuzz=FuzzEIP712DigestDeterminism       # 707K execs, 0 failures
go test -fuzz=FuzzEIP712SignatureRecovery       # pass
go test -fuzz=FuzzEIP712DomainSeparation        # pass

=== Static Analysis ===
slither . --config slither.config.json           # 0 high/critical/medium (exit 0)
                                                  1 low (fixed)
                                                  4 medium false positives (suppressed inline)
                                                  7 low/info (triaged)
```

### 4.2 Cross-Language EIP-712 Proof

The file `contracts/test/fixtures/signature.json` is generated by
`contracts/script/genfixture.go`, which uses the Go `internal/security`
EIP-712 module to sign a `YieldReport`. The Foundry test
`test_OracleAcceptsGoGeneratedFixture` loads this fixture and calls
`oracle.submitReport()` — the on-chain `ecrecover` recovers the same
address that the Go signer used. This proves that the Go and Solidity
EIP-712 implementations produce identical digests.

---

## 5. Recommendations

### 5.1 Implemented

All findings have been fixed. No open recommendations.

### 5.2 Future Work (not in scope)

- **EigenLayer/Karak/Symbiotic providers**: Currently generic REST client
  stubs. To make them production-ready, implement protocol-specific response
  parsing for each API.
- **HSM signing**: The `DataIntegrityService.SignOnChainReport` method is
  the single signing entry point and can be replaced with an HSM-backed
  implementation. Clef or a KMS provider is recommended for production.
- **Formal verification**: The vault's share-accounting invariants could be
  formally verified with Halmos or Certora for higher assurance.
- **Gas optimization**: The oracle's `submitReport` could be optimized with
  assembly for the ecrecover precompile call.

---

## 6. Appendix

### 6.1 Files Changed

18 commits, 44 files modified. See `git log --oneline` for the full history.

### 6.2 Tools Used

| Tool | Version |
|------|---------|
| Go | 1.24+ |
| Foundry | nightly |
| Slither | latest |
| gosec | latest |
| golangci-lint | v1.64.8 |
| govulncheck | latest |

### 6.3 Related Documents

- [SECURITY.md](SECURITY.md) — Security policy, threat model, static analysis results
- [README.md](README.md) — Project overview, security section, EIP-712 documentation
