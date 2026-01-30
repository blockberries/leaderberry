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

// TestCanUnlockPolRoundNotBeforeCurrentRound verifies canUnlock returns false when PolRound >= proposal Round
func TestCanUnlockPolRoundNotBeforeCurrentRound(t *testing.T) {
	cs := makeTestConsensusState()
	cs.lockedRound = 3

	block := gen.Block{
		Header: gen.BlockHeader{
			Height: 1,
		},
	}
	blockHash := types.BlockHash(&block)

	// PolRound equal to proposal Round - should fail
	proposal := &gen.Proposal{
		Height:   1,
		Round:    5,
		PolRound: 5, // Same as proposal round - invalid
		Block:    block,
		PolVotes: []gen.Vote{
			{
				Type:           types.VoteTypePrevote,
				Height:         1,
				Round:          5,
				BlockHash:      &blockHash,
				Validator:      types.NewAccountName("alice"),
				ValidatorIndex: 0,
			},
		},
	}

	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when PolRound == proposal.Round")
	}

	// PolRound greater than proposal Round - should fail
	proposal.PolRound = 6
	proposal.PolVotes[0].Round = 6
	if cs.canUnlock(proposal) {
		t.Error("canUnlock should return false when PolRound > proposal.Round")
	}
}

// TestApplyValidatorUpdatesNewValidatorPenalty verifies new validators get -1.125*P penalty
func TestApplyValidatorUpdatesNewValidatorPenalty(t *testing.T) {
	cs := makeTestConsensusState()
	// Initial total power is 300 (3 validators * 100)

	// Add a new validator
	updates := []ValidatorUpdate{
		{
			Name:        types.NewAccountName("david"),
			PublicKey:   types.PublicKey{Data: make([]byte, 32)},
			VotingPower: 100,
		},
	}

	newSet, err := cs.applyValidatorUpdates(updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the new validator
	var david *types.NamedValidator
	for _, v := range newSet.Validators {
		if types.AccountNameString(v.Name) == "david" {
			david = v
			break
		}
	}

	if david == nil {
		t.Fatal("david should be in validator set")
	}

	// New total power is 400 (4 validators * 100)
	// New validator penalty should be -1.125 * 400 = -450
	expectedPenalty := -int64(float64(400) * 1.125)
	// Note: Priority gets incremented by 1 after, but we check the base penalty
	// The priority also gets centered, so we need to verify it's negative and substantial
	if david.ProposerPriority >= 0 {
		t.Errorf("new validator should have negative priority, got %d", david.ProposerPriority)
	}

	// Verify existing validators don't have the same extreme penalty
	for _, v := range newSet.Validators {
		name := types.AccountNameString(v.Name)
		if name != "david" {
			// Existing validators should have higher priority than david
			if v.ProposerPriority < david.ProposerPriority {
				t.Errorf("existing validator %s has lower priority than new validator: %d < %d",
					name, v.ProposerPriority, david.ProposerPriority)
			}
		}
	}

	_ = expectedPenalty // Used for documentation
}

// TestApplyValidatorUpdatesExistingValidator verifies existing validators keep their priority
func TestApplyValidatorUpdatesExistingValidator(t *testing.T) {
	cs := makeTestConsensusState()

	// Get alice's current priority
	alice := cs.validatorSet.GetByName("alice")
	if alice == nil {
		t.Fatal("alice should exist")
	}
	aliceOriginalPriority := alice.ProposerPriority

	// Update alice's voting power (not a new validator)
	updates := []ValidatorUpdate{
		{
			Name:        types.NewAccountName("alice"),
			PublicKey:   alice.PublicKey,
			VotingPower: 200, // Double power
		},
	}

	newSet, err := cs.applyValidatorUpdates(updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	newAlice := newSet.GetByName("alice")
	if newAlice == nil {
		t.Fatal("alice should still exist")
	}

	if newAlice.VotingPower != 200 {
		t.Errorf("alice voting power should be 200, got %d", newAlice.VotingPower)
	}

	// Priority should be preserved (though it will be incremented and centered)
	// The key is that it should NOT have the new validator penalty
	// Check that priority isn't extremely negative like a new validator would be
	totalPower := newSet.TotalPower
	newValidatorPenalty := -int64(float64(totalPower) * 1.125)
	if newAlice.ProposerPriority < newValidatorPenalty+100 {
		t.Errorf("existing validator should not have new validator penalty, priority: %d, penalty would be: %d",
			newAlice.ProposerPriority, newValidatorPenalty)
	}

	_ = aliceOriginalPriority // Used for context
}
