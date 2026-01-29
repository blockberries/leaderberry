package privval

import (
	"errors"
	"fmt"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// Errors
var (
	ErrDoubleSign       = errors.New("double sign attempt")
	ErrSignerNotFound   = errors.New("signer not found")
	ErrInvalidSignature = errors.New("invalid signature")
	ErrHeightRegression = errors.New("height regression")
	ErrRoundRegression  = errors.New("round regression")
	ErrStepRegression   = errors.New("step regression")
)

// PrivValidator interface for signing consensus messages
type PrivValidator interface {
	// GetPubKey returns the public key
	GetPubKey() types.PublicKey

	// SignVote signs a vote, checking for double-sign
	SignVote(chainID string, vote *gen.Vote) error

	// SignProposal signs a proposal
	SignProposal(chainID string, proposal *gen.Proposal) error

	// GetAddress returns the validator address (derived from public key)
	GetAddress() []byte
}

// LastSignState tracks the last signed vote for double-sign prevention
type LastSignState struct {
	Height    int64
	Round     int32
	Step      int8 // 1 = prevote, 2 = precommit
	Signature types.Signature
	BlockHash *types.Hash
	// TWELFTH_REFACTOR: Hash of complete sign bytes for accurate idempotency check.
	// isSameVote must verify the entire signed payload matches, not just BlockHash,
	// since sign bytes include Timestamp, Validator, and ValidatorIndex.
	SignBytesHash *types.Hash
	// TWELFTH_REFACTOR: Store timestamp for idempotency check.
	// VoteSignBytes includes Timestamp, so re-signing with different timestamp
	// produces different signBytes and thus the cached signature won't verify.
	Timestamp int64
}

// Step values for double-sign prevention
// Proposals come before votes in a round
const (
	StepProposal  int8 = 0
	StepPrevote   int8 = 1
	StepPrecommit int8 = 2
)

// CheckHRS checks if a new vote would be a double sign
// Returns nil if signing is allowed, an error otherwise
func (lss *LastSignState) CheckHRS(height int64, round int32, step int8) error {
	if lss.Height > height {
		return ErrHeightRegression
	}

	if lss.Height == height {
		if lss.Round > round {
			return ErrRoundRegression
		}

		if lss.Round == round {
			// Same height/round - check step
			if lss.Step > step {
				return ErrStepRegression
			}
			if lss.Step == step {
				// Same H/R/S - this would be a double sign unless it's the same vote
				return ErrDoubleSign
			}
		}
	}

	return nil
}

// VoteStep returns the step value for a vote type
// TWENTY_FIFTH_REFACTOR: Now panics on invalid vote type instead of returning 0.
// Returning 0 (StepProposal) for invalid types could cause isSameVote() to incorrectly
// match cached proposal signatures. Panicking is appropriate since invalid vote types
// indicate a programming error in the consensus layer.
func VoteStep(voteType gen.VoteType) int8 {
	switch voteType {
	case types.VoteTypePrevote:
		return StepPrevote
	case types.VoteTypePrecommit:
		return StepPrecommit
	default:
		panic(fmt.Sprintf("privval: invalid vote type: %v", voteType))
	}
}

// ToGenerated converts to generated LastSignState type
func (lss *LastSignState) ToGenerated() *gen.LastSignState {
	// TWENTIETH_REFACTOR: NOTE - SignBytesHash and Timestamp fields are NOT serialized
	// because gen.LastSignState doesn't include them. Schema was updated in wal.cram
	// to include these fields, but requires cramberry regeneration (run: make generate).
	// Until regenerated, these fields will be lost if this function is used for persistence.
	// Current usage: Only called for testing/debugging - actual persistence uses FilePV.saveState()
	// which writes LastSignState directly, preserving all fields.
	return &gen.LastSignState{
		Height:    lss.Height,
		Round:     lss.Round,
		Step:      lss.Step,
		BlockHash: lss.BlockHash,
		Signature: lss.Signature,
		// Missing: SignBytesHash, Timestamp (not in generated schema yet)
	}
}

// LastSignStateFromGenerated creates a LastSignState from generated type
func LastSignStateFromGenerated(g *gen.LastSignState) *LastSignState {
	// TWENTIETH_REFACTOR: NOTE - SignBytesHash and Timestamp are initialized to nil/zero
	// because gen.LastSignState doesn't include them. After schema regeneration with the
	// updated wal.cram, these fields will be properly restored.
	return &LastSignState{
		Height:    g.Height,
		Round:     g.Round,
		Step:      g.Step,
		BlockHash: g.BlockHash,
		Signature: g.Signature,
		// Missing: SignBytesHash, Timestamp (not in generated schema yet)
	}
}
