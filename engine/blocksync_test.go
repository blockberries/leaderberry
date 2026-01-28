package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// mockBlockStore implements BlockStore for testing
type mockBlockStore struct {
	mu      sync.Mutex
	blocks  map[int64]*gen.Block
	commits map[int64]*gen.Commit
	height  int64
}

func newMockBlockStore() *mockBlockStore {
	return &mockBlockStore{
		blocks:  make(map[int64]*gen.Block),
		commits: make(map[int64]*gen.Commit),
		height:  0,
	}
}

func (s *mockBlockStore) SaveBlock(block *gen.Block, commit *gen.Commit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocks[block.Header.Height] = block
	s.commits[block.Header.Height] = commit
	if block.Header.Height > s.height {
		s.height = block.Header.Height
	}
	return nil
}

func (s *mockBlockStore) LoadBlock(height int64) (*gen.Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blocks[height], nil
}

func (s *mockBlockStore) LoadCommit(height int64) (*gen.Commit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits[height], nil
}

func (s *mockBlockStore) Height() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.height
}

// mockBlockProvider implements BlockProvider for testing
type mockBlockProvider struct {
	mu          sync.Mutex
	peerHeights map[string]int64
	requests    []BlockSyncRequest
}

func newMockBlockProvider() *mockBlockProvider {
	return &mockBlockProvider{
		peerHeights: make(map[string]int64),
	}
}

func (p *mockBlockProvider) RequestBlock(ctx context.Context, peerID string, height int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, BlockSyncRequest{Height: height})
	return nil
}

func (p *mockBlockProvider) GetPeerHeight(peerID string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peerHeights[peerID]
}

func (p *mockBlockProvider) GetAvailablePeers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	peers := make([]string, 0, len(p.peerHeights))
	for id := range p.peerHeights {
		peers = append(peers, id)
	}
	return peers
}

func (p *mockBlockProvider) SetPeerHeight(peerID string, height int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peerHeights[peerID] = height
}

func TestBlockSyncerCreation(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	if bs.GetState() != BlockSyncStateIdle {
		t.Error("expected idle state initially")
	}
}

func TestBlockSyncerStartStop(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	if err := bs.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Double start should fail
	if err := bs.Start(); err == nil {
		t.Error("expected error on double start")
	}

	if err := bs.Stop(); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	// Double stop should fail
	if err := bs.Stop(); err == nil {
		t.Error("expected error on double stop")
	}
}

func TestBlockSyncerUpdateTargetHeight(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	// Add some peers with heights
	peer1 := peerSet.AddPeer("peer1")
	peer2 := peerSet.AddPeer("peer2")

	peer1.ApplyNewRoundStep(10, 0, RoundStepPrevote)
	peer2.ApplyNewRoundStep(15, 0, RoundStepPrevote)

	bs.UpdateTargetHeight()

	_, target := bs.GetProgress()
	if target != 15 {
		t.Errorf("expected target height 15, got %d", target)
	}

	// Should transition to syncing
	if bs.GetState() != BlockSyncStateSyncing {
		t.Error("expected syncing state")
	}
}

func TestBlockSyncerProgress(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	// Initial progress
	current, target := bs.GetProgress()
	if current != 0 || target != 0 {
		t.Errorf("expected 0/0 progress, got %d/%d", current, target)
	}

	// Add peer at higher height
	peer := peerSet.AddPeer("peer1")
	peer.ApplyNewRoundStep(5, 0, RoundStepPrevote)

	bs.UpdateTargetHeight()

	current, target = bs.GetProgress()
	if target != 5 {
		t.Errorf("expected target 5, got %d", target)
	}
}

func TestBlockSyncerStatus(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	status := bs.Status()
	if status.State != BlockSyncStateIdle {
		t.Errorf("expected idle state, got %v", status.State)
	}
	if status.StartHeight != 0 {
		t.Errorf("expected start height 0, got %d", status.StartHeight)
	}
	if status.CurrentHeight != 0 {
		t.Errorf("expected current height 0, got %d", status.CurrentHeight)
	}
}

func TestBlockSyncerCaughtUp(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	// Start with store at height 10
	store.height = 10
	bs.currentHeight = 10

	// Add peer also at height 10
	peer := peerSet.AddPeer("peer1")
	peer.ApplyNewRoundStep(10, 0, RoundStepPrevote)

	// Manually transition to syncing first
	bs.state = BlockSyncStateSyncing

	bs.UpdateTargetHeight()

	// Should be caught up
	if bs.GetState() != BlockSyncStateCaughtUp {
		t.Error("expected caught up state")
	}
	if !bs.IsCaughtUp() {
		t.Error("IsCaughtUp should be true")
	}
}

func TestBlockSyncerCallbacks(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	caughtUpCalled := make(chan struct{}, 1)
	bs.SetOnCaughtUp(func() {
		caughtUpCalled <- struct{}{}
	})

	// Setup: syncing state at height 5, peer at height 5
	bs.state = BlockSyncStateSyncing
	bs.currentHeight = 5
	peer := peerSet.AddPeer("peer1")
	peer.ApplyNewRoundStep(5, 0, RoundStepPrevote)

	bs.UpdateTargetHeight()

	// Should trigger caught up callback
	select {
	case <-caughtUpCalled:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("caught up callback not called")
	}
}

func TestBlockSyncerUpdateValidatorSet(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	// Update to new validator set
	newValSet := makeValidatorSetN(t, 6)
	bs.UpdateValidatorSet(newValSet)

	// Verify it was updated
	if bs.valSet.Size() != 6 {
		t.Errorf("expected 6 validators, got %d", bs.valSet.Size())
	}
}

func TestBlockSyncerIsSyncing(t *testing.T) {
	valSet := makeValidatorSetN(t, 4)
	store := newMockBlockStore()
	provider := newMockBlockProvider()
	peerSet := NewPeerSet(valSet)

	bs := NewBlockSyncer("test-chain", store, provider, valSet, peerSet)

	if bs.IsSyncing() {
		t.Error("should not be syncing initially")
	}

	bs.state = BlockSyncStateSyncing
	if !bs.IsSyncing() {
		t.Error("should be syncing")
	}

	bs.state = BlockSyncStateCaughtUp
	if bs.IsSyncing() {
		t.Error("should not be syncing when caught up")
	}
}
