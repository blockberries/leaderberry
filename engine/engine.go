package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/blockberries/leaderberry/privval"
	"github.com/blockberries/leaderberry/types"
	"github.com/blockberries/leaderberry/wal"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// ConsensusMessageType identifies the type of consensus message
type ConsensusMessageType uint8

const (
	// ConsensusMessageTypeProposal identifies a proposal message
	ConsensusMessageTypeProposal ConsensusMessageType = 1
	// ConsensusMessageTypeVote identifies a vote message
	ConsensusMessageTypeVote ConsensusMessageType = 2
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

	if e.validatorSet == nil {
		return nil
	}

	// H1: Return copy to prevent caller from modifying internal state
	vsCopy, err := e.validatorSet.Copy()
	if err != nil {
		// Should never fail for valid set
		log.Printf("[ERROR] engine: failed to copy validator set: %v", err)
		return nil
	}
	return vsCopy
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

// HandleConsensusMessage handles a consensus message from the network.
// Messages must be prefixed with a single byte indicating the message type.
// M4: Fixed length check to properly validate message structure
func (e *Engine) HandleConsensusMessage(peerID string, data []byte) error {
	if len(data) < 1 {
		return ErrInvalidMessage
	}

	msgType := ConsensusMessageType(data[0])
	payload := data[1:] // May be empty

	switch msgType {
	case ConsensusMessageTypeProposal:
		if len(payload) == 0 {
			return fmt.Errorf("%w: empty proposal payload", ErrInvalidMessage)
		}
		proposal := &gen.Proposal{}
		if err := proposal.UnmarshalCramberry(payload); err != nil {
			return fmt.Errorf("%w: failed to unmarshal proposal: %v", ErrInvalidMessage, err)
		}
		return e.AddProposal(proposal)

	case ConsensusMessageTypeVote:
		if len(payload) == 0 {
			return fmt.Errorf("%w: empty vote payload", ErrInvalidMessage)
		}
		vote := &gen.Vote{}
		if err := vote.UnmarshalCramberry(payload); err != nil {
			return fmt.Errorf("%w: failed to unmarshal vote: %v", ErrInvalidMessage, err)
		}
		return e.AddVote(vote)

	default:
		return fmt.Errorf("%w: %d", ErrUnknownMessageType, msgType)
	}
}

// EncodeProposalMessage encodes a proposal with its type prefix for network transmission
func EncodeProposalMessage(proposal *gen.Proposal) ([]byte, error) {
	payload, err := proposal.MarshalCramberry()
	if err != nil {
		return nil, err
	}
	msg := make([]byte, 1+len(payload))
	msg[0] = byte(ConsensusMessageTypeProposal)
	copy(msg[1:], payload)
	return msg, nil
}

// EncodeVoteMessage encodes a vote with its type prefix for network transmission
func EncodeVoteMessage(vote *gen.Vote) ([]byte, error) {
	payload, err := vote.MarshalCramberry()
	if err != nil {
		return nil, err
	}
	msg := make([]byte, 1+len(payload))
	msg[0] = byte(ConsensusMessageTypeVote)
	copy(msg[1:], payload)
	return msg, nil
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
