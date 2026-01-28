package evidence

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// Errors
var (
	ErrInvalidEvidence    = errors.New("invalid evidence")
	ErrDuplicateEvidence  = errors.New("duplicate evidence")
	ErrEvidenceExpired    = errors.New("evidence expired")
	ErrEvidenceNotFound   = errors.New("evidence not found")
	ErrInvalidVoteHeight  = errors.New("votes have different heights")
	ErrInvalidVoteRound   = errors.New("votes have different rounds")
	ErrInvalidVoteType    = errors.New("votes have different types")
	ErrInvalidValidator   = errors.New("votes from different validators")
	ErrSameBlockHash      = errors.New("votes for same block are not equivocation")
)

// Evidence type constants
const (
	EvidenceTypeDuplicateVote = gen.EvidenceTypeEvidenceTypeDuplicateVote
)

// H3: Limits for memory usage
const (
	// MaxSeenVotes limits memory usage for equivocation detection.
	// With 100 validators, 2 vote types per round, this allows ~500 rounds of history.
	MaxSeenVotes = 100000
)

// Config holds evidence pool configuration
type Config struct {
	// MaxAge is the maximum age of evidence that can be included in blocks
	MaxAge time.Duration
	// MaxAgeBlocks is the maximum block height age of evidence
	MaxAgeBlocks int64
	// MaxBytes is the maximum size of evidence to include in a block
	MaxBytes int64
}

// DefaultConfig returns default evidence pool configuration
func DefaultConfig() Config {
	return Config{
		MaxAge:       48 * time.Hour,
		MaxAgeBlocks: 100000,
		MaxBytes:     1048576, // 1MB
	}
}

// Pool manages Byzantine evidence
type Pool struct {
	mu     sync.RWMutex
	config Config

	// Pending evidence to include in blocks
	pending []*gen.Evidence

	// Committed evidence (already included in blocks)
	committed map[string]struct{}

	// Vote tracking for equivocation detection
	// key: validator/height/round/type
	seenVotes map[string]*gen.Vote

	// Current height for age checking
	currentHeight int64
	currentTime   time.Time
}

// NewPool creates a new evidence pool
func NewPool(config Config) *Pool {
	return &Pool{
		config:    config,
		committed: make(map[string]struct{}),
		seenVotes: make(map[string]*gen.Vote),
	}
}

// Update updates the pool's knowledge of current height and time
func (p *Pool) Update(height int64, blockTime time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.currentHeight = height
	p.currentTime = blockTime

	// Prune expired evidence
	p.pruneExpired()
}

// CheckVote checks a vote for equivocation and returns evidence if found.
// H3: Enforces MaxSeenVotes limit to prevent unbounded memory growth.
func (p *Pool) CheckVote(vote *gen.Vote, valSet *types.ValidatorSet) (*gen.DuplicateVoteEvidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := voteKey(vote)

	if existing, ok := p.seenVotes[key]; ok {
		// Check if this is equivocation (different block hash)
		if !votesForSameBlock(existing, vote) {
			// Found equivocation
			ev := &gen.DuplicateVoteEvidence{
				VoteA:            *existing,
				VoteB:            *vote,
				TotalVotingPower: valSet.TotalPower,
				Timestamp:        time.Now().UnixNano(),
			}

			// Get validator power
			if val := valSet.GetByName(types.AccountNameString(vote.Validator)); val != nil {
				ev.ValidatorPower = val.VotingPower
			}

			return ev, nil
		}
		// Same vote, not equivocation
		return nil, nil
	}

	// H3: Enforce size limit - prune oldest entries if needed
	if len(p.seenVotes) >= MaxSeenVotes {
		p.pruneOldestVotes(MaxSeenVotes / 10) // Remove 10%
	}

	// Store this vote for future comparison
	p.seenVotes[key] = vote
	return nil, nil
}

// AddEvidence adds verified evidence to the pool
func (p *Pool) AddEvidence(ev *gen.Evidence) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check for duplicate
	key := evidenceKey(ev)
	if _, ok := p.committed[key]; ok {
		return ErrDuplicateEvidence
	}

	// Check if already pending
	for _, pending := range p.pending {
		if evidenceKey(pending) == key {
			return ErrDuplicateEvidence
		}
	}

	// Check if expired
	if p.isExpired(ev) {
		return ErrEvidenceExpired
	}

	p.pending = append(p.pending, ev)
	return nil
}

// AddDuplicateVoteEvidence adds a DuplicateVoteEvidence to the pool
func (p *Pool) AddDuplicateVoteEvidence(dve *gen.DuplicateVoteEvidence) error {
	// Serialize the duplicate vote evidence
	data, err := dve.MarshalCramberry()
	if err != nil {
		return fmt.Errorf("failed to serialize evidence: %w", err)
	}

	ev := &gen.Evidence{
		Type:   EvidenceTypeDuplicateVote,
		Height: dve.VoteA.Height,
		Time:   dve.Timestamp,
		Data:   data,
	}

	return p.AddEvidence(ev)
}

// evidenceSize calculates the serialized size of evidence.
// L4: Proper size estimate based on Evidence schema structure:
// - Type: 4 bytes (uint32)
// - Height: 8 bytes (int64)
// - Time: 8 bytes (int64)
// - Data: 4 bytes (length prefix) + len(Data)
const evidenceOverhead = 4 + 8 + 8 + 4 // 24 bytes

func evidenceSize(ev *gen.Evidence) int64 {
	return int64(evidenceOverhead + len(ev.Data))
}

// PendingEvidence returns evidence to include in blocks, up to maxBytes
func (p *Pool) PendingEvidence(maxBytes int64) []gen.Evidence {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if maxBytes <= 0 {
		maxBytes = p.config.MaxBytes
	}

	var result []gen.Evidence
	var totalSize int64

	for _, ev := range p.pending {
		// L4: Use proper size calculation
		evSize := evidenceSize(ev)
		if totalSize+evSize > maxBytes {
			break
		}

		result = append(result, *ev)
		totalSize += evSize
	}

	return result
}

// MarkCommitted marks evidence as committed (included in a block)
func (p *Pool) MarkCommitted(evidence []gen.Evidence) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, ev := range evidence {
		key := evidenceKey(&ev)
		p.committed[key] = struct{}{}
	}

	// Remove from pending
	p.removePending(evidence)
}

// Size returns the number of pending evidence items
func (p *Pool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.pending)
}

// VerifyDuplicateVoteEvidence verifies that duplicate vote evidence is valid
func VerifyDuplicateVoteEvidence(dve *gen.DuplicateVoteEvidence, chainID string, valSet *types.ValidatorSet) error {
	voteA := &dve.VoteA
	voteB := &dve.VoteB

	// Votes must be for same height
	if voteA.Height != voteB.Height {
		return ErrInvalidVoteHeight
	}

	// Votes must be for same round
	if voteA.Round != voteB.Round {
		return ErrInvalidVoteRound
	}

	// Votes must be same type
	if voteA.Type != voteB.Type {
		return ErrInvalidVoteType
	}

	// Votes must be from same validator
	if types.AccountNameString(voteA.Validator) != types.AccountNameString(voteB.Validator) {
		return ErrInvalidValidator
	}

	// Votes must be for different blocks
	if votesForSameBlock(voteA, voteB) {
		return ErrSameBlockHash
	}

	// Verify signatures
	val := valSet.GetByName(types.AccountNameString(voteA.Validator))
	if val == nil {
		return ErrInvalidValidator
	}

	if err := types.VerifyVoteSignature(chainID, voteA, val.PublicKey); err != nil {
		return fmt.Errorf("invalid signature on vote A: %w", err)
	}

	if err := types.VerifyVoteSignature(chainID, voteB, val.PublicKey); err != nil {
		return fmt.Errorf("invalid signature on vote B: %w", err)
	}

	return nil
}

// pruneExpired removes expired evidence from pending
func (p *Pool) pruneExpired() {
	var valid []*gen.Evidence
	for _, ev := range p.pending {
		if !p.isExpired(ev) {
			valid = append(valid, ev)
		}
	}
	p.pending = valid

	// Also prune old seen votes
	for key, vote := range p.seenVotes {
		if p.currentHeight-vote.Height > p.config.MaxAgeBlocks {
			delete(p.seenVotes, key)
		}
	}
}

// pruneOldestVotes removes the oldest n votes by height.
// H3: Called when seenVotes exceeds MaxSeenVotes.
// Caller must hold p.mu.
func (p *Pool) pruneOldestVotes(n int) {
	if n <= 0 || len(p.seenVotes) == 0 {
		return
	}

	// Find minimum heights and group votes by height
	heightVotes := make(map[int64][]string)
	for key, vote := range p.seenVotes {
		heightVotes[vote.Height] = append(heightVotes[vote.Height], key)
	}

	// Collect and sort heights
	heights := make([]int64, 0, len(heightVotes))
	for h := range heightVotes {
		heights = append(heights, h)
	}
	// Sort ascending (oldest first)
	for i := 0; i < len(heights)-1; i++ {
		for j := i + 1; j < len(heights); j++ {
			if heights[j] < heights[i] {
				heights[i], heights[j] = heights[j], heights[i]
			}
		}
	}

	// Remove votes starting from oldest heights
	removed := 0
	for _, h := range heights {
		if removed >= n {
			break
		}
		for _, key := range heightVotes[h] {
			delete(p.seenVotes, key)
			removed++
			if removed >= n {
				break
			}
		}
	}
}

// isExpired checks if evidence is too old
func (p *Pool) isExpired(ev *gen.Evidence) bool {
	// Check block height age
	if p.currentHeight-ev.Height > p.config.MaxAgeBlocks {
		return true
	}

	// Check time age
	evTime := time.Unix(0, ev.Time)
	if p.currentTime.Sub(evTime) > p.config.MaxAge {
		return true
	}

	return false
}

// removePending removes evidence from the pending list
func (p *Pool) removePending(toRemove []gen.Evidence) {
	removeSet := make(map[string]struct{})
	for _, ev := range toRemove {
		removeSet[evidenceKey(&ev)] = struct{}{}
	}

	var remaining []*gen.Evidence
	for _, ev := range p.pending {
		if _, ok := removeSet[evidenceKey(ev)]; !ok {
			remaining = append(remaining, ev)
		}
	}
	p.pending = remaining
}

// voteKey returns a unique key for a vote (for equivocation detection)
func voteKey(vote *gen.Vote) string {
	return fmt.Sprintf("%s/%d/%d/%d",
		types.AccountNameString(vote.Validator),
		vote.Height,
		vote.Round,
		vote.Type)
}

// evidenceKey returns a unique key for evidence.
// Includes hash of data to avoid collisions with same type/height/time.
func evidenceKey(ev *gen.Evidence) string {
	dataHash := sha256.Sum256(ev.Data)
	return fmt.Sprintf("%d/%d/%d/%x", ev.Type, ev.Height, ev.Time, dataHash[:8])
}

// votesForSameBlock checks if two votes are for the same block
func votesForSameBlock(a, b *gen.Vote) bool {
	if a.BlockHash == nil && b.BlockHash == nil {
		return true // Both nil votes
	}
	if a.BlockHash == nil || b.BlockHash == nil {
		return false // One nil, one not
	}
	return types.HashEqual(*a.BlockHash, *b.BlockHash)
}
