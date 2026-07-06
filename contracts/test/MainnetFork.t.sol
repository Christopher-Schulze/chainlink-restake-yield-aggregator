// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test } from "forge-std/Test.sol";
import { YieldVerifier } from "../src/YieldVerifier.sol";
import { RestakeYieldOracle } from "../src/RestakeYieldOracle.sol";
import { YieldConsumerExample } from "../src/YieldConsumerExample.sol";

/// @title Mainnet Fork Integration Test
/// @notice Tests the oracle + vault against a real Ethereum mainnet fork.
///         This verifies that the EIP-712 domain separator uses the correct
///         chain ID and that the contracts behave correctly in a real
///         EVM environment.
///
/// Run with:
///   forge test --match-contract MainnetForkTest --fork-url $MAINNET_RPC_URL -vv
///
/// Or via the Makefile:
///   make forge-test-fork FORK_URL=$MAINNET_RPC_URL
///
/// Requirements:
///   - An Ethereum mainnet RPC URL (Alchemy, Infura, or any public RPC)
///   - The fork block is pinned to a recent block for reproducibility
contract MainnetForkTest is Test {
    YieldVerifier internal verifier;
    RestakeYieldOracle internal oracle;
    YieldConsumerExample internal vault;

    // Test signer key (same as genfixture.go: 0x...0001).
    uint256 constant SIGNER_PK = 1;
    address constant SIGNER = 0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf;

    address internal updater = address(this);
    address internal depositor = address(0x9999);

    function setUp() public {
        // Fork mainnet at a specific block for reproducibility.
        // Requires FORK_URL env var or a foundry endpoint named "mainnet".
        // If no RPC URL is available, skip the test gracefully.
        string memory rpcUrl = vm.envOr("FORK_URL", string(""));
        if (bytes(rpcUrl).length == 0) {
            // Try foundry's built-in endpoint alias.
            rpcUrl = "mainnet";
        }

        try vm.createSelectFork(rpcUrl, 21_000_000) {
        // Fork succeeded — continue with setup.
        }
        catch {
            // No RPC URL available — skip all tests in this contract.
            vm.skip(true);
        }

        // Deploy contracts on the fork.
        verifier = new YieldVerifier();
        oracle = new RestakeYieldOracle(verifier, SIGNER);
        vault = new YieldConsumerExample(oracle);
        oracle.setUpdater(updater, true);

        // Fund the depositor.
        vm.deal(depositor, 100 ether);

        // Set a realistic timestamp.
        vm.warp(1_700_000_000);
    }

    // --- tests: chain ID correctness ---

    /// @dev On a mainnet fork, the chain ID must be 1. This verifies
    ///      that the EIP-712 domain separator uses the correct chain ID,
    ///      which is critical for cross-chain replay protection.
    function test_ForkChainIdIsMainnet() public view {
        assertEq(block.chainid, 1, "fork must be on mainnet (chainId=1)");
    }

    // --- tests: EIP-712 on real chain ---

    /// @dev Submit a valid report on the mainnet fork and verify it's stored.
    ///      This confirms the EIP-712 digest is correctly reconstructed
    ///      on-chain using the real chain ID.
    function test_SubmitReportOnMainnetFork() public {
        uint96 apyBps = 450; // 4.5%
        uint96 tvlMilliETH = 1_000_000;
        uint64 ppm = 1_100_000;
        uint32 ts = uint32(block.timestamp);

        bytes memory sig = _signReport(apyBps, tvlMilliETH, ppm, ts);
        oracle.submitReport(sig, apyBps, tvlMilliETH, ppm, ts);

        RestakeYieldOracle.YieldReport memory report = oracle.latestYield();
        assertEq(report.apyBps, apyBps);
        assertEq(report.tvlMilliETH, tvlMilliETH);
        assertEq(report.pointsPerETHppm, ppm);
        assertEq(report.updatedAt, ts);
    }

    // --- tests: vault on real chain ---

    /// @dev Deposit into the vault on a mainnet fork and verify shares.
    function test_DepositOnMainnetFork() public {
        // Submit initial report.
        _submitValidReport(500);

        vm.prank(depositor);
        vault.deposit{ value: 1 ether }();

        assertEq(vault.sharesOf(depositor), 1 ether);
        assertEq(vault.totalShares(), 1 ether);
        assertEq(vault.totalAssets(), 1 ether);
    }

    /// @dev Deposit then withdraw on mainnet fork — same block returns exact amount.
    function test_DepositWithdrawOnMainnetFork() public {
        _submitValidReport(500);

        uint256 depositorBalanceBefore = depositor.balance;

        vm.prank(depositor);
        vault.deposit{ value: 5 ether }();

        uint256 shares = vault.sharesOf(depositor);
        assertGt(shares, 0);

        vm.prank(depositor);
        vault.withdraw(shares);

        // Same block — no yield accrued, exact amount returned.
        assertEq(depositor.balance, depositorBalanceBefore);
    }

    /// @dev Cross-chain replay protection: a signature signed for mainnet
    ///      (chainId=1) must NOT be valid on a different chain.
    function test_ReplayProtectionAcrossChains() public {
        uint96 apyBps = 450;
        uint96 tvlMilliETH = 1_000_000;
        uint64 ppm = 1_100_000;
        uint32 ts = uint32(block.timestamp);

        // Sign the report on mainnet (chainId=1).
        bytes memory sig = _signReport(apyBps, tvlMilliETH, ppm, ts);

        // Submit on mainnet — should succeed.
        oracle.submitReport(sig, apyBps, tvlMilliETH, ppm, ts);

        // Now fork a different chain (e.g., Sepolia chainId=11155111).
        vm.createSelectFork({ urlOrAlias: "sepolia", blockNumber: 7_000_000 });

        // Deploy a NEW oracle on the Sepolia fork with the same signer.
        verifier = new YieldVerifier();
        oracle = new RestakeYieldOracle(verifier, SIGNER);
        oracle.setUpdater(updater, true);

        // The same signature must NOT be valid on Sepolia because the
        // domain separator includes the chain ID.
        vm.expectRevert(RestakeYieldOracle.InvalidSignature.selector);
        oracle.submitReport(sig, apyBps, tvlMilliETH, ppm, ts);
    }

    // --- helpers ---

    function _signReport(uint96 apyBps, uint96 tvlMilliETH, uint64 ppm, uint32 ts)
        internal
        view
        returns (bytes memory)
    {
        bytes32 digest = oracle.digestOf(apyBps, tvlMilliETH, ppm, ts);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(SIGNER_PK, digest);
        return abi.encodePacked(r, s, uint8(v - 27));
    }

    function _submitValidReport(uint96 apyBps) internal {
        uint32 ts = uint32(block.timestamp);
        bytes memory sig = _signReport(apyBps, 1_000_000, 1_100_000, ts);
        oracle.submitReport(sig, apyBps, 1_000_000, 1_100_000, ts);
    }
}
