package engine

import (
	"testing"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// makeTestConsensusState creates a minimal ConsensusState for testing canUnlock
func makeTestConsensusState() *ConsensusState {
	config := DefaultConfig()
	config.ChainID = "test-chain"

	vals := []*types.NamedValidator{
		makeTestValidator("alice", 0, 100),
		makeTestValidator("bob", 1, 100),
		makeTestValidator("carol", 2, 100),
	}
	valSet, _ := types.NewValidatorSet(vals)

	cs := &ConsensusState{
		config:       config,
		validatorSet: valSet,
		lockedRound:  -1, // Not locked initially
	}
	return cs
}

// TestCanUnlockNilProposal verifies canUnlock returns false when proposal is nil
func TestCanUnlockNilProposal(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3 // Lock in round 3

	if cs.canUnlock(nil) {
		t.Error("canUnlock should return false for nil proposal")
	}
}

// TestCanUnlockNoPolRound verifies canUnlock returns false when PolRound is negative
func TestCanUnlockNoPolRound(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3 // Lock in round 3

	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: -1, // No POL
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when PolRound is negative")
	}
}

// TestCanUnlockPolRoundNotLater verifies canUnlock returns false when PolRound <= lockedRound
func TestCanUnlockPolRoundNotLater(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3 // Lock in round 3

	// POL from same round as lock - should not unlock
	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 3, // Same as lockedRound
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when PolRound == lockedRound")
	}

	// POL from earlier round - should not unlock
	proposal.PolRound = 2
	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when PolRound < lockedRound")
	}
}

// TestCanUnlockInvalidPol verifies canUnlock returns false when POL validation fails
func TestCanUnlockInvalidPol(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3 // Lock in round 3

	// POL from later round but empty votes - validation should fail
	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,      // Later than lockedRound
		PolVotes: []gen.Vote{}, // Empty - will fail validation
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL validation fails (empty votes)")
	}
}

// TestCanUnlockWrongVoteType verifies canUnlock returns false when POL contains non-prevote
func TestCanUnlockWrongVoteType(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	blockHash := types.HashBytes([]byte("test-block"))
	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}

	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrecommit, // Wrong type - should be prevote
				Height:         1,
				Round:          4,
				BlockHash:      &blockHash,
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
			},
		},
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL contains non-prevote votes")
	}
}

// TestCanUnlockWrongHeight verifies canUnlock returns false when POL vote has wrong height
func TestCanUnlockWrongHeight(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	blockHash := types.HashBytes([]byte("test-block"))
	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}

	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrevote,
				Height:         2, // Wrong height
				Round:          4,
				BlockHash:      &blockHash,
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
			},
		},
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL vote has wrong height")
	}
}

// TestCanUnlockWrongRound verifies canUnlock returns false when POL vote has wrong round
func TestCanUnlockWrongRound(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	blockHash := types.HashBytes([]byte("test-block"))
	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}

	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrevote,
				Height:         1,
				Round:          3, // Wrong round - should be 4 (PolRound)
				BlockHash:      &blockHash,
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
			},
		},
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL vote has wrong round")
	}
}

// TestCanUnlockInsufficientPower verifies canUnlock returns false when POL has < 2/3 power
func TestCanUnlockInsufficientPower(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}
	blockHash := types.BlockHash(&block)

	// Only one vote (100 power) out of 300 total - not 2/3+
	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrevote,
				Height:         1,
				Round:          4,
				BlockHash:      &blockHash,
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
				// Note: signature would fail verification in real scenario
			},
		},
	}

	// This should fail due to insufficient power (would also fail signature verification)
	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL has insufficient power")
	}
}

// TestCanUnlockDuplicateVote verifies canUnlock returns false when POL has duplicate votes
func TestCanUnlockDuplicateVote(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}
	blockHash := types.BlockHash(&block)

	// Two votes from same validator
	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrevote,
				Height:         1,
				Round:          4,
				BlockHash:      &blockHash,
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
			},
			{
				Type:           types.VoteTypePrevote,
				Height:         1,
				Round:          4,
				BlockHash:      &blockHash,
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0, // Duplicate
			},
		},
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL has duplicate votes")
	}
}

// TestCanUnlockNotLocked verifies canUnlock behavior when not locked
func TestCanUnlockNotLocked(t *testing.T) {
	cs := makeTestConsensusState()
	// lockedRound is -1 (not locked)

	// Even with a valid-looking proposal, if we're not locked, canUnlock isn't relevant
	// This tests that PolRound > lockedRound check works (PolRound=4 > -1)
	// but POL validation will still fail without proper signatures
	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		PolVotes: []gen.Vote{}, // Empty - will fail validation
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false with invalid POL even when not locked")
	}
}

// TestCanUnlockNilBlockHash verifies canUnlock returns false when POL vote has nil block hash
func TestCanUnlockNilBlockHash(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}

	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrevote,
				Height:         1,
				Round:          4,
				BlockHash:      nil, // Nil block hash
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
			},
		},
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL vote has nil block hash")
	}
}

// TestCanUnlockWrongBlockHash verifies canUnlock returns false when POL vote is for different block
func TestCanUnlockWrongBlockHash(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}
	differentBlockHash := types.HashBytes([]byte("different-block"))

	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 4,
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrevote,
				Height:         1,
				Round:          4,
				BlockHash:      &differentBlockHash, // Different block
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
			},
		},
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when POL vote is for different block")
	}
}
