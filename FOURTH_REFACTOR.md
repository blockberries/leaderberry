# Fourth Refactor: Comprehensive Code Review Fixes

This document details all issues identified during the fourth comprehensive code review and their fixes.

## Philosophy

Consistent with previous refactors:
- **PANIC**: Consensus-critical failures that indicate bugs or corruption
- **ERROR**: External input validation, recoverable conditions

---

## Critical Severity (CR)

### CR1: Consensus Livelock in Commit Timeout Handler

**File:** `engine/state.go:944`

**Problem:**
```go
case RoundStepCommit:
    // Start new height
    cs.enterNewRoundLocked(cs.height, 0)  // BUG: Wrong height!
```

After commit timeout, the code enters a new round at the SAME height instead of advancing. The height should have been incremented in `finalizeCommitLocked`, but the timeout handler uses the current (already incremented) height incorrectly.

**Impact:** Complete consensus halt - infinite loop on same height.

**Fix:**
```go
case RoundStepCommit:
    // CR1: Commit timeout means we should move to next height.
    // Height was already incremented in finalizeCommitLocked,
    // so cs.height is already the NEW height. Just start round 0.
    cs.enterNewRoundLocked(cs.height, 0)
```

Actually, the real issue is understanding the flow. Let me trace it:
1. `enterCommitLocked()` is called when 2/3+ precommits received
2. `finalizeCommitLocked()` is called, which increments `cs.height` at line 783
3. Commit timeout fires for the OLD height
4. Handler sees `cs.height` which is now the NEW height
5. Calls `enterNewRoundLocked(cs.height, 0)` - this is CORRECT

Wait - let me re-examine. The issue is that the timeout was scheduled for the OLD height, but by the time it fires, we've already moved on. The timeout should be CANCELLED when we finalize commit.

**Revised Analysis:** The bug is that commit timeouts aren't properly cancelled. When `finalizeCommitLocked` completes, any pending commit timeout should be invalidated. The handler should check if we're still at the expected height.

**Correct Fix:**
```go
case RoundStepCommit:
    // CR1: Verify timeout is still relevant (height might have advanced)
    if ti.Height != cs.height {
        // Stale timeout - ignore
        return
    }
    // This shouldn't happen - commit timeout means block wasn't applied
    // Log and try to recover by starting next round
    log.Printf("[WARN] consensus: commit timeout at height %d - attempting recovery", cs.height)
    cs.enterNewRoundLocked(cs.height, 0)
```

**Error Handling:** Log warning - this indicates a problem but try to recover.

---

### CR2: Broken Bubble Sort in Evidence Pool Pruning

**File:** `evidence/pool.go:328-333`

**Problem:**
```go
// Sort ascending (oldest first)
for i := 0; i < len(heights)-1; i++ {
    for j := i + 1; j < len(heights); j++ {
        if heights[j] < heights[i] {
            heights[i], heights[j] = heights[j], heights[i]
        }
    }
}
```

This is an incorrect bubble sort implementation. The algorithm doesn't properly sort the array.

**Impact:** Wrong votes pruned, Byzantine evidence lost.

**Fix:**
```go
import "sort"

// CR2: Use stdlib sort instead of broken bubble sort
sort.Slice(heights, func(i, j int) bool {
    return heights[i] < heights[j]
})
```

**Error Handling:** N/A - Algorithm fix.

---

### CR3: Merkle Proof Validation is Incomplete

**File:** `types/block_parts.go:219-222`

**Problem:**
```go
// Verify proof (simplified: just check the merkle root matches)
if !HashEqual(part.Proof, ps.hash) {
    return ErrPartSetInvalidProof
}
```

The code stores the Merkle ROOT as the proof, not an actual Merkle path. This provides NO security - any part can claim any root.

**Impact:** Block part integrity not verified - Byzantine peers can send corrupted data.

**Fix:** Implement proper Merkle proof verification. Each part needs a proof path (list of sibling hashes) that allows computing the root from the part hash.

```go
// BlockPart represents a chunk of a serialized block with Merkle proof
type BlockPart struct {
    Index     uint16
    Bytes     []byte
    ProofPath []Hash  // CR3: Sibling hashes for Merkle proof
    ProofRoot Hash    // The claimed Merkle root
}

// verifyMerkleProof verifies that partHash is included in root via proofPath
func verifyMerkleProof(partHash Hash, index uint16, proofPath []Hash, root Hash) bool {
    current := partHash
    idx := index

    for _, sibling := range proofPath {
        var combined []byte
        if idx%2 == 0 {
            // Current is left child
            combined = append(current.Data, sibling.Data...)
        } else {
            // Current is right child
            combined = append(sibling.Data, current.Data...)
        }
        current = HashBytes(combined)
        idx = idx / 2
    }

    return HashEqual(current, root)
}

// In AddPart:
func (ps *PartSet) AddPart(part *BlockPart) error {
    // ... existing validation ...

    // CR3: Verify Merkle proof
    partHash := HashBytes(part.Bytes)
    if !verifyMerkleProof(partHash, part.Index, part.ProofPath, ps.hash) {
        return ErrPartSetInvalidProof
    }

    // ... rest of function ...
}
```

This also requires updating `NewPartSetFromData` to generate proper proof paths:

```go
func NewPartSetFromData(data []byte) (*PartSet, error) {
    // ... split data into parts ...

    // Compute leaf hashes
    leafHashes := make([]Hash, len(parts))
    for i, part := range parts {
        leafHashes[i] = HashBytes(part.Bytes)
    }

    // Build Merkle tree and extract proofs
    root, proofs := buildMerkleTreeWithProofs(leafHashes)

    // Attach proofs to parts
    for i, part := range parts {
        part.ProofPath = proofs[i]
        part.ProofRoot = root
    }

    // ... rest of function ...
}
```

**Error Handling:** ERROR - Invalid proof is external input validation.

---

### CR4: Unsafe Vote Pointer Storage in Evidence Pool

**File:** `evidence/pool.go:135`

**Problem:**
```go
p.seenVotes[key] = vote  // Stores pointer, not copy
```

The pool stores a direct pointer to the caller's vote object.

**Impact:** Vote integrity corruption if caller modifies vote after call.

**Fix:**
```go
// CR4: Copy vote to prevent caller from modifying stored data
voteCopy := &gen.Vote{
    Type:           vote.Type,
    Height:         vote.Height,
    Round:          vote.Round,
    Timestamp:      vote.Timestamp,
    Validator:      vote.Validator,
    ValidatorIndex: vote.ValidatorIndex,
    Signature:      vote.Signature,
}
// Deep copy BlockHash if present
if vote.BlockHash != nil {
    hashCopy := &types.Hash{Data: make([]byte, len(vote.BlockHash.Data))}
    copy(hashCopy.Data, vote.BlockHash.Data)
    voteCopy.BlockHash = hashCopy
}
p.seenVotes[key] = voteCopy
```

**Error Handling:** N/A - Defensive copy.

---

### CR5: Race Condition in WAL Segment Rotation

**File:** `wal/file_wal.go:299-316`

**Problem:**
```go
func (w *FileWAL) rotate() error {
    // Flush and sync current segment
    if err := w.flushAndSync(); err != nil {
        return err
    }

    // Close current file
    if err := w.file.Close(); err != nil {
        return err
    }

    // Increment segment index
    w.segmentIndex++
    w.group.MaxIndex = w.segmentIndex

    // Open new segment
    return w.openSegment(w.segmentIndex)  // If this fails, state is inconsistent!
}
```

If `openSegment` fails, `w.file` is closed but state is partially updated.

**Impact:** Subsequent writes fail or corrupt data.

**Fix:**
```go
func (w *FileWAL) rotate() error {
    // Flush and sync current segment
    if err := w.flushAndSync(); err != nil {
        return err
    }

    // CR5: Open new segment BEFORE closing old one
    newIndex := w.segmentIndex + 1
    newPath := w.segmentPath(newIndex)
    newFile, err := os.OpenFile(newPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, walFilePerm)
    if err != nil {
        // Failed to open new segment - keep using current one
        return fmt.Errorf("failed to open new WAL segment %d: %w", newIndex, err)
    }

    // New segment opened successfully - now safe to close old one
    if err := w.file.Close(); err != nil {
        // Close old file failed, but new file is open
        // Close new file and return error
        newFile.Close()
        return fmt.Errorf("failed to close old WAL segment: %w", err)
    }

    // Update state atomically
    w.file = newFile
    w.buf = bufio.NewWriterSize(newFile, defaultBufSize)
    w.enc = newEncoder(w.buf)
    w.segmentIndex = newIndex
    w.segmentSize = 0
    w.group.MaxIndex = newIndex

    return nil
}
```

**Error Handling:** ERROR - I/O failure is recoverable by continuing with current segment.

---

### CR6: Evidence Loss During Memory Pressure

**File:** `evidence/pool.go:130-132`

**Problem:**
```go
if len(p.seenVotes) >= MaxSeenVotes {
    p.pruneOldestVotes(MaxSeenVotes / 10)
}
```

Pruning by height can delete recent evidence from earlier heights that hasn't been committed yet.

**Impact:** Byzantine evidence lost before recording.

**Fix:** Add a protection window - never delete votes within N heights of current height:

```go
const (
    MaxSeenVotes = 100000
    // CR6: Protect votes within this many heights of current
    VoteProtectionWindow = 1000
)

func (p *Pool) pruneOldestVotes(n int) {
    if n <= 0 || len(p.seenVotes) == 0 {
        return
    }

    // CR6: Calculate protection threshold
    protectedHeight := p.currentHeight - VoteProtectionWindow
    if protectedHeight < 0 {
        protectedHeight = 0
    }

    // Group votes by height, excluding protected heights
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

    // Collect and sort heights (CR2: use stdlib sort)
    heights := make([]int64, 0, len(heightVotes))
    for h := range heightVotes {
        heights = append(heights, h)
    }
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
```

**Error Handling:** N/A - Algorithm improvement.

---

## High Severity (H)

### H1: GetValidatorSet Returns Mutable Reference

**File:** `engine/engine.go:186-196`

**Problem:**
```go
func (e *Engine) GetValidatorSet() *types.ValidatorSet {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.validatorSet  // Direct pointer
}
```

**Fix:** Return a copy:
```go
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
```

---

### H2: Nil Pointer Dereference Risk

**File:** `engine/state.go:405-409`

**Problem:**
```go
proposer := cs.validatorSet.Proposer
if cs.privVal != nil && types.PublicKeyEqual(proposer.PublicKey, cs.privVal.GetPubKey()) {
```

**Fix:**
```go
proposer := cs.validatorSet.Proposer
// H2: Check proposer is not nil before accessing fields
if proposer == nil {
    log.Printf("[WARN] consensus: no proposer set for height %d round %d", cs.height, cs.round)
    return
}
if cs.privVal != nil && types.PublicKeyEqual(proposer.PublicKey, cs.privVal.GetPubKey()) {
    cs.createAndSendProposalLocked()
}
```

---

### H3: VerifyCommit Missing Block Hash Verification

**File:** `types/vote.go:123-157`

**Problem:** Nil BlockHash handling is incomplete.

**Fix:**
```go
for _, sig := range commit.Signatures {
    // H3: Validate signature structure before processing
    if sig.Signature.Data == nil || len(sig.Signature.Data) == 0 {
        // Empty signature - skip but don't count
        continue
    }

    // Check for nil/absent votes (allowed but don't count toward power)
    if sig.BlockHash == nil || IsHashEmpty(sig.BlockHash) {
        continue
    }

    // Must be for this block
    if !HashEqual(*sig.BlockHash, blockHash) {
        continue
    }

    // H3: Now safe to construct vote with non-nil BlockHash
    vote := &Vote{
        Type:           VoteTypePrecommit,
        Height:         commit.Height,
        Round:          commit.Round,
        BlockHash:      sig.BlockHash,  // Guaranteed non-nil here
        Timestamp:      sig.Timestamp,
        ValidatorIndex: sig.ValidatorIndex,
        Signature:      sig.Signature,
    }
    // ... rest of verification
}
```

---

### H4: WAL Checkpoint Can Lose Data

**File:** `wal/file_wal.go:467-480`

**Problem:** Checkpoint stops at first unreadable segment.

**Fix:**
```go
func (w *FileWAL) Checkpoint(checkpointHeight int64) error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if !w.started {
        return ErrWALClosed
    }

    // H4: Track segments that can be deleted (only consecutive from MinIndex)
    segmentsToDelete := []int{}
    lastDeletableIdx := w.group.MinIndex - 1

    for idx := w.group.MinIndex; idx < w.group.MaxIndex; idx++ {
        canDelete, err := w.canDeleteSegment(idx, checkpointHeight)
        if err != nil {
            // H4: Log but continue checking - don't stop at first error
            log.Printf("[WARN] wal: cannot verify segment %d for checkpoint: %v", idx, err)
            // Can't delete this segment or any after it (must be consecutive)
            break
        }
        if canDelete {
            segmentsToDelete = append(segmentsToDelete, idx)
            lastDeletableIdx = idx
        } else {
            // Found segment we can't delete - stop here
            break
        }
    }

    // Delete segments and clean up index
    for _, idx := range segmentsToDelete {
        path := w.segmentPath(idx)
        if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
            return fmt.Errorf("failed to delete segment %d: %w", idx, err)
        }

        // Remove heights from index that pointed to this segment
        for h, segIdx := range w.heightIndex {
            if segIdx == idx {
                delete(w.heightIndex, h)
            }
        }
    }

    // Update MinIndex
    if len(segmentsToDelete) > 0 {
        w.group.MinIndex = lastDeletableIdx + 1
    }

    return nil
}
```

---

### H5: Callback Goroutine Leaks in BlockSyncer

**File:** `engine/blocksync.go:374-376`

**Problem:** Callbacks launched without synchronization with Stop().

**Fix:**
```go
type BlockSyncer struct {
    // ... existing fields ...

    // H5: WaitGroup to track callback goroutines
    callbackWg sync.WaitGroup
}

func (bs *BlockSyncer) ReceiveBlock(block *gen.Block, commit *gen.Commit) error {
    // ... existing code ...

    // Notify callback
    if bs.onBlockCommitted != nil {
        bs.callbackWg.Add(1)
        go func() {
            defer bs.callbackWg.Done()
            bs.onBlockCommitted(block, commit)
        }()
    }

    // ... rest of function ...
}

func (bs *BlockSyncer) Stop() {
    bs.mu.Lock()
    if bs.state == BlockSyncStateIdle {
        bs.mu.Unlock()
        return
    }
    bs.cancel()
    bs.state = BlockSyncStateIdle
    bs.mu.Unlock()

    // H5: Wait for callbacks to complete
    bs.callbackWg.Wait()
}
```

---

### H6: POL Validation Doesn't Check Block Consistency

**File:** `engine/state.go:556-619`

**Problem:** POL votes could be for a different block than the proposal.

**Fix:**
```go
func (cs *ConsensusState) validatePOL(proposal *gen.Proposal) error {
    // ... existing validation ...

    // H6: Verify POL votes are actually for this proposal's block
    proposalBlockHash := types.BlockHash(&proposal.Block)

    for _, vote := range proposal.PolVotes {
        // ... existing signature verification ...

        // H6: Verify vote is for the proposed block
        if vote.BlockHash == nil {
            return fmt.Errorf("POL vote from %s has nil block hash",
                types.AccountNameString(vote.Validator))
        }
        if !types.HashEqual(*vote.BlockHash, proposalBlockHash) {
            return fmt.Errorf("POL vote from %s is for different block: got %x, want %x",
                types.AccountNameString(vote.Validator),
                vote.BlockHash.Data[:8], proposalBlockHash.Data[:8])
        }
    }

    return nil
}
```

---

## Medium Severity (M)

### M1: VerifyAuthorization Cycle Detection Bug

**File:** `types/account.go:87-151`

**Problem:** Visited map shared across recursion branches.

**Fix:** Pass a copy of visited map for each branch:
```go
func VerifyAuthorization(
    auth *gen.Authorization,
    authority *gen.Authority,
    signBytes []byte,
    getAccount AccountGetter,
    visited map[string]bool,
) (uint32, error) {
    if visited == nil {
        visited = make(map[string]bool)
    }

    // ... existing code ...

    for _, accAuth := range auth.AccountAuthorizations {
        accName := AccountNameString(accAuth.Account)

        if visited[accName] {
            continue
        }

        // M1: Create copy of visited map for this branch
        branchVisited := make(map[string]bool, len(visited)+1)
        for k, v := range visited {
            branchVisited[k] = v
        }
        branchVisited[accName] = true

        // ... recursive call with branchVisited ...
        weight, err := VerifyAuthorization(
            accAuth.Authorization,
            &acc.Authority,
            signBytes,
            getAccount,
            branchVisited,  // M1: Use branch copy
        )
        // ... rest of function ...
    }
}
```

---

### M2: PartSetBitmap Deserialization No Bounds Check

**File:** `types/block_parts.go:443-469`

**Fix:**
```go
func PartSetBitmapFromBytes(data []byte) (*PartSetBitmap, error) {
    if len(data) < 2 {
        return nil, errors.New("bitmap data too short")
    }

    total := uint16(data[0]) | uint16(data[1])<<8

    // M2: Validate total against MaxBlockParts
    if total > MaxBlockParts {
        return nil, fmt.Errorf("bitmap total %d exceeds MaxBlockParts %d", total, MaxBlockParts)
    }

    // ... rest of function ...
}
```

---

### M3: Canonical Vote/Proposal Serialization Ambiguity

**File:** `types/vote.go:33-52`

**Fix:**
```go
func VoteSignBytes(chainID string, v *Vote) []byte {
    // M3: Explicitly construct canonical vote with zero signature
    canonical := &Vote{
        Type:           v.Type,
        Height:         v.Height,
        Round:          v.Round,
        BlockHash:      v.BlockHash,
        Timestamp:      v.Timestamp,
        Validator:      v.Validator,
        ValidatorIndex: v.ValidatorIndex,
        Signature:      Signature{Data: nil},  // M3: Explicit zero
    }

    data, err := canonical.MarshalCramberry()
    if err != nil {
        panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal vote for signing: %v", err))
    }

    // Prepend chain ID
    return append([]byte(chainID), data...)
}
```

---

### M4: O(n^2) Sorting in Evidence Pool

**File:** `evidence/pool.go:328-334`

Already addressed in CR2 - use `sort.Slice()`.

---

### M5: No Backpressure on Consensus Channels

**File:** `engine/state.go:280-301`

**Fix:** Add blocking send with timeout as alternative:
```go
const channelSendTimeout = 100 * time.Millisecond

func (cs *ConsensusState) AddProposal(proposal *gen.Proposal) {
    // M5: Try non-blocking first, then blocking with timeout
    select {
    case cs.proposalCh <- proposal:
        return
    default:
    }

    // Channel full - try blocking send with timeout
    select {
    case cs.proposalCh <- proposal:
        return
    case <-time.After(channelSendTimeout):
        dropped := atomic.AddUint64(&cs.droppedProposals, 1)
        log.Printf("[WARN] consensus: dropped proposal after timeout: height=%d round=%d total_dropped=%d",
            proposal.Height, proposal.Round, dropped)
    }
}
```

---

### M6: Silent Proposal Signing Failure

**File:** `engine/state.go:465-467`

**Fix:**
```go
if err := cs.privVal.SignProposal(cs.config.ChainID, proposal); err != nil {
    // M6: Log at ERROR level and increment counter
    atomic.AddUint64(&cs.signingFailures, 1)
    log.Printf("[ERROR] consensus: failed to sign proposal at height %d round %d: %v",
        cs.height, cs.round, err)
    // Schedule retry via timeout - don't silently fail
    return
}
```

---

### M7: GetAddress Truncates Public Key

**File:** `privval/file_pv.go:390-402`

**Fix:**
```go
import "crypto/sha256"

func (pv *FilePV) GetAddress() []byte {
    // M7: Derive address by hashing public key (standard practice)
    hash := sha256.Sum256(pv.pubKey.Data)
    // Return first 20 bytes of hash (similar to Ethereum/Tendermint)
    addr := make([]byte, 20)
    copy(addr, hash[:20])
    return addr
}
```

---

### M8: Unbounded Pending Evidence List

**File:** `evidence/pool.go`

**Fix:**
```go
const MaxPendingEvidence = 10000

func (p *Pool) AddEvidence(ev *gen.Evidence) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    // M8: Check pending evidence limit
    if len(p.pending) >= MaxPendingEvidence {
        return errors.New("pending evidence pool full")
    }

    // ... rest of existing validation ...

    p.pending = append(p.pending, ev)
    return nil
}
```

---

### M9: Merkle Tree Odd-Number Construction

**File:** `types/block_parts.go:307-336`

**Problem:** Hashing last element with itself is non-standard.

**Fix:** Use RFC 6962 compliant construction (pad with empty hash):
```go
func computeMerkleRoot(hashes [][]byte) Hash {
    if len(hashes) == 0 {
        return HashEmpty()
    }
    if len(hashes) == 1 {
        return Hash{Data: hashes[0]}
    }

    // M9: Pad to even number with empty hash (RFC 6962 style)
    if len(hashes)%2 == 1 {
        emptyHash := make([]byte, 32)
        hashes = append(hashes, emptyHash)
    }

    for len(hashes) > 1 {
        next := make([][]byte, 0, len(hashes)/2)
        for i := 0; i < len(hashes); i += 2 {
            combined := append(hashes[i], hashes[i+1]...)
            h := sha256.Sum256(combined)
            next = append(next, h[:])
        }
        hashes = next
    }

    return Hash{Data: hashes[0]}
}
```

---

### M10: WAL Height Index Becomes Stale

**File:** `wal/file_wal.go`

Already partially addressed in H4. Additional fix to rebuild index entries:

```go
func (w *FileWAL) Checkpoint(checkpointHeight int64) error {
    // ... existing deletion code ...

    // M10: Rebuild index for remaining segments if needed
    if len(segmentsToDelete) > 0 {
        // Clear all index entries for deleted heights
        for h := range w.heightIndex {
            if h <= checkpointHeight {
                delete(w.heightIndex, h)
            }
        }
    }

    return nil
}
```

---

## Low Severity (L)

### L1: ValidatorSet.Copy Bypasses NewValidatorSet

Already addressed in THIRD_REFACTOR M2. No additional changes needed.

---

### L2: TwoThirdsMajority Comments Misleading

**File:** `types/validator.go:177`

**Fix:** Update comment:
```go
// TwoThirdsMajority returns the voting power needed for 2/3+ majority.
// The calculation avoids multiplying TotalPower by 2 (which could overflow
// if TotalPower > MaxInt64/2) by computing 2/3 as (1/3 + 1/3 + adjustment).
// Note: third + third can still overflow if third > MaxInt64/2, but this
// is prevented by MaxTotalVotingPower limit.
func (vs *ValidatorSet) TwoThirdsMajority() int64 {
```

---

### L3: No Replay Protection for Proposals

**File:** `engine/state.go:491-552`

**Fix:** Add timestamp validation similar to votes:
```go
func (cs *ConsensusState) handleProposalLocked(proposal *gen.Proposal) {
    // ... existing validation ...

    // L3: Validate proposal timestamp (similar to vote validation)
    proposalTime := time.Unix(0, proposal.Timestamp)
    now := time.Now()
    if proposalTime.After(now.Add(MaxTimestampDrift)) {
        log.Printf("[DEBUG] consensus: rejecting proposal with future timestamp")
        return
    }
    if proposalTime.Before(now.Add(-MaxTimestampDrift)) {
        log.Printf("[DEBUG] consensus: rejecting proposal with old timestamp")
        return
    }

    // ... rest of function ...
}
```

---

### L4: Equivocating Validators Still Count Toward Quorum

**File:** `engine/state.go:831-846`

This is actually intentional behavior in Tendermint - we track the vote even from equivocators because we need to reach consensus. The evidence is used for slashing, not vote exclusion. Add comment:

```go
if dve != nil {
    log.Printf("[WARN] consensus: detected equivocation from %s at height %d",
        types.AccountNameString(vote.Validator), vote.Height)
    if err := cs.evidencePool.AddDuplicateVoteEvidence(dve); err != nil {
        log.Printf("[DEBUG] consensus: failed to add evidence: %v", err)
    }
    // L4: Continue processing - equivocating votes still count toward quorum.
    // The equivocator will be slashed via the evidence pool, but we can't
    // exclude their vote from consensus without breaking liveness (they may
    // be needed for 2/3+ threshold).
}
```

---

### L5: Orphaned Temp Files in PrivVal

**File:** `privval/file_pv.go:206-224`

**Fix:**
```go
if err := tmpFile.Sync(); err != nil {
    tmpFile.Close()
    os.Remove(tmpPath)  // L5: Clean up temp file
    return fmt.Errorf("failed to sync temp key file: %w", err)
}
tmpFile.Close()

if err := os.Rename(tmpPath, pv.keyFilePath); err != nil {
    os.Remove(tmpPath)  // L5: Clean up temp file
    return fmt.Errorf("failed to rename key file: %w", err)
}
```

---

### L6: WAL Search Doesn't Sync Before Read

**File:** `wal/file_wal.go:379-381`

**Fix:**
```go
func (w *FileWAL) SearchForEndHeight(height int64) (Reader, bool, error) {
    w.mu.Lock()
    defer w.mu.Unlock()

    if !w.started {
        return nil, false, ErrWALClosed
    }

    // L6: Sync to ensure all data is on disk before searching
    if err := w.flushAndSync(); err != nil {
        return nil, false, err
    }

    // ... rest of function ...
}
```

---

### L7: Evidence Pool Never Persists State

**File:** `evidence/pool.go`

Add optional persistence:
```go
type Pool struct {
    // ... existing fields ...

    // L7: Optional persistence path
    persistPath string
}

func NewPoolWithPersistence(config Config, persistPath string) *Pool {
    p := NewPool(config)
    p.persistPath = persistPath
    p.loadState()  // Load on startup
    return p
}

func (p *Pool) loadState() {
    if p.persistPath == "" {
        return
    }
    // Load committed evidence keys from file
    // ... implementation ...
}

func (p *Pool) MarkCommitted(evidence []gen.Evidence) {
    p.mu.Lock()
    defer p.mu.Unlock()

    // ... existing code ...

    // L7: Persist committed evidence if path configured
    if p.persistPath != "" {
        p.saveState()
    }
}
```

---

### L8: Inconsistent Error Documentation

Review and document all error variables. Example:
```go
// Errors for consensus state machine
var (
    ErrInvalidHeight    = errors.New("invalid height")           // Vote/proposal for wrong height
    ErrInvalidRound     = errors.New("invalid round")            // Vote/proposal for wrong round
    ErrInvalidVote      = errors.New("invalid vote")             // Malformed or invalid vote
    ErrInvalidProposal  = errors.New("invalid proposal")         // Malformed or invalid proposal
    ErrInvalidSignature = errors.New("invalid signature")        // Signature verification failed
    ErrConflictingVote  = errors.New("conflicting vote")         // Equivocation detected
    ErrUnknownValidator = errors.New("unknown validator")        // Validator not in set
    ErrNotStarted       = errors.New("consensus not started")    // Start() not called
    ErrAlreadyStarted   = errors.New("consensus already started") // Start() called twice
)
```

---

## Implementation Checklist

### Phase 1: Critical Fixes (Immediate)
- [ ] CR1: Fix commit timeout handler
- [ ] CR2: Replace bubble sort with sort.Slice
- [ ] CR3: Implement proper Merkle proof verification
- [ ] CR4: Copy vote before storing in evidence pool
- [ ] CR5: Fix WAL rotation race condition
- [ ] CR6: Add vote protection window in pruning

### Phase 2: High Severity Fixes
- [ ] H1: Return copy from GetValidatorSet
- [ ] H2: Add nil check for proposer
- [ ] H3: Fix VerifyCommit nil handling
- [ ] H4: Fix Checkpoint to handle errors properly
- [ ] H5: Add WaitGroup for callbacks
- [ ] H6: Validate POL votes match proposal block

### Phase 3: Medium Severity Fixes
- [ ] M1: Fix cycle detection in VerifyAuthorization
- [ ] M2: Add bounds check to bitmap deserialization
- [ ] M3: Explicit zero signature in sign bytes
- [ ] M5: Add timeout to channel sends
- [ ] M6: Better handling of signing failures
- [ ] M7: Hash public key for address derivation
- [ ] M8: Limit pending evidence size
- [ ] M9: Fix Merkle tree for odd counts
- [ ] M10: Clear stale index entries

### Phase 4: Low Severity Fixes
- [ ] L2: Update TwoThirdsMajority comments
- [ ] L3: Add timestamp validation for proposals
- [ ] L4: Document equivocation handling
- [ ] L5: Clean up temp files on error
- [ ] L6: Sync before WAL search
- [ ] L7: Optional evidence persistence
- [ ] L8: Document error variables

---

## Testing Requirements

### Critical Path Tests
1. Commit timeout recovery test
2. Evidence pruning under memory pressure
3. Block part verification with corrupted data
4. WAL rotation during concurrent writes
5. Byzantine equivocation detection

### Regression Tests
1. Ensure existing tests still pass
2. Race detection with `go test -race`
3. Fuzz testing for serialization
4. Load testing for memory limits

---

## Notes

This refactor addresses 30 issues across 4 severity levels. The critical fixes (CR1-CR6) should be implemented immediately as they can cause consensus failures or security vulnerabilities. The remaining fixes can be prioritized based on deployment timeline.

Key architectural observations:
1. The Merkle proof system needs redesign (CR3)
2. Evidence pool needs persistence for crash recovery (L7)
3. Channel-based message passing needs backpressure (M5)
4. Several nil pointer risks exist throughout the codebase
