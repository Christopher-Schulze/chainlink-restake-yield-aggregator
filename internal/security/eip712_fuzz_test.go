package security

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// FuzzEIP712DigestDeterminism verifies that the EIP-712 digest is
// deterministic for the same inputs — different inputs must produce
// different digests (with overwhelming probability). This is a security
// property: if two different reports produce the same digest, an attacker
// could substitute one for the other.
func FuzzEIP712DigestDeterminism(f *testing.F) {
	f.Add(uint64(450), uint64(1_000_000), uint64(1_100_000), uint64(1_700_000_000))
	f.Add(uint64(451), uint64(1_000_000), uint64(1_100_000), uint64(1_700_000_000))
	f.Add(uint64(450), uint64(1_000_001), uint64(1_100_000), uint64(1_700_000_000))
	f.Add(uint64(0), uint64(0), uint64(0), uint64(0))
	f.Add(^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0))

	f.Fuzz(func(t *testing.T, apyBps, tvlMilliETH, pointsPerETHppm, timestamp uint64) {
		chainID := big.NewInt(1)
		verifyingContract := common.HexToAddress("0x0000000000000000000000000000000000000123")
		domain := EIP712Domain{
			Name:              "RestakeYieldOracle",
			Version:           "1",
			ChainID:           chainID,
			VerifyingContract: verifyingContract,
		}

		report1 := YieldReport{
			APYBps:          new(big.Int).SetUint64(apyBps),
			TVLMilliETH:     new(big.Int).SetUint64(tvlMilliETH),
			PointsPerETHppm: new(big.Int).SetUint64(pointsPerETHppm),
			Timestamp:       new(big.Int).SetUint64(timestamp),
		}
		report2 := YieldReport{
			APYBps:          new(big.Int).SetUint64(apyBps),
			TVLMilliETH:     new(big.Int).SetUint64(tvlMilliETH),
			PointsPerETHppm: new(big.Int).SetUint64(pointsPerETHppm),
			Timestamp:       new(big.Int).SetUint64(timestamp),
		}

		digest1 := report1.Digest(domain)
		digest2 := report2.Digest(domain)

		// Same inputs → same digest.
		if digest1 != digest2 {
			t.Fatalf("digest not deterministic: %s vs %s",
				hexutil.Encode(digest1.Bytes()), hexutil.Encode(digest2.Bytes()))
		}

		// Different timestamp → different digest.
		if timestamp < ^uint64(0) {
			report3 := YieldReport{
				APYBps:          new(big.Int).SetUint64(apyBps),
				TVLMilliETH:     new(big.Int).SetUint64(tvlMilliETH),
				PointsPerETHppm: new(big.Int).SetUint64(pointsPerETHppm),
				Timestamp:       new(big.Int).SetUint64(timestamp + 1),
			}
			digest3 := report3.Digest(domain)
			if digest1 == digest3 {
				t.Fatalf("different timestamp produced same digest: %s",
					hexutil.Encode(digest1.Bytes()))
			}
		}
	})
}

// FuzzEIP712SignatureRecovery verifies that a signature produced by
// crypto.Sign can always be recovered to the correct signer address
// from the EIP-712 digest. This ensures the signing and recovery paths
// are consistent across all input values.
func FuzzEIP712SignatureRecovery(f *testing.F) {
	f.Add(uint64(450), uint64(1_000_000), uint64(1_100_000), uint64(1_700_000_000))
	f.Add(uint64(0), uint64(0), uint64(0), uint64(1))
	f.Add(uint64(10000), uint64(999_999_999), uint64(5_000_000), uint64(2_000_000_000))

	f.Fuzz(func(t *testing.T, apyBps, tvlMilliETH, pointsPerETHppm, timestamp uint64) {
		privKey, err := crypto.GenerateKey()
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		expectedAddr := crypto.PubkeyToAddress(privKey.PublicKey)

		chainID := big.NewInt(1)
		verifyingContract := common.HexToAddress("0x0000000000000000000000000000000000000123")
		domain := EIP712Domain{
			Name:              "RestakeYieldOracle",
			Version:           "1",
			ChainID:           chainID,
			VerifyingContract: verifyingContract,
		}

		report := YieldReport{
			APYBps:          new(big.Int).SetUint64(apyBps),
			TVLMilliETH:     new(big.Int).SetUint64(tvlMilliETH),
			PointsPerETHppm: new(big.Int).SetUint64(pointsPerETHppm),
			Timestamp:       new(big.Int).SetUint64(timestamp),
		}

		digest := report.Digest(domain)
		sig, err := crypto.Sign(digest.Bytes(), privKey)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}

		pubKey, err := crypto.SigToPub(digest.Bytes(), sig)
		if err != nil {
			t.Fatalf("recover: %v", err)
		}
		recoveredAddr := crypto.PubkeyToAddress(*pubKey)

		if recoveredAddr != expectedAddr {
			t.Fatalf("recovered %s, expected %s (sig=%s)",
				recoveredAddr.Hex(), expectedAddr.Hex(),
				"0x"+hex.EncodeToString(sig))
		}
	})
}

// FuzzEIP712DomainSeparation verifies that changing the chainId or
// verifyingContract produces a different digest. This is the cross-chain
// and cross-contract replay protection property.
func FuzzEIP712DomainSeparation(f *testing.F) {
	f.Add(uint64(1), uint64(137), "0x0000000000000000000000000000000000000123", "0x0000000000000000000000000000000000000456")
	f.Add(uint64(1), uint64(1), "0x0000000000000000000000000000000000000123", "0x0000000000000000000000000000000000000456")

	f.Fuzz(func(t *testing.T, chainID1, chainID2 uint64, addr1Hex, addr2Hex string) {
		if len(addr1Hex) < 2 || len(addr2Hex) < 2 {
			return
		}

		addr1 := common.HexToAddress(addr1Hex)
		addr2 := common.HexToAddress(addr2Hex)

		report := YieldReport{
			APYBps:          big.NewInt(450),
			TVLMilliETH:     big.NewInt(1_000_000),
			PointsPerETHppm: big.NewInt(1_100_000),
			Timestamp:       big.NewInt(1_700_000_000),
		}

		domain1 := EIP712Domain{
			Name:              "RestakeYieldOracle",
			Version:           "1",
			ChainID:           big.NewInt(int64(chainID1)),
			VerifyingContract: addr1,
		}
		domain2 := EIP712Domain{
			Name:              "RestakeYieldOracle",
			Version:           "1",
			ChainID:           big.NewInt(int64(chainID2)),
			VerifyingContract: addr2,
		}

		digest1 := report.Digest(domain1)
		digest2 := report.Digest(domain2)

		sameDomain := chainID1 == chainID2 && addr1 == addr2
		if sameDomain && digest1 != digest2 {
			t.Fatalf("same domain produced different digests")
		}
		if !sameDomain && digest1 == digest2 {
			t.Fatalf("different domain produced same digest (replay possible)")
		}
	})
}
