package types

import (
	"testing"
)

func TestNewAccountName(t *testing.T) {
	name := "alice"
	an := NewAccountName(name)

	if AccountNameString(an) != name {
		t.Errorf("expected %s, got %s", name, AccountNameString(an))
	}
}

func TestIsAccountNameEmpty(t *testing.T) {
	// Empty name
	empty := AccountName{}
	if !IsAccountNameEmpty(empty) {
		t.Error("empty account name should be empty")
	}

	// Empty string
	emptyStr := NewAccountName("")
	if !IsAccountNameEmpty(emptyStr) {
		t.Error("empty string account name should be empty")
	}

	// Non-empty name
	alice := NewAccountName("alice")
	if IsAccountNameEmpty(alice) {
		t.Error("non-empty account name should not be empty")
	}
}

func TestAccountNameEqual(t *testing.T) {
	alice1 := NewAccountName("alice")
	alice2 := NewAccountName("alice")
	bob := NewAccountName("bob")

	if !AccountNameEqual(alice1, alice2) {
		t.Error("same names should be equal")
	}

	if AccountNameEqual(alice1, bob) {
		t.Error("different names should not be equal")
	}

	// Both nil
	empty1 := AccountName{}
	empty2 := AccountName{}
	if !AccountNameEqual(empty1, empty2) {
		t.Error("both nil names should be equal")
	}
}

func TestVerifySignature(t *testing.T) {
	// Create a test key pair
	// Note: This is a simplified test - in real usage, you'd use crypto/ed25519.GenerateKey

	// Invalid key sizes should fail
	invalidPubKey := PublicKey{Data: make([]byte, 16)} // wrong size
	invalidSig := Signature{Data: make([]byte, 32)}    // wrong size
	message := []byte("test message")

	if VerifySignature(invalidPubKey, message, invalidSig) {
		t.Error("invalid key size should fail verification")
	}

	validPubKey := PublicKey{Data: make([]byte, 32)}
	if VerifySignature(validPubKey, message, invalidSig) {
		t.Error("invalid signature size should fail verification")
	}
}
