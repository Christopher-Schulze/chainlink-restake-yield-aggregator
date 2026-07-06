// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { YieldVerifier } from "./YieldVerifier.sol";
import { AccessControl } from "@openzeppelin/contracts/access/AccessControl.sol";
import { Pausable } from "@openzeppelin/contracts/utils/Pausable.sol";

/// @title RestakeYieldOracle
/// @notice An on-chain oracle that stores the latest verified restaking yield
///         report submitted by the off-chain External Adapter.
///
/// @dev The oracle completes the Chainlink oracle loop:
///
///   1. The EA (Go) aggregates yield from multiple providers, signs the
///      report using EIP-712 typed data with a domain separator, and submits
///      the signature + yield data to this contract.
///   2. This contract reconstructs the EIP-712 digest on-chain from the
///      report parameters (binding the signature to the values), verifies
///      the signature via YieldVerifier, checks that the signer is an
///      authorised updater, enforces staleness and deviation bounds, and
///      stores the latest yield.
///   3. Consumer contracts (e.g. YieldConsumerExample) read the latest yield
///      via `latestYield()` and use it in their own logic.
///
/// The yield is stored as integer basis points (1 bp = 0.01%) to avoid
/// fixed-point precision issues on-chain. APY 4.5% is stored as 450 bps.
/// TVL is stored in milli-ETH (1e-3 ETH) as a uint96 to stay well within
/// Solidity integer bounds while preserving 3 decimal places of precision.
///
/// @dev Security: the digest is reconstructed on-chain from the report
///      parameters and the EIP-712 domain separator (chainId +
///      verifyingContract). This binds the signature to the exact values
///      being submitted, preventing value-substitution attacks where a
///      valid signature for one report is paired with different values.
///      The domain separator prevents cross-chain and cross-contract
///      replay. A monotonic timestamp check prevents same-chain replay.
contract RestakeYieldOracle is AccessControl, Pausable {
    // --- errors ---

    error Unauthorized(address caller);
    error InvalidSignature();
    error StaleReport(uint256 submittedAt, uint256 lastUpdate);
    error ReplayDetected(uint256 submittedTimestamp, uint256 lastReportTimestamp);
    error APYDeviationExceeded(int256 deviationBps, uint256 maxDeviationBps);
    error InvalidAPYBounds(uint256 apyBps);
    error ZeroAddress();

    // --- events ---

    /// @dev Emitted when a new yield report is verified and stored.
    event YieldUpdated(
        uint256 indexed apyBps,
        uint96 indexed tvlMilliETH,
        address indexed signer,
        uint256 timestamp
    );

    /// @dev Emitted when the authorised signer is rotated.
    event SignerRotated(address indexed oldSigner, address indexed newSigner);

    /// @dev Emitted when an authorised updater is added or removed.
    event UpdaterSet(address indexed updater, bool authorised);

    /// @dev Emitted when a config parameter is changed by the owner.
    event MaxAPYBpsSet(uint256 oldMax, uint256 newMax);
    event MinAPYBpsSet(uint256 oldMin, uint256 newMin);
    event MaxDeviationBpsSet(uint256 oldMax, uint256 newMax);
    event StalenessThresholdSet(uint256 oldSeconds, uint256 newSeconds);
    event OwnershipTransferred(address indexed oldOwner, address indexed newOwner);

    // --- structs ---

    /// @dev The latest verified yield report stored on-chain.
    struct YieldReport {
        // APY in basis points (1 bp = 0.01%). 450 = 4.5%.
        uint96 apyBps;
        // TVL in milli-ETH (1e-3 ETH). 1_000_000 = 1000 ETH.
        uint96 tvlMilliETH;
        // PointsPerETH scaled by 1e6 (1 ppm = 0.000001). 1_100_000 = 1.1.
        uint64 pointsPerETHppm;
        // Block timestamp of the last accepted report.
        uint32 updatedAt;
    }

    // --- immutables / storage ---

    YieldVerifier public immutable verifier;

    /// @dev The signer address that EA signatures must recover to. This is
    ///      the Ethereum address derived from the EA's secp256k1 private key.
    address public authorisedSigner;

    /// @dev Updaters are addresses authorised to submit signed reports.
    ///      The authorisedSigner is always an updater; additional updaters
    ///      can be added for multi-signer deployments.
    mapping(address => bool) public isUpdater;

    YieldReport public s_latestReport;

    /// @dev Timestamp of the last accepted report. Used for monotonic replay
    ///      prevention — each report must have a timestamp strictly greater
    ///      than the last accepted one.
    uint256 public lastReportTimestamp;

    // --- config (mutable by owner) ---

    /// @dev Maximum acceptable APY in basis points. Default 10000 = 100%.
    uint256 public maxAPYBps = 10_000;

    /// @dev Minimum acceptable APY in basis points. Default 0.
    uint256 public minAPYBps = 0;

    /// @dev Maximum absolute APY deviation between consecutive reports, in bps.
    ///      0 means the check is disabled. Default 500 = 5%.
    uint256 public maxDeviationBps = 500;

    /// @dev Maximum age of a report before it is considered stale, in seconds.
    uint256 public stalenessThreshold = 1 hours;

    // --- EIP-712 typed data ---

    /// @dev EIP-712 domain type hash.
    ///      EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)
    bytes32 public constant EIP712_DOMAIN_TYPEHASH = keccak256(
        "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
    );

    /// @dev EIP-712 YieldReport struct type hash.
    ///      YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)
    bytes32 public constant YIELDREPORT_TYPEHASH = keccak256(
        "YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)"
    );

    /// @dev The EIP-712 domain name, used in the domain separator.
    string public constant EIP712_NAME = "RestakeYieldOracle";

    /// @dev The EIP-712 domain version, used in the domain separator.
    string public constant EIP712_VERSION = "1";

    // --- roles ---

    /// @dev ADMIN_ROLE is the equivalent of owner — can change config and rotate signers.
    bytes32 public constant ADMIN_ROLE = keccak256("ADMIN_ROLE");
    /// @dev UPDATER_ROLE is granted to addresses that may submit signed reports.
    bytes32 public constant UPDATER_ROLE = keccak256("UPDATER_ROLE");

    // --- constructor ---

    /// @param _verifier The YieldVerifier contract used for signature checks.
    /// @param _signer   The EA's signer address (must match SIGNING_PRIVATE_KEY).
    constructor(YieldVerifier _verifier, address _signer) {
        if (address(_verifier) == address(0)) revert ZeroAddress();
        if (_signer == address(0)) revert ZeroAddress();
        verifier = _verifier;
        authorisedSigner = _signer;
        isUpdater[_signer] = true;
        // Grant DEFAULT_ADMIN_ROLE to deployer so they can grant/revoke ADMIN_ROLE.
        _grantRole(DEFAULT_ADMIN_ROLE, msg.sender);
        _grantRole(ADMIN_ROLE, msg.sender);
        _grantRole(UPDATER_ROLE, _signer);
        emit SignerRotated(address(0), _signer);
        emit UpdaterSet(_signer, true);
    }

    // --- EIP-712 digest computation ---

    /// @notice Computes the EIP-712 domain separator for this contract.
    /// @dev The domain separator binds signatures to a specific chain and
    ///      contract, preventing cross-chain and cross-contract replay attacks.
    ///      It is recomputed on every call (using `block.chainid`) so that
    ///      it stays correct after a chain split/fork.
    function domainSeparator() public view returns (bytes32) {
        return keccak256(
            abi.encode(
                EIP712_DOMAIN_TYPEHASH,
                keccak256(bytes(EIP712_NAME)),
                keccak256(bytes(EIP712_VERSION)),
                block.chainid,
                address(this)
            )
        );
    }

    /// @notice Computes the EIP-712 digest that the EA must sign for a report.
    /// @dev The digest is keccak256("\x19\x01" || domainSeparator || structHash),
    ///      where structHash = keccak256(abi.encode(YIELDREPORT_TYPEHASH, ...)).
    ///      This binds the signature to the exact report values, preventing
    ///      value-substitution attacks.
    /// @param apyBps APY in basis points.
    /// @param tvlMilliETH TVL in milli-ETH.
    /// @param pointsPerETHppm PointsPerETH in ppm.
    /// @param timestamp Unix timestamp from the EA report.
    /// @return The 32-byte digest that ecrecover expects.
    function digestOf(uint96 apyBps, uint96 tvlMilliETH, uint64 pointsPerETHppm, uint32 timestamp)
        public
        view
        returns (bytes32)
    {
        bytes32 structHash = keccak256(
            abi.encode(YIELDREPORT_TYPEHASH, apyBps, tvlMilliETH, pointsPerETHppm, timestamp)
        );
        return keccak256(abi.encodePacked("\x19\x01", domainSeparator(), structHash));
    }

    // --- external: submit a signed yield report ---

    /// @notice Submits a signed yield report for on-chain verification and storage.
    /// @dev The digest is reconstructed on-chain from the report parameters
    ///      and the EIP-712 domain separator. The signature must recover to
    ///      the authorised signer for this digest. This prevents value-substitution
    ///      attacks where a valid signature for one report is paired with
    ///      different values.
    /// @param signature 65-byte r||s||v signature (v in {0,1} or {27,28}).
    /// @param apyBps     APY in basis points (e.g. 450 = 4.5%).
    /// @param tvlMilliETH TVL in milli-ETH (e.g. 1_000_000 = 1000 ETH).
    /// @param pointsPerETHppm PointsPerETH in ppm (e.g. 1_100_000 = 1.1).
    /// @param timestamp  Unix timestamp from the EA report.
    function submitReport(
        bytes calldata signature,
        uint96 apyBps,
        uint96 tvlMilliETH,
        uint64 pointsPerETHppm,
        uint32 timestamp
    ) external whenNotPaused {
        if (!isUpdater[msg.sender]) revert Unauthorized(msg.sender);

        // 1. Reconstruct the digest on-chain from the report parameters.
        //    This binds the signature to the exact values being submitted.
        bytes32 digest = digestOf(apyBps, tvlMilliETH, pointsPerETHppm, timestamp);

        // 2. Verify the signature recovers to the authorised signer.
        //    We explicitly check the returned address against authorisedSigner
        //    rather than relying solely on the verifier's internal check.
        //    This makes the security invariant visible at the call site and
        //    robust against future changes to YieldVerifier.
        try verifier.verifyYield(digest, signature, authorisedSigner) returns (address recovered) {
            if (recovered != authorisedSigner) revert InvalidSignature();
        } catch {
            revert InvalidSignature();
        }

        // 3. Replay prevention: reject reports with timestamps not newer than
        //    the last accepted report. Combined with the EIP-712 domain
        //    separator (cross-chain/cross-contract), this closes all replay
        //    vectors.
        if (timestamp <= lastReportTimestamp) {
            revert ReplayDetected(timestamp, lastReportTimestamp);
        }

        // 4. Staleness check: reject reports older than the threshold.
        //    Note: if timestamp > block.timestamp, the subtraction underflows
        //    and reverts (Solidity 0.8.x checked arithmetic), rejecting future
        //    timestamps.
        if (block.timestamp - timestamp > stalenessThreshold) {
            revert StaleReport(timestamp, s_latestReport.updatedAt);
        }

        // 5. APY bounds check.
        if (apyBps < minAPYBps || apyBps > maxAPYBps) {
            revert InvalidAPYBounds(apyBps);
        }

        // 6. Deviation check: reject sudden APY jumps (unless first report).
        if (s_latestReport.updatedAt != 0 && maxDeviationBps > 0) {
            int256 prevAPY = int256(uint256(s_latestReport.apyBps));
            int256 newAPY = int256(uint256(apyBps));
            int256 deviation = newAPY - prevAPY;
            if (deviation < 0) deviation = -deviation;
            if (uint256(deviation) > maxDeviationBps) {
                revert APYDeviationExceeded(deviation, maxDeviationBps);
            }
        }

        // 7. Store the report.
        s_latestReport = YieldReport({
            apyBps: apyBps,
            tvlMilliETH: tvlMilliETH,
            pointsPerETHppm: pointsPerETHppm,
            updatedAt: uint32(block.timestamp)
        });
        lastReportTimestamp = timestamp;

        emit YieldUpdated(apyBps, tvlMilliETH, msg.sender, block.timestamp);
    }

    // --- external: read the latest yield ---

    /// @notice Returns the latest verified yield report.
    function latestYield() external view returns (YieldReport memory) {
        return s_latestReport;
    }

    /// @notice Returns the APY as a decimal scaled by 1e18 (for fixed-point math).
    /// @dev apyBps / 1e4 * 1e18 = apyBps * 1e14.
    function latestAPYScaled() external view returns (uint256) {
        return uint256(s_latestReport.apyBps) * 1e14;
    }

    /// @notice Returns true if the latest report is older than the staleness threshold.
    function isStale() public view returns (bool) {
        // slither-disable-next-line incorrect-equality
        if (s_latestReport.updatedAt == 0) return true;
        return block.timestamp - s_latestReport.updatedAt > stalenessThreshold;
    }

    // --- admin: signer rotation ---

    /// @notice Rotates the authorised signer. Only admins can call this.
    /// @dev The old signer's UPDATER_ROLE and isUpdater flag are revoked
    ///      to maintain access-control hygiene (principle of least privilege).
    function setSigner(address newSigner) external onlyRole(ADMIN_ROLE) {
        if (newSigner == address(0)) revert ZeroAddress();
        address old = authorisedSigner;
        authorisedSigner = newSigner;
        // Revoke old signer's updater privileges.
        isUpdater[old] = false;
        _revokeRole(UPDATER_ROLE, old);
        // Grant new signer's updater privileges.
        isUpdater[newSigner] = true;
        _grantRole(UPDATER_ROLE, newSigner);
        emit SignerRotated(old, newSigner);
    }

    /// @notice Authorises or de-authorises an updater address.
    function setUpdater(address updater, bool authorised) external onlyRole(ADMIN_ROLE) {
        if (updater == address(0)) revert ZeroAddress();
        isUpdater[updater] = authorised;
        if (authorised) {
            _grantRole(UPDATER_ROLE, updater);
        } else {
            _revokeRole(UPDATER_ROLE, updater);
        }
        emit UpdaterSet(updater, authorised);
    }

    // --- admin: config ---

    function setMaxAPYBps(uint256 newMax) external onlyRole(ADMIN_ROLE) {
        emit MaxAPYBpsSet(maxAPYBps, newMax);
        maxAPYBps = newMax;
    }

    function setMinAPYBps(uint256 newMin) external onlyRole(ADMIN_ROLE) {
        emit MinAPYBpsSet(minAPYBps, newMin);
        minAPYBps = newMin;
    }

    function setMaxDeviationBps(uint256 newMax) external onlyRole(ADMIN_ROLE) {
        emit MaxDeviationBpsSet(maxDeviationBps, newMax);
        maxDeviationBps = newMax;
    }

    function setStalenessThreshold(uint256 newSeconds) external onlyRole(ADMIN_ROLE) {
        emit StalenessThresholdSet(stalenessThreshold, newSeconds);
        stalenessThreshold = newSeconds;
    }

    // --- admin: pause ---

    /// @notice Pauses report submission. Use in emergencies (e.g. signer key compromise).
    function pause() external onlyRole(ADMIN_ROLE) {
        _pause();
    }

    /// @notice Resumes report submission after a pause.
    function unpause() external onlyRole(ADMIN_ROLE) {
        _unpause();
    }
}
