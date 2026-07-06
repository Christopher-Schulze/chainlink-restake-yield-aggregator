// Package security provides cryptographic verification and data integrity
// features for the restake-yield-ea. Signatures use the secp256k1 curve and
// Ethereum's signing scheme (keccak256 hash + 65-byte r||s||v signature) so
// that payloads can be verified on-chain by Solidity's ecrecover.
package security

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	crypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/sirupsen/logrus"
)

// Indirection variables allow tests to inject failures into the crypto
// primitives and JSON operations that are otherwise impossible to make fail
// in normal operation. They default to the real implementations so
// production behaviour is unchanged.
var (
	// randReader is the source of randomness for secureNonce. Tests can swap
	// it for a reader that returns an error to exercise the failure path.
	randReader = rand.Reader
	// generateKeyFunc generates a new secp256k1 private key. Tests can swap
	// it to return an error to exercise the NewDataIntegrityService failure
	// path.
	generateKeyFunc = crypto.GenerateKey
	// signFunc signs a digest with a private key using the Ethereum scheme.
	// Tests can swap it to return an error to exercise the Sign/OnChain
	// failure paths.
	signFunc = crypto.Sign
	// jsonMarshalFunc marshals a value to JSON. Tests can swap it to return
	// an error to exercise defensive error branches in canonicalMarshal.
	jsonMarshalFunc = json.Marshal
	// jsonUnmarshalFunc unmarshals JSON into a value. Tests can swap it to
	// return an error to exercise the defensive error branch in canonicalJSON.
	jsonUnmarshalFunc = json.Unmarshal
)

// DataIntegrityService signs yield payloads and verifies their authenticity.
// It uses secp256k1 (the Ethereum curve) so signatures are compatible with
// Solidity's ecrecover precompile and the bundled YieldVerifier contract.
type DataIntegrityService struct {
	privateKey       *ecdsa.PrivateKey
	address          string // 0x-prefixed Ethereum address of the signer
	publicKeyEncoded string // base64 of the uncompressed 65-byte public key
	verificationOpts VerificationOptions
}

// VerificationOptions configures the behaviour of data integrity checks.
type VerificationOptions struct {
	SignatureEnabled     bool          `json:"signature_enabled"`
	VerificationRequired bool          `json:"verification_required"`
	SignatureValidity    time.Duration `json:"signature_validity"`
	StrictMode           bool          `json:"strict_mode"`
}

// NewDataIntegrityService generates a fresh secp256k1 key pair and returns a
// service wrapping it. Keys are ephemeral by default; persist the private key
// externally (via NewDataIntegrityServiceFromKey) if cross-restart signature
// continuity or on-chain verification is required.
func NewDataIntegrityService(opts VerificationOptions) (*DataIntegrityService, error) {
	priv, err := generateKeyFunc()
	if err != nil {
		return nil, fmt.Errorf("failed to generate secp256k1 key: %w", err)
	}

	pubBytes := crypto.FromECDSAPub(&priv.PublicKey)
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	svc := &DataIntegrityService{
		privateKey:       priv,
		address:          addr.Hex(),
		publicKeyEncoded: base64.StdEncoding.EncodeToString(pubBytes),
		verificationOpts: opts,
	}

	logrus.WithFields(logrus.Fields{
		"address": addr.Hex(),
	}).Info("data integrity service initialized (ephemeral key)")
	return svc, nil
}

// NewDataIntegrityServiceFromKey constructs a service from an existing hex
// private key (0x-prefixed or bare). Use this for deterministic deployments
// where the signer address must be stable across restarts.
func NewDataIntegrityServiceFromKey(hexKey string, opts VerificationOptions) (*DataIntegrityService, error) {
	hexKey = strings.TrimPrefix(strings.TrimSpace(hexKey), "0x")
	priv, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	pubBytes := crypto.FromECDSAPub(&priv.PublicKey)
	return &DataIntegrityService{
		privateKey:       priv,
		address:          crypto.PubkeyToAddress(priv.PublicKey).Hex(),
		publicKeyEncoded: base64.StdEncoding.EncodeToString(pubBytes),
		verificationOpts: opts,
	}, nil
}

// Address returns the 0x-prefixed Ethereum address of the signer.
func (s *DataIntegrityService) Address() string { return s.address }

// GetPublicKey returns the base64-encoded uncompressed (65-byte) public key.
func (s *DataIntegrityService) GetPublicKey() string { return s.publicKeyEncoded }

// Sign signs a 32-byte digest using the Ethereum scheme and returns a 65-byte
// r||s||v signature. The input is hashed with keccak256 first if it is not
// already 32 bytes (callers passing a pre-computed digest should use SignDigest).
func (s *DataIntegrityService) Sign(data []byte) ([]byte, error) {
	var digest []byte
	if len(data) == 32 {
		digest = data
	} else {
		digest = crypto.Keccak256(data)
	}
	sig, err := signFunc(digest, s.privateKey)
	if err != nil {
		return nil, fmt.Errorf("signature failed: %w", err)
	}
	return sig, nil
}

// SignDigest signs an arbitrary payload by keccak256-hashing it first.
func (s *DataIntegrityService) SignDigest(payload []byte) ([]byte, error) {
	digest := crypto.Keccak256(payload)
	return signFunc(digest, s.privateKey)
}

// Verify checks a 65-byte r||s||v signature against the signer's public key
// for the given data. data is keccak256-hashed first unless it is already
// 32 bytes. Uses constant-time comparison.
func (s *DataIntegrityService) Verify(data, signature []byte) bool {
	if len(signature) != crypto.SignatureLength {
		return false
	}
	var digest []byte
	if len(data) == 32 {
		digest = data
	} else {
		digest = crypto.Keccak256(data)
	}
	recovered, err := crypto.Ecrecover(digest, signature)
	if err != nil {
		return false
	}
	expected := crypto.FromECDSAPub(&s.privateKey.PublicKey)
	return subtle.ConstantTimeCompare(recovered, expected) == 1
}

// SignPayload adds a cryptographic signature envelope to a payload, returning
// a map ready to be JSON-encoded. The signature covers the canonical JSON of
// the payload (without the _signature field). A nonce is included for replay
// protection; verifiers should track seen nonces within the validity window.
func (s *DataIntegrityService) SignPayload(payload interface{}) (map[string]interface{}, error) {
	if !s.verificationOpts.SignatureEnabled {
		return toMap(payload)
	}

	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("canonical marshal: %w", err)
	}

	sig, err := s.SignDigest(payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("signature generation failed: %w", err)
	}

	var result map[string]interface{}
	if err := jsonUnmarshalFunc(payloadBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	now := time.Now()
	nonce, err := secureNonce()
	if err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	result["_signature"] = map[string]interface{}{
		"signature":  fmt.Sprintf("0x%x", sig),
		"publicKey":  s.publicKeyEncoded,
		"address":    s.address,
		"algorithm":  "secp256k1-keccak256",
		"timestamp":  now.Unix(),
		"validUntil": now.Add(s.verificationOpts.SignatureValidity).Unix(),
		"nonce":      fmt.Sprintf("%016x", nonce),
	}
	return result, nil
}

// secureNonce generates a cryptographically secure random 63-bit positive
// integer suitable for replay-protection nonces. It uses crypto/rand rather
// than math/rand to ensure unpredictability.
func secureNonce() (int64, error) {
	var b [8]byte
	if _, err := randReader.Read(b[:]); err != nil {
		return 0, err
	}
	// Mask the high bit to guarantee a non-negative int64.
	v := binary.BigEndian.Uint64(b[:]) & 0x7fffffffffffffff
	return int64(v), nil //nolint:gosec // G115: masked value is guaranteed < 2^63
}

// VerifyPayload verifies the _signature envelope on a signed payload map.
// It checks expiry, then re-canonicalises the payload (without _signature) and
// compares the recovered public key in constant time.
func (s *DataIntegrityService) VerifyPayload(signedPayload map[string]interface{}) (bool, error) {
	if !s.verificationOpts.SignatureEnabled || !s.verificationOpts.VerificationRequired {
		return true, nil
	}

	sigMeta, ok := signedPayload["_signature"].(map[string]interface{})
	if !ok {
		if s.verificationOpts.StrictMode {
			return false, fmt.Errorf("signature metadata missing")
		}
		logrus.Warn("signature metadata missing from payload")
		return false, nil
	}

	sigStr, ok := sigMeta["signature"].(string)
	if !ok {
		return false, fmt.Errorf("invalid signature format")
	}
	sigBytes, err := hexDecode(sigStr)
	if err != nil {
		return false, fmt.Errorf("failed to decode signature: %w", err)
	}

	validUntil, ok := toUnix(sigMeta["validUntil"])
	if !ok {
		return false, fmt.Errorf("invalid validUntil format")
	}
	if time.Now().Unix() > validUntil {
		return false, fmt.Errorf("signature expired at %v", time.Unix(validUntil, 0))
	}

	// Rebuild the canonical payload (without _signature) and hash it.
	payloadCopy := make(map[string]interface{}, len(signedPayload))
	for k, v := range signedPayload {
		if k != "_signature" {
			payloadCopy[k] = v
		}
	}
	payloadBytes, err := canonicalJSON(payloadCopy)
	if err != nil {
		return false, fmt.Errorf("canonical marshal: %w", err)
	}

	digest := crypto.Keccak256(payloadBytes)
	recovered, err := crypto.Ecrecover(digest, sigBytes)
	if err != nil {
		return false, fmt.Errorf("ecrecover failed: %w", err)
	}
	expected := crypto.FromECDSAPub(&s.privateKey.PublicKey)
	if subtle.ConstantTimeCompare(recovered, expected) != 1 {
		return false, fmt.Errorf("cryptographic verification failed")
	}
	return true, nil
}

// OnChainVerificationData produces a payload bundle that can be verified
// on-chain by the YieldVerifier contract via ecrecover.
func (s *DataIntegrityService) OnChainVerificationData(payload interface{}) (map[string]interface{}, error) {
	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("canonical marshal: %w", err)
	}
	digest := crypto.Keccak256(payloadBytes)
	sig, err := signFunc(digest, s.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}
	return map[string]interface{}{
		"payload":       payload,
		"keccak256Hash": fmt.Sprintf("0x%x", digest),
		"signature":     fmt.Sprintf("0x%x", sig),
		"publicKey":     fmt.Sprintf("0x%x", crypto.FromECDSAPub(&s.privateKey.PublicKey)),
		"signer":        s.address,
		"timestamp":     time.Now().Unix(),
	}, nil
}

// CreateTamperProofWrapper wraps a payload with integrity hashes (Keccak256
// only, for on-chain compatibility) and a signature covering the whole wrapper.
func (s *DataIntegrityService) CreateTamperProofWrapper(payload interface{}, metadata map[string]interface{}) (map[string]interface{}, error) {
	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("canonical marshal: %w", err)
	}

	keccak := crypto.Keccak256Hash(payloadBytes)
	wrapper := map[string]interface{}{
		"payload": payload,
		"integrity": map[string]interface{}{
			"keccak256": keccak.Hex(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	}
	if metadata != nil {
		wrapper["metadata"] = metadata
	}
	return s.SignPayload(wrapper)
}

// VerifyIntegrity performs a comprehensive integrity check: signature first,
// then Keccak256 hash comparison on the payload.
func (s *DataIntegrityService) VerifyIntegrity(wrappedData map[string]interface{}) (bool, map[string]interface{}, error) {
	valid, err := s.VerifyPayload(wrappedData)
	if err != nil {
		return false, nil, fmt.Errorf("signature verification failed: %w", err)
	}
	if !valid {
		return false, nil, fmt.Errorf("invalid signature")
	}

	payload, ok := wrappedData["payload"]
	if !ok {
		return false, nil, fmt.Errorf("payload missing from wrapped data")
	}
	integrity, ok := wrappedData["integrity"].(map[string]interface{})
	if !ok {
		return false, nil, fmt.Errorf("integrity information missing")
	}
	expectedKeccak, ok := integrity["keccak256"].(string)
	if !ok {
		return false, nil, fmt.Errorf("keccak256 hash missing")
	}

	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return false, nil, fmt.Errorf("canonical marshal: %w", err)
	}
	actualKeccak := crypto.Keccak256Hash(payloadBytes).Hex()
	if subtle.ConstantTimeCompare([]byte(expectedKeccak), []byte(actualKeccak)) != 1 {
		return false, nil, fmt.Errorf("keccak256 hash mismatch")
	}

	var metadata map[string]interface{}
	if meta, ok := wrappedData["metadata"].(map[string]interface{}); ok {
		metadata = meta
	}
	return true, map[string]interface{}{"payload": payload, "metadata": metadata}, nil
}

// --- helpers ---

func toMap(v interface{}) (map[string]interface{}, error) {
	if m, ok := v.(map[string]interface{}); ok {
		return m, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// canonicalJSON marshals v to JSON with sorted map keys, producing a
// deterministic byte sequence suitable for cryptographic signing.
//
// Structs are first normalised to map[string]interface{} (via a
// marshal→unmarshal round-trip) so that key sorting applies uniformly. Without
// this, a struct payload would be signed using its declared field order while
// verification (which sees a map after json.Unmarshal) would sort the keys,
// producing different bytes and breaking the signature.
func canonicalJSON(v interface{}) ([]byte, error) {
	raw, err := jsonMarshalFunc(v)
	if err != nil {
		return nil, fmt.Errorf("canonical marshal: %w", err)
	}
	var normalized interface{}
	if err := jsonUnmarshalFunc(raw, &normalized); err != nil {
		return nil, fmt.Errorf("canonical unmarshal: %w", err)
	}
	return canonicalMarshal(normalized)
}

func canonicalMarshal(v interface{}) ([]byte, error) {
	switch t := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, err := jsonMarshalFunc(k)
			if err != nil {
				return nil, err
			}
			b.Write(kb)
			b.WriteByte(':')
			vb, err := canonicalMarshal(t[k])
			if err != nil {
				return nil, err
			}
			b.Write(vb)
		}
		b.WriteByte('}')
		return []byte(b.String()), nil
	case []interface{}:
		var b strings.Builder
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			ib, err := canonicalMarshal(item)
			if err != nil {
				return nil, err
			}
			b.Write(ib)
		}
		b.WriteByte(']')
		return []byte(b.String()), nil
	default:
		return json.Marshal(v)
	}
}

func hexDecode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s)%2 != 0 {
		s = "0" + s
	}
	return hex.DecodeString(s)
}

func toUnix(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	}
	return 0, false
}
