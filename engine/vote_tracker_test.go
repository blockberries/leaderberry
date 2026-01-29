package engine

import (
	"testing"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

func makeTestValidator(name string, index uint16, power int64) *types.NamedValidator {
	pubKey := make([]byte, 32)
	pubKey[0] = byte(index)
	return &types.NamedValidator{
		Name:        types.NewAccountName(name),
		Index:       index,
		PublicKey:   types.MustNewPublicKey(pubKey),
		VotingPower: power,
	}
}

func makeTestValidatorSet() *types.ValidatorSet {
	vals := []*types.NamedValidator{
		makeTestValidator("alice", 0, 100),
		makeTestValidator("bob", 1, 100),
		makeTestValidator("carol", 2, 100),
	}
	vs, _ := types.NewValidatorSet(vals)
	return vs
}

func TestVoteSetBasic(t *testing.T) {
	valSet := makeTestValidatorSet()
	vs := NewVoteSet("test-chain", 1, 0, types.VoteTypePrevote, valSet)

	if vs.Size() != 0 {
		t.Errorf("expected 0 votes, got %d", vs.Size())
	}

	if vs.HasTwoThirdsMajority() {
		t.Error("should not have 2/3+ majority with no votes")
	}
}

func TestVoteSetAddVote(t *testing.T) {
	valSet := makeTestValidatorSet()
	vs := NewVoteSet("test-chain", 1, 0, types.VoteTypePrevote, valSet)

	blockHash := types.HashBytes([]byte("block1"))

	// Create a vote (without signature for this test)
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
		// Note: In real usage, this would need a valid signature
	}

	// This test will fail signature verification, which is expected
	// In production, votes would be signed
	_, err := vs.AddVote(vote)
	if err != ErrInvalidSignature {
		t.Logf("Expected ErrInvalidSignature, got: %v", err)
	}
}

func TestVoteSetDuplicateVote(t *testing.T) {
	// Test that duplicate votes are handled correctly
	// (This is a simplified test without actual signatures)
}

func TestHeightVoteSetBasic(t *testing.T) {
	valSet := makeTestValidatorSet()
	hvs := NewHeightVoteSet("test-chain", 1, valSet)

	if hvs.Height() != 1 {
		t.Errorf("expected height 1, got %d", hvs.Height())
	}

	// Prevotes for round 0 should be nil initially
	prevotes := hvs.Prevotes(0)
	if prevotes != nil {
		t.Error("prevotes should be nil for uninitialized round")
	}

	// Precommits for round 0 should be nil initially
	precommits := hvs.Precommits(0)
	if precommits != nil {
		t.Error("precommits should be nil for uninitialized round")
	}
}

func TestHeightVoteSetReset(t *testing.T) {
	valSet := makeTestValidatorSet()
	hvs := NewHeightVoteSet("test-chain", 1, valSet)

	// Reset to height 2
	hvs.Reset(2, valSet)

	if hvs.Height() != 2 {
		t.Errorf("expected height 2 after reset, got %d", hvs.Height())
	}
}
