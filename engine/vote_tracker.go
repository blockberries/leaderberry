package engine

import (
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// MaxTimestampDrift is the maximum allowed clock drift for vote timestamps.
// Votes with timestamps outside the range [now - MaxTimestampDrift, now + MaxTimestampDrift]
// are rejected as invalid. The boundaries are inclusive:
//   - Vote at exactly (now - 10 min) is ACCEPTED
//   - Vote at exactly (now + 10 min) is ACCEPTED
//   - Vote at (now - 10 min - 1 ns) is REJECTED
//   - Vote at (now + 10 min + 1 ns) is REJECTED
// TWENTY_SECOND_REFACTOR: Documented boundary behavior for clarity.
const MaxTimestampDrift = 10 * time.Minute

// VoteSet tracks votes for a single height/round/type combination
type VoteSet struct {
	mu           sync.RWMutex
	chainID      string
	height       int64
	round        int32
	voteType     gen.VoteType
	validatorSet *types.ValidatorSet

	votes        map[uint16]*gen.Vote // by validator index
	votesByBlock map[string]*blockVotes
	sum          int64
	maj23        *blockVotes

	// Peer claims of 2/3+ majority (used for POL validation and vote requesting)
	peerMaj23 map[string]*types.Hash // peerID -> claimed block hash

	// TWELFTH_REFACTOR: Generation counter and parent reference for stale detection.
	// If parent.generation != myGeneration, this VoteSet is stale and should reject writes.
	parent       *HeightVoteSet
	myGeneration uint64
}

type blockVotes struct {
	blockHash  *types.Hash
	votes      []*gen.Vote
	totalPower int64
}

// NewVoteSet creates a new VoteSet for tracking votes.
// TWELFTH_REFACTOR: Now accepts parent HeightVoteSet for stale reference detection.
// Pass nil for parent in standalone usage (e.g., tests).
func NewVoteSet(
	chainID string,
	height int64,
	round int32,
	voteType gen.VoteType,
	valSet *types.ValidatorSet,
) *VoteSet {
	return &VoteSet{
		chainID:      chainID,
		height:       height,
		round:        round,
		voteType:     voteType,
		validatorSet: valSet,
		votes:        make(map[uint16]*gen.Vote),
		votesByBlock: make(map[string]*blockVotes),
	}
}

// newVoteSetWithParent creates a VoteSet linked to a HeightVoteSet for stale detection.
// Caller must hold hvs.mu.
func newVoteSetWithParent(
	hvs *HeightVoteSet,
	round int32,
	voteType gen.VoteType,
) *VoteSet {
	return &VoteSet{
		chainID:      hvs.chainID,
		height:       hvs.height,
		round:        round,
		voteType:     voteType,
		validatorSet: hvs.validatorSet,
		votes:        make(map[uint16]*gen.Vote),
		votesByBlock: make(map[string]*blockVotes),
		parent:       hvs,
		myGeneration: hvs.generation.Load(),
	}
}

// AddVote adds a vote to the set. Returns true if the vote was added.
// Returns an error if the vote is invalid or conflicts with an existing vote.
// TWELFTH_REFACTOR: Returns ErrStaleVoteSet if this VoteSet is from a previous height.
func (vs *VoteSet) AddVote(vote *gen.Vote) (bool, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	// TWELFTH_REFACTOR: Check for stale VoteSet reference.
	// If this VoteSet was created for a previous height (generation mismatch),
	// reject the vote to prevent lost votes after Reset().
	// Uses atomic to avoid deadlock with HeightVoteSet.AddVote() which holds hvs.mu.
	if vs.parent != nil {
		currentGen := vs.parent.generation.Load()
		if currentGen != vs.myGeneration {
			return false, ErrStaleVoteSet
		}
	}

	// Validate vote matches this set
	if vote.Height != vs.height || vote.Round != vs.round || vote.Type != vs.voteType {
		return false, ErrInvalidVote
	}

	// M1: Validate timestamp is reasonable
	voteTime := time.Unix(0, vote.Timestamp)
	now := time.Now()
	if voteTime.After(now.Add(MaxTimestampDrift)) {
		return false, fmt.Errorf("%w: timestamp too far in future", ErrInvalidVote)
	}
	if voteTime.Before(now.Add(-MaxTimestampDrift)) {
		return false, fmt.Errorf("%w: timestamp too far in past", ErrInvalidVote)
	}

	return vs.addVoteInternal(vote)
}

// addVoteForReplay adds a vote during WAL replay, skipping timestamp validation.
// TWENTIETH_REFACTOR: Timestamp validation fails for votes replayed after extended downtime.
// During replay, votes can be arbitrarily old (node may have been down for days), but
// their timestamps were valid when originally received. Skip timestamp checks but verify
// all other vote properties (signature, validator membership, etc).
func (vs *VoteSet) addVoteForReplay(vote *gen.Vote) (bool, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	// TWELFTH_REFACTOR: Check for stale VoteSet reference.
	if vs.parent != nil {
		currentGen := vs.parent.generation.Load()
		if currentGen != vs.myGeneration {
			return false, ErrStaleVoteSet
		}
	}

	// Validate vote matches this set
	if vote.Height != vs.height || vote.Round != vs.round || vote.Type != vs.voteType {
		return false, ErrInvalidVote
	}

	// Skip timestamp validation during replay - vote was valid when received
	return vs.addVoteInternal(vote)
}

// addVoteInternal completes vote addition after validation.
// Assumes caller holds vs.mu and has performed appropriate validation.
func (vs *VoteSet) addVoteInternal(vote *gen.Vote) (bool, error) {
	// Check validator exists
	val := vs.validatorSet.GetByIndex(vote.ValidatorIndex)
	if val == nil {
		return false, ErrUnknownValidator
	}

	// Verify validator name matches
	if !types.AccountNameEqual(val.Name, vote.Validator) {
		return false, ErrUnknownValidator
	}

	// Verify signature
	signBytes := types.VoteSignBytes(vs.chainID, vote)
	if !types.VerifySignature(val.PublicKey, signBytes, vote.Signature) {
		return false, ErrInvalidSignature
	}

	// Check for duplicate or conflict
	existing := vs.votes[vote.ValidatorIndex]
	if existing != nil {
		if votesEqual(existing, vote) {
			return false, nil // Duplicate, already have it
		}
		return false, ErrConflictingVote // Equivocation!
	}

	// TWENTY_FIFTH_REFACTOR: Check overflow BEFORE making any modifications.
	// Previously overflow checks were after storage, leaving VoteSet in inconsistent
	// state if overflow was detected (vote stored but not counted).
	// Check should never fail due to MaxTotalVotingPower validation, but this is
	// defensive against bugs in duplicate detection or validator set management.
	if val.VotingPower > 0 && vs.sum > math.MaxInt64-val.VotingPower {
		return false, fmt.Errorf("voting power sum would overflow (current: %d, adding: %d)", vs.sum, val.VotingPower)
	}

	// Get or create block votes entry to check per-block overflow
	key := blockHashKey(vote.BlockHash)
	bv, ok := vs.votesByBlock[key]
	isNewBlockVotes := !ok
	if isNewBlockVotes {
		// TWENTY_SEVENTH_REFACTOR: Create with nil blockHash initially.
		// We'll set it from the deep-copied vote below to prevent caller
		// modifications from corrupting bv.blockHash (which is used by
		// TwoThirdsMajority() and MakeCommit()).
		bv = &blockVotes{}
	}

	// Check per-block overflow before modifications
	if val.VotingPower > 0 && bv.totalPower > math.MaxInt64-val.VotingPower {
		return false, fmt.Errorf("block voting power would overflow (current: %d, adding: %d)", bv.totalPower, val.VotingPower)
	}

	// All checks passed - now make modifications atomically
	// ELEVENTH_REFACTOR: Deep copy the vote before storing to prevent caller
	// modifications from corrupting internal state.
	voteCopy := types.CopyVote(vote)
	vs.votes[voteCopy.ValidatorIndex] = voteCopy
	vs.sum += val.VotingPower

	// Store block votes entry if newly created
	if isNewBlockVotes {
		// TWENTY_SEVENTH_REFACTOR: Use deep-copied hash to prevent corruption.
		bv.blockHash = voteCopy.BlockHash
		vs.votesByBlock[key] = bv
	}
	bv.votes = append(bv.votes, voteCopy)
	bv.totalPower += val.VotingPower

	// Check for 2/3+ majority
	quorum := vs.validatorSet.TwoThirdsMajority()
	if bv.totalPower >= quorum && vs.maj23 == nil {
		vs.maj23 = bv
	}

	return true, nil
}

// TwoThirdsMajority returns the block hash with 2/3+ votes, if any.
// NINTH_REFACTOR: Returns a deep copy to prevent callers from modifying internal state.
func (vs *VoteSet) TwoThirdsMajority() (*types.Hash, bool) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.maj23 != nil {
		return types.CopyHash(vs.maj23.blockHash), true
	}
	return nil, false
}

// HasTwoThirdsMajority returns true if any block has 2/3+ votes
func (vs *VoteSet) HasTwoThirdsMajority() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.maj23 != nil
}

// HasTwoThirdsAny returns true if 2/3+ of voting power has voted (for any block or nil)
func (vs *VoteSet) HasTwoThirdsAny() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.sum >= vs.validatorSet.TwoThirdsMajority()
}

// HasAll returns true if all validators have voted
func (vs *VoteSet) HasAll() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.votes) == vs.validatorSet.Size()
}

// GetVote returns the vote from a validator, if any.
// NINTH_REFACTOR: Returns a deep copy to prevent callers from modifying internal state.
func (vs *VoteSet) GetVote(valIndex uint16) *gen.Vote {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	vote := vs.votes[valIndex]
	if vote == nil {
		return nil
	}
	return types.CopyVote(vote)
}

// Size returns the number of votes
func (vs *VoteSet) Size() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.votes)
}

// VotingPower returns the total voting power of votes in the set
func (vs *VoteSet) VotingPower() int64 {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.sum
}

// GetVotes returns all votes sorted by validator index for deterministic ordering.
// H5: Map iteration is non-deterministic; sorting ensures consistent results.
// EIGHTH_REFACTOR: Returns deep copies to prevent callers from modifying internal state.
func (vs *VoteSet) GetVotes() []*gen.Vote {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	votes := make([]*gen.Vote, 0, len(vs.votes))
	for _, v := range vs.votes {
		// EIGHTH_REFACTOR: Return copies instead of pointers to internal votes.
		// This prevents callers from corrupting VoteSet state by modifying
		// the returned votes.
		votes = append(votes, types.CopyVote(v))
	}

	// H5: Sort by validator index for deterministic ordering
	sort.Slice(votes, func(i, j int) bool {
		return votes[i].ValidatorIndex < votes[j].ValidatorIndex
	})

	return votes
}

// GetVotesForBlock returns all votes for a specific block hash.
// TENTH_REFACTOR: Returns deep copies to prevent callers from modifying internal state.
// TWENTY_SECOND_REFACTOR: Returns empty slice instead of nil for consistency with GetVotes().
// This follows Go idioms and prevents nil pointer confusion for callers.
func (vs *VoteSet) GetVotesForBlock(blockHash *types.Hash) []*gen.Vote {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	key := blockHashKey(blockHash)
	bv, ok := vs.votesByBlock[key]
	if !ok {
		// TWENTY_SECOND_REFACTOR: Return empty slice (not nil) for consistency
		return []*gen.Vote{}
	}

	// TENTH_REFACTOR: Return deep copies like GetVotes() does.
	// Previously used copy() which only copies pointers, not vote data.
	votes := make([]*gen.Vote, 0, len(bv.votes))
	for _, v := range bv.votes {
		votes = append(votes, types.CopyVote(v))
	}
	return votes
}

// SetPeerMaj23 records that a peer claims to have seen 2/3+ votes for a block.
// This is used for proof-of-lock validation and requesting missing votes.
// TENTH_REFACTOR: Stores a copy of the hash to prevent caller modifications from corrupting state.
func (vs *VoteSet) SetPeerMaj23(peerID string, blockHash *types.Hash) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if vs.peerMaj23 == nil {
		vs.peerMaj23 = make(map[string]*types.Hash)
	}
	// TENTH_REFACTOR: Store a copy to prevent caller from modifying internal state
	vs.peerMaj23[peerID] = types.CopyHash(blockHash)
}

// GetPeerMaj23Claims returns all peer claims of 2/3+ majority.
// Returns a copy of the map to avoid race conditions.
// TENTH_REFACTOR: Returns deep copies of hashes to prevent callers from modifying internal state.
func (vs *VoteSet) GetPeerMaj23Claims() map[string]*types.Hash {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.peerMaj23 == nil {
		return nil
	}

	result := make(map[string]*types.Hash, len(vs.peerMaj23))
	for k, v := range vs.peerMaj23 {
		// TENTH_REFACTOR: Return copies to prevent callers from modifying internal state
		result[k] = types.CopyHash(v)
	}
	return result
}

// HasPeerMaj23 returns true if any peer has claimed 2/3+ for a block.
func (vs *VoteSet) HasPeerMaj23() bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.peerMaj23) > 0
}

// MakeCommit creates a Commit from 2/3+ precommits.
// Returns nil if there's no 2/3+ majority for a non-nil block.
// M3: Only includes votes for the committed block, not nil votes or votes for other blocks.
func (vs *VoteSet) MakeCommit() *gen.Commit {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.voteType != types.VoteTypePrecommit || vs.maj23 == nil {
		return nil
	}

	// Cannot create commit for nil block
	if vs.maj23.blockHash == nil || types.IsHashEmpty(vs.maj23.blockHash) {
		return nil
	}

	// M3: Only include votes for the committed block
	blockHash := vs.maj23.blockHash
	sigs := make([]gen.CommitSig, 0)

	for _, vote := range vs.votes {
		// Skip nil votes and votes for other blocks
		if vote.BlockHash == nil || types.IsHashEmpty(vote.BlockHash) {
			continue
		}
		if !types.HashEqual(*vote.BlockHash, *blockHash) {
			continue
		}

		// NINTH_REFACTOR: Deep copy signature and block hash to prevent
		// the commit from being corrupted if the original votes are modified.
		sig := gen.CommitSig{
			ValidatorIndex: vote.ValidatorIndex,
			Timestamp:      vote.Timestamp,
			BlockHash:      types.CopyHash(vote.BlockHash),
		}
		// Deep copy signature data
		if len(vote.Signature.Data) > 0 {
			sig.Signature.Data = make([]byte, len(vote.Signature.Data))
			copy(sig.Signature.Data, vote.Signature.Data)
		}
		sigs = append(sigs, sig)
	}

	// M3: Sort for deterministic ordering
	sort.Slice(sigs, func(i, j int) bool {
		return sigs[i].ValidatorIndex < sigs[j].ValidatorIndex
	})

	// NINTH_REFACTOR: Deep copy block hash to prevent corruption
	blockHashCopy := types.CopyHash(blockHash)
	return &gen.Commit{
		Height:     vs.height,
		Round:      vs.round,
		BlockHash:  *blockHashCopy,
		Signatures: sigs,
	}
}

// Helper functions

// blockHashKey returns a string key for a block hash
// L1: Use hex encoding for debuggability instead of raw binary string
func blockHashKey(h *types.Hash) string {
	if h == nil || types.IsHashEmpty(h) {
		return "nil"
	}
	return hex.EncodeToString(h.Data)
}

func votesEqual(a, b *gen.Vote) bool {
	if a.Type != b.Type || a.Height != b.Height || a.Round != b.Round {
		return false
	}
	if a.ValidatorIndex != b.ValidatorIndex {
		return false
	}
	// Check block hash
	if a.BlockHash == nil && b.BlockHash == nil {
		return true
	}
	if a.BlockHash == nil || b.BlockHash == nil {
		return false
	}
	return types.HashEqual(*a.BlockHash, *b.BlockHash)
}

// HeightVoteSet tracks all votes for a height across all rounds
type HeightVoteSet struct {
	mu           sync.RWMutex
	chainID      string
	height       int64
	validatorSet *types.ValidatorSet

	prevotes   map[int32]*VoteSet
	precommits map[int32]*VoteSet

	// TWELFTH_REFACTOR: Generation counter to detect stale VoteSet references.
	// Incremented on Reset() to invalidate VoteSets from previous heights.
	// Uses atomic to avoid lock contention with VoteSet.AddVote().
	generation atomic.Uint64
}

// NewHeightVoteSet creates a HeightVoteSet for a given height
func NewHeightVoteSet(chainID string, height int64, valSet *types.ValidatorSet) *HeightVoteSet {
	return &HeightVoteSet{
		chainID:      chainID,
		height:       height,
		validatorSet: valSet,
		prevotes:     make(map[int32]*VoteSet),
		precommits:   make(map[int32]*VoteSet),
	}
}

// AddVote adds a vote to the appropriate VoteSet.
// CR2: Keep lock held during entire operation to prevent race with Reset().
// TWELFTH_REFACTOR: Uses newVoteSetWithParent for stale reference detection.
func (hvs *HeightVoteSet) AddVote(vote *gen.Vote) (bool, error) {
	hvs.mu.Lock()
	defer hvs.mu.Unlock()

	if vote.Height != hvs.height {
		return false, ErrInvalidHeight
	}

	var voteSet *VoteSet
	if vote.Type == types.VoteTypePrevote {
		voteSet = hvs.prevotes[vote.Round]
		if voteSet == nil {
			voteSet = newVoteSetWithParent(hvs, vote.Round, types.VoteTypePrevote)
			hvs.prevotes[vote.Round] = voteSet
		}
	} else if vote.Type == types.VoteTypePrecommit {
		voteSet = hvs.precommits[vote.Round]
		if voteSet == nil {
			voteSet = newVoteSetWithParent(hvs, vote.Round, types.VoteTypePrecommit)
			hvs.precommits[vote.Round] = voteSet
		}
	} else {
		return false, ErrInvalidVote
	}

	// VoteSet has its own mutex, so nested locking is safe
	return voteSet.AddVote(vote)
}

// AddVoteForReplay adds a vote during WAL replay, creating VoteSets on demand.
// TWENTY_FIFTH_REFACTOR: This method was added to fix votes being silently dropped
// during replay. Unlike AddVote which uses AddVote(), this uses addVoteForReplay()
// to skip timestamp validation (votes from before crash can be arbitrarily old).
func (hvs *HeightVoteSet) AddVoteForReplay(vote *gen.Vote) (bool, error) {
	hvs.mu.Lock()
	defer hvs.mu.Unlock()

	if vote.Height != hvs.height {
		return false, ErrInvalidHeight
	}

	var voteSet *VoteSet
	if vote.Type == types.VoteTypePrevote {
		voteSet = hvs.prevotes[vote.Round]
		if voteSet == nil {
			voteSet = newVoteSetWithParent(hvs, vote.Round, types.VoteTypePrevote)
			hvs.prevotes[vote.Round] = voteSet
		}
	} else if vote.Type == types.VoteTypePrecommit {
		voteSet = hvs.precommits[vote.Round]
		if voteSet == nil {
			voteSet = newVoteSetWithParent(hvs, vote.Round, types.VoteTypePrecommit)
			hvs.precommits[vote.Round] = voteSet
		}
	} else {
		return false, ErrInvalidVote
	}

	// Use addVoteForReplay to skip timestamp validation during WAL recovery
	return voteSet.addVoteForReplay(vote)
}

// Prevotes returns the prevote set for a round
func (hvs *HeightVoteSet) Prevotes(round int32) *VoteSet {
	hvs.mu.RLock()
	defer hvs.mu.RUnlock()
	return hvs.prevotes[round]
}

// Precommits returns the precommit set for a round
func (hvs *HeightVoteSet) Precommits(round int32) *VoteSet {
	hvs.mu.RLock()
	defer hvs.mu.RUnlock()
	return hvs.precommits[round]
}

// SetPeerMaj23 records that a peer claims to have seen 2/3+ votes for a block.
// This is used for proof-of-lock validation and requesting missing votes.
// CR2: Keep lock held during entire operation to prevent race with Reset().
// TWELFTH_REFACTOR: Uses newVoteSetWithParent for stale reference detection.
func (hvs *HeightVoteSet) SetPeerMaj23(peerID string, round int32, voteType gen.VoteType, blockHash *types.Hash) {
	hvs.mu.Lock()
	defer hvs.mu.Unlock()

	var voteSet *VoteSet
	if voteType == types.VoteTypePrevote {
		voteSet = hvs.prevotes[round]
		if voteSet == nil {
			voteSet = newVoteSetWithParent(hvs, round, types.VoteTypePrevote)
			hvs.prevotes[round] = voteSet
		}
	} else {
		voteSet = hvs.precommits[round]
		if voteSet == nil {
			voteSet = newVoteSetWithParent(hvs, round, types.VoteTypePrecommit)
			hvs.precommits[round] = voteSet
		}
	}

	// Record the peer's claim on the vote set
	// VoteSet has its own mutex, so nested locking is safe
	voteSet.SetPeerMaj23(peerID, blockHash)
}

// Height returns the height
// EIGHTEENTH_REFACTOR: Added RLock to prevent data race with Reset().
// Previously read hvs.height without lock, which could race with Reset() writing it.
func (hvs *HeightVoteSet) Height() int64 {
	hvs.mu.RLock()
	defer hvs.mu.RUnlock()
	return hvs.height
}

// Reset clears all votes (used when moving to new height).
// TWELFTH_REFACTOR: Increments generation to invalidate stale VoteSet references.
// Any VoteSet obtained before Reset() will reject new votes after Reset().
func (hvs *HeightVoteSet) Reset(height int64, valSet *types.ValidatorSet) {
	hvs.mu.Lock()
	defer hvs.mu.Unlock()

	hvs.height = height
	hvs.validatorSet = valSet
	hvs.prevotes = make(map[int32]*VoteSet)
	hvs.precommits = make(map[int32]*VoteSet)
	hvs.generation.Add(1) // TWELFTH_REFACTOR: Invalidate stale VoteSet references
}
