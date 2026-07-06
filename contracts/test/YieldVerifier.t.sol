// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test, stdJson } from "forge-std/Test.sol";
import { YieldVerifier } from "../src/YieldVerifier.sol";

/// @title YieldVerifierTest
/// @notice Verifies the on-chain signature checker against:
///   1. A fixture produced by the Go adapter (internal/security) — proving the
///      Go secp256k1 signatures verify in Solidity via ecrecover.
///   2. Foundry's own vm.sign, to validate the v-normalisation logic.
///   3. Negative cases (wrong signer, tampered digest, bad length).
contract YieldVerifierTest is Test {
    YieldVerifier internal verifier;

    function setUp() public {
        verifier = new YieldVerifier();
    }

    /// @dev Reads the Go-generated fixture and asserts the contract accepts it.
    function test_VerifyGoGeneratedFixture() public {
        string memory root = vm.projectRoot();
        string memory path = string.concat(root, "/contracts/test/fixtures/signature.json");
        string memory json = vm.readFile(path);

        bytes32 digest = stdJson.readBytes32(json, ".digest");
        bytes memory signature = stdJson.readBytes(json, ".signature");
        address signer = stdJson.readAddress(json, ".signer");

        address recovered = verifier.verifyYield(digest, signature, signer);
        assertEq(recovered, signer, "fixture signature must recover the Go signer");
    }

    /// @dev Uses Foundry's vm.sign to produce a signature, repacks it into the
    ///      Go r||s||v (v in {0,1}) format, and confirms the contract recovers
    ///      the correct address. This validates the v-normalisation path.
    function test_VerifyNormalisesVFrom01To27() public {
        uint256 privateKey = 0xa1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1;
        address signer = vm.addr(privateKey);
        bytes32 digest = keccak256("restake-yield-ea");

        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        // vm.sign returns v in {27,28}; convert to the Go {0,1} convention.
        uint8 vGo = v - 27;
        bytes memory signature = abi.encodePacked(r, s, vGo);

        address recovered = verifier.verifyYield(digest, signature, signer);
        assertEq(recovered, signer, "v-normalised signature must recover signer");
    }

    /// @dev digestOf must match keccak256 of the payload (mirrors the Go side).
    function test_DigestOfMatchesKeccak256() public {
        bytes memory payload = bytes('{"apy":0.045,"tvl":1234.5}');
        bytes32 expected = keccak256(payload);
        bytes32 got = verifier.digestOf(payload);
        assertEq(got, expected, "digestOf must equal keccak256(payload)");
    }

    function test_RevertOnWrongSigner() public {
        uint256 privateKey = 0xb2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2;
        address signer = vm.addr(privateKey);
        bytes32 digest = keccak256("payload");
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        bytes memory signature = abi.encodePacked(r, s, uint8(v - 27));

        address wrongSigner = address(0xBEEF);
        vm.expectRevert(
            abi.encodeWithSelector(YieldVerifier.SignerMismatch.selector, wrongSigner, signer)
        );
        verifier.verifyYield(digest, signature, wrongSigner);
    }

    function test_RevertOnTamperedDigest() public {
        string memory root = vm.projectRoot();
        string memory path = string.concat(root, "/contracts/test/fixtures/signature.json");
        string memory json = vm.readFile(path);
        bytes32 digest = stdJson.readBytes32(json, ".digest");
        bytes memory signature = stdJson.readBytes(json, ".signature");
        address signer = stdJson.readAddress(json, ".signer");

        bytes32 tampered = bytes32(uint256(digest) ^ 1);
        // A different digest recovers a different (wrong) address; we only
        // assert that verification reverts (SignerMismatch), not the exact
        // recovered address, which depends on ecrecover's output.
        vm.expectRevert();
        verifier.verifyYield(tampered, signature, signer);
    }

    function test_RevertOnBadLength() public {
        bytes memory bad = new bytes(64);
        vm.expectRevert(abi.encodeWithSelector(YieldVerifier.InvalidSignatureLength.selector, 64));
        verifier.verifyYield(bytes32(0), bad, address(0));
    }

    function test_RevertOnInvalidV() public {
        bytes memory sig = new bytes(65);
        // Set v to an invalid value (5) — neither 0/1 nor 27/28.
        sig[64] = bytes1(uint8(5));
        vm.expectRevert(abi.encodeWithSelector(YieldVerifier.InvalidSignatureV.selector, 5));
        verifier.verifyYield(bytes32(0), sig, address(0));
    }

    /// @dev High-s signatures must be rejected to prevent malleability.
    function test_RevertOnHighS() public {
        uint256 privateKey = 0xc3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3;
        address signer = vm.addr(privateKey);
        bytes32 digest = keccak256("malleability-test");
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);

        // Flip s to the high-s form: s' = secp256k1n - s
        uint256 secp256k1n = 0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141;
        bytes32 sHigh = bytes32(secp256k1n - uint256(s));

        bytes memory signature = abi.encodePacked(r, sHigh, uint8(v - 27));
        vm.expectRevert(abi.encodeWithSelector(YieldVerifier.InvalidSignatureS.selector, sHigh));
        verifier.verifyYield(digest, signature, signer);
    }

    /// @dev verifyAndLog must emit YieldVerified on success.
    function test_VerifyAndLogEmitsEvent() public {
        uint256 privateKey = 0xd4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4;
        address signer = vm.addr(privateKey);
        bytes32 digest = keccak256("event-test");
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        bytes memory signature = abi.encodePacked(r, s, uint8(v - 27));

        vm.expectEmit(true, true, false, false);
        emit YieldVerifier.YieldVerified(signer, digest);
        verifier.verifyAndLog(digest, signature, signer);
    }

    /// @dev Signatures with v already in {27,28} must be accepted without
    ///      double-normalisation. This validates the v >= 27 branch.
    function test_VerifyWithVAlreadyNormalized() public {
        uint256 privateKey = 0xe5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5;
        address signer = vm.addr(privateKey);
        bytes32 digest = keccak256("already-normalized");
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        // vm.sign returns v in {27,28}; pass it directly without converting.
        bytes memory signature = abi.encodePacked(r, s, v);

        address recovered = verifier.verifyYield(digest, signature, signer);
        assertEq(recovered, signer, "v=27/28 signature must recover signer");
    }

    /// @dev Signatures longer than 65 bytes must be rejected.
    function test_RevertOnSignatureTooLong() public {
        bytes memory tooLong = new bytes(66);
        vm.expectRevert(abi.encodeWithSelector(YieldVerifier.InvalidSignatureLength.selector, 66));
        verifier.verifyYield(bytes32(0), tooLong, address(0));
    }

    /// @dev verifyAndLog must revert on a wrong signer without emitting an event.
    function test_VerifyAndLogRevertsOnWrongSigner() public {
        uint256 privateKey = 0xf6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6;
        address signer = vm.addr(privateKey);
        bytes32 digest = keccak256("wrong-signer-log");
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        bytes memory signature = abi.encodePacked(r, s, uint8(v - 27));

        address wrongSigner = address(0xCAFE);
        vm.expectRevert(
            abi.encodeWithSelector(YieldVerifier.SignerMismatch.selector, wrongSigner, signer)
        );
        verifier.verifyAndLog(digest, signature, wrongSigner);
    }
}
