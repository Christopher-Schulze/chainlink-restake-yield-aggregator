// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test } from "forge-std/Test.sol";
import { YieldConsumerExample } from "../src/YieldConsumerExample.sol";
import { RestakeYieldOracle } from "../src/RestakeYieldOracle.sol";
import { YieldVerifier } from "../src/YieldVerifier.sol";

/// @title YieldConsumerExample Invariant Tests
/// @notice Foundry invariant tests that verify the vault's core security
///         properties hold across arbitrary sequences of deposit/withdraw
///         and oracle updates.
contract YieldConsumerExampleInvariantTest is Test {
    YieldConsumerExample internal vault;
    RestakeYieldOracle internal oracle;

    address internal signer = address(0xA1);
    address internal updater = address(0xB2);
    address internal owner = address(this);

    // Track depositors for the invariant handler.
    address[] internal depositors;
    mapping(address => bool) internal isDepositor;

    function setUp() public {
        YieldVerifier verifier = new YieldVerifier();
        oracle = new RestakeYieldOracle(verifier, signer);
        vault = new YieldConsumerExample(oracle);

        // Set the updater (the signer is the initial updater, but we need
        // a separate updater for the test to submit reports).
        oracle.setUpdater(updater, true);

        // Create a set of depositors with ETH.
        for (uint160 i = 1; i <= 5; i++) {
            address dep = address(i);
            depositors.push(dep);
            isDepositor[dep] = true;
            vm.deal(dep, 100 ether);
        }

        // Target only the vault for invariant fuzzing. Without this,
        // Foundry calls all public/external functions on every contract
        // in scope (including oracle admin functions like grantRole,
        // pause, setSigner), which wastes ~80% of fuzz calls on
        // reverts from access-control checks.
        targetContract(address(vault));

        // Submit an initial yield report so the oracle is not stale.
        _submitReport(500); // 5% APY
    }

    // --- invariant handlers ---

    function deposit(uint160 depositorIdx, uint96 amount) public {
        depositorIdx = uint160(bound(depositorIdx, 1, 5));
        address dep = address(depositorIdx);
        amount = uint96(bound(amount, 0.01 ether, 50 ether));

        // Ensure depositor has enough ETH.
        if (dep.balance < amount) return;

        vm.prank(dep);
        vault.deposit{ value: amount }();
    }

    function withdraw(uint160 depositorIdx, uint256 sharesBps) public {
        depositorIdx = uint160(bound(depositorIdx, 1, 5));
        address dep = address(depositorIdx);
        sharesBps = bound(sharesBps, 0, 10000);

        uint256 shares = (vault.sharesOf(dep) * sharesBps) / 10000;
        if (shares == 0) return;

        vm.prank(dep);
        vault.withdraw(shares);
    }

    function accrueYield() public {
        vault.accrueYield();
    }

    function warpTime(uint256 secondsToWarp) public {
        secondsToWarp = bound(secondsToWarp, 0, 365 days);
        vm.warp(block.timestamp + secondsToWarp);
        // Submit a fresh report after warping to prevent staleness.
        _submitReport(500);
    }

    // --- invariants ---

    /// @dev The vault's ETH balance must always cover totalAssets.
    function invariant_VaultNeverInsolvent() public {
        assertGe(
            address(vault).balance, vault.totalAssets(), "vault ETH balance must be >= totalAssets"
        );
    }

    /// @dev Total shares must always be > 0 if there are depositors.
    function invariant_TotalSharesConsistent() public {
        if (address(vault).balance > 0) {
            assertGt(vault.totalShares(), 0, "shares must exist if vault has ETH");
        }
    }

    /// @dev Sum of all depositor share values must be <= totalAssets.
    function invariant_DepositorValuesSumToTotalAssets() public {
        uint256 sumOfValues = 0;
        for (uint256 i = 0; i < depositors.length; i++) {
            sumOfValues += vault.balanceOf(depositors[i]);
        }
        // Due to integer division rounding, the sum may be slightly less
        // than totalAssets, but must never exceed it.
        assertLe(sumOfValues, vault.totalAssets(), "depositor values must not exceed totalAssets");
    }

    /// @dev Share price must always be >= 1e18 (never below initial 1:1).
    function invariant_SharePriceNeverBelowInitial() public {
        assertGe(vault.sharePrice(), 1e18, "share price must never drop below 1:1");
    }

    /// @dev No depositor can have more shares than totalShares.
    function invariant_NoDepositorExceedsTotalShares() public {
        for (uint256 i = 0; i < depositors.length; i++) {
            assertLe(
                vault.sharesOf(depositors[i]),
                vault.totalShares(),
                "individual shares must not exceed total"
            );
        }
    }

    // --- helpers ---

    function _submitReport(uint256 apyBps) internal {
        // Build an EIP-712 signature for the report.
        // Use the test's private key (foundry default account 0).
        uint256 privateKey = 1; // address 0x7E5F...5Bdf
        address expectedSigner = vm.addr(privateKey);

        // Update the oracle's signer to match.
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
