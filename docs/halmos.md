# Halmos Formal Verification

This document describes the formal verification setup for the vault's core
security invariants using [Halmos](https://github.com/a16z/halmos) symbolic
execution.

## Overview

While Foundry's invariant tests use random fuzzing to explore the state space,
Halmos uses **symbolic execution** to prove properties hold for **all possible
input values**. This is a stronger guarantee: instead of "no counterexample
found in N random runs", Halmos provides "mathematically proven for all inputs"
(assuming the SMT solver doesn't time out).

## Installation

```bash
pip3 install halmos
```

Requires Python 3.10+ and an SMT solver (Z3 is bundled).

## Running

```bash
# Run all Halmos checks (from repo root)
halmos --function check_ --solver-timeout-assertion 60

# Run a specific check
halmos --function check_deposit_never_insolvent

# With verbose output
halmos --function check_ --solver-timeout-assertion 60 -v
```

## Verified Properties

The test file `contracts/test/HalmosVault.t.sol` contains these `check_`
functions:

| Check | Property | Status |
|-------|----------|--------|
| `check_deposit_never_insolvent` | After any deposit, vault balance >= totalAssets | Verified |
| `check_deposit_share_price_never_below_initial` | Share price >= 1e18 after deposit | Verified |
| `check_first_deposit_shares_equal_amount` | First deposit mints shares == amount | Verified |
| `check_deposit_withdraw_same_block` | Same-block deposit+withdraw returns exact amount | Verified |
| `check_withdraw_excess_shares_reverts` | Withdrawing more shares than owned always reverts | Verified |
| `check_total_shares_ge_individual` | totalShares >= any individual's shares | Verified |
| `check_no_insolvency_after_ops` | No insolvency from deposit+withdraw sequences | Verified |

## How It Works

1. **Symbolic parameters**: Each `check_` function takes `uint96`/`uint256`
   parameters that Halmos treats as symbolic (unbounded) variables.

2. **Path exploration**: Halmos explores all execution paths through the
   function, including branches in `_accrueYield`, `_sharePrice`, etc.

3. **SMT solving**: For each path, Halmos encodes the assertion as an SMT
   formula and asks Z3: "is there any input that reaches this assert(false)?"

4. **Result**: If Z3 says "unsat" (no counterexample), the property is
   **verified**. If Z3 finds a counterexample, it's reported as a failing
   test with the specific input values.

## Limitations

- **Solver timeout**: Complex properties may time out. Increase
  `--solver-timeout-assertion` if needed.
- **Loop unrolling**: Halmos unrolls loops a bounded number of times. The
  vault's loops are bounded by the depositor count (fixed in tests).
- **External calls**: Halmos models external calls to unknown contracts
  conservatively. Our tests only call known contracts (vault, oracle).
- **No time travel**: Halmos doesn't symbolically advance `block.timestamp`.
  The same-block tests verify the zero-elapsed-time case; invariant tests
  cover the time-elapsed case via fuzzing.

## CI Integration

To add Halmos to CI, add this job to `.github/workflows/go.yaml`:

```yaml
  halmos:
    name: Formal verification (Halmos)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          submodules: recursive
      - name: Install Foundry
        uses: foundry-rs/foundry-toolchain@v1
        with:
          version: nightly
      - name: Setup Python
        uses: actions/setup-python@v5
        with:
          python-version: "3.12"
      - name: Install Halmos
        run: pip3 install halmos
      - name: Generate signature fixture
        run: go run contracts/script/genfixture.go
      - name: Run Halmos
        run: halmos --function check_ --solver-timeout-assertion 60
```

## Relationship to Invariant Tests

| Aspect | Foundry Invariants | Halmos |
|--------|-------------------|--------|
| Method | Random fuzzing | Symbolic execution |
| Coverage | Statistical (N random runs) | Exhaustive (all paths) |
| Guarantee | "No counterexample found" | "Proven for all inputs" |
| Speed | Fast (seconds) | Slower (minutes) |
| False positives | None | Possible (solver timeout) |

Both methods are complementary: invariant tests catch bugs quickly during
development, while Halmos provides mathematical proof for the final audit.
