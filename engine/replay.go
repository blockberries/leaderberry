package engine

import (
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/blockberries/leaderberry/types"
	"github.com/blockberries/leaderberry/wal"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// Errors for WAL replay
var (
	ErrReplayFailed     = errors.New("WAL replay failed")
	ErrNoWAL            = errors.New("no WAL configured")
	ErrWALCorrupted     = errors.New("WAL corrupted during replay")
	ErrInvalidWALHeight = errors.New("invalid height in WAL")
)

// WALReplayResult contains the result of a WAL replay
type WALReplayResult struct {
	// Height we recovered to
	Height int64
	// Round we recovered to
	Round int32
	// Step we recovered to
	Step RoundStep
	// Number of messages replayed
	MessagesReplayed int
	// Last proposal found during replay
	Proposal *gen.Proposal
	// Votes found during replay
	Votes []*gen.Vote
	// Whether we found an EndHeight message
	FoundEndHeight bool
	// THIRTEENTH_REFACTOR: Locked/valid state for BFT safety restoration
	LockedRound     int32
	LockedBlockHash *types.Hash
	ValidRound      int32
	ValidBlockHash  *types.Hash
}

// ReplayWAL replays the WAL from the given height to recover consensus state
func (cs *ConsensusState) ReplayWAL(targetHeight int64) (*WALReplayResult, error) {
	if cs.wal == nil {
		return nil, ErrNoWAL
	}

	// Search for the end of the previous height
	reader, found, err := cs.wal.SearchForEndHeight(targetHeight - 1)
	if err != nil {
		return nil, fmt.Errorf("failed to search WAL: %w", err)
	}

	result := &WALReplayResult{
		Height: targetHeight,
	}

	if !found {
		// No previous height found, start fresh
		return result, nil
	}

	defer reader.Close()

	// Replay messages for the target height
	for {
		msg, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read WAL: %w", err)
		}

		// Only replay messages for our target height
		if msg.Height != targetHeight {
			continue
		}

		if err := cs.replayMessage(msg, result); err != nil {
			return nil, fmt.Errorf("failed to replay message: %w", err)
		}

		result.MessagesReplayed++

		// Stop if we hit EndHeight for our target
		if msg.Type == wal.MsgTypeEndHeight {
			result.FoundEndHeight = true
			break
		}
	}

	return result, nil
}

// replayMessage replays a single WAL message
func (cs *ConsensusState) replayMessage(msg *wal.Message, result *WALReplayResult) error {
	switch msg.Type {
	case wal.MsgTypeProposal:
		return cs.replayProposal(msg, result)

	case wal.MsgTypeVote:
		return cs.replayVote(msg, result)

	case wal.MsgTypeState:
		return cs.replayState(msg, result)

	case wal.MsgTypeTimeout:
		// Timeouts don't need replay - they'll be rescheduled
		return nil

	case wal.MsgTypeCommit:
		// Commits are handled by the application
		return nil

	case wal.MsgTypeEndHeight:
		// EndHeight marks the end of a height
		return nil

	default:
		// Unknown message types are ignored for forward compatibility
		return nil
	}
}

// replayProposal replays a proposal message
func (cs *ConsensusState) replayProposal(msg *wal.Message, result *WALReplayResult) error {
	proposal, err := wal.DecodeProposal(msg.Data)
	if err != nil {
		return fmt.Errorf("failed to decode proposal: %w", err)
	}

	result.Proposal = proposal
	result.Round = msg.Round

	// Don't actually process the proposal - just record it
	// The caller will decide how to handle the replayed state
	return nil
}

// replayVote replays a vote message
func (cs *ConsensusState) replayVote(msg *wal.Message, result *WALReplayResult) error {
	vote, err := wal.DecodeVote(msg.Data)
	if err != nil {
		return fmt.Errorf("failed to decode vote: %w", err)
	}

	result.Votes = append(result.Votes, vote)
	result.Round = msg.Round

	return nil
}

// replayState replays a consensus state message
// THIRTEENTH_REFACTOR: Now extracts locked/valid state for BFT safety restoration.
func (cs *ConsensusState) replayState(msg *wal.Message, result *WALReplayResult) error {
	state, err := wal.DecodeState(msg.Data)
	if err != nil {
		return fmt.Errorf("failed to decode state: %w", err)
	}

	result.Height = state.Height
	result.Round = state.Round
	result.Step = state.Step

	// THIRTEENTH_REFACTOR: Extract locked/valid state for BFT safety.
	// If a node crashes while locked on a block, it must remember the lock
	// after recovery to avoid voting for a different block.
	result.LockedRound = state.LockedRound
	result.LockedBlockHash = types.CopyHash(state.LockedBlockHash)
	result.ValidRound = state.ValidRound
	result.ValidBlockHash = types.CopyHash(state.ValidBlockHash)

	return nil
}

// ReplayCatchup replays the WAL and applies the recovered state
// THIRTEENTH_REFACTOR: Now restores locked/valid state for BFT safety.
func (cs *ConsensusState) ReplayCatchup(targetHeight int64) error {
	result, err := cs.ReplayWAL(targetHeight)
	if err != nil {
		return err
	}

	if result.MessagesReplayed == 0 {
		// Nothing to replay
		return nil
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Apply recovered state
	cs.height = result.Height
	cs.round = result.Round
	cs.step = result.Step

	// THIRTEENTH_REFACTOR: Restore locked/valid state for BFT safety.
	// If we were locked on a block before crash, we must remember the lock
	// to avoid voting for a different block (which would violate safety).
	// Note: We restore the round and hash, but the actual block pointer
	// remains nil until we receive the block again via BlockSync or proposal.
	// This is safe because:
	// 1. The round prevents us from locking on a different block at the same round
	// 2. If we need to vote, we'll vote nil (safe) until we get the block
	// 3. Once we receive the matching block, lockedBlock will be set
	cs.lockedRound = result.LockedRound
	cs.validRound = result.ValidRound
	// Note: lockedBlock and validBlock remain nil - they'll be restored when
	// we receive a proposal with matching hash. The important part is that
	// lockedRound is restored so we don't vote for different blocks.
	if result.LockedBlockHash != nil {
		log.Printf("[INFO] consensus: restored locked state from WAL - round=%d hash=%x",
			result.LockedRound, result.LockedBlockHash.Data[:8])
	}
	if result.ValidBlockHash != nil {
		log.Printf("[INFO] consensus: restored valid state from WAL - round=%d hash=%x",
			result.ValidRound, result.ValidBlockHash.Data[:8])
	}

	// If we have a proposal, set it
	if result.Proposal != nil {
		cs.proposal = result.Proposal

		// THIRTEENTH_REFACTOR: If proposal matches locked/valid hash, restore block pointers
		proposalHash := types.BlockHash(&result.Proposal.Block)
		if result.LockedBlockHash != nil && types.HashEqual(proposalHash, *result.LockedBlockHash) {
			cs.lockedBlock = types.CopyBlock(&result.Proposal.Block)
		}
		if result.ValidBlockHash != nil && types.HashEqual(proposalHash, *result.ValidBlockHash) {
			cs.validBlock = types.CopyBlock(&result.Proposal.Block)
		}
	}

	// Add replayed votes to vote sets
	for _, vote := range result.Votes {
		cs.addVoteNoLock(vote)
	}

	return nil
}

// addVoteNoLock adds a vote without acquiring the lock (caller must hold lock)
// ELEVENTH_REFACTOR: Now handles conflicting votes to preserve Byzantine evidence.
func (cs *ConsensusState) addVoteNoLock(vote *gen.Vote) {
	// Get the appropriate vote set
	var voteSet interface {
		AddVote(*gen.Vote) (bool, error)
	}

	switch vote.Type {
	case gen.VoteTypeVoteTypePrevote:
		voteSet = cs.votes.Prevotes(vote.Round)
	case gen.VoteTypeVoteTypePrecommit:
		voteSet = cs.votes.Precommits(vote.Round)
	default:
		return
	}

	if voteSet == nil {
		// Vote set doesn't exist for this round
		return
	}

	// ELEVENTH_REFACTOR: Handle equivocation during replay instead of ignoring.
	// If we see conflicting votes during replay, that's Byzantine evidence.
	_, err := voteSet.AddVote(vote)
	if err == ErrConflictingVote {
		log.Printf("[WARN] consensus: conflicting vote detected during replay: "+
			"height=%d round=%d validator=%d", vote.Height, vote.Round, vote.ValidatorIndex)
	} else if err != nil {
		log.Printf("[DEBUG] consensus: error adding vote during replay: %v", err)
	}
}

// WALWriter provides methods for writing WAL entries
type WALWriter struct {
	wal wal.WAL
}

// NewWALWriter creates a new WAL writer
func NewWALWriter(w wal.WAL) *WALWriter {
	return &WALWriter{wal: w}
}

// WriteProposal writes a proposal to the WAL
func (w *WALWriter) WriteProposal(height int64, round int32, proposal *gen.Proposal) error {
	msg, err := wal.NewProposalMessage(height, round, proposal)
	if err != nil {
		return err
	}
	return w.wal.WriteSync(msg)
}

// WriteVote writes a vote to the WAL
func (w *WALWriter) WriteVote(height int64, round int32, vote *gen.Vote) error {
	msg, err := wal.NewVoteMessage(height, round, vote)
	if err != nil {
		return err
	}
	return w.wal.Write(msg)
}

// WriteEndHeight writes an end-of-height marker to the WAL
func (w *WALWriter) WriteEndHeight(height int64) error {
	msg := wal.NewEndHeightMessage(height)
	return w.wal.WriteSync(msg)
}

// WriteState writes the consensus state to the WAL
func (w *WALWriter) WriteState(state *gen.ConsensusStateData) error {
	msg, err := wal.NewStateMessage(state)
	if err != nil {
		return err
	}
	return w.wal.WriteSync(msg)
}

// Flush flushes the WAL to disk
func (w *WALWriter) Flush() error {
	return w.wal.FlushAndSync()
}
