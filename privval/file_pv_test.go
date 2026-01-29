package privval

import (
	"path/filepath"
	"testing"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

func TestGenerateFilePV(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}
	defer pv.Close()

	// Check that public key is valid
	pubKey := pv.GetPubKey()
	if len(pubKey.Data) != 32 {
		t.Errorf("expected 32-byte public key, got %d bytes", len(pubKey.Data))
	}

	// Check that address is derived correctly
	addr := pv.GetAddress()
	if len(addr) != 20 {
		t.Errorf("expected 20-byte address, got %d bytes", len(addr))
	}
}

func TestNewFilePV(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	// First call should generate new keys
	pv1, err := NewFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to create FilePV: %v", err)
	}

	pubKey1 := pv1.GetPubKey()

	// TWENTY_SECOND_REFACTOR: Close pv1 to release file lock before loading pv2
	// File locking prevents multiple instances from using the same validator
	if err := pv1.Close(); err != nil {
		t.Fatalf("failed to close pv1: %v", err)
	}

	// Second call should load existing keys
	pv2, err := NewFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to load FilePV: %v", err)
	}
	defer pv2.Close()

	pubKey2 := pv2.GetPubKey()

	// Keys should be the same
	if !types.PublicKeyEqual(pubKey1, pubKey2) {
		t.Error("loaded key should match generated key")
	}
}

func TestFilePVSignVote(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}
	defer pv.Close()

	blockHash := types.HashBytes([]byte("test-block"))
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	// Sign the vote
	err = pv.SignVote("test-chain", vote)
	if err != nil {
		t.Fatalf("failed to sign vote: %v", err)
	}

	// Check that signature was added
	if len(vote.Signature.Data) == 0 {
		t.Error("vote should have signature")
	}
}

func TestFilePVDoubleSignPrevention(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}

	blockHash1 := types.HashBytes([]byte("block1"))
	vote1 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash1,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	// Sign first vote
	err = pv.SignVote("test-chain", vote1)
	if err != nil {
		t.Fatalf("failed to sign first vote: %v", err)
	}

	// Try to sign a different vote at same H/R/S
	blockHash2 := types.HashBytes([]byte("block2"))
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash2,
		Timestamp:      1001,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote2)
	if err != ErrDoubleSign {
		t.Errorf("expected ErrDoubleSign, got %v", err)
	}
}

func TestFilePVIdempotentSign(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}

	blockHash := types.HashBytes([]byte("block"))
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	// Sign first time
	err = pv.SignVote("test-chain", vote)
	if err != nil {
		t.Fatalf("failed to sign vote: %v", err)
	}

	sig1 := vote.Signature

	// Sign same vote again (idempotent)
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote2)
	if err != nil {
		t.Fatalf("idempotent sign should succeed: %v", err)
	}

	// Should return same signature
	if string(sig1.Data) != string(vote2.Signature.Data) {
		t.Error("idempotent sign should return same signature")
	}
}

func TestFilePVSignProposal(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}

	proposal := &gen.Proposal{
		Height:    1,
		Round:     0,
		Timestamp: 1000,
		Proposer:  types.NewAccountName("test"),
	}

	// Sign the proposal
	err = pv.SignProposal("test-chain", proposal)
	if err != nil {
		t.Fatalf("failed to sign proposal: %v", err)
	}

	// Check that signature was added
	if len(proposal.Signature.Data) == 0 {
		t.Error("proposal should have signature")
	}
}

func TestFilePVHeightRegression(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}

	blockHash := types.HashBytes([]byte("block"))

	// Sign at height 5
	vote1 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         5,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote1)
	if err != nil {
		t.Fatalf("failed to sign vote: %v", err)
	}

	// Try to sign at height 3 (regression)
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         3,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1001,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote2)
	if err != ErrHeightRegression {
		t.Errorf("expected ErrHeightRegression, got %v", err)
	}
}

func TestFilePVRoundRegression(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}

	blockHash := types.HashBytes([]byte("block"))

	// Sign at round 5
	vote1 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          5,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote1)
	if err != nil {
		t.Fatalf("failed to sign vote: %v", err)
	}

	// Try to sign at round 3 (regression)
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          3,
		BlockHash:      &blockHash,
		Timestamp:      1001,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote2)
	if err != ErrRoundRegression {
		t.Errorf("expected ErrRoundRegression, got %v", err)
	}
}

func TestFilePVStepProgression(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}

	blockHash := types.HashBytes([]byte("block"))

	// Sign prevote
	prevote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", prevote)
	if err != nil {
		t.Fatalf("failed to sign prevote: %v", err)
	}

	// Sign precommit at same H/R should work (step progression)
	precommit := &gen.Vote{
		Type:           types.VoteTypePrecommit,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1001,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", precommit)
	if err != nil {
		t.Fatalf("precommit after prevote should succeed: %v", err)
	}
}

func TestFilePVReset(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")

	pv, err := GenerateFilePV(keyPath, statePath)
	if err != nil {
		t.Fatalf("failed to generate FilePV: %v", err)
	defer pv.Close()
	}

	blockHash := types.HashBytes([]byte("block"))

	// Sign at height 10
	vote := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         10,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	_ = pv.SignVote("test-chain", vote)

	// Reset
	err = pv.Reset()
	if err != nil {
		t.Fatalf("failed to reset: %v", err)
	}

	// Should be able to sign at height 1 now
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1001,
		Validator:      types.NewAccountName("test"),
		ValidatorIndex: 0,
	}

	err = pv.SignVote("test-chain", vote2)
	if err != nil {
		t.Fatalf("should be able to sign after reset: %v", err)
	}
}

func TestLastSignStateCheckHRS(t *testing.T) {
	tests := []struct {
		name    string
		state   LastSignState
		height  int64
		round   int32
		step    int8
		wantErr error
	}{
		{
			name:    "fresh state allows any",
			state:   LastSignState{},
			height:  1,
			round:   0,
			step:    StepPrevote,
			wantErr: nil,
		},
		{
			name:    "height progression",
			state:   LastSignState{Height: 1, Round: 5, Step: StepPrecommit},
			height:  2,
			round:   0,
			step:    StepPrevote,
			wantErr: nil,
		},
		{
			name:    "round progression",
			state:   LastSignState{Height: 1, Round: 0, Step: StepPrecommit},
			height:  1,
			round:   1,
			step:    StepPrevote,
			wantErr: nil,
		},
		{
			name:    "step progression",
			state:   LastSignState{Height: 1, Round: 0, Step: StepPrevote},
			height:  1,
			round:   0,
			step:    StepPrecommit,
			wantErr: nil,
		},
		{
			name:    "height regression",
			state:   LastSignState{Height: 5, Round: 0, Step: StepPrevote},
			height:  3,
			round:   0,
			step:    StepPrevote,
			wantErr: ErrHeightRegression,
		},
		{
			name:    "round regression",
			state:   LastSignState{Height: 1, Round: 5, Step: StepPrevote},
			height:  1,
			round:   3,
			step:    StepPrevote,
			wantErr: ErrRoundRegression,
		},
		{
			name:    "step regression",
			state:   LastSignState{Height: 1, Round: 0, Step: StepPrecommit},
			height:  1,
			round:   0,
			step:    StepPrevote,
			wantErr: ErrStepRegression,
		},
		{
			name:    "double sign same HRS",
			state:   LastSignState{Height: 1, Round: 0, Step: StepPrevote},
			height:  1,
			round:   0,
			step:    StepPrevote,
			wantErr: ErrDoubleSign,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.state.CheckHRS(tt.height, tt.round, tt.step)
			if err != tt.wantErr {
				t.Errorf("CheckHRS() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVoteStep(t *testing.T) {
	if VoteStep(types.VoteTypePrevote) != StepPrevote {
		t.Error("VoteTypePrevote should map to StepPrevote")
	}
	if VoteStep(types.VoteTypePrecommit) != StepPrecommit {
		t.Error("VoteTypePrecommit should map to StepPrecommit")
	}
	if VoteStep(99) != 0 {
		t.Error("unknown vote type should map to 0")
	}
}
