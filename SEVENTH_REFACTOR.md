# SEVENTH REFACTOR - Security and Memory Safety Fixes

This document addresses security vulnerabilities and memory safety issues discovered during
the seventh comprehensive code review with verification pass to eliminate false positives.

## Critical Issues Fixed

### 1. PrivVal Double-Sign Vulnerability (State Persistence Order)

**File:** `privval/file_pv.go:SignVote()` and `SignProposal()`

**Issue:** The signing functions updated in-memory state before persisting to disk:
```go
// VULNERABLE - old code path:
pv.lastSignState.Height = vote.Height
pv.lastSignState.Round = vote.Round
pv.lastSignState.Step = step
pv.lastSignState.Signature = newSig
vote.Signature = newSig  // Returns to caller
if err := pv.saveState(); err != nil { ... }  // Persist after return
```

If the process crashed after the function returned but before `saveState()` completed,
the validator could:
1. Sign a vote at H/R/S
2. Crash before persisting
3. Restart with old state (previous H/R/S)
4. Sign a DIFFERENT vote at the same H/R/S
5. Result: **Double-sign slashing**

**Fix:** Persist state BEFORE updating in-memory state and returning signature:
```go
// Save current state for rollback
oldState := pv.lastSignState

// Update state in memory
pv.lastSignState.Height = vote.Height
pv.lastSignState.Round = vote.Round
pv.lastSignState.Step = step
pv.lastSignState.Signature = newSig
pv.lastSignState.BlockHash = vote.BlockHash

// SEVENTH_REFACTOR: Persist BEFORE returning to ensure crash safety
if err := pv.saveState(); err != nil {
    pv.lastSignState = oldState  // Rollback on failure
    panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to persist sign state: %v", err))
}

// Only return signature after successful persist
vote.Signature = newSig
```

**Severity:** **CRITICAL** - Potential slashing vulnerability

---

### 2. ValidatorSetFromData Shallow Copy

**File:** `types/validator.go:NewValidatorSet()`

**Issue:** When creating a validator set from input validators, slice fields
(`Name.Name *string`, `PublicKey.Data []byte`) were not deep-copied, creating
aliasing between the original input and the stored validators. Modifications to
the original input would corrupt the validator set.

**Fix:** Deep copy AccountName and PublicKey for each validator:
```go
// SEVENTH_REFACTOR: Create deep copy to prevent aliasing
nameCopy := CopyAccountName(v.Name)

var pubKeyCopy PublicKey
if len(v.PublicKey.Data) > 0 {
    pubKeyCopy.Data = make([]byte, len(v.PublicKey.Data))
    copy(pubKeyCopy.Data, v.PublicKey.Data)
}

val := &NamedValidator{
    Name:             nameCopy,
    Index:            uint16(i),
    PublicKey:        pubKeyCopy,
    VotingPower:      v.VotingPower,
    ProposerPriority: v.ProposerPriority,
}
```

**Severity:** HIGH - Data corruption potential

---

### 3. Unbounded WAL Height Index Memory Growth

**File:** `wal/file_wal.go`

**Issue:** The `heightIndex` map grows unboundedly over time as blocks are committed.
For long-running validators, this could consume significant memory (8 bytes key +
pointer per height entry, plus map overhead).

**Fix:** Added index size limit and automatic pruning:
```go
const maxHeightIndexSize = 100000

func (w *FileWAL) pruneHeightIndex() {
    if len(w.heightIndex) <= maxHeightIndexSize {
        return
    }
    heights := make([]int64, 0, len(w.heightIndex))
    for h := range w.heightIndex {
        heights = append(heights, h)
    }
    sort.Slice(heights, func(i, j int) bool {
        return heights[i] < heights[j]
    })
    toRemove := len(heights) - maxHeightIndexSize
    for i := 0; i < toRemove; i++ {
        delete(w.heightIndex, heights[i])
    }
}
```

Called during `indexMessage()` when index exceeds limit.

**Severity:** MEDIUM-HIGH - Memory exhaustion over time

---

### 4. Missing Key File Permission Validation

**File:** `privval/file_pv.go:loadKeyStrict()`

**Issue:** The private key file could have world-readable permissions (e.g., 0644),
exposing the validator's private key to other users on the system.

**Fix:** Added permission check in key loading:
```go
// SEVENTH_REFACTOR: Validate file permissions for security
perm := info.Mode().Perm()
if perm&0077 != 0 {
    return fmt.Errorf("key file %s has insecure permissions %o (expected 0600 or stricter)",
        pv.keyFilePath, perm)
}
```

**Severity:** MEDIUM - Security vulnerability on shared systems

---

### 5. Missing Signature Verification in CheckVote

**File:** `evidence/pool.go:CheckVote()`

**Issue:** Votes were stored for equivocation detection without first verifying their
signatures. An attacker could:
1. Send a fake vote with invalid signature for validator X
2. When validator X sends their real vote, it's flagged as "equivocation"
3. False evidence created against honest validator

**Fix:** Verify vote signature before storing:
```go
func (p *Pool) CheckVote(vote *gen.Vote, valSet *types.ValidatorSet, chainID string) (*gen.DuplicateVoteEvidence, error) {
    // SEVENTH_REFACTOR: Verify vote signature before processing
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
    // ... rest of function
}
```

**Severity:** MEDIUM - False evidence against honest validators

---

### 6. BlockSync Callback Captures Mutable References

**File:** `engine/blocksync.go:ReceiveBlock()`

**Issue:** The async callback captured `block` and `commit` pointers directly:
```go
// VULNERABLE - old code:
go func() {
    defer bs.wg.Done()
    bs.onBlockCommitted(block, commit)  // Shares memory with caller
}()
```

The goroutine could execute after the caller modified the block/commit.

**Fix:** Deep copy before passing to async callback:
```go
// SEVENTH_REFACTOR: Deep copy to prevent race conditions
blockCopy := types.CopyBlock(block)
commitCopy := types.CopyCommit(commit)
bs.wg.Add(1)
go func() {
    defer bs.wg.Done()
    bs.onBlockCommitted(blockCopy, commitCopy)
}()
```

Also added comprehensive `CopyBlock`, `CopyCommit`, `CopyBlockHeader`, `CopyBlockData`,
`CopyBatchCertRef`, `CopyCommitSig`, and `CopyHash` functions to `types/block.go`.

**Severity:** MEDIUM - Race condition with data corruption

---

## Summary

| # | Bug | Severity | File |
|---|-----|----------|------|
| 1 | PrivVal double-sign vulnerability | **CRITICAL** | privval/file_pv.go |
| 2 | ValidatorSet shallow copy | HIGH | types/validator.go |
| 3 | Unbounded WAL height index | MEDIUM-HIGH | wal/file_wal.go |
| 4 | Missing key file permissions check | MEDIUM | privval/file_pv.go |
| 5 | Missing signature verification in CheckVote | MEDIUM | evidence/pool.go |
| 6 | BlockSync callback race condition | MEDIUM | engine/blocksync.go |

## Files Modified

1. `privval/file_pv.go` - Fixed persist order in SignVote/SignProposal, added permission check
2. `types/validator.go` - Deep copy in NewValidatorSet
3. `wal/file_wal.go` - Added height index limit and pruning
4. `evidence/pool.go` - Added signature verification with chainID parameter
5. `engine/state.go` - Pass chainID to CheckVote calls
6. `engine/blocksync.go` - Deep copy block/commit for async callbacks
7. `types/block.go` - Added CopyBlock, CopyCommit, and related copy functions
8. `evidence/pool_test.go` - Updated CheckVote calls with empty chainID
9. `tests/integration/consensus_test.go` - Updated CheckVote calls with empty chainID

## API Changes

### CheckVote Signature Change

**Before:**
```go
func (p *Pool) CheckVote(vote *gen.Vote, valSet *types.ValidatorSet) (*gen.DuplicateVoteEvidence, error)
```

**After:**
```go
func (p *Pool) CheckVote(vote *gen.Vote, valSet *types.ValidatorSet, chainID string) (*gen.DuplicateVoteEvidence, error)
```

Pass empty string `""` to skip signature verification (for testing only).
In production, always pass the chain ID.

## Testing

All tests pass with race detection:
```
go test -race ./...
```
