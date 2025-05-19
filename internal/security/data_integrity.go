// Package security provides cryptographic verification and data integrity features
package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sirupsen/logrus"
)

// DataIntegrityService provides cryptographic verification for yield metrics
type DataIntegrityService struct {
	privateKey       *ecdsa.PrivateKey
	publicKeyEncoded string
	verificationOpts VerificationOptions
}

// VerificationOptions configures the behavior of data integrity checks
type VerificationOptions struct {
	SignatureEnabled     bool          `json:"signature_enabled"`
	VerificationRequired bool          `json:"verification_required"`
	SignatureValidity    time.Duration `json:"signature_validity"`
	StrictMode           bool          `json:"strict_mode"`
}

// NewDataIntegrityService creates a new service for data integrity
func NewDataIntegrityService(opts VerificationOptions) (*DataIntegrityService, error) {
	// Generate a new ECDSA key pair
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Encode public key
	publicKeyBytes := elliptic.Marshal(elliptic.P256(), privateKey.PublicKey.X, privateKey.PublicKey.Y)
	publicKeyEncoded := base64.StdEncoding.EncodeToString(publicKeyBytes)

	service := &DataIntegrityService{
		privateKey:       privateKey,
		publicKeyEncoded: publicKeyEncoded,
		verificationOpts: opts,
	}

	logrus.Infof("Data integrity service initialized with public key: %s", publicKeyEncoded[:16]+"...")
	return service, nil
}

// SignPayload adds cryptographic signatures to data payloads
func (s *DataIntegrityService) SignPayload(payload interface{}) (map[string]interface{}, error) {
	if !s.verificationOpts.SignatureEnabled {
		// If signatures are disabled, return the payload as is
		payloadMap, ok := payload.(map[string]interface{})
		if !ok {
			payloadBytes, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal payload: %w", err)
			}
			var result map[string]interface{}
			if err := json.Unmarshal(payloadBytes, &result); err != nil {
				return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
			}
			return result, nil
		}
		return payloadMap, nil
	}

	// Convert payload to JSON bytes
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Calculate hash of payload
	hash := sha256.Sum256(payloadBytes)

	// Sign the hash
	r, s, err := ecdsa.Sign(rand.Reader, s.privateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("failed to sign payload: %w", err)
	}

	// Convert signature to base64
	signature := append(r.Bytes(), s.Bytes()...)
	signatureEncoded := base64.StdEncoding.EncodeToString(signature)

	// Create result with signature metadata
	var resultMap map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &resultMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	// Add signature metadata
	resultMap["_signature"] = map[string]interface{}{
		"signature":  signatureEncoded,
		"publicKey":  s.publicKeyEncoded,
		"algorithm":  "ECDSA-P256-SHA256",
		"timestamp":  time.Now().Unix(),
		"validUntil": time.Now().Add(s.verificationOpts.SignatureValidity).Unix(),
	}

	return resultMap, nil
}

// VerifyPayload verifies the cryptographic signature on data payloads
func (s *DataIntegrityService) VerifyPayload(signedPayload map[string]interface{}) (bool, error) {
	if !s.verificationOpts.SignatureEnabled || !s.verificationOpts.VerificationRequired {
		// Skip verification if disabled
		return true, nil
	}

	// Extract signature metadata
	sigMetadata, ok := signedPayload["_signature"].(map[string]interface{})
	if !ok {
		if s.verificationOpts.StrictMode {
			return false, fmt.Errorf("signature metadata missing")
		}
		logrus.Warn("Signature metadata missing from payload")
		return false, nil
	}

	// Extract signature components
	signatureStr, ok := sigMetadata["signature"].(string)
	if !ok {
		return false, fmt.Errorf("invalid signature format")
	}

	publicKeyStr, ok := sigMetadata["publicKey"].(string)
	if !ok {
		return false, fmt.Errorf("invalid public key format")
	}

	// Check timestamp validity
	timestamp, ok := sigMetadata["timestamp"].(float64)
	if !ok {
		return false, fmt.Errorf("invalid timestamp format")
	}
	_ = timestamp // Use timestamp to avoid unused variable warning

	validUntil, ok := sigMetadata["validUntil"].(float64)
	if !ok {
		return false, fmt.Errorf("invalid validUntil format")
	}

	// Check if signature is expired
	now := time.Now().Unix()
	if now > int64(validUntil) {
		return false, fmt.Errorf("signature expired at %v (current time: %v)", 
			time.Unix(int64(validUntil), 0), time.Unix(now, 0))
	}

	// Decode signature
	signatureBytes, err := base64.StdEncoding.DecodeString(signatureStr)
	if err != nil {
		return false, fmt.Errorf("failed to decode signature: %w", err)
	}

	// Decode public key
	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyStr)
	if err != nil {
		return false, fmt.Errorf("failed to decode public key: %w", err)
	}

	// Parse public key
	x, y := elliptic.Unmarshal(elliptic.P256(), publicKeyBytes)
	if x == nil {
		return false, fmt.Errorf("failed to unmarshal public key")
	}
	publicKey := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     x,
		Y:     y,
	}

	// Remove signature from payload for hash calculation
	payloadCopy := make(map[string]interface{})
	for k, v := range signedPayload {
		if k != "_signature" {
			payloadCopy[k] = v
		}
	}

	// Calculate hash of payload
	payloadBytes, err := json.Marshal(payloadCopy)
	if err != nil {
		return false, fmt.Errorf("failed to marshal payload: %w", err)
	}
	hash := sha256.Sum256(payloadBytes)

	// Extract r and s from signature
	if len(signatureBytes) != 64 {
		return false, fmt.Errorf("invalid signature length: %d", len(signatureBytes))
	}
	r := new(big.Int).SetBytes(signatureBytes[:32])
	s := new(big.Int).SetBytes(signatureBytes[32:])

	// Verify signature
	if !ecdsa.Verify(publicKey, hash[:], r, s) {
		return false, fmt.Errorf("signature verification failed")
	}

	return true, nil
}

// GetPublicKey returns the base64-encoded public key
func (s *DataIntegrityService) GetPublicKey() string {
	return s.publicKeyEncoded
}

// OnChainVerificationData generates data that can be verified on-chain by Chainlink contracts
func (s *DataIntegrityService) OnChainVerificationData(payload interface{}) (map[string]interface{}, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Calculate Keccak256 hash (Ethereum standard)
	keccakHash := crypto.Keccak256Hash(payloadBytes)
	hashHex := keccakHash.Hex()

	// Sign the hash using Ethereum's signature scheme
	signature, err := crypto.Sign(keccakHash.Bytes(), s.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign with Ethereum scheme: %w", err)
	}

	// Format result for Ethereum verification
	result := map[string]interface{}{
		"payload":       payload,
		"keccak256Hash": hashHex,
		"signature":     fmt.Sprintf("0x%x", signature),
		"publicKey":     fmt.Sprintf("0x%x", crypto.FromECDSAPub(&s.privateKey.PublicKey)),
		"timestamp":     time.Now().Unix(),
	}

	return result, nil
}

// CreateTamperProofWrapper adds tamper-proofing to the payload
func (s *DataIntegrityService) CreateTamperProofWrapper(payload interface{}, metadata map[string]interface{}) (map[string]interface{}, error) {
	// Convert payload to bytes
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Calculate multiple hashes for redundancy
	sha256Hash := sha256.Sum256(payloadBytes)
	keccakHash := crypto.Keccak256Hash(payloadBytes)

	// Create the wrapper
	wrapper := map[string]interface{}{
		"payload": payload,
		"integrity": map[string]interface{}{
			"sha256":    fmt.Sprintf("%x", sha256Hash),
			"keccak256": keccakHash.Hex(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	}

	// Add metadata if provided
	if metadata != nil {
		wrapper["metadata"] = metadata
	}

	// Sign the wrapper
	return s.SignPayload(wrapper)
}

// VerifyIntegrity performs a comprehensive integrity check on the data
func (s *DataIntegrityService) VerifyIntegrity(wrappedData map[string]interface{}) (bool, map[string]interface{}, error) {
	// First verify signature
	validSignature, err := s.VerifyPayload(wrappedData)
	if err != nil {
		return false, nil, fmt.Errorf("signature verification failed: %w", err)
	}

	if !validSignature {
		return false, nil, fmt.Errorf("invalid signature")
	}

	// Extract components
	payload, ok := wrappedData["payload"]
	if !ok {
		return false, nil, fmt.Errorf("payload missing from wrapped data")
	}

	integrity, ok := wrappedData["integrity"].(map[string]interface{})
	if !ok {
		return false, nil, fmt.Errorf("integrity information missing")
	}

	// Get expected hashes
	expectedSHA256, ok := integrity["sha256"].(string)
	if !ok {
		return false, nil, fmt.Errorf("SHA256 hash missing")
	}

	expectedKeccak, ok := integrity["keccak256"].(string)
	if !ok {
		return false, nil, fmt.Errorf("Keccak256 hash missing")
	}

	// Calculate actual hashes
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return false, nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	actualSHA256 := fmt.Sprintf("%x", sha256.Sum256(payloadBytes))
	actualKeccak := crypto.Keccak256Hash(payloadBytes).Hex()

	// Compare hashes
	if expectedSHA256 != actualSHA256 {
		return false, nil, fmt.Errorf("SHA256 hash mismatch")
	}

	if expectedKeccak != actualKeccak {
		return false, nil, fmt.Errorf("Keccak256 hash mismatch")
	}

	// Extract metadata if present
	var metadata map[string]interface{}
	if meta, ok := wrappedData["metadata"].(map[string]interface{}); ok {
		metadata = meta
	}

	// All checks passed, return payload and metadata
	return true, map[string]interface{}{
		"payload":  payload,
		"metadata": metadata,
	}, nil
}
