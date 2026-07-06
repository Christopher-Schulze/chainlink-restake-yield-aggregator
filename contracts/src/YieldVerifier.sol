// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { ECDSA } from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

/// @title YieldVerifier
/// @notice On-chain verifier for restake-yield-ea signatures.
/// @dev The off-chain adapter (internal/security) signs the keccak256 digest
///      of a canonical JSON payload using secp256k1 and produces a 65-byte
///      r||s||v signature with v in {0,1} (go-ethereum crypto.Sign convention).
///      Solidity's ecrecover expects v in {27,28}, so this contract normalises
///      v before calling ECDSA.recover. The contract also enforces low-s
///      malleability protection (s must be <= secp256k1n/2) to prevent
///      signature malleability attacks.
contract YieldVerifier {
    event YieldVerified(address indexed signer, bytes32 indexed digest);

    error InvalidSignatureLength(uint256 length);
    error InvalidSignatureV(uint8 v);
    error InvalidSignatureS(bytes32 s);
    error SignerMismatch(address expected, address recovered);

    /// @notice Half of the secp256k1 curve order, used for low-s enforcement.
    /// @dev ECDSA.recover already checks this in OZ 5.x, but we revert with a
    ///      custom error for clearer diagnostics.
    bytes32 private constant SECP256K1N_HALF =
        0x7fffffffffffffffffffffffffffffff5d576e7357a4501ddfe92f46681b20a0;

    /// @notice Verifies a 65-byte r||s||v signature against a digest.
    /// @param digest   The keccak256 hash of the canonical payload.
    /// @param signature 65-byte signature produced by the adapter (v in {0,1}).
    /// @param expectedSigner The address the signature must recover to.
    /// @return recovered The address recovered by ecrecover.
    function verifyYield(bytes32 digest, bytes calldata signature, address expectedSigner)
        external
        pure
        returns (address recovered)
    {
        return _verifyYield(digest, signature, expectedSigner);
    }

    /// @notice Verifies a signature and emits an event on success. Useful as a
    /// hook for Chainlink consumer contracts that want an on-chain audit trail.
    function verifyAndLog(bytes32 digest, bytes calldata signature, address expectedSigner)
        external
        returns (address recovered)
    {
        recovered = _verifyYield(digest, signature, expectedSigner);
        emit YieldVerified(recovered, digest);
    }

    /// @dev Internal verifier shared by verifyYield and verifyAndLog. Parses
    ///      the 65-byte r||s||v signature, normalises v from {0,1} to {27,28}
    ///      as required by ecrecover, enforces low-s malleability protection,
    ///      and checks that the recovered address matches expectedSigner.
    function _verifyYield(bytes32 digest, bytes calldata signature, address expectedSigner)
        internal
        pure
        returns (address recovered)
    {
        if (signature.length != 65) revert InvalidSignatureLength(signature.length);

        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(signature.offset)
            s := calldataload(add(signature.offset, 32))
            v := byte(0, calldataload(add(signature.offset, 64)))
        }
        // Normalise v from {0,1} to {27,28} as required by ecrecover.
        if (v < 27) {
            if (v == 0 || v == 1) {
                v += 27;
            } else {
                revert InvalidSignatureV(v);
            }
        } else if (v != 27 && v != 28) {
            revert InvalidSignatureV(v);
        }

        // Low-s malleability protection: s must be in the lower half of the
        // curve order. go-ethereum's crypto.Sign already produces low-s, but
        // we check defensively in case a non-conforming signer is used.
        if (s > SECP256K1N_HALF) revert InvalidSignatureS(s);

        recovered = ECDSA.recover(digest, v, r, s);
        if (recovered != expectedSigner) {
            revert SignerMismatch(expectedSigner, recovered);
        }
    }

    /// @notice Computes the digest that the adapter signs for a raw payload.
    /// @dev Mirrors internal/security: digest = keccak256(payload).
    function digestOf(bytes calldata payload) external pure returns (bytes32) {
        return keccak256(payload);
    }
}
