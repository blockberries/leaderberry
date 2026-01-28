package engine

import (
	"encoding/hex"
	"sync"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

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
}

type blockVotes struct {
	blockHash  *types.Hash
	votes      []*gen.Vote
	totalPower int64
}

// NewVoteSet creates a new VoteSet for tracking votes
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

// AddVote adds a vote to the set. Returns true if the vote was added.
// Returns an error if the vote is invalid or conflicts with an existing vote.
func (vs *VoteSet) AddVote(vote *gen.Vote) (bool, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Validate vote matches this set
	if vote.Height != vs.height || vote.Round != vs.round || vote.Type != vs.voteType {
		return false, ErrInvalidVote
	}

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

	// Add vote
	vs.votes[vote.ValidatorIndex] = vote
	vs.sum += val.VotingPower

	// Track by block hash
	key := blockHashKey(vote.BlockHash)
	bv, ok := vs.votesByBlock[key]
	if !ok {
		bv = &blockVotes{blockHash: vote.BlockHash}
		vs.votesByBlock[key] = bv
	}
	bv.votes = append(bv.votes, vote)
	bv.totalPower += val.VotingPower

	// Check for 2/3+ majority
	quorum := vs.validatorSet.TwoThirdsMajority()
	if bv.totalPower >= quorum && vs.maj23 == nil {
		vs.maj23 = bv
	}

	return true, nil
}

// TwoThirdsMajority returns the block hash with 2/3+ votes, if any
func (vs *VoteSet) TwoThirdsMajority() (*types.Hash, bool) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.maj23 != nil {
		return vs.maj23.blockHash, true
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

// GetVote returns the vote from a validator, if any
func (vs *VoteSet) GetVote(valIndex uint16) *gen.Vote {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.votes[valIndex]
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

// GetVotes returns all votes
func (vs *VoteSet) GetVotes() []*gen.Vote {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	votes := make([]*gen.Vote, 0, len(vs.votes))
	for _, v := range vs.votes {
		votes = append(votes, v)
	}
	return votes
}

// GetVotesForBlock returns all votes for a specific block hash
func (vs *VoteSet) GetVotesForBlock(blockHash *types.Hash) []*gen.Vote {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	key := blockHashKey(blockHash)
	bv, ok := vs.votesByBlock[key]
	if !ok {
		return nil
	}

	votes := make([]*gen.Vote, len(bv.votes))
	copy(votes, bv.votes)
	return votes
}

// SetPeerMaj23 records that a peer claims to have seen 2/3+ votes for a block.
// This is used for proof-of-lock validation and requesting missing votes.
func (vs *VoteSet) SetPeerMaj23(peerID string, blockHash *types.Hash) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if vs.peerMaj23 == nil {
		vs.peerMaj23 = make(map[string]*types.Hash)
	}
	vs.peerMaj23[peerID] = blockHash
}

// GetPeerMaj23Claims returns all peer claims of 2/3+ majority.
// Returns a copy of the map to avoid race conditions.
func (vs *VoteSet) GetPeerMaj23Claims() map[string]*types.Hash {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.peerMaj23 == nil {
		return nil
	}

	result := make(map[string]*types.Hash, len(vs.peerMaj23))
	for k, v := range vs.peerMaj23 {
		result[k] = v
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

	sigs := make([]gen.CommitSig, 0, len(vs.votes))
	for _, vote := range vs.votes {
		sig := gen.CommitSig{
			ValidatorIndex: vote.ValidatorIndex,
			Signature:      vote.Signature,
			Timestamp:      vote.Timestamp,
			BlockHash:      vote.BlockHash,
		}
		sigs = append(sigs, sig)
	}

	return &gen.Commit{
		Height:     vs.height,
		Round:      vs.round,
		BlockHash:  *vs.maj23.blockHash,
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
			voteSet = NewVoteSet(hvs.chainID, hvs.height, vote.Round, types.VoteTypePrevote, hvs.validatorSet)
			hvs.prevotes[vote.Round] = voteSet
		}
	} else if vote.Type == types.VoteTypePrecommit {
		voteSet = hvs.precommits[vote.Round]
		if voteSet == nil {
			voteSet = NewVoteSet(hvs.chainID, hvs.height, vote.Round, types.VoteTypePrecommit, hvs.validatorSet)
			hvs.precommits[vote.Round] = voteSet
		}
	} else {
		return false, ErrInvalidVote
	}

	// VoteSet has its own mutex, so nested locking is safe
	return voteSet.AddVote(vote)
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
func (hvs *HeightVoteSet) SetPeerMaj23(peerID string, round int32, voteType gen.VoteType, blockHash *types.Hash) {
	hvs.mu.Lock()
	defer hvs.mu.Unlock()

	var voteSet *VoteSet
	if voteType == types.VoteTypePrevote {
		voteSet = hvs.prevotes[round]
		if voteSet == nil {
			voteSet = NewVoteSet(hvs.chainID, hvs.height, round, types.VoteTypePrevote, hvs.validatorSet)
			hvs.prevotes[round] = voteSet
		}
	} else {
		voteSet = hvs.precommits[round]
		if voteSet == nil {
			voteSet = NewVoteSet(hvs.chainID, hvs.height, round, types.VoteTypePrecommit, hvs.validatorSet)
			hvs.precommits[round] = voteSet
		}
	}

	// Record the peer's claim on the vote set
	// VoteSet has its own mutex, so nested locking is safe
	voteSet.SetPeerMaj23(peerID, blockHash)
}

// Height returns the height
func (hvs *HeightVoteSet) Height() int64 {
	return hvs.height
}

// Reset clears all votes (used when moving to new height)
func (hvs *HeightVoteSet) Reset(height int64, valSet *types.ValidatorSet) {
	hvs.mu.Lock()
	defer hvs.mu.Unlock()

	hvs.height = height
	hvs.validatorSet = valSet
	hvs.prevotes = make(map[int32]*VoteSet)
	hvs.precommits = make(map[int32]*VoteSet)
}
