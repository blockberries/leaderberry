package types

import (
	"bytes"
	"testing"
)

func TestNewHash(t *testing.T) {
	data := make([]byte, 32)
	for i := range data {
		data[i] = byte(i)
	}

	h := NewHash(data)
	if !bytes.Equal(h.Data, data) {
		t.Error("hash data mismatch")
	}
}

func TestNewHashPanicsOnWrongSize(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wrong size")
		}
	}()
	NewHash(make([]byte, 16))
}

func TestHashBytes(t *testing.T) {
	data := []byte("hello world")
	h := HashBytes(data)

	if len(h.Data) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(h.Data))
	}

	// Same input should produce same hash
	h2 := HashBytes(data)
	if !HashEqual(h, h2) {
		t.Error("same input should produce same hash")
	}

	// Different input should produce different hash
	h3 := HashBytes([]byte("different"))
	if HashEqual(h, h3) {
		t.Error("different input should produce different hash")
	}
}

func TestHashEmpty(t *testing.T) {
	h := HashEmpty()
	if len(h.Data) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(h.Data))
	}
	if !IsHashEmpty(&h) {
		t.Error("empty hash should be empty")
	}
}

func TestIsHashEmpty(t *testing.T) {
	// Nil is empty
	if !IsHashEmpty(nil) {
		t.Error("nil hash should be empty")
	}

	// Zero hash is empty
	h := HashEmpty()
	if !IsHashEmpty(&h) {
		t.Error("zero hash should be empty")
	}

	// Non-zero hash is not empty
	data := make([]byte, 32)
	data[0] = 1
	h2 := NewHash(data)
	if IsHashEmpty(&h2) {
		t.Error("non-zero hash should not be empty")
	}
}

func TestHashEqual(t *testing.T) {
	data1 := make([]byte, 32)
	data2 := make([]byte, 32)
	for i := range data1 {
		data1[i] = byte(i)
		data2[i] = byte(i)
	}

	h1 := NewHash(data1)
	h2 := NewHash(data2)

	if !HashEqual(h1, h2) {
		t.Error("equal hashes should be equal")
	}

	data2[0] = 255
	h3 := NewHash(data2)
	if HashEqual(h1, h3) {
		t.Error("different hashes should not be equal")
	}
}

func TestHashString(t *testing.T) {
	data := make([]byte, 32)
	for i := range data {
		data[i] = byte(i)
	}

	h := NewHash(data)
	s := HashString(h)

	if len(s) != 64 { // hex encoded 32 bytes = 64 chars
		t.Errorf("expected 64 chars, got %d", len(s))
	}
}

func TestNewPublicKey(t *testing.T) {
	data := make([]byte, 32)
	pk := NewPublicKey(data)
	if !bytes.Equal(pk.Data, data) {
		t.Error("public key data mismatch")
	}
}

func TestNewSignature(t *testing.T) {
	data := make([]byte, 64)
	sig := NewSignature(data)
	if !bytes.Equal(sig.Data, data) {
		t.Error("signature data mismatch")
	}
}

func TestPublicKeyEqual(t *testing.T) {
	data1 := make([]byte, 32)
	data2 := make([]byte, 32)

	pk1 := NewPublicKey(data1)
	pk2 := NewPublicKey(data2)

	if !PublicKeyEqual(pk1, pk2) {
		t.Error("equal public keys should be equal")
	}

	data2[0] = 1
	pk3 := NewPublicKey(data2)
	if PublicKeyEqual(pk1, pk3) {
		t.Error("different public keys should not be equal")
	}
}
