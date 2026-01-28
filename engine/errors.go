package engine

import "errors"

// Consensus errors
var (
	ErrInvalidVote        = errors.New("invalid vote")
	ErrUnknownValidator   = errors.New("unknown validator")
	ErrInvalidSignature   = errors.New("invalid signature")
	ErrConflictingVote    = errors.New("conflicting vote (equivocation)")
	ErrInvalidProposal    = errors.New("invalid proposal")
	ErrInvalidBlock       = errors.New("invalid block")
	ErrInvalidHeight      = errors.New("invalid height")
	ErrInvalidRound       = errors.New("invalid round")
	ErrNotProposer        = errors.New("not the proposer for this round")
	ErrDoubleSign         = errors.New("double sign attempt")
	ErrWALWrite           = errors.New("WAL write failed")
	ErrWALReplay          = errors.New("WAL replay failed")
	ErrNoPrivValidator    = errors.New("no private validator configured")
	ErrAlreadyStarted     = errors.New("consensus already started")
	ErrNotStarted         = errors.New("consensus not started")
	ErrInvalidMessage     = errors.New("invalid consensus message")
	ErrUnknownMessageType = errors.New("unknown consensus message type")
	// TWELFTH_REFACTOR: Error for stale VoteSet references after Reset()
	ErrStaleVoteSet = errors.New("stale vote set reference")
)
