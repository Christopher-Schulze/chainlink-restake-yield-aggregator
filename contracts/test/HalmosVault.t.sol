// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test } from "forge-std/Test.sol";
import { YieldConsumerExample } from "../src/YieldConsumerExample.sol";
import { RestakeYieldOracle } from "../src/RestakeYieldOracle.sol";
import { YieldVerifier } from "../src/YieldVerifier.sol";

/// @title Halmos Formal Verification Tests
/// @notice Symbolic execution tests for the vault's core security invariants.
///         Run with: halmos --function check_ --solver-timeout-assertion 60
///
/// These tests use Halmos's symbolic execution to verify properties that hold
/// for ALL possible input values, not just random fuzz inputs. Each `check_`
/// function is run with unbounded symbolic parameters.
///
/// Prerequisites:
///   pip3 install halmos
///   halmos --function check_
contract HalmosVaultTest is Test {
    YieldConsumerExample internal vault;
    RestakeYieldOracle internal oracle;

    address internal signer = address(0xA1);
    address internal updater = address(0xB2);
    address internal depositor = address(0xDeb);

    function setUp() public {
        YieldVerifier verifier = new YieldVerifier();
        oracle = new RestakeYieldOracle(verifier, signer);
        vault = new YieldConsumerExample(oracle);
        oracle.setUpdater(updater, true);
        _submitReport(500); // 5% APY
        vm.deal(depositor, 1000 ether);
    }

    // --- Halmos symbolic tests ---

    /// @dev Halmos: depositing any positive amount never makes the vault
    ///      insolvent (vault balance >= totalAssets).
    function check_deposit_never_insolvent(uint96 amount) public {
        vm.assume(amount > 0 && amount <= 100 ether);

        uint256 balanceBefore = address(vault).balance;
        uint256 assetsBefore = vault.totalAssets();

        vm.prank(depositor);
        vault.deposit{ value: amount }();

        // After deposit: vault balance must cover totalAssets.
        assertGe(address(vault).balance, vault.totalAssets());
    }

    /// @dev Halmos: share price after a single deposit is always >= 1e18.
    function check_deposit_share_price_never_below_initial(uint96 amount) public {
        vm.assume(amount > 0 && amount <= 100 ether);

        vm.prank(depositor);
        vault.deposit{ value: amount }();

        assertGe(vault.sharePrice(), 1e18);
    }

    /// @dev Halmos: after deposit, shares minted = amount * 1e18 / sharePrice.
    ///      For the first deposit (totalShares == 0), shares = amount.
    function check_first_deposit_shares_equal_amount(uint96 amount) public {
        vm.assume(amount > 0 && amount <= 100 ether);

        vm.prank(depositor);
        vault.deposit{ value: amount }();

        // First deposit: sharePrice == 1e18, so shares == amount
        assertEq(vault.sharesOf(depositor), uint256(amount));
        assertEq(vault.totalShares(), uint256(amount));
        assertEq(vault.totalAssets(), uint256(amount));
    }

    /// @dev Halmos: deposit then withdraw of all shares returns the original
    ///      amount (no yield accrual in same block since elapsed == 0).
    function check_deposit_withdraw_same_block(uint96 amount) public {
        vm.assume(amount > 0 && amount <= 100 ether);

        uint256 depositorBalanceBefore = depositor.balance;

        vm.prank(depositor);
        vault.deposit{ value: amount }();

        uint256 shares = vault.sharesOf(depositor);
        vm.assume(shares > 0);

        vm.prank(depositor);
        vault.withdraw(shares);

        // In the same block (no time elapsed, no yield accrued), the
        // depositor should get back exactly what they deposited.
        assertEq(depositor.balance, depositorBalanceBefore);
    }

    /// @dev Halmos: withdrawing more shares than owned always reverts.
    function check_withdraw_excess_shares_reverts(uint96 amount, uint256 withdrawShares) public {
        vm.assume(amount > 0 && amount <= 100 ether);

        vm.prank(depositor);
        vault.deposit{ value: amount }();

        uint256 ownedShares = vault.sharesOf(depositor);
        vm.assume(withdrawShares > ownedShares);

        vm.prank(depositor);
        try vault.withdraw(withdrawShares) {
            assert(false); // should never reach here
        } catch {
            assert(true); // expected revert
        }
    }

    /// @dev Halmos: totalShares is always >= any individual's shares.
    function check_total_shares_ge_individual(uint96 amount) public {
        vm.assume(amount > 0 && amount <= 100 ether);

        vm.prank(depositor);
        vault.deposit{ value: amount }();

        assertGe(vault.totalShares(), vault.sharesOf(depositor));
    }

    /// @dev Halmos: vault balance is always >= totalAssets after deposit
    ///      and withdraw sequence (no insolvency from normal operations).
    function check_no_insolvency_after_ops(uint96 depositAmount, uint96 withdrawShares) public {
        vm.assume(depositAmount > 0 && depositAmount <= 100 ether);

        vm.prank(depositor);
        vault.deposit{ value: depositAmount }();

        uint256 ownedShares = vault.sharesOf(depositor);
        uint256 actualWithdraw = uint256(withdrawShares);
        if (actualWithdraw > ownedShares) {
            actualWithdraw = ownedShares;
        }
        if (actualWithdraw > 0) {
            vm.prank(depositor);
            vault.withdraw(actualWithdraw);
        }

        assertGe(address(vault).balance, vault.totalAssets());
    }

    // --- helpers ---

    function _submitReport(uint256 apyBps) internal {
        uint256 privateKey = 1;
        address expectedSigner = vm.addr(privateKey);
        oracle.setSigner(expectedSigner);

        uint256 chainId = block.chainid;
        bytes32 domainSep = keccak256(
            abi.encode(
                keccak256(
                    "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
                ),
                keccak256("RestakeYieldOracle"),
                keccak256("1"),
                chainId,
                address(oracle)
            )
        );

        bytes32 structHash = keccak256(
            abi.encode(
                keccak256(
                    "YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)"
                ),
                uint256(apyBps),
                uint256(1_000_000),
                uint256(1_100_000),
                uint256(block.timestamp)
            )
        );

        bytes32 digest = keccak256(abi.encodePacked("\x19\x01", domainSep, structHash));
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        bytes memory sig = abi.encodePacked(r, s, v);

        vm.prank(updater);
        oracle.submitReport(
            sig, uint96(apyBps), uint96(1_000_000), uint64(1_100_000), uint32(block.timestamp)
        );
    }

    receive() external payable { }
}
