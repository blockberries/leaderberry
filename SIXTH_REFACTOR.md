# SIXTH REFACTOR - Comprehensive Code Review Bug Fixes

This document addresses bugs discovered during the sixth comprehensive code review
with verification pass to eliminate false positives.

## Critical Issues Fixed

### 1. ValidatorSet Hash Included Mutable ProposerPriority

**File:** `types/validator.go:340-378`

**Issue:** The `Hash()` function copied entire `NamedValidator` structs including
`ProposerPriority`, which changes every round. Two validator sets with identical
validators but different priority states would produce different hashes, breaking:
- Light client verification
- State sync between nodes
- Consensus assumptions (same validators should = same hash)

**Fix:** Explicitly construct validators for hashing with `ProposerPriority: 0`:
```go
validators[i] = NamedValidator{
    Name:             v.Name,
    Index:            v.Index,
    PublicKey:        v.PublicKey,
    VotingPower:      v.VotingPower,
    ProposerPriority: 0, // Exclude from hash
}
```

**Severity:** CRITICAL

---

### 2. PrivVal Missing Temp File Cleanup in saveState()

**File:** `privval/file_pv.go:357-371`

**Issue:** Unlike `saveKey()` which properly cleans up temp files on all error paths,
`saveState()` had no cleanup, leaving orphaned `.tmp` files on:
- Failed sync (line 362-364)
- Failed rename (line 369-370)

**Fix:** Added `os.Remove(tmpPath)` on all error paths, matching `saveKey()` behavior.

**Severity:** MEDIUM-HIGH

---

### 3. Evidence VoteB Not Deep-Copied

**File:** `evidence/pool.go:120-125`

**Issue:** When creating `DuplicateVoteEvidence`, `VoteB` was shallow-copied via
`*vote` dereference. The caller's vote's `Signature.Data` and `BlockHash.Data`
slices were shared, allowing post-call modification to corrupt evidence.

**Fix:** Deep copy both votes using `types.CopyVote()`:
```go
voteACopy := types.CopyVote(existing)
voteBCopy := types.CopyVote(vote)
ev := &gen.DuplicateVoteEvidence{
    VoteA: *voteACopy,
    VoteB: *voteBCopy,
    ...
}
```

**Severity:** MEDIUM-HIGH

---

### 4. WAL Rotation Leaves Orphaned Files on Stat Failure

**File:** `wal/file_wal.go:315-319`

**Issue:** After creating a new segment file, if `Stat()` failed, the file was
closed but not removed, leaving orphaned `wal-XXXXX` files that could confuse
WAL recovery.

**Fix:** Added `os.Remove(newPath)` after closing on error:
```go
if err != nil {
    newFile.Close()
    os.Remove(newPath) // Clean up orphaned file
    return fmt.Errorf(...)
}
```

**Severity:** MEDIUM

---

### 5. Authorization Weight Overflow

**File:** `types/account.go:106, 153`

**Issue:** Weight accumulation used direct `totalWeight += kw.Weight` without
overflow protection. With uint32, overflow wraps to small values, potentially
causing authorization checks to pass/fail incorrectly.

**Fix:** Added safe addition with overflow protection:
```go
const MaxAuthWeight = uint32(1<<31 - 1) // ~2 billion

func safeAddWeight(total, add uint32) uint32 {
    if add > MaxAuthWeight-total {
        return MaxAuthWeight // Cap at max to prevent overflow
    }
    return total + add
}
```

Updated both accumulation sites to use `safeAddWeight()`.

**Severity:** LOW (requires unusual configuration)

---

## Verification of False Positives

The following issues were flagged during initial review but verified as non-bugs:

| Issue | Verification |
|-------|-------------|
| Vote type constants in replay.go | `gen.VoteTypeVoteTypePrevote` is correct generated name |
| PeerSet.MarkPeerCatchingUp race | Go memory model keeps peer valid; stale ops harmless |
| Merkle proof verification | Tests with 3, 5 parts pass; index tracking correct |
| BlockSyncer callback capture | Closure captures values correctly |

---

## Summary

| # | Bug | Severity | File |
|---|-----|----------|------|
| 1 | ValidatorSet Hash includes ProposerPriority | **CRITICAL** | types/validator.go |
| 2 | PrivVal temp file cleanup missing | MEDIUM-HIGH | privval/file_pv.go |
| 3 | Evidence VoteB not deep-copied | MEDIUM-HIGH | evidence/pool.go |
| 4 | WAL rotation orphans files | MEDIUM | wal/file_wal.go |
| 5 | Authorization weight overflow | LOW | types/account.go |

## Files Modified

1. `types/validator.go` - Exclude ProposerPriority from hash
2. `privval/file_pv.go` - Add temp file cleanup on error paths
3. `evidence/pool.go` - Deep copy both votes in evidence
4. `wal/file_wal.go` - Clean up orphaned segment on Stat failure
5. `types/account.go` - Add safe weight addition with overflow protection

## Testing

All tests pass with race detection:
```
go test -race ./...
ok  github.com/blockberries/leaderberry/engine
ok  github.com/blockberries/leaderberry/evidence
ok  github.com/blockberries/leaderberry/privval
ok  github.com/blockberries/leaderberry/tests/integration
ok  github.com/blockberries/leaderberry/types
ok  github.com/blockberries/leaderberry/wal
```
