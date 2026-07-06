package security

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSvc(t *testing.T, opts VerificationOptions) *DataIntegrityService {
	t.Helper()
	svc, err := NewDataIntegrityService(opts)
	require.NoError(t, err)
	require.NotEmpty(t, svc.Address())
	require.True(t, strings.HasPrefix(svc.Address(), "0x"))
	return svc
}

func TestSignVerifyRoundtrip(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})

	data := []byte("restake-yield-ea payload")
	sig, err := svc.Sign(data)
	require.NoError(t, err)
	require.Len(t, sig, crypto.SignatureLength, "secp256k1 sig must be 65 bytes (r||s||v)")

	assert.True(t, svc.Verify(data, sig), "signature must verify against original data")
	assert.False(t, svc.Verify([]byte("tampered"), sig), "signature must NOT verify against tampered data")
	assert.False(t, svc.Verify(data, append(sig, 0)), "wrong-length sig must be rejected")
}

func TestSignDigestMatchesEthereumScheme(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})

	payload := []byte(`{"apy":0.0345,"tvl":1234.5}`)
	sig, err := svc.SignDigest(payload)
	require.NoError(t, err)

	// Independently recover the signer address via ecrecover and compare.
	digest := crypto.Keccak256(payload)
	pub, err := crypto.Ecrecover(digest, sig)
	require.NoError(t, err)
	pubKey, err := crypto.UnmarshalPubkey(pub)
	require.NoError(t, err)
	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	assert.Equal(t, svc.Address(), recoveredAddr.Hex(),
		"ecrecover must recover the service's own address")
}

func TestSignPayloadEnvelope(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})

	payload := map[string]interface{}{"apy": 0.05, "tvl": 1000.0}
	signed, err := svc.SignPayload(payload)
	require.NoError(t, err)

	sigMeta, ok := signed["_signature"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "secp256k1-keccak256", sigMeta["algorithm"])
	assert.Equal(t, svc.Address(), sigMeta["address"])
	assert.NotEmpty(t, sigMeta["signature"])

	// Round-trip verification.
	ok, err = svc.VerifyPayload(signed)
	require.NoError(t, err)
	assert.True(t, ok)

	// Tampering must break verification.
	signed["apy"] = 0.99
	ok, err = svc.VerifyPayload(signed)
	require.Error(t, err)
	assert.False(t, ok)
}

func TestVerifyPayloadExpired(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})

	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.05})
	require.NoError(t, err)

	// Force the validUntil into the past. The expiry check runs before the
	// signature check, so this triggers the expired path without invalidating
	// the signature.
	sigMeta := signed["_signature"].(map[string]interface{})
	sigMeta["validUntil"] = float64(time.Now().Add(-time.Hour).Unix())

	ok, err := svc.VerifyPayload(signed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
	assert.False(t, ok)
}

func TestCreateTamperProofWrapper(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})

	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04, "tvl": 500.0},
		map[string]interface{}{"request_id": "abc"},
	)
	require.NoError(t, err)

	valid, _, err := svc.VerifyIntegrity(wrapped)
	require.NoError(t, err)
	assert.True(t, valid)

	// Adding an unexpected integrity field changes the wrapper's canonical
	// bytes, invalidating the signature (the keccak256 field is still correct,
	// but the signature no longer matches the modified wrapper).
	integrity := wrapped["integrity"].(map[string]interface{})
	integrity["sha256"] = "0xdeadbeef"
	valid, _, err = svc.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.False(t, valid)
}

// TestCreateTamperProofWrapperStructPayload is a regression test for the
// canonical-JSON bug where struct payloads were signed using declared field
// order while verification re-canonicalised them as sorted maps, breaking the
// signature. The production EA endpoint wraps a ChainlinkResponse struct, so
// this path must round-trip correctly.
func TestCreateTamperProofWrapperStructPayload(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})

	// Mirror the ChainlinkResponse shape used by cmd/server.handleRequest.
	type eaResponse struct {
		JobRunID string                 `json:"jobRunId,omitempty"`
		Status   string                 `json:"status,omitempty"`
		Data     map[string]interface{} `json:"data"`
		Error    string                 `json:"error,omitempty"`
	}
	resp := eaResponse{
		JobRunID: "job-42",
		Status:   "success",
		Data: map[string]interface{}{
			"result":       0.0451,
			"apy":          0.0451,
			"tvl":          1250000.0,
			"pointsPerETH": 1.1,
			"provider":     "aggregated-weighted",
		},
	}

	wrapped, err := svc.CreateTamperProofWrapper(resp, map[string]interface{}{
		"timestamp":  int64(1715003457),
		"source":     "restake-yield-ea",
		"request_id": "trace-abc",
		"job_run_id": "job-42",
	})
	require.NoError(t, err)

	// The signature must verify against the wrapper as returned (which, after
	// SignPayload, is a map[string]interface{} — the same shape a verifier sees).
	valid, _, err := svc.VerifyIntegrity(wrapped)
	require.NoError(t, err)
	assert.True(t, valid, "struct payload must round-trip through sign+verify")

	// Tampering with the payload must break verification.
	payload := wrapped["payload"].(map[string]interface{})
	payload["apy"] = 0.99
	valid, _, err = svc.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.False(t, valid, "tampered struct payload must fail verification")
}

func TestOnChainVerificationData(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})

	bundle, err := svc.OnChainVerificationData(map[string]interface{}{"apy": 0.03})
	require.NoError(t, err)

	sigHex, ok := bundle["signature"].(string)
	require.True(t, ok)
	sigBytes, err := hexDecode(sigHex)
	require.NoError(t, err)
	require.Len(t, sigBytes, crypto.SignatureLength)

	// The keccak256Hash field must match an independent recomputation.
	payloadBytes, _ := json.Marshal(bundle["payload"])
	expected := crypto.Keccak256Hash(payloadBytes).Hex()
	assert.Equal(t, expected, bundle["keccak256Hash"])

	// ecrecover from the bundle must yield the service address.
	digest := crypto.Keccak256(payloadBytes)
	pub, err := crypto.Ecrecover(digest, sigBytes)
	require.NoError(t, err)
	pubKey, err := crypto.UnmarshalPubkey(pub)
	require.NoError(t, err)
	assert.Equal(t, svc.Address(), crypto.PubkeyToAddress(*pubKey).Hex())
}

func TestNewDataIntegrityServiceFromKey(t *testing.T) {
	priv, err := crypto.GenerateKey()
	require.NoError(t, err)
	hexKey := strings.TrimPrefix(hexutil.Encode(crypto.FromECDSA(priv)), "0x")

	svc, err := NewDataIntegrityServiceFromKey(hexKey, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, crypto.PubkeyToAddress(priv.PublicKey).Hex(), svc.Address())
}

// --- additional tests for 100% coverage ---

func TestGetPublicKey(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	pk := svc.GetPublicKey()
	assert.NotEmpty(t, pk)
	// Base64-encoded 65-byte uncompressed public key → 88 chars without padding,
	// or 88 with standard base64 padding.
	decoded, err := base64.StdEncoding.DecodeString(pk)
	require.NoError(t, err)
	assert.Len(t, decoded, 65)
}

func TestSignWith32ByteDigest(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	digest := crypto.Keccak256([]byte("test"))
	sig, err := svc.Sign(digest)
	require.NoError(t, err)
	assert.Len(t, sig, crypto.SignatureLength)
	assert.True(t, svc.Verify(digest, sig))
}

func TestSignNon32ByteData(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	data := []byte("not-a-digest")
	sig, err := svc.Sign(data)
	require.NoError(t, err)
	assert.True(t, svc.Verify(data, sig))
}

func TestVerifyWrongSignatureLength(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	assert.False(t, svc.Verify([]byte("data"), []byte("short")))
}

func TestVerifyTamperedSignature(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	data := []byte("payload")
	sig, err := svc.Sign(data)
	require.NoError(t, err)
	// Tamper with the signature.
	sig[0] ^= 0xFF
	assert.False(t, svc.Verify(data, sig))
}

func TestSignDigestDirect(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	sig, err := svc.SignDigest([]byte("payload"))
	require.NoError(t, err)
	assert.Len(t, sig, crypto.SignatureLength)
}

func TestSignPayloadDisabled(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: false})
	result, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	_, hasSig := result["_signature"]
	assert.False(t, hasSig, "no signature when disabled")
}

func TestSignPayloadEnabled(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	result, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	sigBlock, ok := result["_signature"].(map[string]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, sigBlock["signature"])
	assert.NotEmpty(t, sigBlock["address"])
	assert.Equal(t, "secp256k1-keccak256", sigBlock["algorithm"])
}

func TestVerifyPayloadDisabled(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     false,
		VerificationRequired: false,
	})
	valid, err := svc.VerifyPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	assert.True(t, valid)
}

func TestVerifyPayloadMissingSignature(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		StrictMode:           true,
	})
	valid, err := svc.VerifyPayload(map[string]interface{}{"apy": 0.04})
	assert.Error(t, err)
	assert.False(t, valid)
}

func TestVerifyPayloadMissingSignatureNonStrict(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		StrictMode:           false,
	})
	valid, err := svc.VerifyPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestVerifyPayloadExpiredShortValidity(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    1 * time.Second,
	})
	// Sign and then wait for expiry (validUntil uses Unix() which has
	// second-level granularity, so we need to wait > 1s).
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	time.Sleep(2 * time.Second)
	valid, err := svc.VerifyPayload(signed)
	assert.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "expired")
}

func TestVerifyPayloadInvalidSigFormat(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	// Tamper the signature field to be non-hex.
	sigBlock := signed["_signature"].(map[string]interface{})
	sigBlock["signature"] = "not-hex"
	valid, err := svc.VerifyPayload(signed)
	assert.Error(t, err)
	assert.False(t, valid)
}

func TestVerifyPayloadBadSigHex(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	sigBlock := signed["_signature"].(map[string]interface{})
	sigBlock["signature"] = "0xZZZZ"
	valid, err := svc.VerifyPayload(signed)
	assert.Error(t, err)
	assert.False(t, valid)
}

func TestVerifyPayloadTampered(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	// Tamper with the payload data.
	signed["apy"] = 0.99
	valid, err := svc.VerifyPayload(signed)
	assert.Error(t, err)
	assert.False(t, valid)
}

func TestVerifyPayloadMissingValidUntil(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	sigBlock := signed["_signature"].(map[string]interface{})
	delete(sigBlock, "validUntil")
	valid, err := svc.VerifyPayload(signed)
	assert.Error(t, err)
	assert.False(t, valid)
}

func TestOnChainVerificationDataMap(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	bundle, err := svc.OnChainVerificationData(map[string]interface{}{"apy": 0.04, "tvl": 1000})
	require.NoError(t, err)
	assert.NotEmpty(t, bundle["keccak256Hash"])
	assert.NotEmpty(t, bundle["signature"])
	assert.NotEmpty(t, bundle["publicKey"])
	assert.NotEmpty(t, bundle["signer"])
	assert.NotNil(t, bundle["timestamp"])
	assert.NotNil(t, bundle["payload"])
}

func TestOnChainVerificationDataError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	// Pass a channel (cannot be JSON-marshalled).
	_, err := svc.OnChainVerificationData(make(chan int))
	assert.Error(t, err)
}

func TestCreateTamperProofWrapperWithMetadata(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04, "tvl": 1000},
		map[string]interface{}{"requestId": "req-123"},
	)
	require.NoError(t, err)
	assert.NotNil(t, wrapped["payload"])
	assert.NotNil(t, wrapped["integrity"])
	assert.NotNil(t, wrapped["metadata"])
	assert.NotNil(t, wrapped["_signature"])
}

func TestCreateTamperProofWrapperNoMetadata(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	assert.NotNil(t, wrapped["payload"])
	assert.NotNil(t, wrapped["integrity"])
	_, hasMeta := wrapped["metadata"]
	assert.False(t, hasMeta)
}

func TestVerifyIntegrityValid(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, VerificationRequired: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04, "tvl": 1000},
		nil,
	)
	require.NoError(t, err)

	valid, payload, err := svc.VerifyIntegrity(wrapped)
	require.NoError(t, err)
	assert.True(t, valid)
	assert.NotNil(t, payload["payload"])
}

func TestVerifyIntegrityTamperedPayload(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, VerificationRequired: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04, "tvl": 1000},
		nil,
	)
	require.NoError(t, err)

	// Tamper with the payload — this breaks both the signature and the hash.
	wrapped["payload"] = map[string]interface{}{"apy": 0.99}
	_, _, err = svc.VerifyIntegrity(wrapped)
	assert.Error(t, err)
}

func TestVerifyIntegrityMissingPayload(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, VerificationRequired: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	delete(wrapped, "payload")
	_, _, err = svc.VerifyIntegrity(wrapped)
	assert.Error(t, err)
}

func TestVerifyIntegrityMissingIntegrity(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, VerificationRequired: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	delete(wrapped, "integrity")
	_, _, err = svc.VerifyIntegrity(wrapped)
	assert.Error(t, err)
}

func TestVerifyIntegrityMissingKeccak(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, VerificationRequired: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	integrity := wrapped["integrity"].(map[string]interface{})
	delete(integrity, "keccak256")
	_, _, err = svc.VerifyIntegrity(wrapped)
	assert.Error(t, err)
}

func TestVerifyIntegrityHashMismatch(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, VerificationRequired: true, SignatureValidity: time.Hour})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	// Replace the integrity hash with a wrong one. Since the signature covers
	// the entire wrapper (including integrity), this also breaks the signature.
	// The error will be "signature verification failed" rather than "hash mismatch".
	integrity := wrapped["integrity"].(map[string]interface{})
	integrity["keccak256"] = "0x0000000000000000000000000000000000000000000000000000000000000000"
	_, _, err = svc.VerifyIntegrity(wrapped)
	assert.Error(t, err)
}

func TestToMap(t *testing.T) {
	// Direct map passthrough.
	m := map[string]interface{}{"key": "value"}
	result, err := toMap(m)
	require.NoError(t, err)
	assert.Equal(t, m, result)
}

func TestToMapFromStruct(t *testing.T) {
	type testStruct struct {
		A string `json:"a"`
		B int    `json:"b"`
	}
	result, err := toMap(testStruct{A: "hello", B: 42})
	require.NoError(t, err)
	assert.Equal(t, "hello", result["a"])
	assert.EqualValues(t, 42, result["b"])
}

func TestToMapInvalidJSON(t *testing.T) {
	_, err := toMap(make(chan int))
	assert.Error(t, err)
}

func TestHexDecode(t *testing.T) {
	// With 0x prefix.
	b, err := hexDecode("0xdeadbeef")
	require.NoError(t, err)
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, b)

	// Without 0x prefix.
	b, err = hexDecode("deadbeef")
	require.NoError(t, err)
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, b)

	// With 0X prefix.
	b, err = hexDecode("0Xdeadbeef")
	require.NoError(t, err)
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, b)

	// Odd-length hex string (should be left-padded with 0).
	b, err = hexDecode("0xabc")
	require.NoError(t, err)
	assert.Equal(t, []byte{0x0a, 0xbc}, b)

	// Whitespace should be trimmed.
	b, err = hexDecode("  0xdeadbeef  ")
	require.NoError(t, err)
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, b)
}

func TestHexDecodeInvalid(t *testing.T) {
	_, err := hexDecode("0xZZZZ")
	assert.Error(t, err)
}

func TestToUnixFloat64(t *testing.T) {
	v, ok := toUnix(float64(1700000000))
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000), v)
}

func TestToUnixInt64(t *testing.T) {
	v, ok := toUnix(int64(1700000000))
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000), v)
}

func TestToUnixInt(t *testing.T) {
	v, ok := toUnix(1700000000)
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000), v)
}

func TestToUnixJSONNumber(t *testing.T) {
	n := json.Number("1700000000")
	v, ok := toUnix(n)
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000), v)
}

func TestToUnixInvalidType(t *testing.T) {
	_, ok := toUnix("not-a-number")
	assert.False(t, ok)
}

func TestToUnixInvalidJSONNumber(t *testing.T) {
	n := json.Number("not-a-number")
	_, ok := toUnix(n)
	assert.False(t, ok)
}

func TestCanonicalJSONMap(t *testing.T) {
	// Keys should be sorted.
	data := map[string]interface{}{"b": 2, "a": 1, "c": 3}
	b, err := canonicalJSON(data)
	require.NoError(t, err)
	// "a" should come before "b" before "c".
	s := string(b)
	assert.Contains(t, s, `"a":1`)
	assert.True(t, strings.Index(s, `"a":`) < strings.Index(s, `"b":`))
	assert.True(t, strings.Index(s, `"b":`) < strings.Index(s, `"c":`))
}

func TestCanonicalJSONArray(t *testing.T) {
	data := map[string]interface{}{"arr": []interface{}{1, 2, 3}}
	b, err := canonicalJSON(data)
	require.NoError(t, err)
	assert.Contains(t, string(b), `[1,2,3]`)
}

func TestCanonicalJSONNested(t *testing.T) {
	data := map[string]interface{}{
		"outer": map[string]interface{}{
			"z": 1,
			"a": 2,
		},
	}
	b, err := canonicalJSON(data)
	require.NoError(t, err)
	s := string(b)
	assert.True(t, strings.Index(s, `"a":2`) < strings.Index(s, `"z":1`))
}

func TestCanonicalJSONError(t *testing.T) {
	_, err := canonicalJSON(make(chan int))
	assert.Error(t, err)
}

func TestSecureNonce(t *testing.T) {
	n1, err := secureNonce()
	require.NoError(t, err)
	assert.True(t, n1 >= 0, "nonce must be non-negative")

	n2, err := secureNonce()
	require.NoError(t, err)
	assert.NotEqual(t, n1, n2, "nonces must be unique")
}

func TestNewDataIntegrityServiceFromKeyInvalid(t *testing.T) {
	_, err := NewDataIntegrityServiceFromKey("not-a-valid-key", VerificationOptions{})
	assert.Error(t, err)
}

func TestNewDataIntegrityServiceFromKeyWith0xPrefix(t *testing.T) {
	priv, err := crypto.GenerateKey()
	require.NoError(t, err)
	hexKey := hexutil.Encode(crypto.FromECDSA(priv)) // includes 0x prefix
	svc, err := NewDataIntegrityServiceFromKey(hexKey, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, crypto.PubkeyToAddress(priv.PublicKey).Hex(), svc.Address())
}

// --- additional branch-coverage tests ---

// TestSignPayloadUnmarshallable covers the SignPayload canonical-marshal error
// path (data_integrity.go ~145-148) and, indirectly, the canonicalJSON marshal
// error path (~357-360): a channel cannot be JSON-marshalled.
func TestSignPayloadUnmarshallable(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	_, err := svc.SignPayload(make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canonical marshal")
}

// TestVerifyPayloadNonStringSignature covers the non-string signature branch
// (data_integrity.go ~206-209): the signature metadata field is a number
// instead of a string.
func TestVerifyPayloadNonStringSignature(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	sigBlock := signed["_signature"].(map[string]interface{})
	sigBlock["signature"] = 12345 // wrong type
	valid, err := svc.VerifyPayload(signed)
	require.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "invalid signature format")
}

// TestVerifyPayloadEcrecoverFailed covers the ecrecover error branch
// (data_integrity.go ~236-237): a valid-hex signature of the wrong length
// causes crypto.Ecrecover to fail.
func TestVerifyPayloadEcrecoverFailed(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	sigBlock := signed["_signature"].(map[string]interface{})
	// Valid hex but only 2 bytes — ecrecover expects a 65-byte signature.
	sigBlock["signature"] = "0x1234"
	valid, err := svc.VerifyPayload(signed)
	require.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "ecrecover failed")
}

// TestVerifyPayloadCanonicalMarshalError covers the canonical-marshal error
// branch inside VerifyPayload (data_integrity.go ~231-232): a payload value
// that cannot be re-canonicalised (a channel) triggers the error after the
// signature format/expiry checks pass.
func TestVerifyPayloadCanonicalMarshalError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		SignatureValidity:    time.Hour,
	})
	signed, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.NoError(t, err)
	// Inject an unmarshallable value into the payload body. The signature and
	// validUntil fields remain valid, so execution reaches canonicalJSON.
	signed["bad"] = make(chan int)
	valid, err := svc.VerifyPayload(signed)
	require.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "canonical marshal")
}

// TestVerifyIntegrityPayloadMissingNoSig covers the "payload missing" branch
// (data_integrity.go ~302-305). With signature verification disabled,
// VerifyPayload is a no-op so execution reaches the payload-presence check.
func TestVerifyIntegrityPayloadMissingNoSig(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: false, VerificationRequired: false})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	delete(wrapped, "payload")
	_, _, err = svc.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payload missing")
}

// TestVerifyIntegrityIntegrityMissingNoSig covers the "integrity information
// missing" branch (data_integrity.go ~306-309).
func TestVerifyIntegrityIntegrityMissingNoSig(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: false, VerificationRequired: false})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	delete(wrapped, "integrity")
	_, _, err = svc.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrity information missing")
}

// TestVerifyIntegrityKeccakMissingNoSig covers the "keccak256 hash missing"
// branch (data_integrity.go ~310-313).
func TestVerifyIntegrityKeccakMissingNoSig(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: false, VerificationRequired: false})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	integrity := wrapped["integrity"].(map[string]interface{})
	delete(integrity, "keccak256")
	_, _, err = svc.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keccak256 hash missing")
}

// TestVerifyIntegrityHashMismatchNoSig covers the keccak256 hash-mismatch
// branch (data_integrity.go ~320-321). With signature verification disabled,
// execution reaches the hash comparison.
func TestVerifyIntegrityHashMismatchNoSig(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: false, VerificationRequired: false})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	integrity := wrapped["integrity"].(map[string]interface{})
	integrity["keccak256"] = "0x0000000000000000000000000000000000000000000000000000000000000000"
	_, _, err = svc.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keccak256 hash mismatch")
}

// TestVerifyIntegrityCanonicalMarshalError covers the canonical-marshal error
// branch inside VerifyIntegrity (data_integrity.go ~316-317): an unmarshallable
// payload triggers the error after the payload-presence check.
func TestVerifyIntegrityCanonicalMarshalError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: false, VerificationRequired: false})
	wrapped, err := svc.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	wrapped["payload"] = make(chan int)
	_, _, err = svc.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canonical marshal")
}

// TestVerifyIntegrityInvalidSignatureNonStrict covers the "invalid signature"
// branch (data_integrity.go ~298-299): in non-strict mode a missing signature
// makes VerifyPayload return (false, nil), which VerifyIntegrity surfaces as
// "invalid signature".
func TestVerifyIntegrityInvalidSignatureNonStrict(t *testing.T) {
	signer := newSvc(t, VerificationOptions{
		SignatureEnabled:     true,
		VerificationRequired: true,
		StrictMode:           false,
		SignatureValidity:    time.Hour,
	})
	// Build a wrapper that has payload + integrity but no _signature envelope.
	verifier := newSvc(t, VerificationOptions{SignatureEnabled: false, VerificationRequired: false})
	wrapped, err := verifier.CreateTamperProofWrapper(
		map[string]interface{}{"apy": 0.04},
		nil,
	)
	require.NoError(t, err)
	_, _, err = signer.VerifyIntegrity(wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

// TestCreateTamperProofWrapperUnmarshallable covers the canonical-marshal error
// path in CreateTamperProofWrapper (data_integrity.go ~273-274).
func TestCreateTamperProofWrapperUnmarshallable(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	_, err := svc.CreateTamperProofWrapper(make(chan int), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canonical marshal")
}

// TestCanonicalMarshalMapWithChannel covers the canonicalMarshal map-value
// error branch (data_integrity.go ~388-391): a map value that cannot be
// marshalled propagates the error.
func TestCanonicalMarshalMapWithChannel(t *testing.T) {
	_, err := canonicalMarshal(map[string]interface{}{"k": make(chan int)})
	require.Error(t, err)
}

// TestCanonicalMarshalSliceWithChannel covers the canonicalMarshal slice-element
// error branch (data_integrity.go ~403-405): a slice element that cannot be
// marshalled propagates the error.
func TestCanonicalMarshalSliceWithChannel(t *testing.T) {
	_, err := canonicalMarshal([]interface{}{make(chan int)})
	require.Error(t, err)
}

// TestToMapNonObject covers the json.Unmarshal error branch in toMap
// (data_integrity.go ~342-343): a non-object value (e.g. an integer) marshals
// to valid JSON that cannot be unmarshalled into a map.
func TestToMapNonObject(t *testing.T) {
	_, err := toMap(42)
	require.Error(t, err)
}

// --- error-path tests using injectable crypto variables ---

// errReader is an io.Reader that always returns an error.
type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, errors.New("rand read failed") }

// withRandReader swaps the package-level randReader for the duration of the
// test and restores it on cleanup.
func withRandReader(t *testing.T, r io.Reader) {
	t.Helper()
	orig := randReader
	randReader = r
	t.Cleanup(func() { randReader = orig })
}

// withGenerateKey swaps the package-level generateKeyFunc for the duration of
// the test and restores it on cleanup.
func withGenerateKey(t *testing.T, fn func() (*ecdsa.PrivateKey, error)) {
	t.Helper()
	orig := generateKeyFunc
	generateKeyFunc = fn
	t.Cleanup(func() { generateKeyFunc = orig })
}

// withSignFunc swaps the package-level signFunc for the duration of the test
// and restores it on cleanup.
func withSignFunc(t *testing.T, fn func(hash []byte, prv *ecdsa.PrivateKey) ([]byte, error)) {
	t.Helper()
	orig := signFunc
	signFunc = fn
	t.Cleanup(func() { signFunc = orig })
}

// TestSecureNonceRandReadError covers the crypto/rand.Read failure path in
// secureNonce (data_integrity.go ~201-203).
func TestSecureNonceRandReadError(t *testing.T) {
	withRandReader(t, errReader{})
	_, err := secureNonce()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rand read failed")
}

// TestNewDataIntegrityServiceGenerateKeyError covers the crypto.GenerateKey
// failure path in NewDataIntegrityService (data_integrity.go ~66-69).
func TestNewDataIntegrityServiceGenerateKeyError(t *testing.T) {
	withGenerateKey(t, func() (*ecdsa.PrivateKey, error) {
		return nil, errors.New("key generation failed")
	})
	_, err := NewDataIntegrityService(VerificationOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to generate secp256k1 key")
}

// TestSignCryptoSignError covers the crypto.Sign failure path in Sign
// (data_integrity.go ~121-124).
func TestSignCryptoSignError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	withSignFunc(t, func(hash []byte, prv *ecdsa.PrivateKey) ([]byte, error) {
		return nil, errors.New("sign failed")
	})
	_, err := svc.Sign([]byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature failed")
}

// TestSignDigestCryptoSignError covers the crypto.Sign failure path in
// SignDigest (data_integrity.go ~130-131).
func TestSignDigestCryptoSignError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	withSignFunc(t, func(hash []byte, prv *ecdsa.PrivateKey) ([]byte, error) {
		return nil, errors.New("sign failed")
	})
	_, err := svc.SignDigest([]byte("payload"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sign failed")
}

// TestSignPayloadNonceError covers the nonce-generation failure path in
// SignPayload (data_integrity.go ~182-184). The signature succeeds but the
// nonce generation fails, so the whole call must fail.
func TestSignPayloadNonceError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	withRandReader(t, errReader{})
	_, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to generate nonce")
}

// TestOnChainVerificationDataSignError covers the crypto.Sign failure path in
// OnChainVerificationData (data_integrity.go ~274-277).
func TestOnChainVerificationDataSignError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	withSignFunc(t, func(hash []byte, prv *ecdsa.PrivateKey) ([]byte, error) {
		return nil, errors.New("sign failed")
	})
	_, err := svc.OnChainVerificationData(map[string]interface{}{"apy": 0.04})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to sign")
}

// TestCanonicalJSONUnmarshalError covers the json.Unmarshal error branch in
// canonicalJSON (data_integrity.go ~387-389). This branch is normally
// unreachable because json.Marshal produces valid JSON that json.Unmarshal
// can always parse. We trigger it by swapping jsonUnmarshalFunc to always
// return an error.
func TestCanonicalJSONUnmarshalError(t *testing.T) {
	orig := jsonUnmarshalFunc
	jsonUnmarshalFunc = func(data []byte, v interface{}) error {
		return errors.New("unmarshal failed")
	}
	t.Cleanup(func() { jsonUnmarshalFunc = orig })

	_, err := canonicalJSON(map[string]interface{}{"a": 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canonical unmarshal")
}

// TestCanonicalMarshalKeyMarshalError covers the json.Marshal(k) error branch
// in canonicalMarshal (data_integrity.go ~407-410). Since map keys are always
// strings, json.Marshal never fails on them in normal operation. We trigger
// it by swapping jsonMarshalFunc to return an error when marshalling a string
// key.
func TestCanonicalMarshalKeyMarshalError(t *testing.T) {
	orig := jsonMarshalFunc
	jsonMarshalFunc = func(v interface{}) ([]byte, error) {
		if _, ok := v.(string); ok {
			return nil, errors.New("marshal key failed")
		}
		return json.Marshal(v)
	}
	t.Cleanup(func() { jsonMarshalFunc = orig })

	_, err := canonicalMarshal(map[string]interface{}{"k": 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal key failed")
}

// TestSignPayloadSignDigestError covers the SignDigest failure path in
// SignPayload (data_integrity.go ~175-177). The canonical marshal succeeds
// but the signing fails.
func TestSignPayloadSignDigestError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	withSignFunc(t, func(hash []byte, prv *ecdsa.PrivateKey) ([]byte, error) {
		return nil, errors.New("sign failed")
	})
	_, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature generation failed")
}

// TestSignPayloadUnmarshalError covers the json.Unmarshal failure path in
// SignPayload (data_integrity.go ~181-183). The canonical marshal and signing
// both succeed, but unmarshalling the canonical bytes into a map fails.
func TestSignPayloadUnmarshalError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	orig := jsonUnmarshalFunc
	jsonUnmarshalFunc = func(data []byte, v interface{}) error {
		// Only fail for the SignPayload unmarshal (which targets a
		// *map[string]interface{}); let canonicalJSON's unmarshal proceed.
		if _, ok := v.(*map[string]interface{}); ok {
			return errors.New("unmarshal payload failed")
		}
		return json.Unmarshal(data, v)
	}
	t.Cleanup(func() { jsonUnmarshalFunc = orig })

	_, err := svc.SignPayload(map[string]interface{}{"apy": 0.04})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal payload")
}

// TestVerifyEcrecoverError covers the crypto.Ecrecover failure path in Verify
// (data_integrity.go ~153-155). A 65-byte signature with an invalid v value
// (not 0 or 1) causes Ecrecover to fail.
func TestVerifyEcrecoverError(t *testing.T) {
	svc := newSvc(t, VerificationOptions{SignatureEnabled: true, SignatureValidity: time.Hour})
	// Build a 65-byte signature with an invalid v value (255). Ecrecover
	// expects v to be 0 or 1 (or 27/28); any other value causes it to fail.
	badSig := make([]byte, crypto.SignatureLength)
	badSig[crypto.SignatureLength-1] = 255 // invalid v
	assert.False(t, svc.Verify([]byte("data"), badSig))
}
