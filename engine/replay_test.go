package engine

import (
	"testing"

	"github.com/blockberries/leaderberry/types"
	"github.com/blockberries/leaderberry/wal"
	gen "github.com/blockberries/leaderberry/types/generated"
)

func TestWALWriterBasic(t *testing.T) {
	dir := t.TempDir()

	w, err := wal.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}
	defer w.Stop()

	writer := NewWALWriter(w)

	// Write a proposal
	blockHash := types.HashBytes([]byte("block"))
	proposal := &gen.Proposal{
		Height:    1,
		Round:     0,
		Timestamp: 1000,
		Proposer:  types.NewAccountName("alice"),
	}

	if err := writer.WriteProposal(1, 0, proposal); err != nil {
		t.Fatalf("failed to write proposal: %v", err)
	}

	// Write a vote
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}

	if err := writer.WriteVote(1, 0, vote); err != nil {
		t.Fatalf("failed to write vote: %v", err)
	}

	// Write end height
	if err := writer.WriteEndHeight(1); err != nil {
		t.Fatalf("failed to write end height: %v", err)
	}

	// Flush
	if err := writer.Flush(); err != nil {
		t.Fatalf("failed to flush: %v", err)
	}
}

func TestWALWriterState(t *testing.T) {
	dir := t.TempDir()

	w, err := wal.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}
	defer w.Stop()

	writer := NewWALWriter(w)

	state := &gen.ConsensusStateData{
		Height: 1,
		Round:  0,
		Step:   RoundStepPropose,
	}

	if err := writer.WriteState(state); err != nil {
		t.Fatalf("failed to write state: %v", err)
	}
}

func TestReplayNoWAL(t *testing.T) {
	// Create consensus state without WAL
	cs := &ConsensusState{}

	_, err := cs.ReplayWAL(1)
	if err != ErrNoWAL {
		t.Errorf("expected ErrNoWAL, got %v", err)
	}
}

func TestReplayEmptyWAL(t *testing.T) {
	dir := t.TempDir()

	w, err := wal.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}
	defer w.Stop()

	cs := &ConsensusState{
		wal: w,
	}

	result, err := cs.ReplayWAL(1)
	if err != nil {
		t.Fatalf("ReplayWAL failed: %v", err)
	}

	if result.MessagesReplayed != 0 {
		t.Errorf("expected 0 messages replayed, got %d", result.MessagesReplayed)
	}
}

func TestReplayWithMessages(t *testing.T) {
	dir := t.TempDir()

	w, err := wal.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}

	// Write messages for height 1
	writer := NewWALWriter(w)

	blockHash := types.HashBytes([]byte("block"))
	proposal := &gen.Proposal{
		Height:    1,
		Round:     0,
		Timestamp: 1000,
		Proposer:  types.NewAccountName("alice"),
	}
	writer.WriteProposal(1, 0, proposal)

	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}
	writer.WriteVote(1, 0, vote)

	writer.WriteEndHeight(1)

	// Write messages for height 2
	proposal2 := &gen.Proposal{
		Height:    2,
		Round:     0,
		Timestamp: 2000,
		Proposer:  types.NewAccountName("bob"),
	}
	writer.WriteProposal(2, 0, proposal2)

	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         2,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      2000,
		Validator:      types.NewAccountName("bob"),
		ValidatorIndex: 1,
	}
	writer.WriteVote(2, 0, vote2)

	writer.Flush()
	w.Stop()

	// Reopen WAL and replay
	w2, err := wal.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("failed to reopen WAL: %v", err)
	}
	if err := w2.Start(); err != nil {
		t.Fatalf("failed to start WAL: %v", err)
	}
	defer w2.Stop()

	cs := &ConsensusState{
		wal: w2,
	}

	// Replay height 2
	result, err := cs.ReplayWAL(2)
	if err != nil {
		t.Fatalf("ReplayWAL failed: %v", err)
	}

	if result.Height != 2 {
		t.Errorf("expected height 2, got %d", result.Height)
	}

	if result.Proposal == nil {
		t.Error("expected proposal to be recovered")
	}

	if len(result.Votes) != 1 {
		t.Errorf("expected 1 vote, got %d", len(result.Votes))
	}
}

func TestReplayResultFields(t *testing.T) {
	result := &WALReplayResult{
		Height: 1,
		Round:  0,
		Step:   RoundStepPropose,
	}

	if result.Height != 1 {
		t.Errorf("expected height 1, got %d", result.Height)
	}
	if result.Round != 0 {
		t.Errorf("expected round 0, got %d", result.Round)
	}
	if result.Step != RoundStepPropose {
		t.Errorf("expected step RoundStepPropose, got %v", result.Step)
	}
}
