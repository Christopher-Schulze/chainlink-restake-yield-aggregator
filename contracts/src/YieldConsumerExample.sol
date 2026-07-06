// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { RestakeYieldOracle } from "./RestakeYieldOracle.sol";
import { ReentrancyGuard } from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

/// @title YieldConsumerExample
/// @notice A minimal DeFi primitive that consumes the RestakeYieldOracle to
///         track accrued restaking yield for depositors.
///
/// @dev This contract demonstrates the consumer side of the oracle loop:
///
///   - Users deposit ETH and receive yield-bearing shares.
///   - The share value accrues based on the oracle's latest APY, applied
///     continuously between oracle updates.
///   - A circuit-breaker halts withdrawals if the oracle goes stale or
///     reports an implausible APY, protecting depositors from stale data.
///
/// @dev Security: the vault uses share-based accounting. `totalAssets`
///      tracks principal + accrued yield as a single pool. New deposits
///      buy shares at the CURRENT share price (which already reflects
///      accrued yield), so new depositors never receive retroactive yield
///      on principal that was not at risk. This prevents the flash-loan
///      yield-theft attack where an attacker deposits and withdraws in
///      the same block to steal existing depositors' accrued yield.
///
/// The yield model is simple linear accrual: between two oracle updates,
/// the effective APY is applied pro-rata by elapsed time. This is a
/// demonstration — production systems would use more sophisticated models
/// or an ERC-4626 vault implementation.
contract YieldConsumerExample is ReentrancyGuard {
    // --- errors ---

    error OracleStale();
    error OracleAPYImplausible(uint256 apyBps);
    error InsufficientShares(uint256 requested, uint256 available);
    error ZeroDeposit();
    error TransferFailed();

    // --- events ---

    event Deposited(address indexed depositor, uint256 amount, uint256 sharesMinted);
    event Withdrawn(address indexed withdrawer, uint256 shares, uint256 amount);
    event YieldAccrued(uint256 indexed totalAssets, uint256 accruedSinceLast);
    event CircuitBreakerTripped(string reason);

    // --- storage ---

    RestakeYieldOracle public immutable oracle;

    // Total assets under management (principal + accrued yield). Yield
    // accrues ONLY on this pool — new deposits add to it AFTER buying
    // shares at the current price, so they do not receive retroactive
    // yield.
    uint256 public totalAssets;

    // Total shares outstanding.
    uint256 public totalShares;

    // Shares per depositor.
    mapping(address => uint256) public sharesOf;

    // The last block timestamp at which yield was accrued.
    uint256 public lastAccrualTime;

    // The APY (in bps) used for the last accrual.
    uint256 public lastAppliedAPYBps;

    // Circuit breaker: when true, withdrawals are paused.
    bool public circuitBreakerOpen;

    // Maximum plausible APY before the circuit trips (in bps). 10000 = 100%.
    uint256 public constant MAX_PLAUSIBLE_APY_BPS = 10_000;

    // Seconds per year (365.25 days) for APY proration.
    uint256 public constant SECONDS_PER_YEAR = 31_557_600;

    // --- constructor ---

    /// @param _oracle The RestakeYieldOracle to read yield data from.
    constructor(RestakeYieldOracle _oracle) {
        if (address(_oracle) == address(0)) revert ZeroDeposit();
        oracle = _oracle;
        lastAccrualTime = block.timestamp;
    }

    // --- external: deposit ---

    /// @notice Deposits ETH and mints shares at the current share price.
    /// @dev Shares are minted AFTER accrual, so the share price already
    ///      reflects yield accrued up to this block. New depositors do
    ///      not receive retroactive yield.
    function deposit() external payable nonReentrant {
        if (msg.value == 0) revert ZeroDeposit();

        _accrueYield();

        uint256 sharesToMint = (msg.value * 1e18) / _sharePrice();
        totalAssets += msg.value;
        totalShares += sharesToMint;
        sharesOf[msg.sender] += sharesToMint;

        emit Deposited(msg.sender, msg.value, sharesToMint);
    }

    // --- external: withdraw ---

    /// @notice Burns shares and returns the corresponding ETH (principal + accrued yield).
    /// @param sharesToBurn The number of shares to redeem.
    function withdraw(uint256 sharesToBurn) external nonReentrant {
        if (sharesToBurn == 0) revert ZeroDeposit();
        if (sharesToBurn > sharesOf[msg.sender]) {
            revert InsufficientShares(sharesToBurn, sharesOf[msg.sender]);
        }
        if (circuitBreakerOpen) revert OracleStale();

        _accrueYield();

        uint256 amount = (sharesToBurn * _sharePrice()) / 1e18;

        sharesOf[msg.sender] -= sharesToBurn;
        totalShares -= sharesToBurn;
        totalAssets -= amount;

        // Emit event before external call (checks-effects-interactions).
        emit Withdrawn(msg.sender, sharesToBurn, amount);

        (bool ok,) = payable(msg.sender).call{ value: amount }("");
        if (!ok) revert TransferFailed();
    }

    // --- public: accrue yield ---

    /// @notice Accrues yield based on the oracle's latest APY since the last accrual.
    /// @dev Anyone can call this; it's also called internally on deposit/withdraw.
    function accrueYield() external {
        _accrueYield();
    }

    // --- public: read functions ---

    /// @notice Returns the total value of the vault (principal + accrued yield).
    function totalValue() external view returns (uint256) {
        return totalAssets;
    }

    /// @notice Returns the ETH value of a depositor's shares at the current price.
    function balanceOf(address depositor) external view returns (uint256) {
        // slither-disable-next-line incorrect-equality
        if (totalShares == 0) return 0;
        return (sharesOf[depositor] * _sharePrice()) / 1e18;
    }

    /// @notice Returns the share price (1 share in ETH, scaled by 1e18).
    function sharePrice() external view returns (uint256) {
        return _sharePrice();
    }

    // --- internal: yield accrual ---

    /// @dev Returns the current share price (1 share in ETH, scaled by 1e18).
    ///      Before any deposits, the price is 1e18 (1:1). After deposits,
    ///      the price grows as yield accrues: price = totalAssets * 1e18 / totalShares.
    function _sharePrice() internal view returns (uint256) {
        // slither-disable-next-line incorrect-equality
        if (totalShares == 0) return 1e18;
        return (totalAssets * 1e18) / totalShares;
    }

    /// @dev Applies the oracle's latest APY pro-rata for the time elapsed since
    ///      the last accrual. Yield accrues ONLY on the existing totalAssets,
    ///      not on future deposits. Also checks the oracle for staleness and
    ///      implausible APY, tripping the circuit breaker if either condition
    ///      is detected.
    function _accrueYield() internal {
        // Check oracle health.
        if (oracle.isStale()) {
            if (!circuitBreakerOpen) {
                circuitBreakerOpen = true;
                emit CircuitBreakerTripped("oracle stale");
            }
            // Still accrue using the last applied APY as a conservative fallback.
        } else {
            RestakeYieldOracle.YieldReport memory report = oracle.latestYield();
            lastAppliedAPYBps = report.apyBps;

            // Trip the circuit breaker if the APY is implausible.
            if (report.apyBps > MAX_PLAUSIBLE_APY_BPS) {
                if (!circuitBreakerOpen) {
                    circuitBreakerOpen = true;
                    emit CircuitBreakerTripped("implausible APY");
                }
            } else if (circuitBreakerOpen) {
                // Oracle is healthy and APY is plausible — clear the breaker.
                circuitBreakerOpen = false;
            }
        }

        uint256 elapsed = block.timestamp - lastAccrualTime;
        // slither-disable-next-line incorrect-equality
        if (elapsed == 0) return;

        // Linear accrual: totalAssets *= (1 + apy * elapsed / year)
        // apy is in bps (1e4 scale), so apyDecimal = apyBps / 1e4.
        // growthFactor = 1e18 + (apyBps * 1e14 * elapsed / SECONDS_PER_YEAR)
        uint256 growthNumerator = uint256(lastAppliedAPYBps) * 1e14 * elapsed;
        uint256 growthFactor = 1e18 + (growthNumerator / SECONDS_PER_YEAR);

        uint256 prevAssets = totalAssets;
        totalAssets = (prevAssets * growthFactor) / 1e18;

        lastAccrualTime = block.timestamp;

        uint256 accrued = totalAssets > prevAssets ? totalAssets - prevAssets : 0;
        emit YieldAccrued(totalAssets, accrued);
    }

    // --- fallback: reject direct ETH sends ---

    receive() external payable {
        revert ZeroDeposit();
    }
}
