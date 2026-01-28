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

	h, err := NewHash(data)
	if err != nil {
		t.Fatalf("NewHash failed: %v", err)
	}
	if !bytes.Equal(h.Data, data) {
		t.Error("hash data mismatch")
	}
}

func TestNewHashError(t *testing.T) {
	// Wrong size should return error
	_, err := NewHash(make([]byte, 16))
	if err == nil {
		t.Error("expected error for wrong size")
	}
}

func TestMustNewHashPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wrong size")
		}
	}()
	MustNewHash(make([]byte, 16))
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
	h2 := MustNewHash(data)
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

	h1 := MustNewHash(data1)
	h2 := MustNewHash(data2)

	if !HashEqual(h1, h2) {
		t.Error("equal hashes should be equal")
	}

	data2[0] = 255
	h3 := MustNewHash(data2)
	if HashEqual(h1, h3) {
		t.Error("different hashes should not be equal")
	}
}

func TestHashString(t *testing.T) {
	data := make([]byte, 32)
	for i := range data {
		data[i] = byte(i)
	}

	h := MustNewHash(data)
	s := HashString(h)

	if len(s) != 64 { // hex encoded 32 bytes = 64 chars
		t.Errorf("expected 64 chars, got %d", len(s))
	}
}

func TestNewPublicKey(t *testing.T) {
	data := make([]byte, 32)
	pk, err := NewPublicKey(data)
	if err != nil {
		t.Fatalf("NewPublicKey failed: %v", err)
	}
	if !bytes.Equal(pk.Data, data) {
		t.Error("public key data mismatch")
	}
}

func TestNewPublicKeyError(t *testing.T) {
	_, err := NewPublicKey(make([]byte, 16))
	if err == nil {
		t.Error("expected error for wrong size")
	}
}

func TestMustNewPublicKeyPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wrong size")
		}
	}()
	MustNewPublicKey(make([]byte, 16))
}

func TestNewSignature(t *testing.T) {
	data := make([]byte, 64)
	sig, err := NewSignature(data)
	if err != nil {
		t.Fatalf("NewSignature failed: %v", err)
	}
	if !bytes.Equal(sig.Data, data) {
		t.Error("signature data mismatch")
	}
}

func TestNewSignatureError(t *testing.T) {
	_, err := NewSignature(make([]byte, 32))
	if err == nil {
		t.Error("expected error for wrong size")
	}
}

func TestMustNewSignaturePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wrong size")
		}
	}()
	MustNewSignature(make([]byte, 32))
}

func TestPublicKeyEqual(t *testing.T) {
	data1 := make([]byte, 32)
	data2 := make([]byte, 32)

	pk1 := MustNewPublicKey(data1)
	pk2 := MustNewPublicKey(data2)

	if !PublicKeyEqual(pk1, pk2) {
		t.Error("equal public keys should be equal")
	}

	data2[0] = 1
	pk3 := MustNewPublicKey(data2)
	if PublicKeyEqual(pk1, pk3) {
		t.Error("different public keys should not be equal")
	}
}
