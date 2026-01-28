# EIGHTH REFACTOR - Code Review Verification Pass

This document addresses bugs discovered during the eighth comprehensive code review,
which included a verification pass to eliminate false positives from automated analysis.

## Issues Fixed

### 1. Shallow Copy of Proposal Block

**File:** `engine/state.go:558-560`

**Issue:** When accepting a proposal, the code made a shallow copy of the block:
```go
// OLD - Shallow copy
blockCopy := proposal.Block
cs.proposalBlock = &blockCopy
```

The `gen.Block` struct contains:
- `Evidence [][]byte` - slice that would share memory with original
- `LastCommit *Commit` - pointer that would share with original
- `Header.BatchCertRefs []BatchCertRef` - slice that would share with original

If the proposal's block was modified after acceptance, our consensus state would be corrupted.

**Fix:** Use proper deep copy:
```go
cs.proposalBlock = types.CopyBlock(&proposal.Block)
```

**Severity:** MEDIUM - Potential consensus state corruption

---

### 2. seenVotes Can Exceed MaxSeenVotes Limit

**File:** `evidence/pool.go:163-170`

**Issue:** When the vote pool reached MaxSeenVotes, pruning was attempted but the
new vote was always added regardless of whether pruning succeeded:
```go
// OLD - Always adds vote even if pruning fails
if len(p.seenVotes) >= MaxSeenVotes {
    p.pruneOldestVotes(MaxSeenVotes / 10) // May not remove anything!
}
p.seenVotes[key] = types.CopyVote(vote) // Always adds
```

If all votes were within the VoteProtectionWindow, pruning would fail and the
pool would grow unbounded, eventually causing OOM.

**Fix:** Check if pruning succeeded before adding:
```go
if len(p.seenVotes) >= MaxSeenVotes {
    p.pruneOldestVotes(MaxSeenVotes / 10)
    if len(p.seenVotes) >= MaxSeenVotes {
        // Pruning didn't help - pool is full of protected votes
        log.Printf("[WARN] evidence: vote pool full, dropping vote")
        return nil, nil
    }
}
p.seenVotes[key] = types.CopyVote(vote)
```

**Severity:** MEDIUM - Potential OOM under sustained attack

---

### 3. GetVotes Returns Mutable Internal Pointers

**File:** `engine/vote_tracker.go:185-199`

**Issue:** The `GetVotes()` method returned pointers to the same Vote objects stored
internally. Callers could modify the returned votes and corrupt VoteSet state:
```go
// OLD - Returns pointers to internal votes
for _, v := range vs.votes {
    votes = append(votes, v)
}
```

**Fix:** Return deep copies:
```go
for _, v := range vs.votes {
    votes = append(votes, types.CopyVote(v))
}
```

**Severity:** MEDIUM - Potential vote tracking corruption

---

### 4. TOCTOU Race in MarkPeerCatchingUp

**File:** `engine/peer_state.go:482-492`

**Issue:** The lock was released between getting the peer and calling SetCatchingUp:
```go
// OLD - TOCTOU race
ps.mu.RLock()
peer := ps.peers[peerID]
ps.mu.RUnlock()  // Lock released here

if peer == nil {
    return
}
peer.SetCatchingUp(...)  // Peer could be removed by now
```

Between releasing the lock and calling SetCatchingUp, the peer could be removed
from the map by another goroutine.

**Fix:** Hold lock during entire operation:
```go
ps.mu.RLock()
defer ps.mu.RUnlock()

peer := ps.peers[peerID]
if peer == nil {
    return
}
peer.SetCatchingUp(...)
```

**Severity:** LOW - Benign race but indicates sloppy concurrency

---

### 5. Misleading Comment in PrivVal

**File:** `privval/file_pv.go:441-445`

**Issue:** The comment said "Persist state BEFORE updating in-memory state" but the
code actually updates in-memory first (which is necessary for serialization). The
code was correct, but the comment was misleading.

**Fix:** Clarified the comment to accurately describe the sequence:
1. Update in-memory state (for saveState to serialize)
2. Persist to disk atomically
3. Only then return signature to caller

The key invariant is that `vote.Signature` is only set AFTER `saveState()` succeeds.

**Severity:** LOW - Documentation clarity

---

## False Positives Identified

The verification pass identified several flagged issues that were NOT bugs:

| Flagged Issue | Verdict | Reason |
|---------------|---------|--------|
| replay.go vote type constants | NOT A BUG | `gen.VoteTypeVoteTypePrevote` is correct generated constant |
| vote_tracker integer overflow | NOT A BUG | Overflow prevented by MaxTotalVotingPower in ValidatorSet |
| CopyCommit nil dereference | NOT A BUG | BlockHash is value type (Hash), not pointer |
| ValidatorSetFromData pointers | NOT A BUG | NewValidatorSet creates deep copies |
| Merkle proof verification | NOT A BUG | Self-pairing for odd nodes handled correctly |
| PrivVal double-sign | NOT A BUG | Signature only returned after saveState() |

---

## Summary

| # | Bug | Severity | File |
|---|-----|----------|------|
| 1 | Shallow copy of proposal block | MEDIUM | engine/state.go |
| 2 | seenVotes unbounded growth | MEDIUM | evidence/pool.go |
| 3 | GetVotes encapsulation leak | MEDIUM | engine/vote_tracker.go |
| 4 | TOCTOU in MarkPeerCatchingUp | LOW | engine/peer_state.go |
| 5 | Misleading comment | LOW | privval/file_pv.go |

## Files Modified

1. `engine/state.go` - Use CopyBlock for proposal block
2. `evidence/pool.go` - Check pruning success before adding vote
3. `engine/vote_tracker.go` - Return vote copies in GetVotes
4. `engine/peer_state.go` - Hold lock during MarkPeerCatchingUp
5. `privval/file_pv.go` - Clarified comments in SignVote/SignProposal

## Testing

All tests pass with race detection:
```
go test -race ./...
```
