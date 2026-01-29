package types

import (
	"fmt"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type Proposal = gen.Proposal

// ProposalSignBytes returns the bytes to sign for a proposal
// SEVENTEENTH_REFACTOR: Added nil check for defensive programming.
func ProposalSignBytes(chainID string, p *Proposal) []byte {
	if p == nil {
		panic("CONSENSUS CRITICAL: nil proposal in ProposalSignBytes")
	}
	// Create a canonical proposal for signing (without signature)
	// EIGHTEENTH_REFACTOR: Explicitly set Signature to zero for clarity and robustness.
	// Previously relied on Go's zero-value which works but is brittle if schema changes.
	canonical := &Proposal{
		Height:    p.Height,
		Round:     p.Round,
		Timestamp: p.Timestamp,
		Block:     p.Block,
		PolRound:  p.PolRound,
		PolVotes:  p.PolVotes,
		Proposer:  p.Proposer,
		Signature: Signature{Data: nil}, // Explicit zero for signing
	}

	// Prepend chain ID
	data, err := canonical.MarshalCramberry()
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal proposal for signing: %v", err))
	}
	return append([]byte(chainID), data...)
}

// NewProposal creates a new proposal
func NewProposal(
	height int64,
	round int32,
	timestamp int64,
	block Block,
	polRound int32,
	polVotes []Vote,
	proposer AccountName,
) *Proposal {
	return &Proposal{
		Height:    height,
		Round:     round,
		Timestamp: timestamp,
		Block:     block,
		PolRound:  polRound,
		PolVotes:  polVotes,
		Proposer:  proposer,
	}
}

// HasPOL returns true if this proposal has a proof-of-lock
// SEVENTEENTH_REFACTOR: Added nil check for defensive programming.
func HasPOL(p *Proposal) bool {
	if p == nil {
		return false
	}
	return p.PolRound >= 0 && len(p.PolVotes) > 0
}

// ProposalBlockHash returns the hash of the proposed block
// SEVENTEENTH_REFACTOR: Added nil check for defensive programming.
func ProposalBlockHash(p *Proposal) Hash {
	if p == nil {
		return HashEmpty()
	}
	return BlockHash(&p.Block)
}

// CopyProposal creates a deep copy of a Proposal.
// SEVENTEENTH_REFACTOR: Added to support safe goroutine callbacks.
// This ensures all slice and pointer fields are copied to prevent
// the original from being modified through the copy.
func CopyProposal(p *Proposal) *Proposal {
	if p == nil {
		return nil
	}

	proposalCopy := &Proposal{
		Height:    p.Height,
		Round:     p.Round,
		Timestamp: p.Timestamp,
		PolRound:  p.PolRound,
	}

	// Deep copy Block
	proposalCopy.Block = *CopyBlock(&p.Block)

	// Deep copy PolVotes slice
	if len(p.PolVotes) > 0 {
		proposalCopy.PolVotes = make([]Vote, len(p.PolVotes))
		for i, vote := range p.PolVotes {
			copied := CopyVote(&vote)
			if copied != nil {
				proposalCopy.PolVotes[i] = *copied
			}
		}
	}

	// Deep copy Proposer (AccountName)
	proposalCopy.Proposer = CopyAccountName(p.Proposer)

	// Deep copy Signature
	if len(p.Signature.Data) > 0 {
		proposalCopy.Signature.Data = make([]byte, len(p.Signature.Data))
		copy(proposalCopy.Signature.Data, p.Signature.Data)
	}

	return proposalCopy
}
