# Gas Report

This document explains how gas usage is tracked, reported, and monitored for the Solidity contracts in this project.

## Overview

Gas optimization is critical for oracle contracts because:
1. **Every report costs gas** — the Chainlink node pays for each `submitReport` transaction
2. **High gas = higher deviation threshold needed** — if gas costs exceed the yield deviation, the update isn't worth submitting
3. **Predictable costs** — operators need to budget for gas

## Running the Gas Report

### Local

```bash
# Generate gas report to stdout
forge test --gas-report

# Save to file
forge test --gas-report > docs/gas-report.txt

# Or use the Makefile
make forge-gas
```

### CI

The `gas-report.yaml` workflow runs on every push to `main` and every PR. It:
1. Builds the contracts with Foundry
2. Runs `forge test --gas-report`
3. Uploads the report as a build artifact (30-day retention)
4. Comments the gas report on PRs for easy review

## Key Functions to Monitor

| Function | Why it matters | Target |
|----------|---------------|--------|
| `submitReport` | Called by Chainlink node on every update | < 150k gas |
| `verifyReport` | EIP-712 digest reconstruction + ecrecover | < 50k gas |
| `latestYield` | View function called by consumers | < 10k gas |
| `deposit` | User deposits ETH into yield vault | < 100k gas |
| `withdraw` | User withdraws from yield vault | < 80k gas |
| `_accrueYield` | Internal yield accrual | < 30k gas |

## Interpreting the Report

Forge's gas report shows:
- **avg**: Average gas per call across all test invocations
- **median**: Median gas (less affected by outliers)
- **max**: Maximum gas observed (worst case)
- **min**: Minimum gas observed (best case)

### What to watch for
- **Sudden spikes** in `avg` or `max` gas after a change — investigate the diff
- **High `max` vs `avg` gap** — indicates a path-dependent gas cost (e.g., loops, storage growth)
- **Storage writes** (SSTORE = 20k gas) — minimize in hot paths
- **External calls** (CALL = ~2.6k gas + callee) — batch when possible

## Optimization Techniques Used

1. **Custom errors** instead of require strings — saves ~50 gas per revert
2. **Packed structs** — `RestakeYieldOracle.Report` packs timestamp + APY into fewer slots
3. **Immutable variables** — `authorisedSigner`, `verifier` stored as immutables (no SLOAD)
4. **Single SLOAD** — `latestYield` cached in memory when used multiple times
5. **Short-circuit checks** — signer verification before expensive EIP-712 reconstruction
6. **Calldata over memory** — function parameters use calldata where possible

## Historical Gas Usage

| Date | submitReport avg | verifyReport avg | Notes |
|------|-----------------|-----------------|-------|
| 2025-07-06 | ~120k | ~35k | Initial audit baseline |

> To update this table, run `make forge-gas` after changes and record the avg values.
