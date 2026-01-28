package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// Block sync configuration
const (
	// MaxBlocksPerRequest is the maximum blocks to request at once
	MaxBlocksPerRequest = 20

	// BlockSyncTimeout is the timeout for block requests
	BlockSyncTimeout = 10 * time.Second

	// PeerHeightUpdateInterval is how often we check peer heights
	PeerHeightUpdateInterval = 2 * time.Second

	// MaxPendingRequests is the max concurrent block requests
	MaxPendingRequests = 5
)

// Block sync errors
var (
	ErrBlockSyncNotStarted = errors.New("block sync not started")
	ErrBlockSyncTimeout    = errors.New("block sync request timeout")
	ErrInvalidCommit       = errors.New("invalid commit received")
	ErrNoAvailablePeers    = errors.New("no peers available for sync")
	ErrBlockHeightMismatch = errors.New("block height mismatch")
)

// BlockProvider is the interface for fetching blocks
type BlockProvider interface {
	// RequestBlock requests a block at a specific height from a peer
	RequestBlock(ctx context.Context, peerID string, height int64) error
	// GetPeerHeight returns the current height of a peer
	GetPeerHeight(peerID string) int64
	// GetAvailablePeers returns peers that can provide blocks
	GetAvailablePeers() []string
}

// BlockStore is the interface for storing/retrieving blocks
type BlockStore interface {
	// SaveBlock saves a block with its commit
	SaveBlock(block *gen.Block, commit *gen.Commit) error
	// LoadBlock loads a block at height
	LoadBlock(height int64) (*gen.Block, error)
	// LoadCommit loads the commit for a block at height
	LoadCommit(height int64) (*gen.Commit, error)
	// Height returns the current height of the store
	Height() int64
}

// BlockSyncState tracks the state of block synchronization
type BlockSyncState int

const (
	// BlockSyncStateIdle - not syncing
	BlockSyncStateIdle BlockSyncState = iota
	// BlockSyncStateSyncing - actively syncing blocks
	BlockSyncStateSyncing
	// BlockSyncStateCaughtUp - caught up with network
	BlockSyncStateCaughtUp
)

// BlockSyncer manages block synchronization with peers
type BlockSyncer struct {
	mu sync.RWMutex

	chainID   string
	state     BlockSyncState
	store     BlockStore
	provider  BlockProvider
	valSet    *types.ValidatorSet
	peerSet   *PeerSet

	// Sync progress
	startHeight   int64  // Height when sync started
	targetHeight  int64  // Target height to sync to
	currentHeight int64  // Current sync height

	// Pending requests
	pendingRequests map[int64]*blockRequest
	requestTimeout  time.Duration

	// Callbacks
	onBlockCommitted func(block *gen.Block, commit *gen.Commit)
	onCaughtUp       func()

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	started bool
}

// blockRequest tracks a pending block request
type blockRequest struct {
	height    int64
	peerID    string
	requestAt time.Time
}

// NewBlockSyncer creates a new block syncer
func NewBlockSyncer(
	chainID string,
	store BlockStore,
	provider BlockProvider,
	valSet *types.ValidatorSet,
	peerSet *PeerSet,
) *BlockSyncer {
	return &BlockSyncer{
		chainID:         chainID,
		store:           store,
		provider:        provider,
		valSet:          valSet,
		peerSet:         peerSet,
		state:           BlockSyncStateIdle,
		pendingRequests: make(map[int64]*blockRequest),
		requestTimeout:  BlockSyncTimeout,
	}
}

// SetOnBlockCommitted sets the callback for when a block is committed during sync
func (bs *BlockSyncer) SetOnBlockCommitted(fn func(*gen.Block, *gen.Commit)) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.onBlockCommitted = fn
}

// SetOnCaughtUp sets the callback for when sync completes
func (bs *BlockSyncer) SetOnCaughtUp(fn func()) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.onCaughtUp = fn
}

// Start starts the block syncer
func (bs *BlockSyncer) Start() error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if bs.started {
		return errors.New("already started")
	}

	bs.ctx, bs.cancel = context.WithCancel(context.Background())
	bs.started = true
	bs.state = BlockSyncStateIdle
	bs.startHeight = bs.store.Height()
	bs.currentHeight = bs.startHeight

	bs.wg.Add(1)
	go bs.syncLoop()

	return nil
}

// Stop stops the block syncer
func (bs *BlockSyncer) Stop() error {
	bs.mu.Lock()
	if !bs.started {
		bs.mu.Unlock()
		return errors.New("not started")
	}
	bs.started = false
	bs.mu.Unlock()

	bs.cancel()
	bs.wg.Wait()
	return nil
}

// GetState returns the current sync state
func (bs *BlockSyncer) GetState() BlockSyncState {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.state
}

// GetProgress returns sync progress (current, target)
func (bs *BlockSyncer) GetProgress() (current, target int64) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.currentHeight, bs.targetHeight
}

// IsSyncing returns true if actively syncing
func (bs *BlockSyncer) IsSyncing() bool {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.state == BlockSyncStateSyncing
}

// IsCaughtUp returns true if caught up with network
func (bs *BlockSyncer) IsCaughtUp() bool {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.state == BlockSyncStateCaughtUp
}

// UpdateTargetHeight updates the target height from peer information
func (bs *BlockSyncer) UpdateTargetHeight() {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Find max height from peers
	maxHeight := bs.currentHeight
	for _, peer := range bs.peerSet.AllPeers() {
		peerHeight := peer.Height()
		if peerHeight > maxHeight {
			maxHeight = peerHeight
		}
	}

	bs.targetHeight = maxHeight

	// Update state based on progress
	// NINTH_REFACTOR: Added CaughtUp → Syncing transition. Previously, a node that
	// caught up and then fell behind (e.g., temporary network disconnect) would
	// never start syncing again because only Idle → Syncing was allowed.
	if bs.currentHeight >= bs.targetHeight {
		if bs.state == BlockSyncStateSyncing {
			bs.state = BlockSyncStateCaughtUp
			log.Printf("[INFO] blocksync: caught up at height %d", bs.currentHeight)
			// TENTH_REFACTOR: Track callback goroutine with WaitGroup like ReceiveBlock does.
			// Previously this goroutine wasn't tracked, causing race conditions on Stop().
			if bs.onCaughtUp != nil {
				bs.wg.Add(1)
				go func() {
					defer bs.wg.Done()
					bs.onCaughtUp()
				}()
			}
		}
	} else if bs.state == BlockSyncStateIdle || bs.state == BlockSyncStateCaughtUp {
		if bs.state == BlockSyncStateCaughtUp {
			log.Printf("[INFO] blocksync: fell behind, resuming sync from %d to %d", bs.currentHeight, bs.targetHeight)
		} else {
			log.Printf("[INFO] blocksync: starting sync from %d to %d", bs.currentHeight, bs.targetHeight)
		}
		bs.state = BlockSyncStateSyncing
	}
}

// syncLoop is the main sync loop
func (bs *BlockSyncer) syncLoop() {
	defer bs.wg.Done()

	ticker := time.NewTicker(PeerHeightUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bs.ctx.Done():
			return
		case <-ticker.C:
			bs.UpdateTargetHeight()
			bs.requestMissingBlocks()
			bs.checkTimeouts()
		}
	}
}

// requestMissingBlocks requests blocks we need
func (bs *BlockSyncer) requestMissingBlocks() {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if bs.state != BlockSyncStateSyncing {
		return
	}

	// Limit concurrent requests
	if len(bs.pendingRequests) >= MaxPendingRequests {
		return
	}

	// Request next blocks
	for height := bs.currentHeight + 1; height <= bs.targetHeight; height++ {
		if len(bs.pendingRequests) >= MaxPendingRequests {
			break
		}

		// Skip if already pending
		if _, exists := bs.pendingRequests[height]; exists {
			continue
		}

		// Find a peer that has this block
		peerID := bs.findPeerForHeight(height)
		if peerID == "" {
			continue
		}

		// Request the block
		if err := bs.provider.RequestBlock(bs.ctx, peerID, height); err != nil {
			log.Printf("[DEBUG] blocksync: failed to request block %d from %s: %v", height, peerID, err)
			continue
		}

		bs.pendingRequests[height] = &blockRequest{
			height:    height,
			peerID:    peerID,
			requestAt: time.Now(),
		}
	}
}

// findPeerForHeight finds a peer that can provide a block at height
func (bs *BlockSyncer) findPeerForHeight(height int64) string {
	for _, peer := range bs.peerSet.AllPeers() {
		if peer.Height() >= height {
			return peer.PeerID()
		}
	}
	return ""
}

// checkTimeouts checks for timed out requests
func (bs *BlockSyncer) checkTimeouts() {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	now := time.Now()
	for height, req := range bs.pendingRequests {
		if now.Sub(req.requestAt) > bs.requestTimeout {
			log.Printf("[DEBUG] blocksync: request for block %d timed out", height)
			delete(bs.pendingRequests, height)
		}
	}
}

// ReceiveBlock handles a received block and commit
func (bs *BlockSyncer) ReceiveBlock(block *gen.Block, commit *gen.Commit) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// M5: Validate inputs
	if block == nil || commit == nil {
		return errors.New("nil block or commit")
	}

	height := block.Header.Height

	// M5: Verify commit height matches block height
	if commit.Height != height {
		return fmt.Errorf("commit height %d doesn't match block height %d", commit.Height, height)
	}

	// Check if we requested this block
	if _, exists := bs.pendingRequests[height]; !exists {
		// Unsolicited block - could be from gossip
		if height != bs.currentHeight+1 {
			return nil // Ignore blocks we don't need right now
		}
	}

	// Verify the commit
	blockHash := types.BlockHash(block)
	if err := types.VerifyCommit(bs.chainID, bs.valSet, blockHash, height, commit); err != nil {
		log.Printf("[WARN] blocksync: invalid commit for block %d: %v", height, err)
		return fmt.Errorf("%w: %v", ErrInvalidCommit, err)
	}

	// Store the block
	if err := bs.store.SaveBlock(block, commit); err != nil {
		return fmt.Errorf("failed to save block: %w", err)
	}

	// Remove from pending
	delete(bs.pendingRequests, height)

	// Update progress
	if height == bs.currentHeight+1 {
		bs.currentHeight = height

		// Notify callback
		// H5: Track callback goroutines with WaitGroup
		// SEVENTH_REFACTOR: Deep copy block and commit before passing to async callback.
		// This prevents race conditions where the caller might modify the originals
		// after ReceiveBlock returns but before the callback executes.
		if bs.onBlockCommitted != nil {
			blockCopy := types.CopyBlock(block)
			commitCopy := types.CopyCommit(commit)
			bs.wg.Add(1)
			go func() {
				defer bs.wg.Done()
				bs.onBlockCommitted(blockCopy, commitCopy)
			}()
		}

		// Check if caught up
		if bs.currentHeight >= bs.targetHeight {
			bs.state = BlockSyncStateCaughtUp
			log.Printf("[INFO] blocksync: caught up at height %d", bs.currentHeight)
			// H5: Track callback goroutines with WaitGroup
			if bs.onCaughtUp != nil {
				bs.wg.Add(1)
				go func() {
					defer bs.wg.Done()
					bs.onCaughtUp()
				}()
			}
		}
	}

	return nil
}

// UpdateValidatorSet updates the validator set used for commit verification
func (bs *BlockSyncer) UpdateValidatorSet(valSet *types.ValidatorSet) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.valSet = valSet
}

// SyncStatus contains sync progress information
type SyncStatus struct {
	State         BlockSyncState
	StartHeight   int64
	CurrentHeight int64
	TargetHeight  int64
	Pending       int
}

// Status returns the current sync status
func (bs *BlockSyncer) Status() SyncStatus {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	return SyncStatus{
		State:         bs.state,
		StartHeight:   bs.startHeight,
		CurrentHeight: bs.currentHeight,
		TargetHeight:  bs.targetHeight,
		Pending:       len(bs.pendingRequests),
	}
}

// BlockSyncRequest represents a request for a block
type BlockSyncRequest struct {
	Height int64
}

// BlockSyncResponse represents a response with a block
type BlockSyncResponse struct {
	Height int64
	Block  *gen.Block
	Commit *gen.Commit
}

// BlockSyncStatusRequest requests sync status from a peer
type BlockSyncStatusRequest struct{}

// BlockSyncStatusResponse contains peer's sync status
type BlockSyncStatusResponse struct {
	Height int64
	Hash   types.Hash // Block hash at height
}
