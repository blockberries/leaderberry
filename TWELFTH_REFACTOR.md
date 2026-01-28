# TWELFTH REFACTOR - Exhaustive Code Review

This document summarizes the findings from an exhaustive line-by-line code review of the Leaderberry codebase.

## Verified Bugs

### 1. vote_tracker.go: Stale VoteSet Reference After Reset (HIGH)

**Location**: `engine/vote_tracker.go:418-429, 464-472`

**Issue**: `Prevotes()` and `Precommits()` return pointers to internal VoteSet objects. When `Reset()` is called, it creates new maps and orphans the old VoteSets. Any goroutine holding a reference to an old VoteSet will:
- Add votes to a stale VoteSet that is no longer tracked
- Lose those votes (they won't be counted toward quorum)

**Code**:
```go
func (hvs *HeightVoteSet) Prevotes(round int32) *VoteSet {
    hvs.mu.RLock()
    defer hvs.mu.RUnlock()
    return hvs.prevotes[round]  // Returns pointer to internal VoteSet
}

func (hvs *HeightVoteSet) Reset(height int64, valSet *types.ValidatorSet) {
    hvs.mu.Lock()
    defer hvs.mu.Unlock()
    hvs.height = height
    hvs.validatorSet = valSet
    hvs.prevotes = make(map[int32]*VoteSet)  // Old VoteSets orphaned
    hvs.precommits = make(map[int32]*VoteSet)
}
```

**Risk**: Race condition where votes are lost if Reset() is called while another goroutine is processing votes.

**Fix**: Add a generation counter to detect stale references and reject votes to stale VoteSets.

---

### 2. privval/file_pv.go: isSameVote Incomplete Check (MEDIUM-HIGH)

**Location**: `privval/file_pv.go:532-543`

**Issue**: `isSameVote()` only checks BlockHash, but `VoteSignBytes` (types/vote.go:33-52) includes Timestamp, Validator, and ValidatorIndex in the signed data. If a vote has the same BlockHash but different Timestamp, the stored signature won't match.

**VoteSignBytes includes**:
- Type, Height, Round, BlockHash (checked by isSameVote)
- **Timestamp** (NOT checked)
- **Validator** (NOT checked)
- **ValidatorIndex** (NOT checked)

**Impact**: If isSameVote returns true but Timestamp differs:
1. The old signature is returned
2. Signature verification fails (signBytes include Timestamp)
3. Vote is rejected by peers
4. Consensus liveness may be affected

**Code**:
```go
func (pv *FilePV) isSameVote(vote *gen.Vote) bool {
    // Only checks BlockHash, NOT Timestamp/Validator/ValidatorIndex
    if pv.lastSignState.BlockHash == nil && vote.BlockHash == nil {
        return true
    }
    // ...
    return types.HashEqual(*pv.lastSignState.BlockHash, *vote.BlockHash)
}
```

**Fix**: Store and compare the complete canonical vote data, or store a hash of the signBytes.

---

### 3. types/block.go: CopyCommitSig Missing Nil Check (LOW)

**Location**: `types/block.go:100-116`

**Issue**: `CopyCommitSig` takes a pointer but doesn't check for nil. Direct calls with nil will panic.

**Code**:
```go
func CopyCommitSig(sig *CommitSig) CommitSig {
    sigCopy := CommitSig{
        ValidatorIndex: sig.ValidatorIndex,  // PANIC if sig is nil
        Timestamp:      sig.Timestamp,
    }
    // ...
}
```

**Current Risk**: Low - all current call sites use `&sig` where sig is a value from a slice iteration, never nil.

**Fix**: Add nil check for defensive programming.

---

## Verified False Positives

### 1. evidence/pool.go: Vote Protection Window Logic - CORRECT

The agent claimed the vote protection window was inverted. Analysis shows it's correct:

```go
protectedHeight := p.currentHeight - VoteProtectionWindow
if vote.Height >= protectedHeight {
    continue  // Skip (don't prune) - these are PROTECTED
}
```

- Example: currentHeight=2000, VoteProtectionWindow=1000, protectedHeight=1000
- Vote at height 1500: `1500 >= 1000` = true → SKIP (protected, recent vote)
- Vote at height 500: `500 >= 1000` = false → PRUNE (old vote)

This correctly protects recent votes (last N heights) and prunes old ones.

---

### 2. peer_state.go: Deadlock in MarkPeerCatchingUp - NO DEADLOCK

Lock ordering is explicitly documented (lines 12-30) and consistently followed:
1. PeerSet.mu
2. PeerState.mu
3. VoteBitmap.mu

`MarkPeerCatchingUp` follows this order correctly:
- Holds PeerSet.mu (RLock)
- Then acquires PeerState.mu via SetCatchingUp()

No violation of lock ordering detected.

---

### 3. wal/file_wal.go: Torn Page Vulnerability - HANDLED

The WAL uses CRC32 checksums to detect corruption from partial writes:

```go
// Encoder writes: length (4 bytes) + data + CRC32 (4 bytes)
// Decoder validates CRC and returns ErrWALCorrupted on mismatch
if expectedCRC != actualCRC {
    return nil, fmt.Errorf("%w: CRC mismatch", ErrWALCorrupted)
}
```

Partial writes (torn pages) are detected as corruption and handled appropriately. The buildIndex function also logs warnings on corruption and continues with partial index (lines 145-148).

---

## Summary

| Issue | Severity | Status |
|-------|----------|--------|
| Stale VoteSet reference | HIGH | FIXED |
| isSameVote incomplete | MEDIUM-HIGH | FIXED |
| CopyCommitSig nil check | LOW | FIXED |
| Vote protection window | FALSE POSITIVE | N/A |
| Deadlock in peer_state | FALSE POSITIVE | N/A |
| WAL torn page | FALSE POSITIVE | N/A |

---

## Fixes Applied

### 1. Stale VoteSet Reference - FIXED

**Files Modified**:
- `engine/vote_tracker.go`
- `engine/errors.go`

**Changes**:
1. Added `generation` field (atomic.Uint64) to HeightVoteSet to track height changes
2. Added `parent` and `myGeneration` fields to VoteSet to detect staleness
3. Added `newVoteSetWithParent()` helper that links VoteSets to their parent HeightVoteSet
4. VoteSet.AddVote() now checks if the generation matches before accepting votes
5. HeightVoteSet.Reset() increments generation to invalidate old VoteSets
6. Added ErrStaleVoteSet error for rejected votes

**Mechanism**: When Reset() is called, the generation counter increments atomically. Any VoteSet created before Reset() will have a mismatched generation and will reject new votes with ErrStaleVoteSet.

---

### 2. isSameVote Incomplete Check - FIXED

**Files Modified**:
- `privval/signer.go`
- `privval/file_pv.go`

**Changes**:
1. Added `Timestamp` field to LastSignState to store the timestamp of the signed vote/proposal
2. Added `SignBytesHash` field to LastSignState for complete payload verification
3. Updated isSameVote() to check Timestamp before returning cached signature
4. Updated isSameProposal() similarly
5. Updated SignVote() and SignProposal() to store Timestamp and SignBytesHash
6. Updated loadState/saveState to persist the new fields

**Mechanism**: VoteSignBytes includes Timestamp, so if the timestamp differs, the cached signature won't verify. By storing and checking Timestamp, we avoid returning invalid signatures.

---

### 3. CopyCommitSig Nil Check - FIXED

**Files Modified**:
- `types/block.go`

**Changes**:
- Added nil check at the start of CopyCommitSig()
- Returns empty CommitSig{} if input is nil

**Mechanism**: Defensive programming to prevent panics if CopyCommitSig is called with nil pointer.
