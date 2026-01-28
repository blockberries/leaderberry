package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blockberries/leaderberry/types"
	"github.com/blockberries/leaderberry/wal"
	gen "github.com/blockberries/leaderberry/types/generated"
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

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// State
	started bool
}

// PrivValidator interface for signing consensus messages
type PrivValidator interface {
	GetPubKey() types.PublicKey
	SignVote(chainID string, vote *gen.Vote) error
	SignProposal(chainID string, proposal *gen.Proposal) error
}

// WAL is an alias for the wal package's WAL interface
type WAL = wal.WAL

// BlockExecutor interface for executing blocks
type BlockExecutor interface {
	// CreateProposalBlock creates a new block proposal
	CreateProposalBlock(height int64, lastCommit *gen.Commit, proposer types.AccountName) (*gen.Block, error)
	// ValidateBlock validates a proposed block
	ValidateBlock(block *gen.Block) error
	// ApplyBlock applies a committed block
	ApplyBlock(block *gen.Block, commit *gen.Commit) error
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
		proposalCh:    make(chan *gen.Proposal, 10),
		voteCh:        make(chan *gen.Vote, 1000),
		commitCh:      make(chan struct{}, 1),
		lockedRound:   -1,
		validRound:    -1,
	}
}

// Start starts the consensus state machine at the given height
func (cs *ConsensusState) Start(height int64, lastCommit *gen.Commit) error {
	cs.mu.Lock()

	if cs.started {
		cs.mu.Unlock()
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

	cs.mu.Unlock()

	// Enter new height (outside lock to avoid deadlock)
	cs.enterNewRound(height, 0)

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
		// Channel full, drop proposal
	}
}

// AddVote adds a vote from the network
func (cs *ConsensusState) AddVote(vote *gen.Vote) {
	select {
	case cs.voteCh <- vote:
	default:
		// Channel full, drop vote
	}
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

// enterNewRound enters a new round at the given height
func (cs *ConsensusState) enterNewRound(height int64, round int32) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

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
	if cs.privVal != nil && types.PublicKeyEqual(proposer.PublicKey, cs.privVal.GetPubKey()) {
		cs.createAndSendProposal()
	}
}

// createAndSendProposal creates and broadcasts a proposal
func (cs *ConsensusState) createAndSendProposal() {
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
		var err error
		block, err = cs.blockExecutor.CreateProposalBlock(
			cs.height,
			cs.lastCommit,
			cs.validatorSet.Proposer.Name,
		)
		if err != nil {
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
		return
	}

	cs.proposal = proposal
	cs.proposalBlock = block

	// Broadcast proposal (via callback, not implemented here)
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
	if !types.AccountNameEqual(proposal.Proposer, proposer.Name) {
		return
	}

	// Verify signature
	signBytes := types.ProposalSignBytes(cs.config.ChainID, proposal)
	if !types.VerifySignature(proposer.PublicKey, signBytes, proposal.Signature) {
		return
	}

	// Validate block
	if err := cs.blockExecutor.ValidateBlock(&proposal.Block); err != nil {
		return
	}

	// Accept proposal
	cs.proposal = proposal
	cs.proposalBlock = &proposal.Block

	// Enter prevote
	cs.enterPrevote(cs.height, cs.round)
}

// enterPrevote enters the prevote step
func (cs *ConsensusState) enterPrevote(height int64, round int32) {
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

	cs.signAndSendVote(types.VoteTypePrevote, blockHash)
}

// enterPrevoteWait enters the prevote wait step
func (cs *ConsensusState) enterPrevoteWait(height int64, round int32) {
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

// enterPrecommit enters the precommit step
func (cs *ConsensusState) enterPrecommit(height int64, round int32) {
	if cs.height != height || cs.round != round || cs.step >= RoundStepPrecommit {
		return
	}

	cs.step = RoundStepPrecommit

	prevotes := cs.votes.Prevotes(round)
	if prevotes == nil {
		cs.signAndSendVote(types.VoteTypePrecommit, nil)
		return
	}

	blockHash, ok := prevotes.TwoThirdsMajority()
	if !ok {
		// No 2/3+ for any block
		cs.signAndSendVote(types.VoteTypePrecommit, nil)
		return
	}

	if blockHash == nil || types.IsHashEmpty(blockHash) {
		// 2/3+ prevoted nil - unlock
		cs.lockedRound = -1
		cs.lockedBlock = nil
		cs.signAndSendVote(types.VoteTypePrecommit, nil)
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
			cs.signAndSendVote(types.VoteTypePrecommit, blockHash)
			return
		}
	}

	// We don't have the block, precommit nil
	cs.signAndSendVote(types.VoteTypePrecommit, nil)
}

// enterPrecommitWait enters the precommit wait step
func (cs *ConsensusState) enterPrecommitWait(height int64, round int32) {
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

// enterCommit enters the commit step
func (cs *ConsensusState) enterCommit(height int64) {
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
				cs.finalizeCommit(height, commit)
				return
			}
		}
	}
}

// finalizeCommit finalizes a committed block
func (cs *ConsensusState) finalizeCommit(height int64, commit *gen.Commit) {
	// Get the block
	block := cs.lockedBlock
	if block == nil {
		block = cs.proposalBlock
	}
	if block == nil {
		return
	}

	// Apply block
	if err := cs.blockExecutor.ApplyBlock(block, commit); err != nil {
		return
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

	// Increment proposer priority
	cs.validatorSet.IncrementProposerPriority(1)

	// Reset votes
	cs.votes.Reset(cs.height, cs.validatorSet)

	// Schedule commit timeout before starting new height
	if !cs.config.SkipTimeoutCommit {
		cs.scheduleTimeout(TimeoutInfo{
			Height: height,
			Round:  0,
			Step:   RoundStepCommit,
		})
	} else {
		cs.enterNewRound(cs.height, 0)
	}
}

// handleVote processes a received vote
func (cs *ConsensusState) handleVote(vote *gen.Vote) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Add vote
	added, err := cs.votes.AddVote(vote)
	if err != nil || !added {
		return
	}

	// Check for state transitions
	switch vote.Type {
	case types.VoteTypePrevote:
		cs.handlePrevote(vote)
	case types.VoteTypePrecommit:
		cs.handlePrecommit(vote)
	}
}

func (cs *ConsensusState) handlePrevote(vote *gen.Vote) {
	prevotes := cs.votes.Prevotes(vote.Round)
	if prevotes == nil {
		return
	}

	// Check for 2/3+ prevotes for any block
	if prevotes.HasTwoThirdsMajority() {
		// Enter precommit
		if cs.step == RoundStepPrevote && vote.Round == cs.round {
			cs.enterPrecommit(cs.height, cs.round)
		}
	} else if prevotes.HasTwoThirdsAny() {
		// Enter prevote wait
		if cs.step == RoundStepPrevote && vote.Round == cs.round {
			cs.enterPrevoteWait(cs.height, cs.round)
		}
	}
}

func (cs *ConsensusState) handlePrecommit(vote *gen.Vote) {
	precommits := cs.votes.Precommits(vote.Round)
	if precommits == nil {
		return
	}

	// Check for 2/3+ precommits for a block
	blockHash, ok := precommits.TwoThirdsMajority()
	if ok && blockHash != nil && !types.IsHashEmpty(blockHash) {
		// Commit!
		cs.enterCommit(cs.height)
	} else if precommits.HasTwoThirdsAny() {
		// Enter precommit wait
		if cs.step == RoundStepPrecommit && vote.Round == cs.round {
			cs.enterPrecommitWait(cs.height, cs.round)
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
			cs.enterPrevote(cs.height, cs.round)
		}

	case RoundStepPrevoteWait:
		if cs.step == RoundStepPrevoteWait {
			cs.enterPrecommit(cs.height, cs.round)
		}

	case RoundStepPrecommitWait:
		if cs.step == RoundStepPrecommitWait {
			// Move to next round
			cs.enterNewRound(cs.height, cs.round+1)
		}

	case RoundStepCommit:
		// Start new height
		cs.enterNewRound(cs.height, 0)
	}
}

// signAndSendVote signs and broadcasts a vote
func (cs *ConsensusState) signAndSendVote(voteType types.VoteType, blockHash *types.Hash) {
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
		return
	}

	// Add to our own vote set
	cs.votes.AddVote(vote)

	// Broadcast vote (via callback, not implemented here)
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
