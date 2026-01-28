package types

import (
	"bytes"
	"crypto/ed25519"
	"errors"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type AccountName = gen.AccountName
type Account = gen.Account
type Authority = gen.Authority
type KeyWeight = gen.KeyWeight
type AccountWeight = gen.AccountWeight

// Errors
var (
	ErrInsufficientWeight = errors.New("insufficient authorization weight")
	ErrCycleDetected      = errors.New("authorization cycle detected")
	ErrAccountNotFound    = errors.New("account not found")
	ErrInvalidSignature   = errors.New("invalid signature")
)

// NewAccountName creates an AccountName
func NewAccountName(name string) AccountName {
	return AccountName{Name: &name}
}

// AccountNameString returns the account name string
func AccountNameString(a AccountName) string {
	if a.Name == nil {
		return ""
	}
	return *a.Name
}

// IsAccountNameEmpty returns true if account name is empty
func IsAccountNameEmpty(a AccountName) bool {
	return a.Name == nil || *a.Name == ""
}

// AccountNameEqual compares two account names
func AccountNameEqual(a, b AccountName) bool {
	if a.Name == nil && b.Name == nil {
		return true
	}
	if a.Name == nil || b.Name == nil {
		return false
	}
	return *a.Name == *b.Name
}

// VerifySignature verifies an Ed25519 signature
func VerifySignature(pubKey PublicKey, message []byte, sig Signature) bool {
	if len(pubKey.Data) != ed25519.PublicKeySize {
		return false
	}
	if len(sig.Data) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pubKey.Data, message, sig.Data)
}

// AccountGetter is a function that retrieves an account by name
type AccountGetter func(AccountName) (*Account, error)

// VerifyAuthorization verifies that an authorization satisfies an authority.
// It returns the total weight achieved and any error.
// visited tracks accounts already checked (for cycle detection).
func VerifyAuthorization(
	auth *gen.Authorization,
	authority *gen.Authority,
	signBytes []byte,
	getAccount AccountGetter,
	visited map[string]bool,
) (uint32, error) {
	if auth == nil || authority == nil {
		return 0, ErrInsufficientWeight
	}

	if visited == nil {
		visited = make(map[string]bool)
	}

	var totalWeight uint32

	// Check direct signatures
	for _, sig := range auth.Signatures {
		// Find the key in authority
		for _, kw := range authority.Keys {
			if bytes.Equal(sig.PublicKey.Data, kw.PublicKey.Data) {
				// Verify signature
				if VerifySignature(kw.PublicKey, signBytes, sig.Signature) {
					totalWeight += kw.Weight
				}
				break
			}
		}
	}

	// Check delegated authorizations
	for _, accAuth := range auth.AccountAuthorizations {
		accName := AccountNameString(accAuth.Account)

		// Cycle detection
		if visited[accName] {
			continue
		}
		visited[accName] = true

		// Find the account in authority
		for _, aw := range authority.Accounts {
			if AccountNameEqual(aw.Account, accAuth.Account) {
				// Get the delegating account
				acc, err := getAccount(aw.Account)
				if err != nil {
					continue
				}

				// Recursively verify
				weight, err := VerifyAuthorization(
					&accAuth.Authorization,
					&acc.Authority,
					signBytes,
					getAccount,
					visited,
				)
				if err != nil {
					continue
				}

				// If delegated account's authority is satisfied, add its weight
				if weight >= acc.Authority.Threshold {
					totalWeight += aw.Weight
				}
				break
			}
		}
	}

	return totalWeight, nil
}

// IsAuthoritySatisfied checks if an authorization satisfies an authority's threshold
func IsAuthoritySatisfied(
	auth *gen.Authorization,
	authority *gen.Authority,
	signBytes []byte,
	getAccount AccountGetter,
) (bool, error) {
	weight, err := VerifyAuthorization(auth, authority, signBytes, getAccount, nil)
	if err != nil {
		return false, err
	}
	return weight >= authority.Threshold, nil
}
