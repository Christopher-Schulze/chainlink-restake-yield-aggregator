// genfixture generates an EIP-712 signature fixture for the Solidity
// RestakeYieldOracle test. It signs a YieldReport with a known private key
// using the same EIP-712 typed-data scheme as internal/security, and writes
// contracts/test/fixtures/eip712_signature.json.
//
// This is the cross-language signature compatibility proof: the Go EA signs
// here, the Solidity oracle reconstructs the same digest on-chain and
// verifies via ecrecover. The Foundry test `test_OracleAcceptsGoGeneratedFixture`
// loads this fixture and submits it to the oracle.
//
// Usage:
//
//	cd contracts && go run script/genfixture.go
//
// The fixture uses a hardcoded verifyingContract address. To produce a
// fixture that verifies at the actual deployed oracle address, deploy the
// oracle first, then re-run this script with the deployed address as the
// first argument:
//
//	go run script/genfixture.go 0xYourOracleAddress
//go:build ignore
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// EIP-712 type hashes — MUST match the Solidity constants in
// RestakeYieldOracle.sol and the Go constants in internal/security/eip712.go.
var (
	eip712DomainTypeHash = crypto.Keccak256Hash([]byte(
		"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)",
	))
	yieldReportTypeHash = crypto.Keccak256Hash([]byte(
		"YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)",
	))
)

func bigTo32(x *big.Int) [32]byte {
	var out [32]byte
	b := x.Bytes()
	if len(b) > 32 {
		panic(fmt.Sprintf("value exceeds 32 bytes: %d bytes", len(b)))
	}
	copy(out[32-len(b):], b)
	return out
}

func keccak256Bytes(s string) [32]byte {
	return crypto.Keccak256Hash([]byte(s))
}

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

func domainSeparator(name, version string, chainID *big.Int, verifyingContract common.Address) common.Hash {
	return crypto.Keccak256Hash(
		abiEncodeWords(
			eip712DomainTypeHash,
			keccak256Bytes(name),
			keccak256Bytes(version),
			bigTo32(chainID),
			common.BytesToHash(verifyingContract.Bytes()),
		),
	)
}

func yieldReportStructHash(apyBps, tvlMilliETH, pointsPerETHppm, timestamp *big.Int) common.Hash {
	return crypto.Keccak256Hash(
		abiEncodeWords(
			yieldReportTypeHash,
			bigTo32(apyBps),
			bigTo32(tvlMilliETH),
			bigTo32(pointsPerETHppm),
			bigTo32(timestamp),
		),
	)
}

func yieldReportDigest(domainSep, structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash(
		[]byte{0x19, 0x01},
		domainSep.Bytes(),
		structHash.Bytes(),
	)
}

func main() {
	// Well-known test key (address 0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf).
	privHex := "0000000000000000000000000000000000000000000000000000000000000001"
	priv, err := crypto.HexToECDSA(privHex)
	if err != nil {
		panic(err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	// EIP-712 domain — MUST match the Solidity domainSeparator() values.
	domainName := "RestakeYieldOracle"
	domainVersion := "1"
	chainID := big.NewInt(1)
	// Default verifyingContract — can be overridden via command-line arg.
	verifyingContract := common.HexToAddress("0x0000000000000000000000000000000000000123")
	if len(os.Args) > 1 {
		arg := strings.TrimSpace(os.Args[1])
		if !common.IsHexAddress(arg) {
			panic(fmt.Errorf("invalid verifying contract address: %s", arg))
		}
		verifyingContract = common.HexToAddress(arg)
	}

	// Report values — MUST match what the Foundry test submits.
	apyBps := big.NewInt(450)            // 4.5%
	tvlMilliETH := big.NewInt(1_000_000) // 1000 ETH
	pointsPerETHppm := big.NewInt(1_100_000) // 1.1
	timestamp := big.NewInt(1_700_000_000)

	domainSep := domainSeparator(domainName, domainVersion, chainID, verifyingContract)
	structHash := yieldReportStructHash(apyBps, tvlMilliETH, pointsPerETHppm, timestamp)
	digest := yieldReportDigest(domainSep, structHash)

	sig, err := crypto.Sign(digest.Bytes(), priv)
	if err != nil {
		panic(err)
	}
	// crypto.Sign returns 65 bytes r||s||v with v in {0,1}.

	out := map[string]interface{}{
		"privateKey":        "0x" + privHex,
		"signer":            addr.Hex(),
		"domain": map[string]interface{}{
			"name":              domainName,
			"version":           domainVersion,
			"chainId":           chainID.String(),
			"verifyingContract": verifyingContract.Hex(),
		},
		"chainId":           chainID.String(),
		"verifyingContract": verifyingContract.Hex(),
		"report": map[string]interface{}{
			"apyBps":          apyBps.String(),
			"tvlMilliETH":     tvlMilliETH.String(),
			"pointsPerETHppm": pointsPerETHppm.String(),
			"timestamp":       timestamp.String(),
		},
		"domainSeparator": hexutil.Encode(domainSep.Bytes()),
		"structHash":      hexutil.Encode(structHash.Bytes()),
		"digest":          hexutil.Encode(digest.Bytes()),
		"signature":       hexutil.Encode(sig),
		"r":               hexutil.Encode(sig[:32]),
		"s":               hexutil.Encode(sig[32:64]),
		"v":               sig[64],
		"signatureHex":    "0x" + hex.EncodeToString(sig),
	}

	root, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("get working directory: %w", err))
	}
	// The fixture must always land at <repo-root>/contracts/test/fixtures/.
	// The repo root is the directory containing foundry.toml. We check the
	// current directory first, then the parent (in case we're in contracts/).
	repoRoot := root
	if _, err := os.Stat(filepath.Join(root, "foundry.toml")); err != nil {
		if _, err := os.Stat(filepath.Join(root, "..", "foundry.toml")); err == nil {
			repoRoot = filepath.Join(root, "..")
		}
	}
	fixtureDir := filepath.Join(repoRoot, "contracts", "test", "fixtures")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		panic(err)
	}
	path := filepath.Join(fixtureDir, "eip712_signature.json")
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		panic(fmt.Errorf("marshal fixture: %w", err))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
	fmt.Println("wrote", path)
	fmt.Println("signer:", addr.Hex())
	fmt.Println("verifyingContract:", verifyingContract.Hex())
	fmt.Println("chainId:", chainID)
	fmt.Println("digest:", hexutil.Encode(digest.Bytes()))
	fmt.Println("signature:", hexutil.Encode(sig))
}
