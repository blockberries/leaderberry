package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// makeValidatorSetN creates a validator set with n validators for testing
func makeValidatorSetN(t *testing.T, n int) *types.ValidatorSet {
	t.Helper()
	vals := make([]*types.NamedValidator, n)
	for i := 0; i < n; i++ {
		pubKey := make([]byte, 32)
		pubKey[0] = byte(i)
		pubKey[1] = byte(i >> 8)
		vals[i] = &types.NamedValidator{
			Name:        types.NewAccountName(fmt.Sprintf("val%d", i)),
			Index:       uint16(i),
			PublicKey:   types.MustNewPublicKey(pubKey),
			VotingPower: 100,
		}
	}
	vs, err := types.NewValidatorSet(vals)
	if err != nil {
		t.Fatalf("failed to create validator set: %v", err)
	}
	return vs
}

func TestVoteBitmap(t *testing.T) {
	valSet := makeValidatorSetN(t, 10)
	vb := NewVoteBitmap(valSet)

	// Initially empty
	if vb.Count() != 0 {
		t.Errorf("expected count 0, got %d", vb.Count())
	}

	// Set some bits
	vb.Set(0)
	vb.Set(5)
	vb.Set(9)

	if vb.Count() != 3 {
		t.Errorf("expected count 3, got %d", vb.Count())
	}

	// Check has
	if !vb.Has(0) {
		t.Error("expected Has(0) to be true")
	}
	if !vb.Has(5) {
		t.Error("expected Has(5) to be true")
	}
	if vb.Has(3) {
		t.Error("expected Has(3) to be false")
	}

	// Double set should not increment count
	vb.Set(0)
	if vb.Count() != 3 {
		t.Errorf("expected count still 3 after double set, got %d", vb.Count())
	}

	// Missing should return non-set indices
	missing := vb.Missing()
	if len(missing) != 7 {
		t.Errorf("expected 7 missing, got %d", len(missing))
	}

	// Copy should be independent
	vb2 := vb.Copy()
	vb2.Set(3)
	if vb.Has(3) {
		t.Error("original should not be affected by copy")
	}
	if !vb2.Has(3) {
		t.Error("copy should have bit 3 set")
	}
}

func TestVoteBitmapLarge(t *testing.T) {
	// Test with validators that span multiple uint64 words
	valSet := makeValidatorSetN(t, 150)
	vb := NewVoteBitmap(valSet)

	// Set some in different words
	vb.Set(0)   // Word 0
	vb.Set(63)  // Word 0, last bit
	vb.Set(64)  // Word 1, first bit
	vb.Set(127) // Word 1, last bit
	vb.Set(128) // Word 2, first bit

	if vb.Count() != 5 {
		t.Errorf("expected count 5, got %d", vb.Count())
	}

	for _, idx := range []uint16{0, 63, 64, 127, 128} {
		if !vb.Has(idx) {
			t.Errorf("expected Has(%d) to be true", idx)
		}
	}

	for _, idx := range []uint16{1, 62, 65, 126, 129} {
		if vb.Has(idx) {
			t.Errorf("expected Has(%d) to be false", idx)
		}
	}
}

func TestPeerState(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	ps := NewPeerState("peer1", valSet)

	// Initial state
	prs := ps.GetRoundState()
	if prs.Height != 0 || prs.Round != 0 {
		t.Errorf("unexpected initial state: H=%d R=%d", prs.Height, prs.Round)
	}

	// Apply new round step
	ps.ApplyNewRoundStep(1, 0, RoundStepPropose)
	prs = ps.GetRoundState()
	if prs.Height != 1 || prs.Round != 0 || prs.Step != RoundStepPropose {
		t.Errorf("unexpected state after step: H=%d R=%d S=%d", prs.Height, prs.Round, prs.Step)
	}

	// Set proposal
	blockHash := types.HashBytes([]byte("block1"))
	ps.SetHasProposal(1, 0, blockHash)
	prs = ps.GetRoundState()
	if !prs.Proposal {
		t.Error("expected peer to have proposal")
	}

	// Stale proposal update should be ignored
	ps.SetHasProposal(1, 1, blockHash) // Wrong round
	prs = ps.GetRoundState()
	if prs.Round != 0 {
		t.Error("round should not have changed from stale update")
	}
}

func TestPeerStateVotes(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	ps := NewPeerState("peer1", valSet)

	// Move to height 1
	ps.ApplyNewRoundStep(1, 0, RoundStepPrevote)

	// Set some votes
	vote := &gen.Vote{
		Height:         1,
		Round:          0,
		Type:           types.VoteTypePrevote,
		ValidatorIndex: 0,
	}
	ps.SetHasVote(vote)

	prs := ps.GetRoundState()
	if !prs.Prevotes.Has(0) {
		t.Error("expected prevote from validator 0")
	}
	if prs.Prevotes.Count() != 1 {
		t.Errorf("expected 1 prevote, got %d", prs.Prevotes.Count())
	}

	// Add precommit
	precommit := &gen.Vote{
		Height:         1,
		Round:          0,
		Type:           types.VoteTypePrecommit,
		ValidatorIndex: 2,
	}
	ps.SetHasVote(precommit)

	prs = ps.GetRoundState()
	if !prs.Precommits.Has(2) {
		t.Error("expected precommit from validator 2")
	}

	// Wrong height vote should be ignored
	wrongVote := &gen.Vote{
		Height:         2,
		Round:          0,
		Type:           types.VoteTypePrevote,
		ValidatorIndex: 1,
	}
	ps.SetHasVote(wrongVote)

	prs = ps.GetRoundState()
	if prs.Prevotes.Has(1) {
		t.Error("should not have prevote from wrong height")
	}
}

func TestPeerStateRoundProgression(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	ps := NewPeerState("peer1", valSet)

	// Start at height 1, round 0
	ps.ApplyNewRoundStep(1, 0, RoundStepPrevote)

	// Add a vote
	vote := &gen.Vote{
		Height:         1,
		Round:          0,
		Type:           types.VoteTypePrevote,
		ValidatorIndex: 0,
	}
	ps.SetHasVote(vote)

	prs := ps.GetRoundState()
	if prs.Prevotes.Count() != 1 {
		t.Error("should have 1 prevote")
	}

	// Move to round 1 - votes should reset
	ps.ApplyNewRoundStep(1, 1, RoundStepPropose)

	prs = ps.GetRoundState()
	if prs.Round != 1 {
		t.Errorf("expected round 1, got %d", prs.Round)
	}
	if prs.Prevotes.Count() != 0 {
		t.Error("prevotes should reset on round change")
	}
	if prs.Proposal {
		t.Error("proposal should reset on round change")
	}
}

func TestPeerStateHeightProgression(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	ps := NewPeerState("peer1", valSet)

	// Start at height 1
	ps.ApplyNewRoundStep(1, 2, RoundStepPrevote)
	ps.SetHasProposal(1, 2, types.HashBytes([]byte("block")))

	// Move to height 2 - everything resets
	ps.ApplyNewRoundStep(2, 0, RoundStepNewRound)

	prs := ps.GetRoundState()
	if prs.Height != 2 {
		t.Errorf("expected height 2, got %d", prs.Height)
	}
	if prs.Round != 0 {
		t.Errorf("expected round 0, got %d", prs.Round)
	}
	if prs.Proposal {
		t.Error("proposal should reset on height change")
	}
}

func TestPeerStateNeedsVote(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	ps := NewPeerState("peer1", valSet)

	ps.ApplyNewRoundStep(1, 0, RoundStepPrevote)

	vote := &gen.Vote{
		Height:         1,
		Round:          0,
		Type:           types.VoteTypePrevote,
		ValidatorIndex: 0,
	}

	// Should need the vote initially
	if !ps.NeedsVote(vote) {
		t.Error("should need vote initially")
	}

	// After setting, should not need it
	ps.SetHasVote(vote)
	if ps.NeedsVote(vote) {
		t.Error("should not need vote after having it")
	}

	// Wrong height/round vote - should not need
	wrongVote := &gen.Vote{
		Height:         2,
		Round:          0,
		Type:           types.VoteTypePrevote,
		ValidatorIndex: 1,
	}
	if ps.NeedsVote(wrongVote) {
		t.Error("should not need vote from different height")
	}
}

func TestPeerSet(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	peerSet := NewPeerSet(valSet)

	// Add peers
	ps1 := peerSet.AddPeer("peer1")
	ps2 := peerSet.AddPeer("peer2")

	if peerSet.Size() != 2 {
		t.Errorf("expected size 2, got %d", peerSet.Size())
	}

	// Get peer
	if peerSet.GetPeer("peer1") != ps1 {
		t.Error("GetPeer returned wrong peer")
	}

	// Add same peer again - should return existing
	ps1Again := peerSet.AddPeer("peer1")
	if ps1Again != ps1 {
		t.Error("adding same peer should return existing")
	}
	if peerSet.Size() != 2 {
		t.Error("size should not change for duplicate add")
	}

	// Remove peer
	peerSet.RemovePeer("peer1")
	if peerSet.Size() != 1 {
		t.Errorf("expected size 1 after remove, got %d", peerSet.Size())
	}
	if peerSet.GetPeer("peer1") != nil {
		t.Error("removed peer should be nil")
	}
	if peerSet.GetPeer("peer2") != ps2 {
		t.Error("other peer should still exist")
	}
}

func TestPeerSetNeedingVote(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	peerSet := NewPeerSet(valSet)

	ps1 := peerSet.AddPeer("peer1")
	ps2 := peerSet.AddPeer("peer2")

	// Both at height 1, round 0
	ps1.ApplyNewRoundStep(1, 0, RoundStepPrevote)
	ps2.ApplyNewRoundStep(1, 0, RoundStepPrevote)

	vote := &gen.Vote{
		Height:         1,
		Round:          0,
		Type:           types.VoteTypePrevote,
		ValidatorIndex: 0,
	}

	// Both should need it
	needing := peerSet.PeersNeedingVote(vote)
	if len(needing) != 2 {
		t.Errorf("expected 2 peers needing vote, got %d", len(needing))
	}

	// Give it to peer1
	ps1.SetHasVote(vote)

	// Now only peer2 needs it
	needing = peerSet.PeersNeedingVote(vote)
	if len(needing) != 1 {
		t.Errorf("expected 1 peer needing vote, got %d", len(needing))
	}
	if needing[0].PeerID() != "peer2" {
		t.Error("wrong peer needs vote")
	}
}

func TestPeerSetPeersAtHeight(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	peerSet := NewPeerSet(valSet)

	ps1 := peerSet.AddPeer("peer1")
	ps2 := peerSet.AddPeer("peer2")
	ps3 := peerSet.AddPeer("peer3")

	ps1.ApplyNewRoundStep(1, 0, RoundStepPrevote)
	ps2.ApplyNewRoundStep(2, 0, RoundStepPrevote)
	ps3.ApplyNewRoundStep(1, 0, RoundStepPrevote)

	peersAt1 := peerSet.PeersAtHeight(1)
	if len(peersAt1) != 2 {
		t.Errorf("expected 2 peers at height 1, got %d", len(peersAt1))
	}

	peersAt2 := peerSet.PeersAtHeight(2)
	if len(peersAt2) != 1 {
		t.Errorf("expected 1 peer at height 2, got %d", len(peersAt2))
	}
}

func TestPeerStateCatchingUp(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	peerSet := NewPeerSet(valSet)

	ps := peerSet.AddPeer("peer1")
	ps.ApplyNewRoundStep(5, 0, RoundStepPrevote)

	// Not catching up initially
	if ps.IsCatchingUp() {
		t.Error("should not be catching up initially")
	}

	// Mark as catching up (we're at height 10)
	peerSet.MarkPeerCatchingUp("peer1", 10)
	if !ps.IsCatchingUp() {
		t.Error("should be catching up when behind")
	}

	// Peer catches up
	ps.ApplyNewRoundStep(10, 0, RoundStepPrevote)
	peerSet.MarkPeerCatchingUp("peer1", 10)
	if ps.IsCatchingUp() {
		t.Error("should not be catching up when at same height")
	}
}

func TestPeerStateLastSeen(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	ps := NewPeerState("peer1", valSet)

	before := ps.LastSeen()

	time.Sleep(10 * time.Millisecond)

	ps.ApplyNewRoundStep(1, 0, RoundStepPrevote)

	after := ps.LastSeen()
	if !after.After(before) {
		t.Error("LastSeen should update after state change")
	}
}

func TestPeerSetUpdateValidatorSet(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	peerSet := NewPeerSet(valSet)

	ps1 := peerSet.AddPeer("peer1")
	ps1.ApplyNewRoundStep(1, 0, RoundStepPrevote)

	// Set a vote
	vote := &gen.Vote{
		Height:         1,
		Round:          0,
		Type:           types.VoteTypePrevote,
		ValidatorIndex: 0,
	}
	ps1.SetHasVote(vote)

	// Update validator set - vote tracking should reset
	newValSet := makeValidatorSetN(t, 6)
	peerSet.UpdateValidatorSet(newValSet)

	prs := ps1.GetRoundState()
	if prs.Prevotes.Count() != 0 {
		t.Error("votes should reset after validator set update")
	}
}
