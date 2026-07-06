// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test } from "forge-std/Test.sol";
import { YieldVerifier } from "../src/YieldVerifier.sol";
import { RestakeYieldOracle } from "../src/RestakeYieldOracle.sol";
import { YieldConsumerExample } from "../src/YieldConsumerExample.sol";

/// @title YieldConsumerExampleTest
/// @notice Tests the consumer-side DeFi primitive: deposit, withdraw, yield
///         accrual, and circuit breaker behaviour.
contract YieldConsumerExampleTest is Test {
    YieldVerifier internal verifier;
    RestakeYieldOracle internal oracle;
    YieldConsumerExample internal vault;

    uint256 constant SIGNER_PK = 1;
    address constant SIGNER = 0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf;

    address alice = address(0xA11CE);
    address bob = address(0xB0B);

    function setUp() public {
        // Set a realistic timestamp so arithmetic doesn't underflow.
        vm.warp(1_700_000_000);
        verifier = new YieldVerifier();
        oracle = new RestakeYieldOracle(verifier, SIGNER);
        // The test contract needs updater rights to submit reports.
        oracle.setUpdater(address(this), true);
        vault = new YieldConsumerExample(oracle);

        // Give test accounts some ETH.
        vm.deal(alice, 100 ether);
        vm.deal(bob, 100 ether);
    }

    // --- helpers ---

    /// @dev Signs the EIP-712 digest for a report with the test key.
    function _signReport(uint96 apyBps, uint96 tvlMilliETH, uint64 ppm, uint32 ts)
        internal
        view
        returns (bytes memory)
    {
        bytes32 digest = oracle.digestOf(apyBps, tvlMilliETH, ppm, ts);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(SIGNER_PK, digest);
        return abi.encodePacked(r, s, uint8(v - 27));
    }

    /// @dev Submits a yield report to the oracle with the given APY (in bps).
    function _submitYield(uint96 apyBps) internal {
        uint32 ts = uint32(block.timestamp);
        bytes memory sig = _signReport(apyBps, 1_000_000, 1_100_000, ts);
        oracle.submitReport(sig, apyBps, 1_000_000, 1_100_000, ts);
    }

    // --- tests: deposit ---

    function test_DepositMintsShares() public {
        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        assertEq(vault.totalShares(), 1 ether, "first deposit: 1 share = 1 wei");
        assertEq(vault.sharesOf(alice), 1 ether);
        assertEq(vault.totalAssets(), 1 ether);
    }

    function test_RevertOnZeroDeposit() public {
        vm.prank(alice);
        vm.expectRevert(YieldConsumerExample.ZeroDeposit.selector);
        vault.deposit{ value: 0 }();
    }

    function test_RevertOnDirectEthSend() public {
        vm.prank(alice);
        (bool ok,) = address(vault).call{ value: 1 ether }("");
        assertFalse(ok, "receive() should revert");
    }

    // --- tests: withdraw ---

    function test_WithdrawReturnsPrincipal() public {
        // Submit a yield report so the oracle is not stale.
        _submitYield(450);

        vm.startPrank(alice);
        vault.deposit{ value: 1 ether }();

        uint256 balBefore = alice.balance;
        vault.withdraw(1 ether);
        uint256 balAfter = alice.balance;

        // Should get back ~1 ether (minimal yield accrued in ~0 time).
        assertApproxEqAbs(balAfter - balBefore, 1 ether, 1e15, "withdrawn amount ~= deposit");
        assertEq(vault.sharesOf(alice), 0);
        assertEq(vault.totalShares(), 0);
        vm.stopPrank();
    }

    function test_RevertWhenInsufficientShares() public {
        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        vm.prank(alice);
        vm.expectRevert(
            abi.encodeWithSelector(
                YieldConsumerExample.InsufficientShares.selector, 2 ether, 1 ether
            )
        );
        vault.withdraw(2 ether);
    }

    function test_RevertWhenZeroWithdraw() public {
        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        vm.prank(alice);
        vm.expectRevert(YieldConsumerExample.ZeroDeposit.selector);
        vault.withdraw(0);
    }

    // --- tests: yield accrual ---

    function test_YieldAccruesOverTime() public {
        // Submit a 10% APY report.
        _submitYield(1000); // 10% = 1000 bps

        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        uint256 valueBefore = vault.totalValue();

        // Advance 1/10 of a year (~36.5 days) → ~1% yield.
        vm.warp(block.timestamp + 3_155_760); // ~1/10 year
        vault.accrueYield();

        uint256 valueAfter = vault.totalValue();
        assertGt(valueAfter, valueBefore, "value must increase after accrual");

        // 10% APY for 1/10 year ≈ 1% gain.
        uint256 expectedGain = (1 ether * 1000 * 1e14 * 3_155_760) / vault.SECONDS_PER_YEAR() / 1e18;
        assertApproxEqAbs(valueAfter - valueBefore, expectedGain, 1e15, "gain ~= 1% of 1 ETH");
    }

    function test_SharePriceIncreasesAfterAccrual() public {
        _submitYield(500); // 5%

        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        uint256 priceBefore = vault.sharePrice();

        vm.warp(block.timestamp + 1 days);
        vault.accrueYield();

        uint256 priceAfter = vault.sharePrice();
        assertGt(priceAfter, priceBefore, "share price must increase");
    }

    function test_SecondDepositorGetsFairShares() public {
        _submitYield(1000); // 10% APY

        // Alice deposits 1 ETH.
        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        // Advance time to accrue yield.
        vm.warp(block.timestamp + 3_155_760); // ~1/10 year
        vault.accrueYield();

        // Bob deposits 1 ETH — should get fewer shares than Alice because
        // the share price has increased.
        vm.prank(bob);
        vault.deposit{ value: 1 ether }();

        assertLt(vault.sharesOf(bob), vault.sharesOf(alice), "Bob gets fewer shares");
        assertGt(vault.sharesOf(bob), 0, "Bob gets some shares");
    }

    // --- tests: circuit breaker ---

    function test_CircuitBreakerTripsOnStaleOracle() public {
        oracle.setStalenessThreshold(1 hours);
        _submitYield(450);

        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        // Warp past staleness threshold.
        vm.warp(block.timestamp + 2 hours);
        vault.accrueYield();

        assertTrue(vault.circuitBreakerOpen(), "breaker should be open");

        // Withdrawals should be blocked.
        vm.prank(alice);
        vm.expectRevert(YieldConsumerExample.OracleStale.selector);
        vault.withdraw(0.5 ether);
    }

    function test_CircuitBreakerTripsOnImplausibleAPY() public {
        // Raise the oracle's max APY so the report is accepted, then let
        // the consumer's own circuit breaker catch the implausible value.
        oracle.setMaxAPYBps(20_000);
        _submitYield(15_000); // 150%

        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        vault.accrueYield();
        assertTrue(vault.circuitBreakerOpen(), "breaker should trip on implausible APY");
    }

    function test_CircuitBreakerClearsWhenOracleRecovers() public {
        oracle.setStalenessThreshold(1 hours);
        _submitYield(450);

        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        // Go stale.
        vm.warp(block.timestamp + 2 hours);
        vault.accrueYield();
        assertTrue(vault.circuitBreakerOpen());

        // Oracle recovers: submit a fresh report and accrue.
        vm.warp(block.timestamp + 1 hours);
        _submitYield(450);
        vault.accrueYield();
        assertFalse(vault.circuitBreakerOpen(), "breaker should clear");
    }

    // --- tests: flash-loan attack mitigation ---

    /// @dev Proves that a flash-loan-style deposit+withdraw in the same
    ///      block does NOT steal yield from existing depositors. This is
    ///      the core security property of share-based accounting: new
    ///      deposits buy shares at the current (post-accrual) price, so
    ///      they receive exactly zero retroactive yield.
    function test_FlashLoanAttackMitigated() public {
        // Alice deposits 10 ETH at 10% APY.
        _submitYield(1000);
        vm.prank(alice);
        vault.deposit{ value: 10 ether }();

        // Advance time to accrue yield (~3.65 days = 1% of year = 0.1% yield).
        vm.warp(block.timestamp + 315_576); // ~1% of year
        // Submit a fresh report so the oracle is not stale after warping.
        _submitYield(1000);
        vault.accrueYield();

        uint256 aliceValueBefore = vault.balanceOf(alice);
        assertGt(aliceValueBefore, 10 ether, "Alice must have accrued yield");

        // Bob attempts a flash-loan attack: deposit 100 ETH and withdraw
        // in the SAME block. With the old (buggy) accounting, Bob would
        // receive retroactive yield on his 100 ETH. With share-based
        // accounting, Bob buys shares at the current price and withdraws
        // exactly what he deposited (no yield).
        address bob = address(0xB0B);
        vm.deal(bob, 100 ether);

        uint256 bobBalBefore = bob.balance;
        vm.startPrank(bob);
        vault.deposit{ value: 100 ether }();
        uint256 bobShares = vault.sharesOf(bob);
        vault.withdraw(bobShares);
        vm.stopPrank();

        uint256 bobBalAfter = bob.balance;
        // Use signed comparison: Bob may have slightly less due to gas.
        // The key assertion is that Bob did NOT profit (no yield stolen).
        int256 bobProfit = int256(int256(bobBalAfter) - int256(bobBalBefore));

        // Bob should have approximately zero or negative profit (within gas
        // tolerance). He gets back exactly what he deposited because no time
        // elapsed between his deposit and withdrawal. A positive profit would
        // indicate yield theft.
        assertLe(bobProfit, int256(0.01 ether), "flash-loan attacker must not profit");

        // Alice's value must be unchanged by Bob's attack.
        uint256 aliceValueAfter = vault.balanceOf(alice);
        assertApproxEqAbs(
            aliceValueAfter, aliceValueBefore, 1e15, "Alice's yield must not be stolen"
        );
    }

    /// @dev Proves that a new depositor gets fewer shares per ETH after
    ///      yield has accrued, and that their withdrawal returns only
    ///      their deposit (no stolen yield).
    function test_NewDepositorGetsNoRetroactiveYield() public {
        _submitYield(1000); // 10% APY

        // Alice deposits 1 ETH.
        vm.prank(alice);
        vault.deposit{ value: 1 ether }();

        // Advance 1/10 year → ~1% yield.
        vm.warp(block.timestamp + 3_155_760);
        // Submit a fresh report so the oracle is not stale after warping.
        _submitYield(1000);
        vault.accrueYield();

        uint256 aliceValueBefore = vault.balanceOf(alice);
        assertGt(aliceValueBefore, 1 ether, "Alice must have accrued yield");

        // Bob deposits 1 ETH — gets fewer shares because share price > 1.0.
        vm.prank(bob);
        vault.deposit{ value: 1 ether }();

        uint256 bobShares = vault.sharesOf(bob);
        assertLt(bobShares, 1 ether, "Bob must get fewer shares than Alice");

        // Bob withdraws immediately (same block) — gets back ~1 ETH, no yield.
        vm.prank(bob);
        vault.withdraw(bobShares);

        // Bob's balance should be approximately 1 ETH (his deposit).
        assertApproxEqAbs(bob.balance, 100 ether, 0.01 ether, "Bob gets back ~deposit, no yield");

        // Alice's value must be unchanged.
        uint256 aliceValueAfter = vault.balanceOf(alice);
        assertApproxEqAbs(aliceValueAfter, aliceValueBefore, 1e15, "Alice's yield preserved");
    }

    /// @dev Proves that the vault is never insolvent: the contract's ETH
    ///      balance is always >= totalAssets.
    function invariant_VaultNeverInsolvent() public {
        assertGe(address(vault).balance, vault.totalAssets(), "vault must never be insolvent");
    }

    // --- tests: read functions ---

    function test_BalanceOfReturnsZeroBeforeDeposit() public {
        assertEq(vault.balanceOf(alice), 0);
    }

    function test_BalanceOfReturnsDepositValue() public {
        vm.prank(alice);
        vault.deposit{ value: 2 ether }();
        assertApproxEqAbs(vault.balanceOf(alice), 2 ether, 1e12);
    }

    function test_SharePriceInitial() public {
        assertEq(vault.sharePrice(), 1e18, "initial share price = 1.0");
    }

    // --- tests: constructor validation ---

    function test_RevertWhenZeroOracle() public {
        vm.expectRevert(YieldConsumerExample.ZeroDeposit.selector);
        new YieldConsumerExample(RestakeYieldOracle(address(0)));
    }
}
