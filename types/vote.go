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
	// TWENTIETH_REFACTOR: Validate input to prevent panic
	if v == nil {
		panic("CONSENSUS CRITICAL: nil vote in VoteSignBytes")
	}

	// M3: Create canonical vote with explicit zero signature for deterministic signing
	canonical := &Vote{
		Type:           v.Type,
		Height:         v.Height,
		Round:          v.Round,
		BlockHash:      v.BlockHash,
		Timestamp:      v.Timestamp,
		Validator:      v.Validator,
		ValidatorIndex: v.ValidatorIndex,
		Signature:      Signature{Data: nil}, // M3: Explicit zero for signing
	}

	// Prepend chain ID
	data, err := canonical.MarshalCramberry()
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal vote for signing: %v", err))
	}
	return append([]byte(chainID), data...)
}

// IsNilVote returns true if the vote is for nil (no block)
// TWENTY_THIRD_REFACTOR: A nil vote pointer is treated as a nil vote.
func IsNilVote(v *Vote) bool {
	if v == nil {
		return true
	}
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
// SIXTEENTH_REFACTOR: Added valSet nil check at function entry.
func VerifyCommit(
	chainID string,
	valSet *ValidatorSet,
	blockHash Hash,
	height int64,
	commit *Commit,
) error {
	if valSet == nil {
		return errors.New("nil validator set")
	}
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
		// SEVENTEENTH_REFACTOR: Check for duplicate FIRST, before any filtering.
		// Previously, empty signatures would skip this check, allowing the same
		// validator to appear twice (once with empty sig, once with valid sig).
		// This could allow accepting commits with insufficient voting power.
		if seenValidators[sig.ValidatorIndex] {
			return fmt.Errorf("%w: validator %d appears twice", ErrDuplicateCommitSig, sig.ValidatorIndex)
		}
		seenValidators[sig.ValidatorIndex] = true

		// H3: Validate signature structure before processing
		if len(sig.Signature.Data) == 0 {
			// Empty signature - skip but don't count toward power
			continue
		}

		// Check for nil/absent votes (allowed but don't count toward power)
		if sig.BlockHash == nil || IsHashEmpty(sig.BlockHash) {
			continue
		}

		// Must be for this block
		if !HashEqual(*sig.BlockHash, blockHash) {
			// Validator voted for different block - don't count
			continue
		}

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

// CopyVote creates a deep copy of a Vote.
// H3: This ensures all slice fields (Signature.Data, BlockHash.Data) are copied
// to prevent the original from being modified through the copy.
func CopyVote(v *Vote) *Vote {
	if v == nil {
		return nil
	}

	voteCopy := &Vote{
		Type:           v.Type,
		Height:         v.Height,
		Round:          v.Round,
		Timestamp:      v.Timestamp,
		ValidatorIndex: v.ValidatorIndex,
	}

	// Deep copy BlockHash (pointer to Hash with Data []byte)
	if v.BlockHash != nil {
		hashCopy := &Hash{}
		if len(v.BlockHash.Data) > 0 {
			hashCopy.Data = make([]byte, len(v.BlockHash.Data))
			copy(hashCopy.Data, v.BlockHash.Data)
		}
		voteCopy.BlockHash = hashCopy
	}

	// Deep copy Validator (AccountName with Name *string)
	voteCopy.Validator = CopyAccountName(v.Validator)

	// Deep copy Signature (Data []byte)
	if len(v.Signature.Data) > 0 {
		voteCopy.Signature.Data = make([]byte, len(v.Signature.Data))
		copy(voteCopy.Signature.Data, v.Signature.Data)
	}

	return voteCopy
}

// VerifyCommitLight is a lighter version that only checks voting power
// without re-verifying signatures (for use when signatures were already verified).
// SIXTEENTH_REFACTOR: Added valSet nil check at function entry.
func VerifyCommitLight(
	valSet *ValidatorSet,
	blockHash Hash,
	height int64,
	commit *Commit,
) error {
	if valSet == nil {
		return errors.New("nil validator set")
	}
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
		// SEVENTEENTH_REFACTOR: Check for duplicate FIRST, before any filtering.
		// Same fix as in VerifyCommit to prevent double-counting.
		if seenValidators[sig.ValidatorIndex] {
			return fmt.Errorf("%w: validator %d", ErrDuplicateCommitSig, sig.ValidatorIndex)
		}
		seenValidators[sig.ValidatorIndex] = true

		// Skip nil/absent votes
		if sig.BlockHash == nil || IsHashEmpty(sig.BlockHash) {
			continue
		}

		// Must be for this block
		if !HashEqual(*sig.BlockHash, blockHash) {
			continue
		}

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
