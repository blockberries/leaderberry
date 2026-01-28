package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type Hash = gen.Hash
type Signature = gen.Signature
type PublicKey = gen.PublicKey
type Timestamp = gen.Timestamp

// HashSize is the expected size of a hash in bytes
const HashSize = 32

// SignatureSize is the expected size of a signature in bytes
const SignatureSize = 64

// PublicKeySize is the expected size of a public key in bytes
const PublicKeySize = 32

// NewHash creates a Hash from bytes, returning error if invalid.
// Use for untrusted input (network, files).
// H1: Copies input data to prevent caller from modifying internal state.
func NewHash(data []byte) (Hash, error) {
	if len(data) != HashSize {
		return Hash{}, fmt.Errorf("hash must be %d bytes, got %d", HashSize, len(data))
	}
	// H1: Copy to prevent caller from modifying our internal data
	copied := make([]byte, HashSize)
	copy(copied, data)
	return Hash{Data: copied}, nil
}

// MustNewHash creates a Hash, panicking if invalid.
// Use only for trusted internal data.
func MustNewHash(data []byte) Hash {
	h, err := NewHash(data)
	if err != nil {
		panic(err)
	}
	return h
}

// HashBytes computes SHA-256 hash of data
func HashBytes(data []byte) Hash {
	h := sha256.Sum256(data)
	return Hash{Data: h[:]}
}

// HashEmpty returns an empty (zero) hash
func HashEmpty() Hash {
	return Hash{Data: make([]byte, 32)}
}

// IsHashEmpty returns true if hash is nil or all zeros
func IsHashEmpty(h *Hash) bool {
	if h == nil {
		return true
	}
	for _, b := range h.Data {
		if b != 0 {
			return false
		}
	}
	return true
}

// HashEqual compares two hashes
func HashEqual(a, b Hash) bool {
	return bytes.Equal(a.Data, b.Data)
}

// HashString returns hex-encoded hash
func HashString(h Hash) string {
	return hex.EncodeToString(h.Data)
}

// NewSignature creates a Signature from bytes, returning error if invalid.
// Use for untrusted input (network, files).
// H1: Copies input data to prevent caller from modifying internal state.
func NewSignature(data []byte) (Signature, error) {
	if len(data) != SignatureSize {
		return Signature{}, fmt.Errorf("signature must be %d bytes, got %d", SignatureSize, len(data))
	}
	// H1: Copy to prevent caller from modifying our internal data
	copied := make([]byte, SignatureSize)
	copy(copied, data)
	return Signature{Data: copied}, nil
}

// MustNewSignature creates a Signature, panicking if invalid.
// Use only for trusted internal data (e.g., crypto library output).
func MustNewSignature(data []byte) Signature {
	s, err := NewSignature(data)
	if err != nil {
		panic(err)
	}
	return s
}

// NewPublicKey creates a PublicKey from bytes, returning error if invalid.
// Use for untrusted input (network, files).
// H1: Copies input data to prevent caller from modifying internal state.
func NewPublicKey(data []byte) (PublicKey, error) {
	if len(data) != PublicKeySize {
		return PublicKey{}, fmt.Errorf("public key must be %d bytes, got %d", PublicKeySize, len(data))
	}
	// H1: Copy to prevent caller from modifying our internal data
	copied := make([]byte, PublicKeySize)
	copy(copied, data)
	return PublicKey{Data: copied}, nil
}

// MustNewPublicKey creates a PublicKey, panicking if invalid.
// Use only for trusted internal data.
func MustNewPublicKey(data []byte) PublicKey {
	p, err := NewPublicKey(data)
	if err != nil {
		panic(err)
	}
	return p
}

// PublicKeyEqual compares two public keys
func PublicKeyEqual(a, b PublicKey) bool {
	return bytes.Equal(a.Data, b.Data)
}
