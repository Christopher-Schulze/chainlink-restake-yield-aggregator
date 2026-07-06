// Package security provides cryptographic verification and data integrity
// features for the restake-yield-ea. Signatures use the secp256k1 curve and
// Ethereum's signing scheme (keccak256 hash + 65-byte r||s||v signature) so
// that payloads can be verified on-chain by Solidity's ecrecover.
package security

import (
	"fmt"
	"math/big"
	"strings"

	crypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/common"
)

// EIP712Domain describes the EIP-712 domain separator used to bind
// signatures to a specific application, chain, and contract. It is the
// Go counterpart of the Solidity `domainSeparator()` view function.
//
// The domain separator prevents:
//   - cross-chain replay (chainId differs)
//   - cross-contract replay (verifyingContract differs)
//   - cross-application replay (name + version differ)
//
// See https://eips.ethereum.org/EIPS/eip-712 for the specification.
type EIP712Domain struct {
	Name              string         // human-readable name, e.g. "RestakeYieldOracle"
	Version           string         // version string, e.g. "1"
	ChainID           *big.Int       // EIP-155 chain id
	VerifyingContract common.Address // address of the verifying contract
}

// EIP712DomainTypeHash is the keccak256 of the EIP712Domain type string.
// It MUST match the Solidity constant EIP712_DOMAIN_TYPEHASH.
var EIP712DomainTypeHash = crypto.Keccak256Hash([]byte(
	"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)",
))

// Separator returns the EIP-712 domain separator hash. It MUST match the
// Solidity `domainSeparator()` view function for the same domain values.
func (d EIP712Domain) Separator() common.Hash {
	return crypto.Keccak256Hash(
		abiEncodeWords(
			EIP712DomainTypeHash,
			keccak256Bytes(d.Name),
			keccak256Bytes(d.Version),
			bigTo32(d.ChainID),
			common.BytesToHash(d.VerifyingContract.Bytes()),
		),
	)
}

// YieldReport is the EIP-712 typed struct that the EA signs for on-chain
// verification by the RestakeYieldOracle contract. The fields MUST match
// the Solidity `YieldReport` struct and the YIELDREPORT_TYPEHASH.
//
// All fields are integers (no floating point on-chain):
//   - APYBps: APY in basis points (450 = 4.5%)
//   - TVLMilliETH: TVL in milli-ETH (1_000_000 = 1000 ETH)
//   - PointsPerETHppm: points-per-ETH in ppm (1_100_000 = 1.1)
//   - Timestamp: unix timestamp of the report
type YieldReport struct {
	APYBps          *big.Int // uint96, basis points
	TVLMilliETH     *big.Int // uint96, milli-ETH
	PointsPerETHppm *big.Int // uint64, ppm
	Timestamp       *big.Int // uint32, unix seconds
}

// YieldReportTypeHash is the keccak256 of the YieldReport type string.
// It MUST match the Solidity constant YIELDREPORT_TYPEHASH.
var YieldReportTypeHash = crypto.Keccak256Hash([]byte(
	"YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)",
))

// StructHash returns the keccak256 of the abi.encode of the type hash and
// the struct fields. It MUST match the Solidity `structHash` computation.
func (r YieldReport) StructHash() common.Hash {
	return crypto.Keccak256Hash(
		abiEncodeWords(
			YieldReportTypeHash,
			bigTo32(r.APYBps),
			bigTo32(r.TVLMilliETH),
			bigTo32(r.PointsPerETHppm),
			bigTo32(r.Timestamp),
		),
	)
}

// Digest returns the final EIP-712 digest that the signer signs and that
// ecrecover expects on-chain:
//
//	keccak256("\x19\x01" || domainSeparator || structHash)
//
// It MUST match the Solidity `digestOf` view function for the same domain
// and report values.
func (r YieldReport) Digest(domain EIP712Domain) common.Hash {
	sep := domain.Separator()
	structHash := r.StructHash()
	return crypto.Keccak256Hash(
		[]byte{0x19, 0x01},
		sep.Bytes(),
		structHash.Bytes(),
	)
}

// NewYieldReport constructs a YieldReport from plain Go integers,
// validating that the values fit in their Solidity counterparts.
// Since the inputs are uint64/uint32 they can never exceed uint96/uint64,
// but the checks are kept for clarity and future-proofing.
func NewYieldReport(apyBps, tvlMilliETH, pointsPerETHppm uint64, timestamp uint32) (YieldReport, error) {
	return YieldReport{
		APYBps:          new(big.Int).SetUint64(apyBps),
		TVLMilliETH:     new(big.Int).SetUint64(tvlMilliETH),
		PointsPerETHppm: new(big.Int).SetUint64(pointsPerETHppm),
		Timestamp:       new(big.Int).SetUint64(uint64(timestamp)),
	}, nil
}

// SignOnChainReport signs an EIP-712 YieldReport digest with the
// service's private key and returns the digest, the 65-byte r||s||v
// signature (v in {0,1}), and the signer address. The digest is the
// exact value that the Solidity `RestakeYieldOracle.digestOf` view
// function computes for the same domain and report values, so the
// signature will verify on-chain via `submitReport`.
//
// This is the cryptographic bridge between the off-chain EA and the
// on-chain oracle: the EA signs here, the oracle reconstructs the same
// digest on-chain and verifies via ecrecover.
func (s *DataIntegrityService) SignOnChainReport(report YieldReport, domain EIP712Domain) (digest common.Hash, signature []byte, signer common.Address, err error) {
	digest = report.Digest(domain)
	sig, err := signFunc(digest.Bytes(), s.privateKey)
	if err != nil {
		return common.Hash{}, nil, common.Address{}, fmt.Errorf("EIP-712 sign failed: %w", err)
	}
	signer = common.HexToAddress(s.address)
	return digest, sig, signer, nil
}

// EIP712DomainFromHex constructs an EIP712Domain from hex/string inputs,
// convenient for config-driven construction.
func EIP712DomainFromHex(name, version, chainIDStr, verifyingContractHex string) (EIP712Domain, error) {
	chainID, ok := new(big.Int).SetString(strings.TrimSpace(chainIDStr), 10)
	if !ok {
		return EIP712Domain{}, fmt.Errorf("invalid chain id %q", chainIDStr)
	}
	if !common.IsHexAddress(verifyingContractHex) {
		return EIP712Domain{}, fmt.Errorf("invalid verifying contract %q", verifyingContractHex)
	}
	return EIP712Domain{
		Name:              name,
		Version:           version,
		ChainID:           chainID,
		VerifyingContract: common.HexToAddress(verifyingContractHex),
	}, nil
}

// --- abi.encode helpers ---

// bigTo32 right-aligns a big.Int into a 32-byte array, mirroring
// Solidity's abi.encode which left-pads integers to 32 bytes.
func bigTo32(x *big.Int) [32]byte {
	var out [32]byte
	if x == nil {
		return out
	}
	b := x.Bytes()
	if len(b) > 32 {
		panic(fmt.Sprintf("value exceeds 32 bytes: %d bytes", len(b)))
	}
	copy(out[32-len(b):], b)
	return out
}

// abiEncodeWords concatenates 32-byte words the same way Solidity's
// abi.encode does. Each argument must be a [32]byte or common.Hash.
func abiEncodeWords(words ...interface{}) []byte {
	var buf []byte
	for _, w := range words {
		switch v := w.(type) {
		case [32]byte:
			buf = append(buf, v[:]...)
		case common.Hash:
			buf = append(buf, v.Bytes()...)
		default:
			panic(fmt.Sprintf("abiEncodeWords: unsupported type %T", w))
		}
	}
	return buf
}

func keccak256Bytes(s string) [32]byte {
	return crypto.Keccak256Hash([]byte(s))
}
