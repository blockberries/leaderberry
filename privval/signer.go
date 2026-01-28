package privval

import (
	"errors"

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
}

// StepPrevote is the step value for prevotes
const StepPrevote int8 = 1

// StepPrecommit is the step value for precommits
const StepPrecommit int8 = 2

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
func VoteStep(voteType gen.VoteType) int8 {
	switch voteType {
	case types.VoteTypePrevote:
		return StepPrevote
	case types.VoteTypePrecommit:
		return StepPrecommit
	default:
		return 0
	}
}

// ToGenerated converts to generated LastSignState type
func (lss *LastSignState) ToGenerated() *gen.LastSignState {
	return &gen.LastSignState{
		Height:    lss.Height,
		Round:     lss.Round,
		Step:      lss.Step,
		BlockHash: lss.BlockHash,
		Signature: lss.Signature,
	}
}

// LastSignStateFromGenerated creates a LastSignState from generated type
func LastSignStateFromGenerated(g *gen.LastSignState) *LastSignState {
	return &LastSignState{
		Height:    g.Height,
		Round:     g.Round,
		Step:      g.Step,
		BlockHash: g.BlockHash,
		Signature: g.Signature,
	}
}
