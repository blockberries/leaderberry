package engine

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockberries/leaderberry/evidence"
	"github.com/blockberries/leaderberry/types"
	"github.com/blockberries/leaderberry/wal"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// Channel buffer sizes
const (
	proposalChannelSize = 100
	voteChannelSize     = 10000
)

// ConsensusState manages the consensus state machine
type ConsensusState struct {
	mu sync.RWMutex

	// Configuration
	config *Config

	// Validator management
	validatorSet *types.ValidatorSet
	privVal      PrivValidator

	// WAL for crash recovery
	wal WAL

	// Current consensus state
	height int64
	round  int32
	step   RoundStep

	// Proposal for current round
	proposal      *gen.Proposal
	proposalBlock *gen.Block

	// Locking - set when we see 2/3+ prevotes for a block
	lockedRound int32
	lockedBlock *gen.Block

	// Valid block - set when we receive a valid proposal
	validRound int32
	validBlock *gen.Block

	// Vote tracking
	votes *HeightVoteSet

	// Last commit (for the previous height)
	lastCommit *gen.Commit

	// Timeouts
	timeoutTicker *TimeoutTicker

	// Event channels
	proposalCh chan *gen.Proposal
	voteCh     chan *gen.Vote
	commitCh   chan struct{} // signal that commit is ready

	// Block execution callback
	blockExecutor BlockExecutor

	// Broadcast callbacks (CR1/M8: for sending messages to peers)
	onProposal func(*gen.Proposal)
	onVote     func(*gen.Vote)

	// M3: Evidence pool for Byzantine fault detection
	evidencePool *evidence.Pool

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// State
	started bool

	// Metrics - track dropped messages
	droppedProposals uint64
	droppedVotes     uint64
}

// PrivValidator interface for signing consensus messages
type PrivValidator interface {
	GetPubKey() types.PublicKey
	SignVote(chainID string, vote *gen.Vote) error
	SignProposal(chainID string, proposal *gen.Proposal) error
}

// WAL is an alias for the wal package's WAL interface
type WAL = wal.WAL

// ValidatorUpdate represents a change to a validator's voting power.
// MF3: Used to communicate validator set changes from block execution.
type ValidatorUpdate struct {
	Name        types.AccountName
	PublicKey   types.PublicKey
	VotingPower int64 // 0 means remove validator
}

// BlockExecutor interface for executing blocks
type BlockExecutor interface {
	// CreateProposalBlock creates a new block proposal
	CreateProposalBlock(height int64, lastCommit *gen.Commit, proposer types.AccountName) (*gen.Block, error)
	// ValidateBlock validates a proposed block
	ValidateBlock(block *gen.Block) error
	// ApplyBlock applies a committed block and returns validator updates.
	// MF3: Returns validator updates to be applied to the validator set.
	// An empty slice means no changes. Validators with VotingPower=0 are removed.
	ApplyBlock(block *gen.Block, commit *gen.Commit) ([]ValidatorUpdate, error)
}

// NewConsensusState creates a new ConsensusState
func NewConsensusState(
	config *Config,
	valSet *types.ValidatorSet,
	privVal PrivValidator,
	wal WAL,
	executor BlockExecutor,
) *ConsensusState {
	return &ConsensusState{
		config:        config,
		validatorSet:  valSet,
		privVal:       privVal,
		wal:           wal,
		blockExecutor: executor,
		timeoutTicker: NewTimeoutTicker(config.Timeouts),
		proposalCh:    make(chan *gen.Proposal, proposalChannelSize),
		voteCh:        make(chan *gen.Vote, voteChannelSize),
		commitCh:      make(chan struct{}, 1),
		lockedRound:   -1,
		validRound:    -1,
	}
}

// SetBroadcastCallbacks sets the callbacks for broadcasting messages to peers (M8)
func (cs *ConsensusState) SetBroadcastCallbacks(
	onProposal func(*gen.Proposal),
	onVote func(*gen.Vote),
) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.onProposal = onProposal
	cs.onVote = onVote
}

// SetEvidencePool sets the evidence pool for Byzantine fault detection (M3)
func (cs *ConsensusState) SetEvidencePool(pool *evidence.Pool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.evidencePool = pool
}

// applyValidatorUpdates applies validator updates to the current set and returns a new set.
// MF3: This method handles validator set changes from block execution:
// - VotingPower > 0: Add or update validator
// - VotingPower == 0: Remove validator
// The returned set has proposer priorities incremented by 1.
func (cs *ConsensusState) applyValidatorUpdates(updates []ValidatorUpdate) (*types.ValidatorSet, error) {
	// Build a map of current validators by name for quick lookup
	currentVals := make(map[string]*types.NamedValidator)
	for _, v := range cs.validatorSet.Validators {
		name := types.AccountNameString(v.Name)
		currentVals[name] = v
	}

	// Apply updates
	for _, update := range updates {
		name := types.AccountNameString(update.Name)

		if update.VotingPower == 0 {
			// Remove validator
			delete(currentVals, name)
		} else {
			// Add or update validator
			if existing, ok := currentVals[name]; ok {
				// Update existing - preserve priority, update power and key
				currentVals[name] = &types.NamedValidator{
					Name:             existing.Name,
					Index:            existing.Index, // Will be reassigned
					PublicKey:        update.PublicKey,
					VotingPower:      update.VotingPower,
					ProposerPriority: existing.ProposerPriority,
				}
			} else {
				// Add new validator with zero priority (will be adjusted)
				currentVals[name] = &types.NamedValidator{
					Name:             update.Name,
					Index:            0, // Will be assigned by NewValidatorSet
					PublicKey:        update.PublicKey,
					VotingPower:      update.VotingPower,
					ProposerPriority: 0,
				}
			}
		}
	}

	// Convert map back to slice
	newVals := make([]*types.NamedValidator, 0, len(currentVals))
	for _, v := range currentVals {
		newVals = append(newVals, v)
	}

	// Check we still have validators
	if len(newVals) == 0 {
		return nil, fmt.Errorf("validator set would be empty after updates")
	}

	// Create new validator set (this reindexes and validates)
	newSet, err := types.NewValidatorSet(newVals)
	if err != nil {
		return nil, fmt.Errorf("failed to create new validator set: %w", err)
	}

	// CR1: Increment proposer priority using immutable pattern (not deprecated mutable method)
	newSet, err = newSet.WithIncrementedPriority(1)
	if err != nil {
		// This should never fail for a valid set we just created
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to increment priority on new validator set: %v", err))
	}

	return newSet, nil
}

// Start starts the consensus state machine at the given height
// CR3: Fixed race condition by calling enterNewRoundLocked while holding lock
func (cs *ConsensusState) Start(height int64, lastCommit *gen.Commit) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.started {
		return ErrAlreadyStarted
	}

	cs.ctx, cs.cancel = context.WithCancel(context.Background())
	cs.height = height
	cs.lastCommit = lastCommit
	cs.votes = NewHeightVoteSet(cs.config.ChainID, height, cs.validatorSet)
	cs.started = true

	// Start timeout ticker
	cs.timeoutTicker.Start()

	// Start main loop
	cs.wg.Add(1)
	go cs.receiveRoutine()

	// Enter new round while holding lock (CR3 fix)
	cs.enterNewRoundLocked(height, 0)

	return nil
}

// Stop stops the consensus state machine
func (cs *ConsensusState) Stop() error {
	cs.mu.Lock()
	if !cs.started {
		cs.mu.Unlock()
		return ErrNotStarted
	}
	cs.started = false
	cs.mu.Unlock()

	cs.cancel()
	cs.timeoutTicker.Stop()
	cs.wg.Wait()

	return nil
}

// AddProposal adds a proposal from the network
func (cs *ConsensusState) AddProposal(proposal *gen.Proposal) {
	select {
	case cs.proposalCh <- proposal:
	default:
		// Channel full, drop proposal and log warning
		dropped := atomic.AddUint64(&cs.droppedProposals, 1)
		log.Printf("[WARN] consensus: dropped proposal due to full channel (height=%d round=%d proposer=%s total_dropped=%d)",
			proposal.Height, proposal.Round, types.AccountNameString(proposal.Proposer), dropped)
	}
}

// AddVote adds a vote from the network
func (cs *ConsensusState) AddVote(vote *gen.Vote) {
	select {
	case cs.voteCh <- vote:
	default:
		// Channel full, drop vote and log warning
		dropped := atomic.AddUint64(&cs.droppedVotes, 1)
		log.Printf("[WARN] consensus: dropped vote due to full channel (height=%d round=%d type=%d validator=%s total_dropped=%d)",
			vote.Height, vote.Round, vote.Type, types.AccountNameString(vote.Validator), dropped)
	}
}

// GetDroppedMessageCounts returns counts of dropped messages for monitoring
func (cs *ConsensusState) GetDroppedMessageCounts() (proposals, votes uint64) {
	return atomic.LoadUint64(&cs.droppedProposals), atomic.LoadUint64(&cs.droppedVotes)
}

// receiveRoutine is the main event loop
func (cs *ConsensusState) receiveRoutine() {
	defer cs.wg.Done()

	for {
		select {
		case <-cs.ctx.Done():
			return

		case proposal := <-cs.proposalCh:
			cs.handleProposal(proposal)

		case vote := <-cs.voteCh:
			cs.handleVote(vote)

		case ti := <-cs.timeoutTicker.Chan():
			cs.handleTimeout(ti)
		}
	}
}

// ============================================================================
// State transition functions - public versions acquire lock
// ============================================================================

// enterNewRound enters a new round at the given height (public, acquires lock)
func (cs *ConsensusState) enterNewRound(height int64, round int32) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.enterNewRoundLocked(height, round)
}

// enterPrevote enters the prevote step (public, acquires lock)
func (cs *ConsensusState) enterPrevote(height int64, round int32) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.enterPrevoteLocked(height, round)
}

// enterPrevoteWait enters the prevote wait step (public, acquires lock)
func (cs *ConsensusState) enterPrevoteWait(height int64, round int32) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.enterPrevoteWaitLocked(height, round)
}

// enterPrecommit enters the precommit step (public, acquires lock)
func (cs *ConsensusState) enterPrecommit(height int64, round int32) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.enterPrecommitLocked(height, round)
}

// enterPrecommitWait enters the precommit wait step (public, acquires lock)
func (cs *ConsensusState) enterPrecommitWait(height int64, round int32) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.enterPrecommitWaitLocked(height, round)
}

// enterCommit enters the commit step (public, acquires lock)
func (cs *ConsensusState) enterCommit(height int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.enterCommitLocked(height)
}

// ============================================================================
// State transition functions - locked versions assume lock is held (CR1 fix)
// ============================================================================

// enterNewRoundLocked enters a new round (internal, caller must hold lock)
func (cs *ConsensusState) enterNewRoundLocked(height int64, round int32) {
	if cs.height != height || round < cs.round {
		return
	}

	// Reset round state
	cs.round = round
	cs.step = RoundStepNewRound
	cs.proposal = nil
	cs.proposalBlock = nil

	if round == 0 {
		// Reset valid block on new height
		cs.validRound = -1
		cs.validBlock = nil
	}

	// Schedule propose timeout
	cs.step = RoundStepPropose
	cs.scheduleTimeout(TimeoutInfo{
		Height: height,
		Round:  round,
		Step:   RoundStepPropose,
	})

	// Check if we're the proposer
	proposer := cs.validatorSet.Proposer
	// H2: Check proposer is not nil before accessing fields
	if proposer == nil {
		log.Printf("[WARN] consensus: no proposer set for height %d round %d", cs.height, cs.round)
		return
	}
	if cs.privVal != nil && types.PublicKeyEqual(proposer.PublicKey, cs.privVal.GetPubKey()) {
		cs.createAndSendProposalLocked()
	}
}

// createAndSendProposalLocked creates and broadcasts a proposal (caller must hold lock)
func (cs *ConsensusState) createAndSendProposalLocked() {
	// Get block to propose
	var block *gen.Block

	if cs.validBlock != nil {
		// Re-propose valid block
		block = cs.validBlock
	} else if cs.lockedBlock != nil {
		// Re-propose locked block
		block = cs.lockedBlock
	} else {
		// Create new block
		if cs.blockExecutor == nil {
			// No executor configured, cannot create proposal
			return
		}
		var err error
		block, err = cs.blockExecutor.CreateProposalBlock(
			cs.height,
			cs.lastCommit,
			cs.validatorSet.Proposer.Name,
		)
		if err != nil {
			log.Printf("[WARN] consensus: failed to create proposal block: %v", err)
			return
		}
	}

	// Create proposal
	proposal := &gen.Proposal{
		Height:    cs.height,
		Round:     cs.round,
		Timestamp: time.Now().UnixNano(),
		Block:     *block,
		PolRound:  cs.validRound,
		Proposer:  cs.validatorSet.Proposer.Name,
	}

	// Add POL votes if we have them
	if cs.validRound >= 0 {
		prevotes := cs.votes.Prevotes(cs.validRound)
		if prevotes != nil {
			votes := prevotes.GetVotes()
			polVotes := make([]gen.Vote, len(votes))
			for i, v := range votes {
				polVotes[i] = *v
			}
			proposal.PolVotes = polVotes
		}
	}

	// Sign proposal
	if err := cs.privVal.SignProposal(cs.config.ChainID, proposal); err != nil {
		// M6: Log at ERROR level - this is a significant failure
		log.Printf("[ERROR] consensus: failed to sign proposal at height %d round %d: %v",
			cs.height, cs.round, err)
		return
	}

	// H1: Write to WAL BEFORE accepting - PANIC on failure
	if cs.wal != nil {
		msg, err := wal.NewProposalMessage(proposal.Height, proposal.Round, proposal)
		if err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to create WAL message for own proposal: %v", err))
		}
		if err := cs.wal.WriteSync(msg); err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write own proposal to WAL: %v", err))
		}
	}

	cs.proposal = proposal
	cs.proposalBlock = block

	// M8: Broadcast proposal to peers
	if cs.onProposal != nil {
		cs.onProposal(proposal)
	}
}

// handleProposal processes a received proposal
func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Validate basic
	if proposal.Height != cs.height || proposal.Round != cs.round {
		return
	}

	// Already have a proposal
	if cs.proposal != nil {
		return
	}

	// Verify proposer
	proposer := cs.validatorSet.Proposer
	// H2: Check proposer is not nil before accessing fields
	if proposer == nil {
		log.Printf("[WARN] consensus: no proposer set, cannot verify proposal")
		return
	}
	if !types.AccountNameEqual(proposal.Proposer, proposer.Name) {
		return
	}

	// Verify signature
	signBytes := types.ProposalSignBytes(cs.config.ChainID, proposal)
	if !types.VerifySignature(proposer.PublicKey, signBytes, proposal.Signature) {
		return
	}

	// M2: Validate POL (Proof of Lock) if present
	if proposal.PolRound >= 0 {
		if err := cs.validatePOL(proposal); err != nil {
			log.Printf("[DEBUG] consensus: proposal POL validation failed: %v", err)
			return
		}
	}

	// Validate block (if executor available)
	if cs.blockExecutor != nil {
		if err := cs.blockExecutor.ValidateBlock(&proposal.Block); err != nil {
			return
		}
	}

	// H1: Write to WAL BEFORE accepting - PANIC on failure
	if cs.wal != nil {
		msg, err := wal.NewProposalMessage(proposal.Height, proposal.Round, proposal)
		if err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to create WAL message for proposal: %v", err))
		}
		if err := cs.wal.WriteSync(msg); err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write proposal to WAL: %v", err))
		}
	}

	// Accept proposal
	cs.proposal = proposal

	// EIGHTH_REFACTOR: Deep copy the block to prevent aliasing issues.
	// A shallow copy is insufficient because Block contains slices (Evidence,
	// Header.BatchCertRefs) and pointers (LastCommit) that would share memory
	// with the original proposal. If the proposal's block is modified after
	// acceptance, it would corrupt our consensus state.
	cs.proposalBlock = types.CopyBlock(&proposal.Block)

	// Enter prevote (use locked version since we hold the lock)
	cs.enterPrevoteLocked(cs.height, cs.round)
}

// validatePOL validates the Proof of Lock votes in a proposal
// M2: Ensures POL votes are valid, from known validators, and total to 2/3+
func (cs *ConsensusState) validatePOL(proposal *gen.Proposal) error {
	if len(proposal.PolVotes) == 0 {
		return fmt.Errorf("POL round %d set but no POL votes provided", proposal.PolRound)
	}

	blockHash := types.BlockHash(&proposal.Block)
	var polPower int64
	seenValidators := make(map[uint16]bool)

	for i, vote := range proposal.PolVotes {
		// Must be prevote
		if vote.Type != types.VoteTypePrevote {
			return fmt.Errorf("POL vote %d is not a prevote (type=%d)", i, vote.Type)
		}

		// Must be for the correct height/round
		if vote.Height != proposal.Height {
			return fmt.Errorf("POL vote %d has wrong height: %d != %d", i, vote.Height, proposal.Height)
		}
		if vote.Round != proposal.PolRound {
			return fmt.Errorf("POL vote %d has wrong round: %d != %d", i, vote.Round, proposal.PolRound)
		}

		// Must be for this block
		if vote.BlockHash == nil {
			return fmt.Errorf("POL vote %d has nil block hash", i)
		}
		if !types.HashEqual(*vote.BlockHash, blockHash) {
			return fmt.Errorf("POL vote %d is for different block", i)
		}

		// Validator must exist
		val := cs.validatorSet.GetByIndex(vote.ValidatorIndex)
		if val == nil {
			return fmt.Errorf("POL vote %d from unknown validator index %d", i, vote.ValidatorIndex)
		}

		// Validator name must match
		if !types.AccountNameEqual(val.Name, vote.Validator) {
			return fmt.Errorf("POL vote %d validator name mismatch", i)
		}

		// No duplicate votes
		if seenValidators[vote.ValidatorIndex] {
			return fmt.Errorf("duplicate POL vote from validator %d", vote.ValidatorIndex)
		}
		seenValidators[vote.ValidatorIndex] = true

		// Verify signature
		if err := types.VerifyVoteSignature(cs.config.ChainID, &vote, val.PublicKey); err != nil {
			return fmt.Errorf("POL vote %d has invalid signature: %w", i, err)
		}

		polPower += val.VotingPower
	}

	// Must have 2/3+ power
	required := cs.validatorSet.TwoThirdsMajority()
	if polPower < required {
		return fmt.Errorf("POL has insufficient power: %d < %d", polPower, required)
	}

	return nil
}

// enterPrevoteLocked enters the prevote step (internal, caller must hold lock)
func (cs *ConsensusState) enterPrevoteLocked(height int64, round int32) {
	if cs.height != height || cs.round != round || cs.step >= RoundStepPrevote {
		return
	}

	cs.step = RoundStepPrevote

	// Decide what to prevote for
	var blockHash *types.Hash

	if cs.lockedBlock != nil {
		// Prevote for locked block
		hash := types.BlockHash(cs.lockedBlock)
		blockHash = &hash
	} else if cs.proposalBlock != nil {
		// Prevote for proposal
		hash := types.BlockHash(cs.proposalBlock)
		blockHash = &hash
	}
	// else prevote nil

	cs.signAndSendVoteLocked(types.VoteTypePrevote, blockHash)
}

// enterPrevoteWaitLocked enters the prevote wait step (internal, caller must hold lock)
func (cs *ConsensusState) enterPrevoteWaitLocked(height int64, round int32) {
	if cs.height != height || cs.round != round || cs.step >= RoundStepPrevoteWait {
		return
	}

	cs.step = RoundStepPrevoteWait

	cs.scheduleTimeout(TimeoutInfo{
		Height: height,
		Round:  round,
		Step:   RoundStepPrevoteWait,
	})
}

// enterPrecommitLocked enters the precommit step (internal, caller must hold lock)
func (cs *ConsensusState) enterPrecommitLocked(height int64, round int32) {
	if cs.height != height || cs.round != round || cs.step >= RoundStepPrecommit {
		return
	}

	cs.step = RoundStepPrecommit

	prevotes := cs.votes.Prevotes(round)
	if prevotes == nil {
		cs.signAndSendVoteLocked(types.VoteTypePrecommit, nil)
		return
	}

	blockHash, ok := prevotes.TwoThirdsMajority()
	if !ok {
		// No 2/3+ for any block
		cs.signAndSendVoteLocked(types.VoteTypePrecommit, nil)
		return
	}

	if blockHash == nil || types.IsHashEmpty(blockHash) {
		// 2/3+ prevoted nil - unlock
		cs.lockedRound = -1
		cs.lockedBlock = nil
		cs.signAndSendVoteLocked(types.VoteTypePrecommit, nil)
		return
	}

	// 2/3+ prevoted for a block - lock on it
	if cs.proposalBlock != nil {
		proposalHash := types.BlockHash(cs.proposalBlock)
		if types.HashEqual(proposalHash, *blockHash) {
			cs.lockedRound = round
			cs.lockedBlock = cs.proposalBlock
			cs.validRound = round
			cs.validBlock = cs.proposalBlock

			// THIRTEENTH_REFACTOR: Write state to WAL when locking on a block.
			// This ensures BFT safety is preserved after crash - the node will
			// remember it was locked and won't vote for a different block.
			cs.writeStateLocked()

			cs.signAndSendVoteLocked(types.VoteTypePrecommit, blockHash)
			return
		}
	}

	// We don't have the block, precommit nil
	cs.signAndSendVoteLocked(types.VoteTypePrecommit, nil)
}

// enterPrecommitWaitLocked enters the precommit wait step (internal, caller must hold lock)
func (cs *ConsensusState) enterPrecommitWaitLocked(height int64, round int32) {
	if cs.height != height || cs.round != round || cs.step >= RoundStepPrecommitWait {
		return
	}

	cs.step = RoundStepPrecommitWait

	cs.scheduleTimeout(TimeoutInfo{
		Height: height,
		Round:  round,
		Step:   RoundStepPrecommitWait,
	})
}

// enterCommitLocked enters the commit step (internal, caller must hold lock)
func (cs *ConsensusState) enterCommitLocked(height int64) {
	if cs.height != height || cs.step >= RoundStepCommit {
		return
	}

	cs.step = RoundStepCommit

	// Find the round that committed
	for round := cs.round; round >= 0; round-- {
		precommits := cs.votes.Precommits(round)
		if precommits == nil {
			continue
		}
		blockHash, ok := precommits.TwoThirdsMajority()
		if ok && blockHash != nil && !types.IsHashEmpty(blockHash) {
			// Found commit round
			commit := precommits.MakeCommit()
			if commit != nil {
				cs.finalizeCommitLocked(height, commit)
				return
			}
		}
	}

	// M7: If we reach here, enterCommit was called incorrectly
	// This indicates a bug in the state machine
	panic(fmt.Sprintf("CONSENSUS CRITICAL: enterCommit called at height %d but no commit found", height))
}

// finalizeCommitLocked finalizes a committed block (internal, caller must hold lock)
// CR1: Now uses locked state transition functions to avoid deadlock
func (cs *ConsensusState) finalizeCommitLocked(height int64, commit *gen.Commit) {
	// Get the block
	block := cs.lockedBlock
	if block == nil {
		block = cs.proposalBlock
	}
	if block == nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: finalizeCommit called with no block at height %d", height))
	}

	// MF3: Apply block and get validator updates
	var valUpdates []ValidatorUpdate
	if cs.blockExecutor != nil {
		var err error
		valUpdates, err = cs.blockExecutor.ApplyBlock(block, commit)
		if err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to apply committed block at height %d: %v", height, err))
		}
	}

	// H1: Write EndHeight to WAL - PANIC on failure
	if cs.wal != nil {
		msg := wal.NewEndHeightMessage(height)
		if err := cs.wal.WriteSync(msg); err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write EndHeight to WAL: %v", err))
		}
	}

	// Move to next height
	cs.height = height + 1
	cs.round = 0
	cs.step = RoundStepNewHeight
	cs.proposal = nil
	cs.proposalBlock = nil
	cs.lockedRound = -1
	cs.lockedBlock = nil
	cs.validRound = -1
	cs.validBlock = nil
	cs.lastCommit = commit

	// MF3: Apply validator updates if any
	if len(valUpdates) > 0 {
		newValSet, err := cs.applyValidatorUpdates(valUpdates)
		if err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to apply validator updates at height %d: %v", height, err))
		}
		cs.validatorSet = newValSet
	} else {
		// CR4: Create new validator set with incremented priority (immutable pattern)
		newValSet, err := cs.validatorSet.WithIncrementedPriority(1)
		if err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to update validator set: %v", err))
		}
		cs.validatorSet = newValSet
	}

	// Reset votes
	cs.votes.Reset(cs.height, cs.validatorSet)

	// Schedule commit timeout before starting new height
	// NINTH_REFACTOR: Use cs.height (new height) not the 'height' parameter (old height).
	// Previously, commit timeouts were scheduled with the old height, which meant
	// they would always be ignored in handleTimeout because ti.Height != cs.height.
	if !cs.config.SkipTimeoutCommit {
		cs.scheduleTimeout(TimeoutInfo{
			Height: cs.height,
			Round:  0,
			Step:   RoundStepCommit,
		})
	} else {
		// CR1: Use locked version to avoid deadlock
		cs.enterNewRoundLocked(cs.height, 0)
	}
}

// handleVote processes a received vote
func (cs *ConsensusState) handleVote(vote *gen.Vote) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// M3: Check for equivocation BEFORE adding
	// SEVENTH_REFACTOR: Pass chainID for signature verification
	if cs.evidencePool != nil {
		dve, err := cs.evidencePool.CheckVote(vote, cs.validatorSet, cs.config.ChainID)
		if err != nil {
			log.Printf("[DEBUG] consensus: evidence check error: %v", err)
		}
		if dve != nil {
			// Found equivocation! Add to evidence pool
			log.Printf("[WARN] consensus: detected equivocation from validator %s at H=%d R=%d",
				types.AccountNameString(vote.Validator), vote.Height, vote.Round)
			if err := cs.evidencePool.AddDuplicateVoteEvidence(dve); err != nil {
				log.Printf("[DEBUG] consensus: failed to add evidence: %v", err)
			}
			// L4: Continue processing - equivocating votes still count toward quorum.
			// The equivocator will be slashed via the evidence pool, but we can't
			// exclude their vote from consensus without breaking liveness (they may
			// be needed for 2/3+ threshold).
		}
	}

	// H1: Write to WAL BEFORE adding - PANIC on failure
	if cs.wal != nil {
		msg, err := wal.NewVoteMessage(vote.Height, vote.Round, vote)
		if err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to create WAL message for vote: %v", err))
		}
		if err := cs.wal.Write(msg); err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write vote to WAL: %v", err))
		}
	}

	// Add vote
	added, err := cs.votes.AddVote(vote)
	if err != nil || !added {
		return
	}

	// Check for state transitions
	switch vote.Type {
	case types.VoteTypePrevote:
		cs.handlePrevoteLocked(vote)
	case types.VoteTypePrecommit:
		cs.handlePrecommitLocked(vote)
	}
}

// handlePrevoteLocked handles a prevote (internal, caller must hold lock)
func (cs *ConsensusState) handlePrevoteLocked(vote *gen.Vote) {
	prevotes := cs.votes.Prevotes(vote.Round)
	if prevotes == nil {
		return
	}

	// Check for 2/3+ prevotes for any block
	if prevotes.HasTwoThirdsMajority() {
		// Enter precommit
		if cs.step == RoundStepPrevote && vote.Round == cs.round {
			cs.enterPrecommitLocked(cs.height, cs.round)
		}
	} else if prevotes.HasTwoThirdsAny() {
		// Enter prevote wait
		if cs.step == RoundStepPrevote && vote.Round == cs.round {
			cs.enterPrevoteWaitLocked(cs.height, cs.round)
		}
	}
}

// handlePrecommitLocked handles a precommit (internal, caller must hold lock)
func (cs *ConsensusState) handlePrecommitLocked(vote *gen.Vote) {
	precommits := cs.votes.Precommits(vote.Round)
	if precommits == nil {
		return
	}

	// Check for 2/3+ precommits for a block
	blockHash, ok := precommits.TwoThirdsMajority()
	if ok && blockHash != nil && !types.IsHashEmpty(blockHash) {
		// Commit!
		cs.enterCommitLocked(cs.height)
	} else if precommits.HasTwoThirdsAny() {
		// Enter precommit wait
		if cs.step == RoundStepPrecommit && vote.Round == cs.round {
			cs.enterPrecommitWaitLocked(cs.height, cs.round)
		}
	}
}

// handleTimeout processes a timeout event
func (cs *ConsensusState) handleTimeout(ti TimeoutInfo) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Ignore old timeouts
	if ti.Height != cs.height || ti.Round < cs.round {
		return
	}

	switch ti.Step {
	case RoundStepPropose:
		if cs.step == RoundStepPropose {
			cs.enterPrevoteLocked(cs.height, cs.round)
		}

	case RoundStepPrevoteWait:
		if cs.step == RoundStepPrevoteWait {
			cs.enterPrecommitLocked(cs.height, cs.round)
		}

	case RoundStepPrecommitWait:
		if cs.step == RoundStepPrecommitWait {
			// Move to next round
			// ELEVENTH_REFACTOR: Prevent int32 overflow when incrementing round.
			// While extremely unlikely in normal operation, a malicious network
			// could potentially cause excessive round increments.
			nextRound := cs.round + 1
			if nextRound < 0 {
				log.Printf("[ERROR] consensus: round overflow at height %d, capping at MaxInt32", cs.height)
				nextRound = math.MaxInt32
			}
			cs.enterNewRoundLocked(cs.height, nextRound)
		}

	case RoundStepCommit:
		// CR1: Commit timeout should rarely fire - if we get here, the block wasn't
		// applied to state in time. Log a warning and try to recover.
		log.Printf("[WARN] consensus: commit timeout at height %d - attempting recovery", cs.height)
		cs.enterNewRoundLocked(cs.height, 0)
	}
}

// signAndSendVoteLocked signs and broadcasts a vote (internal, caller must hold lock)
// CR5: Now PANICs if own vote fails to add (consensus critical)
func (cs *ConsensusState) signAndSendVoteLocked(voteType types.VoteType, blockHash *types.Hash) {
	if cs.privVal == nil {
		return
	}

	// Find our validator index
	pubKey := cs.privVal.GetPubKey()
	var valIndex uint16
	var found bool
	for _, v := range cs.validatorSet.Validators {
		if types.PublicKeyEqual(v.PublicKey, pubKey) {
			valIndex = v.Index
			found = true
			break
		}
	}
	if !found {
		return
	}

	validator := cs.validatorSet.GetByIndex(valIndex)
	if validator == nil {
		return
	}

	vote := &gen.Vote{
		Type:           voteType,
		Height:         cs.height,
		Round:          cs.round,
		BlockHash:      blockHash,
		Timestamp:      time.Now().UnixNano(),
		Validator:      validator.Name,
		ValidatorIndex: valIndex,
	}

	// Sign vote
	if err := cs.privVal.SignVote(cs.config.ChainID, vote); err != nil {
		log.Printf("[WARN] consensus: failed to sign vote: %v", err)
		return
	}

	// H1: Write to WAL BEFORE adding - PANIC on failure
	if cs.wal != nil {
		msg, err := wal.NewVoteMessage(vote.Height, vote.Round, vote)
		if err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to create WAL message for own vote: %v", err))
		}
		if err := cs.wal.WriteSync(msg); err != nil {
			panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write own vote to WAL: %v", err))
		}
	}

	// CR5: Add to our own vote set - PANIC on failure (our own vote must be tracked)
	added, err := cs.votes.AddVote(vote)
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to add own vote: %v", err))
	}
	if !added {
		// This shouldn't happen for our own fresh vote
		panic("CONSENSUS CRITICAL: own vote was not added (duplicate?)")
	}

	// M8: Broadcast vote to peers
	if cs.onVote != nil {
		cs.onVote(vote)
	}
}

// writeStateLocked writes the current consensus state to WAL (caller must hold lock).
// THIRTEENTH_REFACTOR: This is called when locking on a block to ensure BFT safety
// is preserved after crash recovery. The node will remember it was locked.
func (cs *ConsensusState) writeStateLocked() {
	if cs.wal == nil {
		return
	}

	// Build state data with locked/valid information
	state := &gen.ConsensusStateData{
		Height: cs.height,
		Round:  cs.round,
		Step:   stepToGenerated(cs.step),
	}

	// Add locked state
	if cs.lockedBlock != nil {
		state.LockedRound = cs.lockedRound
		lockedHash := types.BlockHash(cs.lockedBlock)
		state.LockedBlockHash = &lockedHash
	}

	// Add valid state
	if cs.validBlock != nil {
		state.ValidRound = cs.validRound
		validHash := types.BlockHash(cs.validBlock)
		state.ValidBlockHash = &validHash
	}

	msg, err := wal.NewStateMessage(state)
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to create state WAL message: %v", err))
	}
	if err := cs.wal.WriteSync(msg); err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write state to WAL: %v", err))
	}
}

// stepToGenerated converts RoundStep to the generated enum type
func stepToGenerated(step RoundStep) gen.RoundStepType {
	switch step {
	case RoundStepNewHeight:
		return gen.RoundStepTypeRoundStepNewHeight
	case RoundStepNewRound:
		return gen.RoundStepTypeRoundStepNewRound
	case RoundStepPropose:
		return gen.RoundStepTypeRoundStepPropose
	case RoundStepPrevote:
		return gen.RoundStepTypeRoundStepPrevote
	case RoundStepPrevoteWait:
		return gen.RoundStepTypeRoundStepPrevoteWait
	case RoundStepPrecommit:
		return gen.RoundStepTypeRoundStepPrecommit
	case RoundStepPrecommitWait:
		return gen.RoundStepTypeRoundStepPrecommitWait
	case RoundStepCommit:
		return gen.RoundStepTypeRoundStepCommit
	default:
		return gen.RoundStepTypeRoundStepNewHeight
	}
}

// scheduleTimeout schedules a timeout
func (cs *ConsensusState) scheduleTimeout(ti TimeoutInfo) {
	cs.timeoutTicker.ScheduleTimeout(ti)
}

// GetState returns the current consensus state (for debugging/monitoring)
func (cs *ConsensusState) GetState() (height int64, round int32, step RoundStep) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.height, cs.round, cs.step
}

// GetValidatorSet returns the current validator set
func (cs *ConsensusState) GetValidatorSet() *types.ValidatorSet {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if cs.validatorSet == nil {
		return nil
	}

	// H1: Return copy to prevent caller from modifying internal state
	vsCopy, err := cs.validatorSet.Copy()
	if err != nil {
		log.Printf("[ERROR] consensus: failed to copy validator set: %v", err)
		return nil
	}
	return vsCopy
}

// String returns a string representation of the step
func StepString(step RoundStep) string {
	switch step {
	case RoundStepNewHeight:
		return "NewHeight"
	case RoundStepNewRound:
		return "NewRound"
	case RoundStepPropose:
		return "Propose"
	case RoundStepPrevote:
		return "Prevote"
	case RoundStepPrevoteWait:
		return "PrevoteWait"
	case RoundStepPrecommit:
		return "Precommit"
	case RoundStepPrecommitWait:
		return "PrecommitWait"
	case RoundStepCommit:
		return "Commit"
	default:
		return fmt.Sprintf("Unknown(%d)", step)
	}
}
