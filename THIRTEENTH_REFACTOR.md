# THIRTEENTH REFACTOR - Exhaustive Code Review

This document summarizes the findings from an exhaustive line-by-line code review of the Leaderberry codebase, following the TWELFTH REFACTOR.

## Verified Bugs

### 1. replay.go: Locked/Valid State Not Restored After Crash (HIGH)

**Location**: `engine/replay.go:151-196`

**Issue**: The WAL replay mechanism does not restore the locked block or valid block state after a crash. This violates BFT safety guarantees.

**Schema supports it** (schema/wal.cram:56-64):
```
message ConsensusStateData {
    int64 height = 1;
    int32 round = 2;
    RoundStepType step = 3;
    int32 locked_round = 4;
    *Hash locked_block_hash = 5;
    int32 valid_round = 6;
    *Hash valid_block_hash = 7;
}
```

**But replay ignores it** (engine/replay.go:151-163):
```go
func (cs *ConsensusState) replayState(msg *wal.Message, result *WALReplayResult) error {
    state, err := wal.DecodeState(msg.Data)
    // ...
    result.Height = state.Height
    result.Round = state.Round
    result.Step = state.Step
    // MISSING: locked_round, locked_block_hash, valid_round, valid_block_hash
    return nil
}
```

**And ReplayCatchup doesn't restore it** (engine/replay.go:180-183):
```go
cs.height = result.Height
cs.round = result.Round
cs.step = result.Step
// MISSING: cs.lockedRound, cs.lockedBlock, cs.validRound, cs.validBlock
```

**Impact**: If a node crashes while it has a locked block, after recovery it won't remember the lock. The node could then vote for a different block than it was locked on before the crash, potentially causing a fork or safety violation.

**Fix**:
1. Add locked/valid fields to `WALReplayResult`
2. Extract these fields in `replayState()`
3. Restore them in `ReplayCatchup()`
4. Store the actual blocks (not just hashes) or re-fetch them during replay

---

### 2. file_pv.go: SignBytesHash Analysis (RESOLVED - NOT A BUG)

**Location**: `privval/file_pv.go:477-502, 583-598`

**Initial Concern**: The TWELFTH REFACTOR added `SignBytesHash` to `LastSignState` but `isSameVote()` doesn't use it.

**Analysis**: After deeper review, the current `isSameVote()` implementation is actually correct and sufficient:

1. **Type**: Implicitly checked - same H/R/S implies same step, which determines vote type
2. **Height/Round/Step**: Checked by `CheckHRS()` before `isSameVote()` is called
3. **BlockHash**: Explicitly checked by `isSameVote()`
4. **Timestamp**: Explicitly checked by `isSameVote()` (added in TWELFTH REFACTOR)
5. **Validator**: Deterministic - it's the validator's own name, cannot differ
6. **ValidatorIndex**: Deterministic - derived from Validator name and validator set

**Conclusion**: The current implementation correctly verifies all meaningful fields. The `SignBytesHash` field is retained for potential future use (e.g., if sign bytes format changes) but is not required for correctness.

**Status**: RESOLVED - Updated comments in `isSameVote()` to document this analysis

---

## Verified False Positives

### 1. state.go: TOCTOU Bug in Proposal Handling - FALSE POSITIVE

**Agent Claim**: "TOCTOU bug allowing two proposals for same H/R"

**Analysis**: The lock is held throughout `handleProposal()`:
```go
func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
    cs.mu.Lock()          // Lock acquired at line 500
    defer cs.mu.Unlock()  // Lock released at end

    // Check already have proposal
    if cs.proposal != nil {
        return            // Checked while holding lock
    }
    // ...
    cs.proposal = proposal // Set while holding lock
}
```

The check and assignment are in the same locked section - no TOCTOU.

---

### 2. vote_tracker.go: Missing Stale Checks in Read Methods - FALSE POSITIVE

**Agent Claim**: "GetVotes/TwoThirdsMajority/MakeCommit have NO stale generation check"

**Analysis**: The stale check is specifically for `AddVote()` to prevent lost writes. Read operations on stale VoteSets are harmless:
- They return data from a previous height
- The consensus state machine already checks height/round before using vote data
- No data loss occurs from reading stale sets

The TWELFTH REFACTOR's generation counter correctly protects writes; reads don't need protection.

---

### 3. peer_state.go: Lock Ordering Violations - FALSE POSITIVE

**Agent Claim**: "Lock ordering violations in PeersAtHeight/PeersNeedingVote causing deadlocks"

**Analysis**: The documented lock ordering (lines 12-30) is correctly followed:
1. PeerSet.mu
2. PeerState.mu
3. VoteBitmap.mu

`PeersAtHeight()`:
- Holds PeerSet.mu (RLock)
- Calls p.Height() which acquires PeerState.mu (RLock)
- Order: 1 → 2 ✓

`PeersNeedingVote()`:
- Holds PeerSet.mu (RLock)
- Calls p.NeedsVote() → PeerState.mu → VoteBitmap.mu
- Order: 1 → 2 → 3 ✓

No lock ordering violation.

---

### 4. evidence/pool.go: Signature Verification Bypass - INTENTIONAL

**Agent Claim**: "Signature verification skipped when chainID==''"

**Analysis**: This is documented and intentional (evidence/pool.go:112-113):
```go
// Pass chainID for signature verification. If empty, verification is skipped
// (useful for testing, but should always be provided in production).
```

In production, `chainID` is always provided. The empty check enables testing without requiring valid signatures.

---

### 5. file_wal.go: Crash Safety Issues - FALSE POSITIVE

**Agent Claim**: "Crash during Write loses buffered data, rotation not atomic"

**Analysis**:
- CRC32 checksums (lines 636-714) detect torn/partial writes
- `WriteSync()` flushes and syncs for critical messages
- `Write()` uses buffering for performance (acceptable for non-critical messages)
- Rotation opens new segment before closing old (lines 337-370)

The design is sound for a WAL - critical messages use sync, performance messages use buffering.

---

### 6. file_pv.go: Timestamp==0 Edge Case - BACKWARD COMPATIBLE

**Agent Claim**: "Timestamp==0 could bypass idempotency check"

**Analysis**: The check `Timestamp != 0 && Timestamp != vote.Timestamp` is intentional backward compatibility:
- Old state files don't have Timestamp (defaults to 0)
- When Timestamp is 0: skip timestamp check, rely on BlockHash
- When Timestamp is non-zero: check both

This allows gradual migration from old state files while providing improved checking for new ones.

---

## Summary

| Issue | Severity | Status |
|-------|----------|--------|
| Locked/valid state not restored | HIGH | FIXED |
| SignBytesHash unused | MEDIUM | RESOLVED (Not a bug) |
| state.go TOCTOU | FALSE POSITIVE | N/A |
| vote_tracker stale reads | FALSE POSITIVE | N/A |
| peer_state lock ordering | FALSE POSITIVE | N/A |
| evidence chainID bypass | INTENTIONAL | N/A |
| WAL crash safety | FALSE POSITIVE | N/A |
| Timestamp==0 edge case | BACKWARD COMPAT | N/A |

---

## Fixes Applied

### 1. Locked/Valid State Restoration - FIXED

**Files Modified**:
- `engine/replay.go`
- `engine/state.go`

**Changes**:
1. Added `LockedRound`, `LockedBlockHash`, `ValidRound`, `ValidBlockHash` fields to `WALReplayResult`
2. Updated `replayState()` to extract locked/valid state from `ConsensusStateData`
3. Updated `ReplayCatchup()` to restore `lockedRound`, `validRound`, and attempt to restore block pointers if a matching proposal is found
4. Added `writeStateLocked()` helper in state.go to write state to WAL when locking on a block
5. Added `stepToGenerated()` helper for RoundStep conversion

**Mechanism**: When `enterPrecommitLocked()` locks on a block (sees 2/3+ prevotes), it now writes the consensus state (including locked round and hash) to WAL. On crash recovery, `ReplayCatchup()` restores this state, ensuring the node remembers it was locked and won't vote for a different block.

**Note**: Block pointers (`lockedBlock`, `validBlock`) remain nil until the block is received again via proposal or BlockSync. This is safe because:
1. The locked round prevents voting for different blocks at the same round
2. If we need to vote without the block, we vote nil (safe)
3. Once we receive a matching block, the pointers are restored

---

### 2. SignBytesHash Analysis - RESOLVED (Not a Bug)

**Files Modified**:
- `privval/file_pv.go` (documentation only)

**Changes**:
1. Updated comments in `isSameVote()` to document why current checks are sufficient
2. No functional changes needed - the existing implementation is correct
