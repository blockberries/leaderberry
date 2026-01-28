package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type Hash = gen.Hash
type Signature = gen.Signature
type PublicKey = gen.PublicKey
type Timestamp = gen.Timestamp

// NewHash creates a Hash from bytes
func NewHash(data []byte) Hash {
	if len(data) != 32 {
		panic("hash must be 32 bytes")
	}
	return Hash{Data: data}
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

// NewSignature creates a Signature from bytes
func NewSignature(data []byte) Signature {
	if len(data) != 64 {
		panic("signature must be 64 bytes")
	}
	return Signature{Data: data}
}

// NewPublicKey creates a PublicKey from bytes
func NewPublicKey(data []byte) PublicKey {
	if len(data) != 32 {
		panic("public key must be 32 bytes")
	}
	return PublicKey{Data: data}
}

// PublicKeyEqual compares two public keys
func PublicKeyEqual(a, b PublicKey) bool {
	return bytes.Equal(a.Data, b.Data)
}
