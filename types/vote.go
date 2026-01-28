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

// Commit verification errors
var (
	ErrInvalidCommit           = errors.New("invalid commit")
	ErrCommitHeightMismatch    = errors.New("commit height mismatch")
	ErrCommitBlockHashMismatch = errors.New("commit block hash mismatch")
	ErrInsufficientVotePower   = errors.New("insufficient voting power in commit")
	ErrInvalidCommitSignature  = errors.New("invalid signature in commit")
	ErrDuplicateCommitSig      = errors.New("duplicate signature in commit")
	ErrUnknownCommitValidator  = errors.New("unknown validator in commit")
)

// VerifyCommit verifies a commit certificate against a validator set.
// MF4: This is used for light client verification and historical block validation.
// It verifies:
// - All signatures are valid
// - Signatures are from known validators
// - No duplicate signatures
// - 2/3+ of voting power has signed for the block
func VerifyCommit(
	chainID string,
	valSet *ValidatorSet,
	blockHash Hash,
	height int64,
	commit *Commit,
) error {
	if commit == nil {
		return ErrInvalidCommit
	}
	if commit.Height != height {
		return fmt.Errorf("%w: expected %d, got %d", ErrCommitHeightMismatch, height, commit.Height)
	}
	if !HashEqual(commit.BlockHash, blockHash) {
		return ErrCommitBlockHashMismatch
	}
	if len(commit.Signatures) == 0 {
		return fmt.Errorf("%w: no signatures", ErrInvalidCommit)
	}

	var votingPower int64
	seenValidators := make(map[uint16]bool)

	for _, sig := range commit.Signatures {
		// Check for nil/absent votes (allowed but don't count toward power)
		if sig.BlockHash == nil || IsHashEmpty(sig.BlockHash) {
			continue
		}

		// Must be for this block
		if !HashEqual(*sig.BlockHash, blockHash) {
			// Validator voted for different block - don't count
			continue
		}

		// Check for duplicate
		if seenValidators[sig.ValidatorIndex] {
			return fmt.Errorf("%w: validator %d appears twice", ErrDuplicateCommitSig, sig.ValidatorIndex)
		}
		seenValidators[sig.ValidatorIndex] = true

		// Get validator
		val := valSet.GetByIndex(sig.ValidatorIndex)
		if val == nil {
			return fmt.Errorf("%w: index %d", ErrUnknownCommitValidator, sig.ValidatorIndex)
		}

		// Construct vote for signature verification
		vote := &Vote{
			Type:           VoteTypePrecommit,
			Height:         commit.Height,
			Round:          commit.Round,
			BlockHash:      sig.BlockHash,
			Timestamp:      sig.Timestamp,
			Validator:      val.Name,
			ValidatorIndex: sig.ValidatorIndex,
			Signature:      sig.Signature,
		}

		// Verify signature
		if err := VerifyVoteSignature(chainID, vote, val.PublicKey); err != nil {
			return fmt.Errorf("%w: validator %d: %v", ErrInvalidCommitSignature, sig.ValidatorIndex, err)
		}

		votingPower += val.VotingPower
	}

	// Check 2/3+ majority
	required := valSet.TwoThirdsMajority()
	if votingPower < required {
		return fmt.Errorf("%w: got %d, need %d", ErrInsufficientVotePower, votingPower, required)
	}

	return nil
}

// VerifyCommitLight is a lighter version that only checks voting power
// without re-verifying signatures (for use when signatures were already verified).
func VerifyCommitLight(
	valSet *ValidatorSet,
	blockHash Hash,
	height int64,
	commit *Commit,
) error {
	if commit == nil {
		return ErrInvalidCommit
	}
	if commit.Height != height {
		return fmt.Errorf("%w: expected %d, got %d", ErrCommitHeightMismatch, height, commit.Height)
	}
	if !HashEqual(commit.BlockHash, blockHash) {
		return ErrCommitBlockHashMismatch
	}

	var votingPower int64
	seenValidators := make(map[uint16]bool)

	for _, sig := range commit.Signatures {
		// Skip nil/absent votes
		if sig.BlockHash == nil || IsHashEmpty(sig.BlockHash) {
			continue
		}

		// Must be for this block
		if !HashEqual(*sig.BlockHash, blockHash) {
			continue
		}

		// No duplicates
		if seenValidators[sig.ValidatorIndex] {
			return fmt.Errorf("%w: validator %d", ErrDuplicateCommitSig, sig.ValidatorIndex)
		}
		seenValidators[sig.ValidatorIndex] = true

		// Get validator power
		val := valSet.GetByIndex(sig.ValidatorIndex)
		if val == nil {
			return fmt.Errorf("%w: index %d", ErrUnknownCommitValidator, sig.ValidatorIndex)
		}

		votingPower += val.VotingPower
	}

	// Check 2/3+ majority
	required := valSet.TwoThirdsMajority()
	if votingPower < required {
		return fmt.Errorf("%w: got %d, need %d", ErrInsufficientVotePower, votingPower, required)
	}

	return nil
}
