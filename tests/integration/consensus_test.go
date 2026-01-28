package integration

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/blockberries/leaderberry/engine"
	"github.com/blockberries/leaderberry/evidence"
	"github.com/blockberries/leaderberry/privval"
	"github.com/blockberries/leaderberry/types"
	"github.com/blockberries/leaderberry/wal"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// TestNode represents a test consensus node
type TestNode struct {
	Name      string
	Engine    *engine.Engine
	PrivVal   *privval.FilePV
	WAL       *wal.FileWAL
	ValSet    *types.ValidatorSet
	Proposals []*gen.Proposal
	Votes     []*gen.Vote
}

func setupTestNode(t *testing.T, name string, valSet *types.ValidatorSet, dir string) *TestNode {
	// Create private validator
	keyPath := filepath.Join(dir, name+"_key.json")
	statePath := filepath.Join(dir, name+"_state.json")
	pv, err := privval.GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to create private validator: %v", err)
	}

	// Create WAL
	walDir := filepath.Join(dir, name+"_wal")
	w, err := wal.NewFileWAL(walDir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	// Create config
	cfg := &engine.Config{
		ChainID: "test-chain",
		Timeouts: engine.TimeoutConfig{
			Propose:        100 * time.Millisecond,
			ProposeDelta:   10 * time.Millisecond,
			Prevote:        100 * time.Millisecond,
			PrevoteDelta:   10 * time.Millisecond,
			Precommit:      100 * time.Millisecond,
			PrecommitDelta: 10 * time.Millisecond,
			Commit:         100 * time.Millisecond,
		},
	}

	// Create engine
	eng := engine.NewEngine(cfg, valSet, pv, w, nil)

	return &TestNode{
		Name:    name,
		Engine:  eng,
		PrivVal: pv,
		WAL:     w,
		ValSet:  valSet,
	}
}

func makeTestValidatorSet(t *testing.T, names []string, powers []int64) *types.ValidatorSet {
	vals := make([]*types.NamedValidator, len(names))
	for i, name := range names {
		pubKey := make([]byte, 32)
		pubKey[0] = byte(i)
		vals[i] = &types.NamedValidator{
			Name:        types.NewAccountName(name),
			Index:       uint16(i),
			PublicKey:   types.MustNewPublicKey(pubKey),
			VotingPower: powers[i],
		}
	}
	vs, err := types.NewValidatorSet(vals)
	if err != nil {
		t.Fatalf("failed to create validator set: %v", err)
	}
	return vs
}

func TestEngineCreation(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	if node.Engine == nil {
		t.Fatal("engine should not be nil")
	}

	// Check validator set
	vs := node.Engine.GetValidatorSet()
	if vs.Size() != 3 {
		t.Errorf("expected 3 validators, got %d", vs.Size())
	}
}

func TestEngineStartStop(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	// Start engine
	err := node.Engine.Start(1, nil)
	if err != nil {
		t.Fatalf("failed to start engine: %v", err)
	}

	// Check state
	height, round, _, err := node.Engine.GetState()
	if err != nil {
		t.Fatalf("failed to get state: %v", err)
	}
	if height != 1 {
		t.Errorf("expected height 1, got %d", height)
	}
	if round != 0 {
		t.Errorf("expected round 0, got %d", round)
	}

	// Stop engine
	err = node.Engine.Stop()
	if err != nil {
		t.Fatalf("failed to stop engine: %v", err)
	}
}

func TestEngineDoubleStart(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	// First start
	err := node.Engine.Start(1, nil)
	if err != nil {
		t.Fatalf("failed to start engine: %v", err)
	}
	defer node.Engine.Stop()

	// Second start should fail
	err = node.Engine.Start(1, nil)
	if err != engine.ErrAlreadyStarted {
		t.Errorf("expected ErrAlreadyStarted, got %v", err)
	}
}

func TestEngineAddProposal(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	err := node.Engine.Start(1, nil)
	if err != nil {
		t.Fatalf("failed to start engine: %v", err)
	}
	defer node.Engine.Stop()

	// Create a proposal
	proposal := &gen.Proposal{
		Height:    1,
		Round:     0,
		Timestamp: time.Now().UnixNano(),
		Proposer:  types.NewAccountName("alice"),
	}

	// Add proposal should succeed
	err = node.Engine.AddProposal(proposal)
	if err != nil {
		t.Fatalf("AddProposal failed: %v", err)
	}
}

func TestEngineAddVote(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	err := node.Engine.Start(1, nil)
	if err != nil {
		t.Fatalf("failed to start engine: %v", err)
	}
	defer node.Engine.Stop()

	blockHash := types.HashBytes([]byte("block"))
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      time.Now().UnixNano(),
		Validator:      types.NewAccountName("bob"),
		ValidatorIndex: 1,
	}

	// Add vote should succeed
	err = node.Engine.AddVote(vote)
	if err != nil {
		t.Fatalf("AddVote failed: %v", err)
	}
}

func TestEngineMetrics(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	err := node.Engine.Start(1, nil)
	if err != nil {
		t.Fatalf("failed to start engine: %v", err)
	}
	defer node.Engine.Stop()

	metrics, err := node.Engine.GetMetrics()
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}

	if metrics.Height != 1 {
		t.Errorf("expected height 1, got %d", metrics.Height)
	}
	if metrics.Validators != 3 {
		t.Errorf("expected 3 validators, got %d", metrics.Validators)
	}
	if metrics.TotalVotingPower != 300 {
		t.Errorf("expected total power 300, got %d", metrics.TotalVotingPower)
	}
}

func TestEngineChainID(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	if node.Engine.ChainID() != "test-chain" {
		t.Errorf("expected chain ID 'test-chain', got %q", node.Engine.ChainID())
	}
}

func TestEngineUpdateValidatorSet(t *testing.T) {
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	// Create new validator set with different powers
	newValSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{200, 100, 100})

	node.Engine.UpdateValidatorSet(newValSet)

	vs := node.Engine.GetValidatorSet()
	if vs.TotalPower != 400 {
		t.Errorf("expected total power 400, got %d", vs.TotalPower)
	}
}

func TestEvidenceIntegration(t *testing.T) {
	// Test evidence pool integration
	pool := evidence.NewPool(evidence.DefaultConfig())
	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})

	pool.Update(1, time.Now())

	blockHash1 := types.HashBytes([]byte("block1"))
	blockHash2 := types.HashBytes([]byte("block2"))

	// First vote
	vote1 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash1,
		Timestamp:      time.Now().UnixNano(),
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}
	pool.CheckVote(vote1, valSet, "")

	// Conflicting vote - equivocation
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash2,
		Timestamp:      time.Now().UnixNano(),
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}

	ev, err := pool.CheckVote(vote2, valSet, "")
	if err != nil {
		t.Fatalf("CheckVote failed: %v", err)
	}
	if ev == nil {
		t.Fatal("should detect equivocation")
	}

	// Add evidence
	err = pool.AddDuplicateVoteEvidence(ev)
	if err != nil {
		t.Fatalf("AddDuplicateVoteEvidence failed: %v", err)
	}

	if pool.Size() != 1 {
		t.Errorf("expected 1 evidence, got %d", pool.Size())
	}
}

func TestPrivValidatorIntegration(t *testing.T) {
	dir := t.TempDir()

	keyPath := filepath.Join(dir, "key.json")
	statePath := filepath.Join(dir, "state.json")

	pv, err := privval.GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate private validator: %v", err)
	}

	// Sign a vote
	blockHash := types.HashBytes([]byte("block"))
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      time.Now().UnixNano(),
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote)
	if err != nil {
		t.Fatalf("SignVote failed: %v", err)
	}

	if len(vote.Signature.Data) == 0 {
		t.Error("vote should have signature")
	}

	// Verify signature
	err = types.VerifyVoteSignature("test-chain", vote, pv.GetPubKey())
	if err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

func TestWALIntegration(t *testing.T) {
	dir := t.TempDir()

	// Create and start WAL
	w, err := wal.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}

	// Write some messages
	writer := engine.NewWALWriter(w)

	proposal := &gen.Proposal{
		Height:    1,
		Round:     0,
		Timestamp: time.Now().UnixNano(),
		Proposer:  types.NewAccountName("alice"),
	}
	writer.WriteProposal(1, 0, proposal)

	blockHash := types.HashBytes([]byte("block"))
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      time.Now().UnixNano(),
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}
	writer.WriteVote(1, 0, vote)
	writer.WriteEndHeight(1)
	writer.Flush()

	w.Stop()

	// Reopen and verify messages can be read
	w2, err := wal.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to reopen WAL: %v", err)
	}
	if err := w2.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}
	defer w2.Stop()

	// Search for end height
	_, found, err := w2.SearchForEndHeight(1)
	if err != nil {
		t.Fatalf("SearchForEndHeight failed: %v", err)
	}
	if !found {
		t.Error("should find end height 1")
	}
}

func TestFullConsensusFlow(t *testing.T) {
	// This test simulates a basic consensus flow with proposal and votes
	dir := t.TempDir()

	valSet := makeTestValidatorSet(t, []string{"alice", "bob", "carol"}, []int64{100, 100, 100})
	node := setupTestNode(t, "alice", valSet, dir)

	// Set up broadcast functions (mock)
	var broadcastedProposals []*gen.Proposal
	var broadcastedVotes []*gen.Vote

	node.Engine.SetProposalBroadcaster(func(p *gen.Proposal) {
		broadcastedProposals = append(broadcastedProposals, p)
	})
	node.Engine.SetVoteBroadcaster(func(v *gen.Vote) {
		broadcastedVotes = append(broadcastedVotes, v)
	})

	// Start engine
	err := node.Engine.Start(1, nil)
	if err != nil {
		t.Fatalf("failed to start engine: %v", err)
	}
	defer node.Engine.Stop()

	// Give time for initial operations
	time.Sleep(50 * time.Millisecond)

	// Add votes from other validators
	blockHash := types.HashBytes([]byte("block"))
	for i, name := range []string{"bob", "carol"} {
		vote := &gen.Vote{
			Type:           types.VoteTypePrevote,
			Height:         1,
			Round:          0,
			BlockHash:      &blockHash,
			Timestamp:      time.Now().UnixNano(),
			Validator:      types.NewAccountName(name),
			ValidatorIndex: uint16(i + 1),
		}
		node.Engine.AddVote(vote)
	}

	// Verify engine state
	height, round, step, err := node.Engine.GetState()
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}

	t.Logf("Consensus state: height=%d, round=%d, step=%v", height, round, step)
}
