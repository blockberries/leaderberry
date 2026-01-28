package types

import (
	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type Proposal = gen.Proposal

// ProposalSignBytes returns the bytes to sign for a proposal
func ProposalSignBytes(chainID string, p *Proposal) []byte {
	// Create a canonical proposal for signing (without signature)
	canonical := &Proposal{
		Height:    p.Height,
		Round:     p.Round,
		Timestamp: p.Timestamp,
		Block:     p.Block,
		PolRound:  p.PolRound,
		PolVotes:  p.PolVotes,
		Proposer:  p.Proposer,
		// Signature is nil for signing
	}

	// Prepend chain ID
	data, _ := canonical.MarshalCramberry()
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
func HasPOL(p *Proposal) bool {
	return p.PolRound >= 0 && len(p.PolVotes) > 0
}

// ProposalBlockHash returns the hash of the proposed block
func ProposalBlockHash(p *Proposal) Hash {
	return BlockHash(&p.Block)
}
