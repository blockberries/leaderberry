package types

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type Vote = gen.Vote
type VoteType = gen.VoteType
type Commit = gen.Commit
type CommitSig = gen.CommitSig

// Vote type constants
const (
	VoteTypeUnknown   = gen.VoteTypeVoteTypeUnknown
	VoteTypePrevote   = gen.VoteTypeVoteTypePrevote
	VoteTypePrecommit = gen.VoteTypeVoteTypePrecommit
)

// Errors
var (
	ErrInvalidVote        = errors.New("invalid vote")
	ErrVoteConflict       = errors.New("conflicting vote")
	ErrDuplicateVote      = errors.New("duplicate vote")
	ErrUnexpectedVoteType = errors.New("unexpected vote type")
)

// VoteSignBytes returns the bytes to sign for a vote
func VoteSignBytes(chainID string, v *Vote) []byte {
	// Create a canonical vote for signing (without signature)
	canonical := &Vote{
		Type:           v.Type,
		Height:         v.Height,
		Round:          v.Round,
		BlockHash:      v.BlockHash,
		Timestamp:      v.Timestamp,
		Validator:      v.Validator,
		ValidatorIndex: v.ValidatorIndex,
		// Signature is nil for signing
	}

	// Prepend chain ID
	data, err := canonical.MarshalCramberry()
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal vote for signing: %v", err))
	}
	return append([]byte(chainID), data...)
}

// IsNilVote returns true if the vote is for nil (no block)
func IsNilVote(v *Vote) bool {
	return v.BlockHash == nil || IsHashEmpty(v.BlockHash)
}

// NOTE: Vote tracking is implemented in engine.VoteSet (engine/vote_tracker.go)
// which provides thread-safe vote collection with signature verification.
// The engine.VoteSet is the canonical implementation used by consensus.

// VerifyVoteSignature verifies the signature on a vote
func VerifyVoteSignature(chainID string, vote *Vote, pubKey PublicKey) error {
	if vote == nil {
		return ErrInvalidVote
	}
	if len(vote.Signature.Data) == 0 {
		return errors.New("vote has no signature")
	}
	if len(pubKey.Data) != ed25519.PublicKeySize {
		return errors.New("invalid public key size")
	}

	signBytes := VoteSignBytes(chainID, vote)
	if !ed25519.Verify(pubKey.Data, signBytes, vote.Signature.Data) {
		return errors.New("invalid vote signature")
	}
	return nil
}
