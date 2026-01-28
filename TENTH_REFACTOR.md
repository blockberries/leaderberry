# TENTH REFACTOR - Comprehensive Code Review

This document addresses bugs discovered during the tenth comprehensive code review,
which included an exhaustive line-by-line analysis followed by a verification pass
to eliminate false positives.

## Issues Identified

### 1. GetVotesForBlock() Returns Shallow Copy

**File:** `engine/vote_tracker.go:213-226`

**Issue:** The `GetVotesForBlock()` method returns a shallow copy of the votes slice.
The `copy()` function on a slice of pointers only copies the pointers, not the underlying
vote data. Callers can modify the returned votes and corrupt internal state:
```go
// OLD - Shallow copy
func (vs *VoteSet) GetVotesForBlock(blockHash *types.Hash) []*gen.Vote {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    key := blockHashKey(blockHash)
    bv, ok := vs.votesByBlock[key]
    if !ok {
        return nil
    }

    votes := make([]*gen.Vote, len(bv.votes))
    copy(votes, bv.votes)  // BUG: Only copies pointers, not vote data!
    return votes
}
```

Note: `GetVotes()` was fixed in EIGHTH_REFACTOR but `GetVotesForBlock()` was missed.

**Fix:** Deep copy each vote:
```go
func (vs *VoteSet) GetVotesForBlock(blockHash *types.Hash) []*gen.Vote {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    key := blockHashKey(blockHash)
    bv, ok := vs.votesByBlock[key]
    if !ok {
        return nil
    }

    // TENTH_REFACTOR: Return deep copies like GetVotes() does
    votes := make([]*gen.Vote, 0, len(bv.votes))
    for _, v := range bv.votes {
        votes = append(votes, types.CopyVote(v))
    }
    return votes
}
```

**Severity:** MEDIUM - Potential vote tracking corruption

---

### 2. SetPeerMaj23() Stores Input Pointer Directly

**File:** `engine/vote_tracker.go:228-238`

**Issue:** The `SetPeerMaj23()` method stores the input hash pointer directly without
copying. If the caller modifies the hash after calling this method, internal state is corrupted:
```go
// OLD - Stores input pointer
func (vs *VoteSet) SetPeerMaj23(peerID string, blockHash *types.Hash) {
    vs.mu.Lock()
    defer vs.mu.Unlock()

    if vs.peerMaj23 == nil {
        vs.peerMaj23 = make(map[string]*types.Hash)
    }
    vs.peerMaj23[peerID] = blockHash  // BUG: Stores input pointer!
}
```

**Fix:** Store a deep copy:
```go
func (vs *VoteSet) SetPeerMaj23(peerID string, blockHash *types.Hash) {
    vs.mu.Lock()
    defer vs.mu.Unlock()

    if vs.peerMaj23 == nil {
        vs.peerMaj23 = make(map[string]*types.Hash)
    }
    // TENTH_REFACTOR: Store a copy to prevent caller modifications from corrupting state
    vs.peerMaj23[peerID] = types.CopyHash(blockHash)
}
```

**Severity:** MEDIUM - Potential consensus state corruption

---

### 3. GetPeerMaj23Claims() Returns Internal Pointers

**File:** `engine/vote_tracker.go:240-255`

**Issue:** The method returns internal hash pointers. Callers can modify these
hashes and corrupt internal state:
```go
// OLD - Returns internal pointers
func (vs *VoteSet) GetPeerMaj23Claims() map[string]*types.Hash {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    if vs.peerMaj23 == nil {
        return nil
    }

    result := make(map[string]*types.Hash, len(vs.peerMaj23))
    for k, v := range vs.peerMaj23 {
        result[k] = v  // BUG: Returns internal pointer!
    }
    return result
}
```

**Fix:** Return deep copies:
```go
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
```

**Severity:** MEDIUM - Potential consensus state corruption

---

### 4. BlockSyncer.UpdateTargetHeight() Callback Not Tracked

**File:** `engine/blocksync.go:234-236`

**Issue:** The `onCaughtUp` callback is spawned without WaitGroup tracking.
If `Stop()` is called while this callback is running, Stop() won't wait for it:
```go
// OLD - No WaitGroup tracking
if bs.onCaughtUp != nil {
    go bs.onCaughtUp()  // BUG: Not tracked in WaitGroup!
}
```

Compare with `ReceiveBlock()` at lines 400-406 which correctly uses WaitGroup:
```go
// Correct pattern from ReceiveBlock
if bs.onCaughtUp != nil {
    bs.wg.Add(1)
    go func() {
        defer bs.wg.Done()
        bs.onCaughtUp()
    }()
}
```

**Fix:** Track the callback with WaitGroup:
```go
if bs.onCaughtUp != nil {
    bs.wg.Add(1)
    go func() {
        defer bs.wg.Done()
        bs.onCaughtUp()
    }()
}
```

**Severity:** MEDIUM - Race condition on shutdown

---

## False Positives Identified

| Flagged Issue | Verdict | Reason |
|---------------|---------|--------|
| ValidatorSetFromData shallow copy | NOT A BUG | NewValidatorSet() deep-copies all validators at lines 82-99 |
| CopyCommitSig nil dereference | NOT A BUG | Only caller is CopyCommit which iterates slice, never passes nil |
| Checkpoint deletes before flush | NOT A BUG | Only deletes old closed segments; current segment never touched |
| WAL partial write buffer corruption | NOT A BUG | Encoder writes atomically; partial writes detectable by CRC |
| AddVote stores input pointer | DESIGN CHOICE | Performance optimization; callers don't modify votes after adding |

---

## Summary

| # | Bug | Severity | File |
|---|-----|----------|------|
| 1 | GetVotesForBlock() shallow copy | MEDIUM | engine/vote_tracker.go |
| 2 | SetPeerMaj23() stores input pointer | MEDIUM | engine/vote_tracker.go |
| 3 | GetPeerMaj23Claims() returns internal pointers | MEDIUM | engine/vote_tracker.go |
| 4 | UpdateTargetHeight() callback not tracked | MEDIUM | engine/blocksync.go |

## Files To Modify

1. `engine/vote_tracker.go` - Fix GetVotesForBlock, SetPeerMaj23, GetPeerMaj23Claims
2. `engine/blocksync.go` - Fix UpdateTargetHeight callback tracking

## Testing

All tests pass with race detection:
```
$ go test -race ./...
ok      github.com/blockberries/leaderberry/engine      1.545s
ok      github.com/blockberries/leaderberry/evidence    (cached)
ok      github.com/blockberries/leaderberry/privval     (cached)
ok      github.com/blockberries/leaderberry/tests/integration   1.745s
ok      github.com/blockberries/leaderberry/types       (cached)
ok      github.com/blockberries/leaderberry/wal         (cached)
```

Build succeeds with no errors:
```
$ go build ./...
```
