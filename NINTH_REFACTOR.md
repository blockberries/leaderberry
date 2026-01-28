# NINTH REFACTOR - Comprehensive Code Review

This document addresses bugs discovered during the ninth comprehensive code review,
which included an exhaustive line-by-line analysis followed by a verification pass.

## Issues Identified

### 1. GetVote() Returns Internal Pointer

**File:** `engine/vote_tracker.go:163-167`

**Issue:** The `GetVote()` method returns a pointer to the internal vote stored in the VoteSet.
Callers can modify the returned vote and corrupt internal state:
```go
// OLD - Returns internal pointer
func (vs *VoteSet) GetVote(valIndex uint16) *gen.Vote {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return vs.votes[valIndex]  // Exposes internal pointer!
}
```

Note: EIGHTH_REFACTOR fixed `GetVotes()` but this method was missed.

**Fix:** Return a deep copy:
```go
func (vs *VoteSet) GetVote(valIndex uint16) *gen.Vote {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    vote := vs.votes[valIndex]
    if vote == nil {
        return nil
    }
    return types.CopyVote(vote)
}
```

**Severity:** MEDIUM - Potential vote tracking corruption

---

### 2. TwoThirdsMajority() Returns Internal Hash Pointer

**File:** `engine/vote_tracker.go:131-139`

**Issue:** Returns pointer to internal blockHash. Caller modifications corrupt VoteSet state:
```go
// OLD - Returns internal pointer
func (vs *VoteSet) TwoThirdsMajority() (*types.Hash, bool) {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    if vs.maj23 != nil {
        return vs.maj23.blockHash, true  // Internal pointer exposed!
    }
    return nil, false
}
```

**Fix:** Return a copy of the hash:
```go
func (vs *VoteSet) TwoThirdsMajority() (*types.Hash, bool) {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    if vs.maj23 != nil && vs.maj23.blockHash != nil {
        hashCopy := types.CopyHash(vs.maj23.blockHash)
        return hashCopy, true
    }
    return nil, false
}
```

**Severity:** MEDIUM - Potential consensus state corruption

---

### 3. MakeCommit() Shallow Copies Signatures and BlockHash

**File:** `engine/vote_tracker.go:287-294`

**Issue:** CommitSig creation uses shallow copies for slice fields:
```go
// OLD - Shallow copies
sig := gen.CommitSig{
    ValidatorIndex: vote.ValidatorIndex,
    Signature:      vote.Signature,   // Shallow - Data slice shared!
    Timestamp:      vote.Timestamp,
    BlockHash:      vote.BlockHash,   // Pointer copy!
}
```

If the original votes are modified after commit creation, the commit is corrupted.

**Fix:** Deep copy signature and hash:
```go
sig := gen.CommitSig{
    ValidatorIndex: vote.ValidatorIndex,
    Signature:      types.CopySignature(vote.Signature),
    Timestamp:      vote.Timestamp,
    BlockHash:      types.CopyHash(vote.BlockHash),
}
```

**Severity:** MEDIUM - Commit data corruption risk

---

### 4. Commit Timeout Scheduled With Wrong Height

**File:** `engine/state.go:829-834`

**Issue:** In `finalizeCommitLocked`, the height is incremented at line 798, but the commit
timeout is scheduled with the OLD height parameter:
```go
// Line 798:
cs.height = height + 1  // Height incremented to new height

// Lines 829-834:
if !cs.config.SkipTimeoutCommit {
    cs.scheduleTimeout(TimeoutInfo{
        Height: height,  // BUG: Uses old height, not cs.height!
        Round:  0,
        Step:   RoundStepCommit,
    })
}
```

When the timeout fires in `handleTimeout`:
```go
// Line 940:
if ti.Height != cs.height || ti.Round < cs.round {
    return  // Always returns! height != height+1
}
```

**Result:** Commit timeouts NEVER fire because the height check always fails.

**Fix:** Use `cs.height` (new height) for the timeout:
```go
if !cs.config.SkipTimeoutCommit {
    cs.scheduleTimeout(TimeoutInfo{
        Height: cs.height,  // Use new height
        Round:  0,
        Step:   RoundStepCommit,
    })
}
```

**Severity:** HIGH - Commit timeout mechanism completely broken

---

### 5. BlockSyncer Cannot Transition CaughtUp → Syncing

**File:** `engine/blocksync.go:227-238`

**Issue:** State machine only allows Idle → Syncing transition:
```go
// Update state based on progress
if bs.currentHeight >= bs.targetHeight {
    if bs.state == BlockSyncStateSyncing {
        bs.state = BlockSyncStateCaughtUp
    }
} else if bs.state == BlockSyncStateIdle {  // Only from Idle!
    bs.state = BlockSyncStateSyncing
}
```

If a node is CaughtUp and then falls behind (e.g., temporary network disconnect),
it will NEVER start syncing again.

**Fix:** Allow CaughtUp → Syncing transition:
```go
if bs.currentHeight >= bs.targetHeight {
    if bs.state == BlockSyncStateSyncing {
        bs.state = BlockSyncStateCaughtUp
        // ... callback
    }
} else if bs.state == BlockSyncStateIdle || bs.state == BlockSyncStateCaughtUp {
    if bs.state == BlockSyncStateCaughtUp {
        log.Printf("[INFO] blocksync: fell behind, resuming sync from %d to %d",
            bs.currentHeight, bs.targetHeight)
    } else {
        log.Printf("[INFO] blocksync: starting sync from %d to %d",
            bs.currentHeight, bs.targetHeight)
    }
    bs.state = BlockSyncStateSyncing
}
```

**Severity:** HIGH - Nodes can get permanently stuck

---

### 6. File Handle Leak in multiSegmentReader

**File:** `wal/file_wal.go:836-838`

**Issue:** When `Read()` encounters a non-EOF error, the reader is not closed:
```go
msg, err := r.reader.Read()
if err == io.EOF {
    r.reader.Close()  // Only closed on EOF
    r.reader = nil
    continue
}
if err != nil {
    return nil, err  // BUG: r.reader NOT closed!
}
```

**Fix:** Close reader before returning error:
```go
if err != nil {
    r.reader.Close()
    r.reader = nil
    return nil, err
}
```

**Severity:** MEDIUM - File handle leak on corruption

---

### 7. Evidence Pool committed Map Grows Unbounded

**File:** `evidence/pool.go`

**Issue:** The `committed` map tracks committed evidence but is never pruned.
Over time, it grows without bound:
```go
// MarkCommitted adds to map but pruneExpired() never removes from it
p.committed[key] = struct{}{}
```

The `pruneExpired()` function prunes `pending` and `seenVotes` but not `committed`.

**Fix:** Prune old entries from committed map in `pruneExpired()`:
```go
func (p *Pool) pruneExpired() {
    // ... existing pending pruning ...

    // Prune old committed evidence
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

    // ... existing seenVotes pruning ...
}
```

**Severity:** MEDIUM - Memory leak in long-running nodes

---

## False Positives Identified

| Flagged Issue | Verdict | Reason |
|---------------|---------|--------|
| handlePrevoteLocked step check | NOT A BUG | PrevoteWait timeout handles transition; adds latency but correct |
| Empty chainID bypasses verification | DESIGN CHOICE | Intentional for testing; should log warning in production |
| POL validation missing PolRound check | NOT A BUG | PolRound validated against vote rounds in validatePOL |

---

## Summary

| # | Bug | Severity | File |
|---|-----|----------|------|
| 1 | GetVote() returns internal pointer | MEDIUM | engine/vote_tracker.go |
| 2 | TwoThirdsMajority() returns internal pointer | MEDIUM | engine/vote_tracker.go |
| 3 | MakeCommit() shallow copies | MEDIUM | engine/vote_tracker.go |
| 4 | Commit timeout wrong height | HIGH | engine/state.go |
| 5 | CaughtUp → Syncing missing | HIGH | engine/blocksync.go |
| 6 | File handle leak | MEDIUM | wal/file_wal.go |
| 7 | committed map unbounded | MEDIUM | evidence/pool.go |

## Files To Modify

1. `engine/vote_tracker.go` - Fix GetVote, TwoThirdsMajority, MakeCommit
2. `engine/state.go` - Fix commit timeout height
3. `engine/blocksync.go` - Add CaughtUp → Syncing transition
4. `wal/file_wal.go` - Close reader on non-EOF error
5. `evidence/pool.go` - Prune committed map

## Testing

All tests pass with race detection:
```
$ go test -race ./...
ok      github.com/blockberries/leaderberry/engine      2.048s
ok      github.com/blockberries/leaderberry/evidence    2.553s
ok      github.com/blockberries/leaderberry/privval     (cached)
ok      github.com/blockberries/leaderberry/tests/integration   2.657s
ok      github.com/blockberries/leaderberry/types       (cached)
ok      github.com/blockberries/leaderberry/wal         2.249s
```

Build succeeds with no errors:
```
$ go build ./...
```
