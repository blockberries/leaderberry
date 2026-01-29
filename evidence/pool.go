package evidence

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"sort"
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
	// EIGHTEENTH_REFACTOR: Added error for vote pool full to avoid silent dropping
	ErrVotePoolFull = errors.New("vote pool full")
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

	// CR6: Protect votes within this many heights of current to prevent
	// losing evidence that hasn't been committed yet.
	VoteProtectionWindow = 1000

	// M8: Maximum pending evidence to prevent unbounded memory growth
	MaxPendingEvidence = 10000
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
// SEVENTH_REFACTOR: Now verifies vote signature before storing to prevent
// fake votes from being stored and triggering false equivocation detection.
// Pass chainID for signature verification. If empty, verification is skipped
// (useful for testing, but should always be provided in production).
func (p *Pool) CheckVote(vote *gen.Vote, valSet *types.ValidatorSet, chainID string) (*gen.DuplicateVoteEvidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// SEVENTH_REFACTOR: Verify vote signature before processing
	// This prevents attackers from injecting fake votes to fill memory or
	// trigger false equivocation detection.
	val := valSet.GetByName(types.AccountNameString(vote.Validator))
	if val == nil {
		return nil, ErrInvalidValidator
	}

	// Verify signature if chainID is provided (production use)
	if chainID != "" {
		if err := types.VerifyVoteSignature(chainID, vote, val.PublicKey); err != nil {
			return nil, fmt.Errorf("invalid vote signature: %w", err)
		}
	}

	key := voteKey(vote)

	if existing, ok := p.seenVotes[key]; ok {
		// Check if this is equivocation (different block hash)
		if !votesForSameBlock(existing, vote) {
			// Found equivocation
			// SIXTH_REFACTOR: Deep copy both votes to ensure evidence is immutable.
			// VoteA (existing) was already deep-copied when stored.
			// VoteB (vote) must also be deep-copied to prevent caller modification.
			voteACopy := types.CopyVote(existing)
			voteBCopy := types.CopyVote(vote)
			ev := &gen.DuplicateVoteEvidence{
				VoteA:            *voteACopy,
				VoteB:            *voteBCopy,
				TotalVotingPower: valSet.TotalPower,
				Timestamp:        time.Now().UnixNano(),
			}

			// Get validator power (reuse val from earlier lookup)
			ev.ValidatorPower = val.VotingPower

			// EIGHTEENTH_REFACTOR: Validate created evidence before returning.
			// Previously returned unvalidated evidence which could be invalid if
			// the existing vote's signature was compromised or validator key changed.
			if chainID != "" {
				if err := VerifyDuplicateVoteEvidence(ev, chainID, valSet); err != nil {
					log.Printf("[ERROR] evidence: created invalid evidence for equivocation: %v", err)
					return nil, fmt.Errorf("created invalid evidence: %w", err)
				}
			}

			return ev, nil
		}
		// Same vote, not equivocation
		return nil, nil
	}

	// H3: Enforce size limit - prune oldest entries if needed
	// EIGHTH_REFACTOR: Check if pruning actually succeeded before adding.
	// If all votes are in the protection window, pruning fails and we must
	// reject the vote to prevent unbounded memory growth.
	// EIGHTEENTH_REFACTOR: Return ErrVotePoolFull instead of silently dropping.
	// Previously returned nil,nil which made caller think success occurred.
	if len(p.seenVotes) >= MaxSeenVotes {
		p.pruneOldestVotes(MaxSeenVotes / 10) // Try to remove 10%
		if len(p.seenVotes) >= MaxSeenVotes {
			// Pruning didn't help - pool is full of protected votes
			log.Printf("[WARN] evidence: vote pool full (%d votes in protection window), cannot track vote", len(p.seenVotes))
			return nil, ErrVotePoolFull
		}
	}

	// H3: Deep copy vote before storing to prevent caller from modifying it.
	// A shallow copy is insufficient because slice fields (Signature.Data,
	// BlockHash.Data) would still share memory with the original.
	p.seenVotes[key] = types.CopyVote(vote)
	return nil, nil
}

// AddEvidence adds verified evidence to the pool
func (p *Pool) AddEvidence(ev *gen.Evidence) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// TWENTIETH_REFACTOR: Validate input to prevent adding invalid evidence
	if ev == nil {
		return ErrInvalidEvidence
	}

	// Basic validation: ensure evidence has required fields
	if len(ev.Data) == 0 {
		return ErrInvalidEvidence
	}

	// M8: Check pending evidence limit
	if len(p.pending) >= MaxPendingEvidence {
		return errors.New("pending evidence pool full")
	}

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

	// TWENTIETH_REFACTOR: Deep copy evidence before storing to prevent caller corruption.
	// If we store the pointer directly, caller could modify the evidence after adding.
	evCopy := &gen.Evidence{
		Type:   ev.Type,
		Height: ev.Height,
		Time:   ev.Time,
		Data:   make([]byte, len(ev.Data)),
	}
	copy(evCopy.Data, ev.Data)
	p.pending = append(p.pending, evCopy)
	return nil
}

// AddDuplicateVoteEvidence adds a DuplicateVoteEvidence to the pool.
// EIGHTEENTH_REFACTOR: Now requires chainID and valSet to validate evidence before adding.
// Previously accepted external evidence without verification.
// If chainID is empty, validation is skipped (useful for testing).
func (p *Pool) AddDuplicateVoteEvidence(dve *gen.DuplicateVoteEvidence, chainID string, valSet *types.ValidatorSet) error {
	// TWENTIETH_REFACTOR: Validate inputs
	if dve == nil {
		return ErrInvalidEvidence
	}

	// EIGHTEENTH_REFACTOR: Validate evidence before adding to prevent invalid evidence
	// from being stored and propagated to the network.
	// Skip validation if chainID is empty (for testing only - production should always provide chainID).
	if chainID != "" {
		if err := VerifyDuplicateVoteEvidence(dve, chainID, valSet); err != nil {
			return fmt.Errorf("invalid duplicate vote evidence: %w", err)
		}
	}

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

		// TWENTIETH_REFACTOR: Deep copy evidence to prevent caller corruption of internal state.
		// The gen.Evidence struct contains a Data []byte field which is shared if we only
		// dereference the pointer. Caller could modify Data and corrupt the pool.
		evCopy := gen.Evidence{
			Type:   ev.Type,
			Height: ev.Height,
			Time:   ev.Time,
			Data:   make([]byte, len(ev.Data)),
		}
		copy(evCopy.Data, ev.Data)
		result = append(result, evCopy)
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
	// TWENTIETH_REFACTOR: Validate inputs to prevent panic
	if dve == nil {
		return ErrInvalidEvidence
	}
	if valSet == nil {
		return errors.New("nil validator set")
	}

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

	// NINTH_REFACTOR: Prune old committed evidence to prevent unbounded memory growth.
	// Previously, the committed map was never pruned and would grow forever.
	for key := range p.committed {
		// Parse height from key (format: type/height/time/hash)
		var evType int
		var height int64
		var evTime int64
		if _, err := fmt.Sscanf(key, "%d/%d/%d/", &evType, &height, &evTime); err == nil {
			if p.currentHeight-height > p.config.MaxAgeBlocks {
				delete(p.committed, key)
			}
		}
	}

	// Also prune old seen votes
	for key, vote := range p.seenVotes {
		if p.currentHeight-vote.Height > p.config.MaxAgeBlocks {
			delete(p.seenVotes, key)
		}
	}
}

// pruneOldestVotes removes the oldest n votes by height.
// H3: Called when seenVotes exceeds MaxSeenVotes.
// CR6: Protects votes within VoteProtectionWindow of current height.
// Caller must hold p.mu.
func (p *Pool) pruneOldestVotes(n int) {
	if n <= 0 || len(p.seenVotes) == 0 {
		return
	}

	// CR6: Calculate protection threshold
	protectedHeight := p.currentHeight - VoteProtectionWindow
	if protectedHeight < 0 {
		protectedHeight = 0
	}

	// Find minimum heights and group votes by height, excluding protected heights
	heightVotes := make(map[int64][]string)
	for key, vote := range p.seenVotes {
		// CR6: Don't prune votes in protection window
		if vote.Height >= protectedHeight {
			continue
		}
		heightVotes[vote.Height] = append(heightVotes[vote.Height], key)
	}

	if len(heightVotes) == 0 {
		// All votes are in protection window - can't prune
		log.Printf("[WARN] evidence: cannot prune votes - all %d votes in protection window", len(p.seenVotes))
		return
	}

	// Collect and sort heights
	heights := make([]int64, 0, len(heightVotes))
	for h := range heightVotes {
		heights = append(heights, h)
	}
	// CR2: Use stdlib sort instead of broken bubble sort
	sort.Slice(heights, func(i, j int) bool {
		return heights[i] < heights[j]
	})

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
	return p.currentTime.Sub(evTime) > p.config.MaxAge
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
