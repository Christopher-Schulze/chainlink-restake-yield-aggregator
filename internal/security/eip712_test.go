package security

import (
	"math/big"
	"testing"

	crypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/common"
)

// TestEIP712DomainTypeHashMatchesSolidity verifies that the Go-computed
// EIP712Domain type hash matches the hardcoded Solidity constant. The
// expected value is keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)").
func TestEIP712DomainTypeHashMatchesSolidity(t *testing.T) {
	expected := crypto.Keccak256Hash([]byte(
		"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)",
	))
	if EIP712DomainTypeHash != expected {
		t.Fatalf("EIP712DomainTypeHash mismatch:\n  got  %x\n  want %x", EIP712DomainTypeHash, expected)
	}
}

// TestYieldReportTypeHashMatchesSolidity verifies that the Go-computed
// YieldReport type hash matches the hardcoded Solidity constant.
func TestYieldReportTypeHashMatchesSolidity(t *testing.T) {
	expected := crypto.Keccak256Hash([]byte(
		"YieldReport(uint96 apyBps,uint96 tvlMilliETH,uint64 pointsPerETHppm,uint32 timestamp)",
	))
	if YieldReportTypeHash != expected {
		t.Fatalf("YieldReportTypeHash mismatch:\n  got  %x\n  want %x", YieldReportTypeHash, expected)
	}
}

// TestEIP712DomainSeparatorDeterministic verifies that the same domain
// values produce the same separator, and that changing any value
// changes the separator.
func TestEIP712DomainSeparatorDeterministic(t *testing.T) {
	d1 := EIP712Domain{
		Name:              "RestakeYieldOracle",
		Version:           "1",
		ChainID:           big.NewInt(1),
		VerifyingContract: common.HexToAddress("0x0000000000000000000000000000000000000123"),
	}
	d2 := d1
	if d1.Separator() != d2.Separator() {
		t.Fatal("domain separator must be deterministic for the same inputs")
	}

	// Changing chainId changes the separator.
	d2.ChainID = big.NewInt(137)
	if d1.Separator() == d2.Separator() {
		t.Fatal("domain separator must change when chainId changes")
	}

	// Changing verifyingContract changes the separator.
	d2 = d1
	d2.VerifyingContract = common.HexToAddress("0x0000000000000000000000000000000000000456")
	if d1.Separator() == d2.Separator() {
		t.Fatal("domain separator must change when verifyingContract changes")
	}

	// Changing name changes the separator.
	d2 = d1
	d2.Name = "OtherOracle"
	if d1.Separator() == d2.Separator() {
		t.Fatal("domain separator must change when name changes")
	}

	// Changing version changes the separator.
	d2 = d1
	d2.Version = "2"
	if d1.Separator() == d2.Separator() {
		t.Fatal("domain separator must change when version changes")
	}
}

// TestYieldReportDigestDeterministic verifies that the same report +
// domain produce the same digest, and that changing any field changes
// the digest.
func TestYieldReportDigestDeterministic(t *testing.T) {
	domain := EIP712Domain{
		Name:              "RestakeYieldOracle",
		Version:           "1",
		ChainID:           big.NewInt(1),
		VerifyingContract: common.HexToAddress("0x0000000000000000000000000000000000000123"),
	}
	r, err := NewYieldReport(450, 1_000_000, 1_100_000, 1_700_000_000)
	if err != nil {
		t.Fatalf("NewYieldReport: %v", err)
	}

	base := r.Digest(domain)
	r2, _ := NewYieldReport(450, 1_000_000, 1_100_000, 1_700_000_000)
	if base != r2.Digest(domain) {
		t.Fatal("digest must be deterministic for the same inputs")
	}

	// Changing any field changes the digest.
	cases := []struct {
		name   string
		report YieldReport
	}{
		{"apyBps", mustReport(t, 451, 1_000_000, 1_100_000, 1_700_000_000)},
		{"tvlMilliETH", mustReport(t, 450, 1_000_001, 1_100_000, 1_700_000_000)},
		{"pointsPerETHppm", mustReport(t, 450, 1_000_000, 1_100_001, 1_700_000_000)},
		{"timestamp", mustReport(t, 450, 1_000_000, 1_100_000, 1_700_000_001)},
	}
	for _, c := range cases {
		if base == c.report.Digest(domain) {
			t.Fatalf("digest must change when %s changes", c.name)
		}
	}

	// Changing the domain changes the digest.
	otherDomain := domain
	otherDomain.ChainID = big.NewInt(137)
	if base == r.Digest(otherDomain) {
		t.Fatal("digest must change when domain chainId changes")
	}
}

// TestSignOnChainReportRoundTrip signs an EIP-712 report and recovers
// the signer address via ecrecover, verifying the signature is valid
// and matches the signer.
func TestSignOnChainReportRoundTrip(t *testing.T) {
	svc, err := NewDataIntegrityServiceFromKey(
		"0000000000000000000000000000000000000000000000000000000000000001",
		VerificationOptions{SignatureEnabled: true},
	)
	if err != nil {
		t.Fatalf("NewDataIntegrityServiceFromKey: %v", err)
	}

	domain := EIP712Domain{
		Name:              "RestakeYieldOracle",
		Version:           "1",
		ChainID:           big.NewInt(1),
		VerifyingContract: common.HexToAddress("0x0000000000000000000000000000000000000123"),
	}
	report, err := NewYieldReport(450, 1_000_000, 1_100_000, 1_700_000_000)
	if err != nil {
		t.Fatalf("NewYieldReport: %v", err)
	}

	digest, sig, signer, err := svc.SignOnChainReport(report, domain)
	if err != nil {
		t.Fatalf("SignOnChainReport: %v", err)
	}

	// The signer must be the well-known address for key 0x...0001.
	expectedSigner := common.HexToAddress("0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf")
	if signer != expectedSigner {
		t.Fatalf("signer mismatch:\n  got  %s\n  want %s", signer, expectedSigner)
	}

	// ecrecover must return the same address.
	pub, err := crypto.SigToPub(digest.Bytes(), sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	recoveredAddr := crypto.PubkeyToAddress(*pub)
	if recoveredAddr != expectedSigner {
		t.Fatalf("ecrecover mismatch:\n  got  %s\n  want %s", recoveredAddr, expectedSigner)
	}

	// Signature length must be 65 bytes (r||s||v).
	if len(sig) != crypto.SignatureLength {
		t.Fatalf("signature length: got %d, want %d", len(sig), crypto.SignatureLength)
	}

	// v must be 0 or 1 (the Go convention, not 27/28).
	v := sig[64]
	if v != 0 && v != 1 {
		t.Fatalf("v byte: got %d, want 0 or 1", v)
	}
}

// TestSignOnChainReportRejectsValueSubstitution proves that a signature
// for one set of report values does NOT verify for different values.
// This is the core security property of EIP-712 digest binding.
func TestSignOnChainReportRejectsValueSubstitution(t *testing.T) {
	svc, err := NewDataIntegrityServiceFromKey(
		"0000000000000000000000000000000000000000000000000000000000000001",
		VerificationOptions{SignatureEnabled: true},
	)
	if err != nil {
		t.Fatalf("NewDataIntegrityServiceFromKey: %v", err)
	}

	domain := EIP712Domain{
		Name:              "RestakeYieldOracle",
		Version:           "1",
		ChainID:           big.NewInt(1),
		VerifyingContract: common.HexToAddress("0x0000000000000000000000000000000000000123"),
	}
	original, _ := NewYieldReport(450, 1_000_000, 1_100_000, 1_700_000_000)
	tampered, _ := NewYieldReport(999, 1_000_000, 1_100_000, 1_700_000_000)

	_, sig, expectedSigner, err := svc.SignOnChainReport(original, domain)
	if err != nil {
		t.Fatalf("SignOnChainReport: %v", err)
	}

	// Recover against the TAMPERED digest — must NOT match the signer.
	tamperedDigest := tampered.Digest(domain)
	pub, err := crypto.SigToPub(tamperedDigest.Bytes(), sig)
	if err == nil {
		recoveredAddr := crypto.PubkeyToAddress(*pub)
		if recoveredAddr == expectedSigner {
			t.Fatal("signature for original report must NOT verify against tampered values")
		}
	}
}

// TestSignOnChainReportRejectsCrossChainReplay proves that a signature
// for chain 1 does NOT verify on chain 137.
func TestSignOnChainReportRejectsCrossChainReplay(t *testing.T) {
	svc, err := NewDataIntegrityServiceFromKey(
		"0000000000000000000000000000000000000000000000000000000000000001",
		VerificationOptions{SignatureEnabled: true},
	)
	if err != nil {
		t.Fatalf("NewDataIntegrityServiceFromKey: %v", err)
	}

	domain1 := EIP712Domain{
		Name:              "RestakeYieldOracle",
		Version:           "1",
		ChainID:           big.NewInt(1),
		VerifyingContract: common.HexToAddress("0x0000000000000000000000000000000000000123"),
	}
	domain137 := domain1
	domain137.ChainID = big.NewInt(137)

	report, _ := NewYieldReport(450, 1_000_000, 1_100_000, 1_700_000_000)
	_, sig, expectedSigner, err := svc.SignOnChainReport(report, domain1)
	if err != nil {
		t.Fatalf("SignOnChainReport: %v", err)
	}

	// Recover against the chain-137 digest — must NOT match the signer.
	crossChainDigest := report.Digest(domain137)
	pub, err := crypto.SigToPub(crossChainDigest.Bytes(), sig)
	if err == nil {
		recoveredAddr := crypto.PubkeyToAddress(*pub)
		if recoveredAddr == expectedSigner {
			t.Fatal("signature for chain 1 must NOT verify on chain 137")
		}
	}
}

// TestSignOnChainReportRejectsCrossContractReplay proves that a signature
// for contract A does NOT verify for contract B.
func TestSignOnChainReportRejectsCrossContractReplay(t *testing.T) {
	svc, err := NewDataIntegrityServiceFromKey(
		"0000000000000000000000000000000000000000000000000000000000000001",
		VerificationOptions{SignatureEnabled: true},
	)
	if err != nil {
		t.Fatalf("NewDataIntegrityServiceFromKey: %v", err)
	}

	domainA := EIP712Domain{
		Name:              "RestakeYieldOracle",
		Version:           "1",
		ChainID:           big.NewInt(1),
		VerifyingContract: common.HexToAddress("0x0000000000000000000000000000000000000AAA"),
	}
	domainB := domainA
	domainB.VerifyingContract = common.HexToAddress("0x0000000000000000000000000000000000000BBB")

	report, _ := NewYieldReport(450, 1_000_000, 1_100_000, 1_700_000_000)
	_, sig, expectedSigner, err := svc.SignOnChainReport(report, domainA)
	if err != nil {
		t.Fatalf("SignOnChainReport: %v", err)
	}

	crossContractDigest := report.Digest(domainB)
	pub, err := crypto.SigToPub(crossContractDigest.Bytes(), sig)
	if err == nil {
		recoveredAddr := crypto.PubkeyToAddress(*pub)
		if recoveredAddr == expectedSigner {
			t.Fatal("signature for contract A must NOT verify on contract B")
		}
	}
}

// TestNewYieldReportAcceptsUint64Max verifies that the largest uint64
// value for pointsPerETHppm is accepted (it fits in uint64).
func TestNewYieldReportAcceptsUint64Max(t *testing.T) {
	_, err := NewYieldReport(0, 0, 0xFFFFFFFFFFFFFFFF, 0)
	if err != nil {
		t.Fatalf("uint64 max must be accepted: %v", err)
	}
}

// TestEIP712DomainFromHex verifies config-driven domain construction.
func TestEIP712DomainFromHex(t *testing.T) {
	d, err := EIP712DomainFromHex("RestakeYieldOracle", "1", "1", "0x0000000000000000000000000000000000000123")
	if err != nil {
		t.Fatalf("EIP712DomainFromHex: %v", err)
	}
	if d.Name != "RestakeYieldOracle" {
		t.Fatalf("name: got %q", d.Name)
	}
	if d.ChainID.Int64() != 1 {
		t.Fatalf("chainId: got %d", d.ChainID)
	}
	if d.VerifyingContract != common.HexToAddress("0x0000000000000000000000000000000000000123") {
		t.Fatalf("verifyingContract: got %s", d.VerifyingContract)
	}

	// Invalid chain id.
	if _, err := EIP712DomainFromHex("X", "1", "not-a-number", "0x123"); err == nil {
		t.Fatal("expected error for invalid chain id")
	}
	// Invalid address.
	if _, err := EIP712DomainFromHex("X", "1", "1", "not-an-address"); err == nil {
		t.Fatal("expected error for invalid address")
	}
}

// --- helpers ---

func mustReport(t *testing.T, apy, tvl, ppm uint64, ts uint32) YieldReport {
	t.Helper()
	r, err := NewYieldReport(apy, tvl, ppm, ts)
	if err != nil {
		t.Fatalf("NewYieldReport: %v", err)
	}
	return r
}
