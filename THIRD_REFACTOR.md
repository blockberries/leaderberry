# Third Refactor: Comprehensive Code Review Findings

This document captures all issues identified during the comprehensive line-by-line code review of the Leaderberry consensus engine. Issues are categorized by severity and include implementation guidance.

## Error Handling Philosophy

**PANIC** - Use for any condition that indicates:
- Consensus invariant violation
- Internal state corruption
- Conditions that could break safety or liveness
- Programming errors that should never occur in correct code
- Any failure after consensus has made a decision

**ERROR** - Use ONLY for:
- Invalid external input (network messages, user input)
- Resource unavailability that can be retried
- Conditions where the caller can take corrective action
- Input validation that rejects bad data without affecting consensus

---

## Critical Severity (CR)

### CR1: applyValidatorUpdates Uses Deprecated Mutable Method

**File:** `engine/state.go:224`

**Problem:**
```go
newSet, err := types.NewValidatorSet(newVals)
if err != nil {
    return nil, fmt.Errorf("failed to create new validator set: %w", err)
}
// Increment proposer priority for the new round
newSet.IncrementProposerPriority(1)  // DEPRECATED mutable method
```

The `applyValidatorUpdates` method uses the deprecated `IncrementProposerPriority` instead of the immutable `WithIncrementedPriority` pattern established in the second refactor (CR4).

**Impact:** Inconsistent API usage; potential for future bugs if code is copied or the deprecated method is removed.

**Fix:**
```go
// Create new validator set (this reindexes and validates)
newSet, err := types.NewValidatorSet(newVals)
if err != nil {
    return nil, fmt.Errorf("failed to create new validator set: %w", err)
}

// Increment proposer priority for the new round using immutable pattern
newSet, err = newSet.WithIncrementedPriority(1)
if err != nil {
    // This should never fail for a valid set we just created
    panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to increment priority on new validator set: %v", err))
}
```

**Error Handling:** PANIC - If we just created a valid validator set, incrementing priority should never fail. Failure indicates internal corruption.

---

### CR2: Potential Lock Ordering Issue in PeerSet.UpdateValidatorSet

**File:** `engine/peer_state.go:431-438`

**Problem:**
```go
func (ps *PeerSet) UpdateValidatorSet(valSet *types.ValidatorSet) {
    ps.mu.Lock()
    defer ps.mu.Unlock()

    ps.valSet = valSet
    for _, p := range ps.peers {
        p.UpdateValidatorSet(valSet)  // Acquires p.mu inside
    }
}
```

This acquires `PeerSet.mu` then `PeerState.mu` for each peer. If any code path acquires these locks in reverse order, deadlock occurs.

**Impact:** Potential deadlock under concurrent peer operations.

**Fix:** Document and enforce lock ordering. Add lock order documentation:

```go
// Lock ordering (must always acquire in this order to prevent deadlock):
// 1. PeerSet.mu
// 2. PeerState.mu
// 3. VoteBitmap.mu
//
// UpdateValidatorSet updates the validator set for all peers.
// Caller must NOT hold any PeerState locks.
func (ps *PeerSet) UpdateValidatorSet(valSet *types.ValidatorSet) {
    ps.mu.Lock()
    defer ps.mu.Unlock()

    ps.valSet = valSet
    for _, p := range ps.peers {
        p.UpdateValidatorSet(valSet)
    }
}
```

Also audit all call sites to ensure no reverse lock ordering exists.

**Error Handling:** N/A - This is a design/documentation fix.

---

### CR3: WAL buildIndex Silently Ignores Corruption

**File:** `wal/file_wal.go:120-149`

**Problem:**
```go
for {
    msg, err := dec.Decode()
    if err == io.EOF {
        break
    }
    if err != nil {
        break  // Silently ignores corruption!
    }
    if msg.Type == MsgTypeEndHeight {
        w.heightIndex[msg.Height] = idx
    }
}
```

When WAL decoding fails with an error other than EOF, the loop breaks silently and returns `nil`. Corrupted segments are skipped without any logging or error reporting.

**Impact:** WAL corruption goes undetected, potentially causing incorrect height index and consensus replay issues.

**Fix:**
```go
for {
    msg, err := dec.Decode()
    if err == io.EOF {
        break
    }
    if err != nil {
        // Log corruption but continue - partial index is better than none
        // The WAL is append-only, so corruption at the end is recoverable
        log.Printf("[WARN] wal: corruption detected in segment %d during index build: %v", idx, err)
        break
    }
    if msg.Type == MsgTypeEndHeight {
        w.heightIndex[msg.Height] = idx
    }
}
```

**Error Handling:** LOG WARNING - WAL corruption during index build is not fatal (we can still replay what we have), but operators need to know.

---

## High Severity (H)

### H1: Safe Constructors Don't Copy Input Data

**Files:** `types/hash.go:29-33`, `types/hash.go:82-86`, `types/hash.go:101-105`

**Problem:**
```go
func NewHash(data []byte) (Hash, error) {
    if len(data) != HashSize {
        return Hash{}, fmt.Errorf("hash must be %d bytes, got %d", HashSize, len(data))
    }
    return Hash{Data: data}, nil  // Shares input slice!
}
```

The input slice is stored directly without copying. If the caller modifies their slice after creating the hash, the hash's internal data changes.

**Impact:** Hash/Signature/PublicKey corruption if callers modify input buffers. Critical for network input where buffers may be reused.

**Fix:**
```go
func NewHash(data []byte) (Hash, error) {
    if len(data) != HashSize {
        return Hash{}, fmt.Errorf("hash must be %d bytes, got %d", HashSize, len(data))
    }
    // Copy to prevent caller from modifying our internal data
    copied := make([]byte, HashSize)
    copy(copied, data)
    return Hash{Data: copied}, nil
}

func NewSignature(data []byte) (Signature, error) {
    if len(data) != SignatureSize {
        return Signature{}, fmt.Errorf("signature must be %d bytes, got %d", SignatureSize, len(data))
    }
    copied := make([]byte, SignatureSize)
    copy(copied, data)
    return Signature{Data: copied}, nil
}

func NewPublicKey(data []byte) (PublicKey, error) {
    if len(data) != PublicKeySize {
        return PublicKey{}, fmt.Errorf("public key must be %d bytes, got %d", PublicKeySize, len(data))
    }
    copied := make([]byte, PublicKeySize)
    copy(copied, data)
    return PublicKey{Data: copied}, nil
}
```

**Error Handling:** ERROR - Invalid input length is caller error, not consensus failure.

---

### H2: VoteBitmap Stores Shared ValidatorSet Reference

**File:** `engine/peer_state.go:34-42`

**Problem:**
```go
func NewVoteBitmap(valSet *types.ValidatorSet) *VoteBitmap {
    numVals := valSet.Size()
    numWords := (numVals + 63) / 64
    return &VoteBitmap{
        bits:   make([]uint64, numWords),
        valSet: valSet,  // Shared reference - can change!
    }
}
```

`VoteBitmap` stores a reference to the validator set. When `UpdateValidatorSet` is called, old bitmaps may reference the new validator set, causing `Size()` mismatches.

**Impact:** Incorrect vote tracking if validator set size changes; potential out-of-bounds access.

**Fix:** Store only the size, not the reference:

```go
type VoteBitmap struct {
    mu      sync.RWMutex
    bits    []uint64
    count   int
    numVals int  // Store size, not reference
}

func NewVoteBitmap(valSet *types.ValidatorSet) *VoteBitmap {
    numVals := valSet.Size()
    numWords := (numVals + 63) / 64
    return &VoteBitmap{
        bits:    make([]uint64, numWords),
        numVals: numVals,
    }
}

func (vb *VoteBitmap) Set(index uint16) {
    vb.mu.Lock()
    defer vb.mu.Unlock()

    if int(index) >= vb.numVals {
        return
    }
    // ... rest unchanged
}
```

**Error Handling:** N/A - Design fix, no runtime errors.

---

### H3: Evidence Pool seenVotes Map Unbounded Growth

**File:** `evidence/pool.go:121-123`

**Problem:**
```go
// Store this vote for future comparison
p.seenVotes[key] = vote
return nil, nil
```

While `pruneExpired` cleans up based on `MaxAgeBlocks`, the map can grow very large with many validators and multiple rounds per height. Key format is `validator/height/round/type`.

**Impact:** Memory exhaustion over time, especially with many validators or frequent round changes.

**Fix:** Add maximum size limit and more aggressive pruning:

```go
const (
    // MaxSeenVotes limits memory usage for equivocation detection
    MaxSeenVotes = 100000
)

func (p *Pool) CheckVote(vote *gen.Vote, valSet *types.ValidatorSet) (*gen.DuplicateVoteEvidence, error) {
    p.mu.Lock()
    defer p.mu.Unlock()

    key := voteKey(vote)

    if existing, ok := p.seenVotes[key]; ok {
        if !votesForSameBlock(existing, vote) {
            // Found equivocation
            ev := &gen.DuplicateVoteEvidence{
                VoteA:            *existing,
                VoteB:            *vote,
                TotalVotingPower: valSet.TotalPower,
                Timestamp:        time.Now().UnixNano(),
            }
            if val := valSet.GetByName(types.AccountNameString(vote.Validator)); val != nil {
                ev.ValidatorPower = val.VotingPower
            }
            return ev, nil
        }
        return nil, nil
    }

    // Enforce size limit - prune oldest entries if needed
    if len(p.seenVotes) >= MaxSeenVotes {
        p.pruneOldestVotes(MaxSeenVotes / 10)  // Remove 10%
    }

    p.seenVotes[key] = vote
    return nil, nil
}

// pruneOldestVotes removes the oldest n votes by height
func (p *Pool) pruneOldestVotes(n int) {
    // Find minimum height to keep
    heights := make([]int64, 0, len(p.seenVotes))
    heightVotes := make(map[int64][]string)

    for key, vote := range p.seenVotes {
        heights = append(heights, vote.Height)
        heightVotes[vote.Height] = append(heightVotes[vote.Height], key)
    }

    sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })

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
```

**Error Handling:** N/A - Internal memory management, no errors to callers.

---

### H4: WAL Checkpoint Can Create Segment Gaps

**File:** `wal/file_wal.go:462-496`

**Problem:**
```go
for idx := w.group.MinIndex; idx < w.group.MaxIndex; idx++ {
    canDelete, err := w.canDeleteSegment(idx, checkpointHeight)
    if err != nil {
        continue  // Skip on error, but don't stop
    }
    if canDelete {
        segmentsToDelete = append(segmentsToDelete, idx)
    } else {
        break
    }
}
```

If segment 0 has an error (corrupted), it's skipped. If segment 1 is deletable, it gets deleted. This creates a gap: segment 0 exists (corrupted) but segment 1 is gone.

**Impact:** WAL replay could fail due to missing segments; data loss.

**Fix:**
```go
for idx := w.group.MinIndex; idx < w.group.MaxIndex; idx++ {
    canDelete, err := w.canDeleteSegment(idx, checkpointHeight)
    if err != nil {
        // Cannot determine if segment is safe to delete - stop here
        // Don't delete later segments without deleting earlier ones
        log.Printf("[WARN] wal: cannot check segment %d for checkpoint: %v", idx, err)
        break
    }
    if canDelete {
        segmentsToDelete = append(segmentsToDelete, idx)
    } else {
        break
    }
}
```

**Error Handling:** LOG WARNING and stop - Corrupted segments should block further deletion to prevent gaps.

---

### H5: GetVotes Returns Non-Deterministic Order

**File:** `engine/vote_tracker.go:168-177`

**Problem:**
```go
func (vs *VoteSet) GetVotes() []*gen.Vote {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    votes := make([]*gen.Vote, 0, len(vs.votes))
    for _, v := range vs.votes {  // Map iteration is non-deterministic!
        votes = append(votes, v)
    }
    return votes
}
```

Map iteration order is not deterministic in Go. This affects POL vote ordering in proposals, making proposals non-deterministic across nodes.

**Impact:** Non-deterministic proposal construction; makes debugging harder; could theoretically affect signature bytes if POL is included in sign bytes (it is).

**Fix:**
```go
func (vs *VoteSet) GetVotes() []*gen.Vote {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    votes := make([]*gen.Vote, 0, len(vs.votes))
    for _, v := range vs.votes {
        votes = append(votes, v)
    }

    // Sort by validator index for deterministic ordering
    sort.Slice(votes, func(i, j int) bool {
        return votes[i].ValidatorIndex < votes[j].ValidatorIndex
    })

    return votes
}
```

Also fix `GetVotesForBlock`:
```go
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

    // Sort by validator index for deterministic ordering
    sort.Slice(votes, func(i, j int) bool {
        return votes[i].ValidatorIndex < votes[j].ValidatorIndex
    })

    return votes
}
```

**Error Handling:** N/A - Determinism fix.

---

## Medium Severity (M)

### M1: Vote Timestamp Not Validated

**File:** `engine/vote_tracker.go:56-111`

**Problem:** The `AddVote` function validates height, round, type, validator, and signature, but does NOT validate that the timestamp is reasonable.

**Impact:** Malicious validators could submit votes with far-future timestamps, potentially affecting evidence expiration or causing issues if timestamps are used for ordering.

**Fix:** Add timestamp bounds checking:
```go
const (
    // MaxTimestampDrift is the maximum allowed clock drift for vote timestamps
    MaxTimestampDrift = 10 * time.Minute
)

func (vs *VoteSet) AddVote(vote *gen.Vote) (bool, error) {
    vs.mu.Lock()
    defer vs.mu.Unlock()

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

    // ... rest of validation
}
```

**Error Handling:** ERROR - Invalid timestamp is external input validation, not consensus failure.

---

### M2: ValidatorSet.Copy May Reinitialize Priorities

**File:** `types/validator.go:251-273`

**Problem:**
```go
func (vs *ValidatorSet) Copy() (*ValidatorSet, error) {
    validators := make([]*NamedValidator, len(vs.Validators))
    for i, v := range vs.Validators {
        // ... copy validators ...
    }
    return NewValidatorSet(validators)  // This may reinitialize priorities!
}
```

`NewValidatorSet` calls `initProposerPriorities()` if all priorities are zero. If copying a set where all priorities happen to be zero (e.g., right after initialization), the copy would have different priorities.

**Impact:** Copied validator set could have different proposer selection than original.

**Fix:** Preserve priorities explicitly:
```go
func (vs *ValidatorSet) Copy() (*ValidatorSet, error) {
    validators := make([]*NamedValidator, len(vs.Validators))
    for i, v := range vs.Validators {
        nameCopy := CopyAccountName(v.Name)
        var pubKeyCopy PublicKey
        if len(v.PublicKey.Data) > 0 {
            pubKeyCopy.Data = make([]byte, len(v.PublicKey.Data))
            copy(pubKeyCopy.Data, v.PublicKey.Data)
        }

        validators[i] = &NamedValidator{
            Name:             nameCopy,
            Index:            v.Index,
            PublicKey:        pubKeyCopy,
            VotingPower:      v.VotingPower,
            ProposerPriority: v.ProposerPriority,
        }
    }

    // Build the set manually to preserve priorities exactly
    newVS := &ValidatorSet{
        Validators: validators,
        TotalPower: vs.TotalPower,
        byName:     make(map[string]*NamedValidator),
        byIndex:    make(map[uint16]*NamedValidator),
    }

    for _, v := range validators {
        name := AccountNameString(v.Name)
        newVS.byName[name] = v
        newVS.byIndex[v.Index] = v
    }

    // Copy proposer reference
    if vs.Proposer != nil {
        newVS.Proposer = newVS.byIndex[vs.Proposer.Index]
    }

    return newVS, nil
}
```

**Error Handling:** This method should not return error since it's copying valid data. Consider changing signature to just return `*ValidatorSet` and panic on internal errors:
```go
func (vs *ValidatorSet) Copy() *ValidatorSet {
    // ... implementation ...
    // Any failure here indicates programming error
}
```

---

### M3: MakeCommit Includes All Votes, Not Just Block Votes

**File:** `engine/vote_tracker.go:233-263`

**Problem:**
```go
func (vs *VoteSet) MakeCommit() *gen.Commit {
    // ...
    sigs := make([]gen.CommitSig, 0, len(vs.votes))
    for _, vote := range vs.votes {  // Includes ALL votes
        sig := gen.CommitSig{
            ValidatorIndex: vote.ValidatorIndex,
            Signature:      vote.Signature,
            Timestamp:      vote.Timestamp,
            BlockHash:      vote.BlockHash,  // Could be nil or different block!
        }
        sigs = append(sigs, sig)
    }
    // ...
}
```

The commit includes votes for nil and votes for other blocks, not just votes for the 2/3+ majority block.

**Impact:** Commit certificates are larger than necessary and include non-contributing votes.

**Fix:**
```go
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

    // Only include votes for the committed block
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

        sig := gen.CommitSig{
            ValidatorIndex: vote.ValidatorIndex,
            Signature:      vote.Signature,
            Timestamp:      vote.Timestamp,
            BlockHash:      vote.BlockHash,
        }
        sigs = append(sigs, sig)
    }

    // Sort for deterministic ordering
    sort.Slice(sigs, func(i, j int) bool {
        return sigs[i].ValidatorIndex < sigs[j].ValidatorIndex
    })

    return &gen.Commit{
        Height:     vs.height,
        Round:      vs.round,
        BlockHash:  *blockHash,
        Signatures: sigs,
    }
}
```

**Error Handling:** Returns nil for invalid conditions (existing behavior).

---

### M4: Timeout Delta Integer Overflow

**File:** `engine/timeout.go:188`

**Problem:**
```go
func (tt *TimeoutTicker) calculateDuration(ti TimeoutInfo) time.Duration {
    switch ti.Step {
    case RoundStepPropose:
        return tt.config.Propose + time.Duration(ti.Round)*tt.config.ProposeDelta
```

If `ti.Round` is very large (max int32 = 2147483647) and delta is 500ms, the multiplication overflows.

**Impact:** Extremely unlikely (would require 2B+ rounds), but could cause unexpected timeout behavior.

**Fix:** Add overflow protection:
```go
const (
    // MaxRoundForTimeout prevents overflow in timeout calculations
    MaxRoundForTimeout = 10000
)

func (tt *TimeoutTicker) calculateDuration(ti TimeoutInfo) time.Duration {
    round := ti.Round
    if round > MaxRoundForTimeout {
        round = MaxRoundForTimeout
    }

    switch ti.Step {
    case RoundStepPropose:
        return tt.config.Propose + time.Duration(round)*tt.config.ProposeDelta
    // ... etc
    }
}
```

**Error Handling:** N/A - Clamp value to prevent overflow.

---

### M5: BlockSyncer Doesn't Validate Block Height Matches Request

**File:** `engine/blocksync.go:330-378`

**Problem:**
```go
func (bs *BlockSyncer) ReceiveBlock(block *gen.Block, commit *gen.Commit) error {
    bs.mu.Lock()
    defer bs.mu.Unlock()

    height := block.Header.Height

    // Check if we requested this block
    if _, exists := bs.pendingRequests[height]; !exists {
        // Unsolicited block - could be from gossip
        if height != bs.currentHeight+1 {
            return nil // Ignore blocks we don't need right now
        }
    }
    // ... processes block without checking commit.Height matches block.Header.Height
```

The code doesn't verify that `commit.Height` matches `block.Header.Height`.

**Fix:**
```go
func (bs *BlockSyncer) ReceiveBlock(block *gen.Block, commit *gen.Commit) error {
    bs.mu.Lock()
    defer bs.mu.Unlock()

    if block == nil || commit == nil {
        return errors.New("nil block or commit")
    }

    height := block.Header.Height

    // Verify commit matches block
    if commit.Height != height {
        return fmt.Errorf("commit height %d doesn't match block height %d", commit.Height, height)
    }

    // ... rest unchanged
}
```

**Error Handling:** ERROR - Invalid external input.

---

## Low Severity (L)

### L1: Inconsistent Error Message Formatting

**Various Files**

**Problem:** Error messages use inconsistent patterns:
- `fmt.Errorf("failed to X: %w", err)`
- `fmt.Errorf("%w: details", ErrSentinel)`
- `errors.New("description")`

**Fix:** Standardize on the pattern: `fmt.Errorf("context: %w", err)` for wrapped errors, `ErrSentinel` for return values that match sentinel errors.

---

### L2: Magic Number for Pool Buffer Size

**File:** `wal/file_wal.go:25-26`

**Problem:**
```go
defaultPoolBufSize = 4096
```

The 4KB default may cause frequent reallocations for larger WAL messages.

**Fix:** Increase default or make it configurable based on typical message sizes:
```go
// defaultPoolBufSize is the initial buffer size for WAL decoder.
// Set to 64KB to match typical proposal/block sizes and reduce reallocations.
defaultPoolBufSize = 65536
```

---

### L3: No Rate Limiting on Vote/Proposal Channels

**File:** `engine/state.go:276-297`

**Problem:** While dropped message counters exist, there's no rate limiting. A malicious peer could flood channels with invalid messages.

**Fix:** Add per-peer rate limiting at the network layer (outside scope of consensus engine), or add a simple token bucket in AddVote/AddProposal:

```go
type ConsensusState struct {
    // ... existing fields ...

    // Rate limiting
    voteRateLimit   *rate.Limiter
    proposalRateLimit *rate.Limiter
}

func NewConsensusState(...) *ConsensusState {
    return &ConsensusState{
        // ... existing init ...
        voteRateLimit:     rate.NewLimiter(rate.Limit(1000), 100),  // 1000/sec, burst 100
        proposalRateLimit: rate.NewLimiter(rate.Limit(10), 5),     // 10/sec, burst 5
    }
}

func (cs *ConsensusState) AddVote(vote *gen.Vote) {
    if !cs.voteRateLimit.Allow() {
        atomic.AddUint64(&cs.droppedVotes, 1)
        return
    }
    // ... existing code
}
```

---

### L4: PeerState.ApplyNewRoundStep Silently Ignores Regressions

**File:** `engine/peer_state.go:202-247`

**Problem:**
```go
if height < ps.prs.Height {
    return // Peer regressed - ignore
}
```

Peer regressions are silently ignored without logging. This could indicate network issues or malicious peers.

**Fix:** Add debug logging:
```go
if height < ps.prs.Height {
    log.Printf("[DEBUG] peer %s: ignoring height regression %d -> %d", ps.peerID, ps.prs.Height, height)
    return
}
```

---

### L5: PartSet.assembleData Should Verify Total Size

**File:** `types/block_parts.go:239-265`

**Problem:** The assembled data size is calculated by summing part sizes, but there's no check that this matches any expected size.

**Fix:** If a size hint is available (e.g., from header), verify it:
```go
func (ps *PartSet) assembleData() error {
    totalSize := 0
    for _, part := range ps.parts {
        totalSize += len(part.Bytes)
    }

    ps.data = make([]byte, 0, totalSize)
    for _, part := range ps.parts {
        ps.data = append(ps.data, part.Bytes...)
    }

    // Verify hash
    partHashes := make([][]byte, len(ps.parts))
    for i, part := range ps.parts {
        h := sha256.Sum256(part.Bytes)
        partHashes[i] = h[:]
    }
    computedHash := computeMerkleRoot(partHashes)

    if !HashEqual(computedHash, ps.hash) {
        ps.data = nil
        return fmt.Errorf("assembled data hash mismatch")
    }

    return nil
}
```

---

## Implementation Checklist

### Phase 1: Critical Fixes
- [ ] CR1: Update applyValidatorUpdates to use WithIncrementedPriority
- [ ] CR2: Document and enforce lock ordering in peer_state.go
- [ ] CR3: Add logging for WAL corruption in buildIndex

### Phase 2: High Severity Fixes
- [ ] H1: Copy input data in NewHash, NewSignature, NewPublicKey
- [ ] H2: Store validator count instead of reference in VoteBitmap
- [ ] H3: Add size limit to evidence pool seenVotes map
- [ ] H4: Fix WAL Checkpoint to stop on segment errors
- [ ] H5: Sort votes in GetVotes for deterministic ordering

### Phase 3: Medium Severity Fixes
- [ ] M1: Add vote timestamp validation
- [ ] M2: Fix ValidatorSet.Copy to preserve priorities exactly
- [ ] M3: Filter MakeCommit to only include votes for committed block
- [ ] M4: Add overflow protection to timeout calculations
- [ ] M5: Validate commit height matches block height in BlockSyncer

### Phase 4: Low Severity Fixes
- [ ] L1: Standardize error message formatting
- [ ] L2: Increase WAL decoder pool buffer size
- [ ] L3: Consider rate limiting for vote/proposal channels
- [ ] L4: Add debug logging for peer state regressions
- [ ] L5: Return error from assembleData on hash mismatch

---

## Testing Requirements

Each fix must include:
1. Unit tests covering the specific fix
2. Negative tests for error conditions
3. Race detection tests (`go test -race`)
4. Integration tests where applicable

---

## Documentation Updates

After implementation:
1. Update CHANGELOG.md with version 0.8.0 entry
2. Update ARCHITECTURE.md if design changes
3. Add comments explaining panic vs error decisions
4. Update CLAUDE.md if new patterns are established
