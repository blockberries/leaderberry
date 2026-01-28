package wal

import (
	"errors"
	"io"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Errors
var (
	ErrWALClosed     = errors.New("WAL is closed")
	ErrWALCorrupted  = errors.New("WAL is corrupted")
	ErrWALNotFound   = errors.New("WAL file not found")
	ErrInvalidHeight = errors.New("invalid height in WAL")
)

// MessageType identifies the type of WAL message
type MessageType = gen.WalmsgType

const (
	MsgTypeUnknown   = gen.WalmsgTypeWalMsgUnknown
	MsgTypeProposal  = gen.WalmsgTypeWalMsgProposal
	MsgTypeVote      = gen.WalmsgTypeWalMsgVote
	MsgTypeCommit    = gen.WalmsgTypeWalMsgCommit
	MsgTypeEndHeight = gen.WalmsgTypeWalMsgEndHeight
	MsgTypeState     = gen.WalmsgTypeWalMsgState
	MsgTypeTimeout   = gen.WalmsgTypeWalMsgTimeout
)

// Message represents a WAL message with metadata
type Message struct {
	Type   MessageType
	Height int64
	Round  int32
	Data   []byte
}

// MarshalCramberry serializes the message
func (m *Message) MarshalCramberry() ([]byte, error) {
	msg := &gen.Walmessage{
		Type:   m.Type,
		Height: m.Height,
		Round:  m.Round,
		Data:   m.Data,
	}
	return msg.MarshalCramberry()
}

// UnmarshalCramberry deserializes the message
func (m *Message) UnmarshalCramberry(data []byte) error {
	msg := &gen.Walmessage{}
	if err := msg.UnmarshalCramberry(data); err != nil {
		return err
	}
	m.Type = msg.Type
	m.Height = msg.Height
	m.Round = msg.Round
	m.Data = msg.Data
	return nil
}

// WAL interface for write-ahead logging
type WAL interface {
	// Write writes a message to the WAL
	Write(msg *Message) error

	// WriteSync writes a message and ensures it's synced to disk
	WriteSync(msg *Message) error

	// FlushAndSync flushes and syncs all pending writes
	FlushAndSync() error

	// SearchForEndHeight searches for the end of a height in the WAL
	// Returns a Reader positioned after the EndHeight message, or false if not found
	SearchForEndHeight(height int64) (Reader, bool, error)

	// Start starts the WAL
	Start() error

	// Stop stops the WAL
	Stop() error

	// Group returns the current WAL group (for rotation)
	Group() *Group
}

// Reader interface for reading from WAL
type Reader interface {
	// Read reads the next message from the WAL
	Read() (*Message, error)

	// Close closes the reader
	Close() error
}

// Group represents a group of WAL files (for rotation)
type Group struct {
	Dir      string
	Prefix   string
	MaxSize  int64
	MinIndex int
	MaxIndex int
}

// NewWALMessage creates a WAL message for a proposal
func NewProposalMessage(height int64, round int32, proposal *gen.Proposal) (*Message, error) {
	data, err := proposal.MarshalCramberry()
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:   MsgTypeProposal,
		Height: height,
		Round:  round,
		Data:   data,
	}, nil
}

// NewVoteMessage creates a WAL message for a vote
func NewVoteMessage(height int64, round int32, vote *gen.Vote) (*Message, error) {
	data, err := vote.MarshalCramberry()
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:   MsgTypeVote,
		Height: height,
		Round:  round,
		Data:   data,
	}, nil
}

// NewCommitMessage creates a WAL message for a commit
func NewCommitMessage(height int64, commit *gen.Commit) (*Message, error) {
	data, err := commit.MarshalCramberry()
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:   MsgTypeCommit,
		Height: height,
		Round:  commit.Round,
		Data:   data,
	}, nil
}

// NewEndHeightMessage creates a WAL message marking end of height
func NewEndHeightMessage(height int64) *Message {
	return &Message{
		Type:   MsgTypeEndHeight,
		Height: height,
	}
}

// NewStateMessage creates a WAL message for consensus state
func NewStateMessage(state *gen.ConsensusStateData) (*Message, error) {
	data, err := state.MarshalCramberry()
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:   MsgTypeState,
		Height: state.Height,
		Round:  state.Round,
		Data:   data,
	}, nil
}

// NewTimeoutMessage creates a WAL message for a timeout
func NewTimeoutMessage(height int64, round int32, step gen.RoundStepType) (*Message, error) {
	timeout := &gen.TimeoutMessage{
		Height: height,
		Round:  round,
		Step:   step,
	}
	data, err := timeout.MarshalCramberry()
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:   MsgTypeTimeout,
		Height: height,
		Round:  round,
		Data:   data,
	}, nil
}

// DecodeProposal decodes a proposal from WAL message data
func DecodeProposal(data []byte) (*gen.Proposal, error) {
	proposal := &gen.Proposal{}
	if err := proposal.UnmarshalCramberry(data); err != nil {
		return nil, err
	}
	return proposal, nil
}

// DecodeVote decodes a vote from WAL message data
func DecodeVote(data []byte) (*gen.Vote, error) {
	vote := &gen.Vote{}
	if err := vote.UnmarshalCramberry(data); err != nil {
		return nil, err
	}
	return vote, nil
}

// DecodeCommit decodes a commit from WAL message data
func DecodeCommit(data []byte) (*gen.Commit, error) {
	commit := &gen.Commit{}
	if err := commit.UnmarshalCramberry(data); err != nil {
		return nil, err
	}
	return commit, nil
}

// DecodeState decodes consensus state from WAL message data
func DecodeState(data []byte) (*gen.ConsensusStateData, error) {
	state := &gen.ConsensusStateData{}
	if err := state.UnmarshalCramberry(data); err != nil {
		return nil, err
	}
	return state, nil
}

// DecodeTimeout decodes a timeout from WAL message data
func DecodeTimeout(data []byte) (*gen.TimeoutMessage, error) {
	timeout := &gen.TimeoutMessage{}
	if err := timeout.UnmarshalCramberry(data); err != nil {
		return nil, err
	}
	return timeout, nil
}

// NopWAL is a no-op WAL implementation for testing
type NopWAL struct{}

func (w *NopWAL) Write(msg *Message) error                              { return nil }
func (w *NopWAL) WriteSync(msg *Message) error                          { return nil }
func (w *NopWAL) FlushAndSync() error                                   { return nil }
func (w *NopWAL) SearchForEndHeight(height int64) (Reader, bool, error) { return nil, false, nil }
func (w *NopWAL) Start() error                                          { return nil }
func (w *NopWAL) Stop() error                                           { return nil }
func (w *NopWAL) Group() *Group                                         { return nil }

// Ensure NopWAL implements WAL
var _ WAL = (*NopWAL)(nil)

// NopReader is a no-op reader
type NopReader struct{}

func (r *NopReader) Read() (*Message, error) { return nil, io.EOF }
func (r *NopReader) Close() error            { return nil }

var _ Reader = (*NopReader)(nil)
