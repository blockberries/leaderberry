package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/blockberries/leaderberry/privval"
	"github.com/blockberries/leaderberry/types"
	"github.com/blockberries/leaderberry/wal"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// Engine is the main consensus engine that implements the BFT consensus protocol
type Engine struct {
	mu sync.RWMutex

	// Configuration
	config *Config

	// Components
	state    *ConsensusState
	wal      wal.WAL
	privVal  privval.PrivValidator
	executor BlockExecutor

	// Validator set management
	validatorSet *types.ValidatorSet

	// Message broadcasting
	proposalBroadcast func(*gen.Proposal)
	voteBroadcast     func(*gen.Vote)

	// State
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewEngine creates a new consensus engine
func NewEngine(
	config *Config,
	valSet *types.ValidatorSet,
	pv privval.PrivValidator,
	w wal.WAL,
	executor BlockExecutor,
) *Engine {
	return &Engine{
		config:       config,
		validatorSet: valSet,
		privVal:      pv,
		wal:          w,
		executor:     executor,
	}
}

// SetProposalBroadcaster sets the function used to broadcast proposals
func (e *Engine) SetProposalBroadcaster(fn func(*gen.Proposal)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proposalBroadcast = fn
}

// SetVoteBroadcaster sets the function used to broadcast votes
func (e *Engine) SetVoteBroadcaster(fn func(*gen.Vote)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.voteBroadcast = fn
}

// Start starts the consensus engine
func (e *Engine) Start(height int64, lastCommit *gen.Commit) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.started {
		return ErrAlreadyStarted
	}

	e.ctx, e.cancel = context.WithCancel(context.Background())

	// Start WAL
	if e.wal != nil {
		if err := e.wal.Start(); err != nil {
			return fmt.Errorf("failed to start WAL: %w", err)
		}
	}

	// Create consensus state
	e.state = NewConsensusState(
		e.config,
		e.validatorSet,
		e.privVal,
		e.wal,
		e.executor,
	)

	// Start consensus state machine
	if err := e.state.Start(height, lastCommit); err != nil {
		return fmt.Errorf("failed to start consensus state: %w", err)
	}

	e.started = true
	return nil
}

// Stop stops the consensus engine
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.started {
		return ErrNotStarted
	}

	e.started = false
	e.cancel()

	// Stop consensus state
	if e.state != nil {
		if err := e.state.Stop(); err != nil {
			return fmt.Errorf("failed to stop consensus state: %w", err)
		}
	}

	// Stop WAL
	if e.wal != nil {
		if err := e.wal.Stop(); err != nil {
			return fmt.Errorf("failed to stop WAL: %w", err)
		}
	}

	return nil
}

// AddProposal adds a proposal received from the network
func (e *Engine) AddProposal(proposal *gen.Proposal) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.started {
		return ErrNotStarted
	}

	e.state.AddProposal(proposal)
	return nil
}

// AddVote adds a vote received from the network
func (e *Engine) AddVote(vote *gen.Vote) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.started {
		return ErrNotStarted
	}

	e.state.AddVote(vote)
	return nil
}

// GetState returns the current consensus state
func (e *Engine) GetState() (height int64, round int32, step RoundStep, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.started {
		return 0, 0, 0, ErrNotStarted
	}

	height, round, step = e.state.GetState()
	return height, round, step, nil
}

// GetValidatorSet returns the current validator set
func (e *Engine) GetValidatorSet() *types.ValidatorSet {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.validatorSet
}

// UpdateValidatorSet updates the validator set (typically after a block is committed)
func (e *Engine) UpdateValidatorSet(valSet *types.ValidatorSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validatorSet = valSet
}

// IsValidator returns true if the local node is a validator
func (e *Engine) IsValidator() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.privVal == nil {
		return false
	}

	pubKey := e.privVal.GetPubKey()
	for _, v := range e.validatorSet.Validators {
		if types.PublicKeyEqual(v.PublicKey, pubKey) {
			return true
		}
	}
	return false
}

// GetProposer returns the proposer for the current round
func (e *Engine) GetProposer() *types.NamedValidator {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.validatorSet.Proposer
}

// ChainID returns the chain ID
func (e *Engine) ChainID() string {
	return e.config.ChainID
}

// --- BFTConsensus Interface Implementation ---
// These methods can be used to integrate with blockberry

// HandleConsensusMessage handles a consensus message from the network
func (e *Engine) HandleConsensusMessage(peerID string, data []byte) error {
	// Decode polymorphic message
	// Try to decode as different message types

	// Try Proposal
	proposal := &gen.Proposal{}
	if err := proposal.UnmarshalCramberry(data); err == nil && proposal.Height > 0 {
		return e.AddProposal(proposal)
	}

	// Try Vote
	vote := &gen.Vote{}
	if err := vote.UnmarshalCramberry(data); err == nil && vote.Height > 0 {
		return e.AddVote(vote)
	}

	return ErrInvalidBlock
}

// BroadcastProposal broadcasts a proposal to all peers
func (e *Engine) BroadcastProposal(proposal *gen.Proposal) {
	e.mu.RLock()
	fn := e.proposalBroadcast
	e.mu.RUnlock()

	if fn != nil {
		fn(proposal)
	}
}

// BroadcastVote broadcasts a vote to all peers
func (e *Engine) BroadcastVote(vote *gen.Vote) {
	e.mu.RLock()
	fn := e.voteBroadcast
	e.mu.RUnlock()

	if fn != nil {
		fn(vote)
	}
}

// --- Metrics and Monitoring ---

// Metrics holds consensus metrics
type Metrics struct {
	Height             int64
	Round              int32
	Step               string
	Validators         int
	TotalVotingPower   int64
	IsValidator        bool
	ProposerName       string
}

// GetMetrics returns current consensus metrics
func (e *Engine) GetMetrics() (*Metrics, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.started {
		return nil, ErrNotStarted
	}

	height, round, step := e.state.GetState()
	proposer := e.validatorSet.Proposer

	proposerName := ""
	if proposer != nil {
		proposerName = types.AccountNameString(proposer.Name)
	}

	return &Metrics{
		Height:           height,
		Round:            round,
		Step:             StepString(step),
		Validators:       e.validatorSet.Size(),
		TotalVotingPower: e.validatorSet.TotalPower,
		IsValidator:      e.IsValidator(),
		ProposerName:     proposerName,
	}, nil
}
