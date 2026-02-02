# Leaderberry Design Patterns

This document catalogs the key design patterns used throughout the Leaderberry codebase, extracted from extensive code reviews and architecture analysis.

---

## Table of Contents

1. [Concurrency Patterns](#concurrency-patterns)
2. [Memory Safety Patterns](#memory-safety-patterns)
3. [Consensus Safety Patterns](#consensus-safety-patterns)
4. [Error Handling Patterns](#error-handling-patterns)
5. [Resource Management Patterns](#resource-management-patterns)

---

## Concurrency Patterns

### 1. Locked Pattern

**Problem:** Need both external API (with locking) and internal API (lock-free) for same operations.

**Solution:** Pair of methods - public acquires lock, internal assumes lock held.

**Implementation:**

```go
// Public version - acquires lock
func (cs *ConsensusState) EnterNewRound(height int64, round int32) {
    cs.mu.Lock()
    defer cs.mu.Unlock()
    cs.enterNewRoundLocked(height, round)
}

// Internal version - assumes lock held
// MUST be called with cs.mu held
func (cs *ConsensusState) enterNewRoundLocked(height int64, round int32) {
    // Safe to call other *Locked methods without deadlock
    if someCondition {
        cs.enterPrevoteLocked(height, round)
    }
}
```

**Benefits:**
- No deadlock from recursive locking
- Clear API contract (naming convention documents lock requirements)
- Performance (no repeated lock/unlock in call chain)
- Internal methods can safely compose

**Usage Count:** ~15 method pairs in ConsensusState

**When to Use:**
- Complex internal state machines with multiple transitions
- When internal methods need to call each other
- When public API needs same functionality as internal

**Naming Convention:**
- Public: `MethodName()`
- Internal: `methodNameLocked()`
- Always document: "MUST be called with mu held"

---

### 2. Generation Counter Pattern

**Problem:** After `Reset()`, old references (like VoteSet) might still exist and accept operations, silently losing data.

**Solution:** Counter incremented on reset; old references detect they're stale.

**Implementation:**

```go
type HeightVoteSet struct {
    mu         sync.RWMutex
    generation atomic.Uint64  // Incremented on Reset
    prevotes   map[int32]*VoteSet
    precommits map[int32]*VoteSet
}

type VoteSet struct {
    mu           sync.RWMutex
    parent       *HeightVoteSet
    myGeneration uint64  // Captured at creation
    votes        map[uint16]*gen.Vote
}

func newVoteSetWithParent(hvs *HeightVoteSet, round int32, voteType gen.VoteType) *VoteSet {
    return &VoteSet{
        parent:       hvs,
        myGeneration: hvs.generation.Load(),  // Capture current generation
        votes:        make(map[uint16]*gen.Vote),
    }
}

func (vs *VoteSet) AddVote(vote *gen.Vote) (bool, error) {
    vs.mu.Lock()
    defer vs.mu.Unlock()

    // Check if we're stale
    if vs.parent != nil {
        currentGen := vs.parent.generation.Load()  // Atomic read
        if currentGen != vs.myGeneration {
            return false, ErrStaleVoteSet  // REJECT!
        }
    }

    // Safe to add vote
    vs.votes[vote.ValidatorIndex] = types.CopyVote(vote)
    return true, nil
}

func (hvs *HeightVoteSet) Reset(height int64, valSet *ValidatorSet) {
    hvs.mu.Lock()
    defer hvs.mu.Unlock()

    hvs.height = height
    hvs.prevotes = make(map[int32]*VoteSet)
    hvs.precommits = make(map[int32]*VoteSet)
    hvs.generation.Add(1)  // Invalidate all old VoteSets!
}
```

**Benefits:**
- Detects stale references at runtime
- Prevents silent data loss
- Atomic generation check (no deadlock with parent)
- Fails fast with clear error

**When to Use:**
- Parent-child relationships where parent can be reset
- Long-lived references to short-lived objects
- When resource lifecycle is complex

**Key Points:**
- Use `atomic.Uint64` for generation counter (lock-free reads)
- Capture generation at child creation
- Check generation before each mutation
- Return clear error when stale

---

### 3. Callback Goroutine Pattern

**Problem:** Invoking callback while holding lock can deadlock if callback calls back into the same mutex.

**Solution:** Invoke callbacks in separate goroutine with deep-copied data.

**Implementation:**

```go
func (cs *ConsensusState) createAndSendProposalLocked() {
    // ... create proposal while holding lock ...

    if cs.onProposal != nil {
        callback := cs.onProposal         // Capture callback
        proposalCopy := types.CopyProposal(proposal)  // Deep copy
        go callback(proposalCopy)          // Async + safe
    }

    // Lock can be released safely
}
```

**Why Deep Copy?**
- Goroutine may execute after lock released
- Without copy, callback sees mutable state
- With copy, callback has independent snapshot

**Anti-Pattern (Deadlock):**

```go
// WRONG - Deadlock risk!
func (cs *ConsensusState) createAndSendProposalLocked() {
    if cs.onProposal != nil {
        cs.onProposal(proposal)  // Callback while holding lock
        // If callback calls cs.AddVote() → deadlock
    }
}
```

**When to Use:**
- Observer pattern callbacks
- Event notifications
- Network broadcast functions
- Any callback from locked context

---

## Memory Safety Patterns

### 1. Deep Copy Pattern

**Problem:** Returning pointers to internal state allows caller to corrupt that state.

**Solution:** Return deep copies of all data crossing API boundaries.

**Implementation:**

```go
// WRONG - Returns internal pointer
func (e *Engine) GetValidatorSet() *types.ValidatorSet {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.validatorSet  // Caller can modify!
}

// CORRECT - Returns deep copy
func (e *Engine) GetValidatorSet() *types.ValidatorSet {
    e.mu.RLock()
    defer e.mu.RUnlock()
    vsCopy, _ := e.validatorSet.Copy()
    return vsCopy  // Caller has independent copy
}

// Deep copy implementation
func (vs *ValidatorSet) Copy() (*ValidatorSet, error) {
    validators := make([]*NamedValidator, len(vs.Validators))
    for i, v := range vs.Validators {
        validators[i] = CopyValidator(v)  // Deep copy each validator
    }

    // Rebuild validator set from copies
    return NewValidatorSet(validators)
}

func CopyValidator(v *NamedValidator) *NamedValidator {
    if v == nil {
        return nil
    }
    return &NamedValidator{
        Name:             CopyAccountName(v.Name),
        PublicKey:        CopyPublicKey(v.PublicKey),  // Copy []byte
        VotingPower:      v.VotingPower,
        ProposerPriority: v.ProposerPriority,
        Index:            v.Index,
    }
}
```

**When to Copy:**

1. **Before returning:** Copy internal state before returning to caller
2. **Before storing:** Copy caller data before storing internally
3. **Before callbacks:** Copy data before passing to async callbacks

**Copy Functions in Codebase:**

| Function | Purpose |
|----------|---------|
| `CopyValidator()` | Deep copy validator with all fields |
| `CopyVote()` | Deep copy vote including signature |
| `CopyProposal()` | Deep copy proposal with POL votes |
| `CopyBlock()` | Deep copy block (recursive, 4 levels) |
| `CopyHash()` | Deep copy hash bytes |
| `CopySignature()` | Deep copy signature bytes |
| `CopyPublicKey()` | Deep copy public key bytes |
| `CopyBlockPart()` | Deep copy block part with proof |

**Multi-Level Deep Copy Example:**

```go
func CopyBlock(b *Block) *Block {
    if b == nil {
        return nil
    }

    blockCopy := &Block{
        Header: CopyBlockHeader(&b.Header),  // Level 2
        Data:   CopyBlockData(&b.Data),      // Level 2
    }

    // Deep copy Evidence ([][]byte - 2D slice!)
    if b.Evidence != nil {
        blockCopy.Evidence = make([][]byte, len(b.Evidence))
        for i, ev := range b.Evidence {
            if len(ev) > 0 {
                blockCopy.Evidence[i] = make([]byte, len(ev))
                copy(blockCopy.Evidence[i], ev)  // Copy each byte slice
            }
        }
    }

    blockCopy.LastCommit = CopyCommit(b.LastCommit)  // Level 2

    return blockCopy
}
```

**Common Pitfall - Shallow Slice Copy:**

```go
// WRONG - Shallow copy
func (vs *VoteSet) GetVotes() []*gen.Vote {
    votes := make([]*gen.Vote, len(vs.votes))
    copy(votes, vs.votes)  // Copies pointers, not data!
    return votes
}

// CORRECT - Deep copy
func (vs *VoteSet) GetVotes() []*gen.Vote {
    votes := make([]*gen.Vote, 0, len(vs.votes))
    for _, v := range vs.votes {
        votes = append(votes, types.CopyVote(v))  // Copy each vote
    }
    return votes
}
```

---

### 2. Duplicate Detection Pattern

**Problem:** Processing same item multiple times (e.g., same signature) can cause security issues like threshold bypass.

**Solution:** Track processed items with a map.

**Implementation:**

```go
// Example: Verify authorization with duplicate signature prevention
func VerifyAuthorization(auth *Authorization, authority *Authority) (uint32, error) {
    seenKeys := make(map[string]bool, len(auth.Signatures))
    totalWeight := uint32(0)

    for _, sig := range auth.Signatures {
        // Compute unique key for this signature
        keyID := hex.EncodeToString(sig.PublicKey.Data)

        // Check if already processed
        if seenKeys[keyID] {
            continue  // Skip duplicate (don't count weight again!)
        }

        // Verify signature
        if !crypto.Verify(sig.PublicKey, signBytes, sig.Signature) {
            return 0, ErrInvalidSignature
        }

        // Add weight
        totalWeight += sig.Weight

        // Mark as seen
        seenKeys[keyID] = true
    }

    if totalWeight < authority.Threshold {
        return totalWeight, ErrInsufficientWeight
    }

    return totalWeight, nil
}
```

**Attack Prevented:**

Without duplicate detection:
```
Threshold: 2
Key A weight: 1
Key B weight: 1

Attacker submits:
- Signature from Key A (valid)
- Signature from Key A again (duplicate!)
- Total weight: 2 → BYPASS! ❌
```

With duplicate detection:
```
First Key A signature: weight = 1
Second Key A signature: skipped (duplicate)
Total weight: 1 → REJECT ✓
```

**When to Use:**
- Processing lists with potential duplicates
- Authorization/voting systems
- Signature verification
- Any accumulation where duplicates could cause issues

---

## Consensus Safety Patterns

### 1. Atomic Persistence Pattern

**Problem:** If signature is returned before state is persisted, crash can enable double-signing.

**Solution:** Persist state BEFORE returning signature. Panic if persistence fails.

**Implementation:**

```go
func (pv *FilePV) SignVote(chainID string, vote *gen.Vote) error {
    pv.mu.Lock()
    defer pv.mu.Unlock()

    step := VoteStep(vote.Type)

    // 1. Check for double-sign
    if err := pv.lastSignState.CheckHRS(vote.Height, vote.Round, step); err != nil {
        // Check if it's the same vote (idempotency)
        signBytes := types.VoteSignBytes(chainID, vote)
        signBytesHash := types.HashBytes(signBytes)
        if types.HashEqual(*pv.lastSignState.SignBytesHash, signBytesHash) {
            // Same vote - return cached signature (idempotent)
            vote.Signature = pv.lastSignState.Signature
            return nil
        }
        // Different vote at same H/R/S - DOUBLE SIGN!
        return fmt.Errorf("%w: %v", ErrDoubleSign, err)
    }

    // 2. Sign the vote
    signBytes := types.VoteSignBytes(chainID, vote)
    sig := ed25519.Sign(pv.privKey, signBytes)

    // 3. Save old state for rollback
    oldState := pv.lastSignState

    // 4. Update in-memory state
    pv.lastSignState.Height = vote.Height
    pv.lastSignState.Round = vote.Round
    pv.lastSignState.Step = step
    pv.lastSignState.Signature = sig
    pv.lastSignState.BlockHash = vote.BlockHash
    pv.lastSignState.SignBytesHash = &signBytesHash
    pv.lastSignState.Timestamp = vote.Timestamp

    // 5. PERSIST - PANIC if fails (consensus critical!)
    if err := pv.saveState(); err != nil {
        // Rollback in-memory state
        pv.lastSignState = oldState
        panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to persist last sign state: %v", err))
    }

    // 6. ONLY NOW set the signature on vote
    // Signature is returned ONLY AFTER successful persistence
    vote.Signature = sig

    return nil
}
```

**Invariant:** `signature returned` ⟹ `state persisted`

**Why Panic on Persistence Failure?**
- Double-sign prevention is consensus-critical
- If we return error, caller might retry → double sign
- Panicking forces node to restart and operator to fix filesystem
- Better to halt than violate safety

**Attack Prevented:**

Without atomic persistence:
```
1. Sign vote for block A at H=100, R=0
2. Return signature
3. CRASH before persisting state ❌
4. Restart, state shows H=99
5. Sign vote for block B at H=100, R=0
6. DOUBLE SIGN → Byzantine fault!
```

With atomic persistence:
```
1. Sign vote for block A at H=100, R=0
2. Persist state (H=100, R=0, BlockHash=A)
3. Return signature
4. CRASH
5. Restart, state shows H=100, R=0, BlockHash=A
6. Attempt to sign for block B → REJECTED ✓
```

---

### 2. Deterministic Tie-Breaking Pattern

**Problem:** Non-deterministic operations (map iteration, equal priorities) can cause consensus forks.

**Solution:** Always use deterministic ordering for consensus-affecting decisions.

**Implementation:**

```go
// Proposer selection with priority tie-breaking
func (vs *ValidatorSet) getProposer() *NamedValidator {
    var proposer *NamedValidator

    for _, v := range vs.Validators {  // Validators slice is deterministically ordered
        if proposer == nil {
            proposer = v
            continue
        }

        // Primary: Compare priorities
        if v.ProposerPriority > proposer.ProposerPriority {
            proposer = v
        } else if v.ProposerPriority == proposer.ProposerPriority {
            // Tie-breaker: Lexicographic ordering of names
            if AccountNameString(v.Name) < AccountNameString(proposer.Name) {
                proposer = v
            }
        }
    }

    return proposer
}
```

**Why Determinism Matters:**

Without tie-breaking:
```
Node 1: alice (priority=100) vs bob (priority=100)
        → Chooses alice (map iteration order)
Node 2: alice (priority=100) vs bob (priority=100)
        → Chooses bob (different map iteration order)
FORK! Nodes propose different blocks ❌
```

With tie-breaking:
```
Node 1: alice (priority=100) vs bob (priority=100)
        → Chooses alice ("alice" < "bob" lexicographically)
Node 2: alice (priority=100) vs bob (priority=100)
        → Chooses alice ("alice" < "bob" lexicographically)
CONSENSUS! All nodes propose same block ✓
```

**Other Determinism Requirements:**

1. **Map Iteration:** Sort keys before iterating
2. **Float Arithmetic:** Avoid (use fixed-point or integers)
3. **Time:** Use block timestamp, not local time
4. **Randomness:** Use deterministic seed (block hash)

---

### 3. Pre-Operation Overflow Check Pattern

**Problem:** Checking for overflow AFTER arithmetic can miss wrapping to negative.

**Solution:** Check for overflow BEFORE performing arithmetic.

**Implementation:**

```go
// WRONG - Check after (too late!)
func IncrementProposerPriority(v *Validator) {
    v.ProposerPriority += v.VotingPower  // Could overflow!
    if v.ProposerPriority > MaxPriority {
        // Too late - already wrapped to negative
        v.ProposerPriority = MaxPriority
    }
}

// CORRECT - Check before
func IncrementProposerPriority(v *Validator) {
    // Check if addition would overflow
    if v.ProposerPriority > MaxPriority - v.VotingPower {
        v.ProposerPriority = MaxPriority  // Clamp
    } else {
        v.ProposerPriority += v.VotingPower  // Safe
    }
}
```

**Why It Matters:**

```go
// Example with 64-bit signed integers
MaxPriority = 9223372036854775807  // math.MaxInt64

priority = 9223372036854775800
votingPower = 100

// Wrong approach:
priority += votingPower  // → -9223372036854775716 (wraps negative!)
if priority > MaxPriority {  // false (negative < MaxPriority)
    // Never reached!
}
// Result: Invalid negative priority → wrong proposer → FORK!

// Correct approach:
if priority > MaxPriority - votingPower {  // true (overflow would occur)
    priority = MaxPriority  // Clamp
}
// Result: MaxPriority (safe)
```

---

## Error Handling Patterns

### 1. Error Wrapping with Context

**Pattern:** Always wrap errors with context using `%w` verb.

**Implementation:**

```go
func (e *Engine) Start(height int64, lastCommit *gen.Commit) error {
    if err := e.wal.Start(); err != nil {
        return fmt.Errorf("failed to start WAL: %w", err)
    }

    if err := e.state.Start(height, lastCommit); err != nil {
        return fmt.Errorf("failed to start consensus state: %w", err)
    }

    return nil
}
```

**Error Chain Example:**

```
failed to start consensus state:
  failed to start WAL:
    failed to open WAL segment 0:
      permission denied
```

**Benefits:**
- Clear error provenance
- Preserved error types for `errors.Is()` and `errors.As()`
- Debugging made easier

---

### 2. Panic vs Error Pattern

**Pattern:** Use panic for consensus-critical failures, return errors for recoverable issues.

**When to Panic:**

```go
// Consensus-critical: WAL write failure
if err := cs.wal.WriteSync(msg); err != nil {
    panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write own vote to WAL: %v", err))
}

// Consensus-critical: Own vote rejected
added, err := cs.votes.AddVote(vote)
if !added {
    panic("CONSENSUS CRITICAL: own vote was not added")
}
```

**When to Return Error:**

```go
// Network input: validation error
if vote.Height != vs.height {
    return false, fmt.Errorf("vote height %d doesn't match vote set height %d", vote.Height, vs.height)
}

// External operation: can retry
if err := network.Broadcast(msg); err != nil {
    return fmt.Errorf("failed to broadcast: %w", err)
}
```

**Decision Criteria:**

| Use Panic When | Use Error When |
|----------------|----------------|
| Data loss would compromise safety | Validation of external input |
| State machine invariant violated | Network/IO operations |
| Recovery impossible without restart | Expected failure conditions |
| Programming bug detected | Caller can handle gracefully |

---

## Resource Management Patterns

### 1. First-Error Pattern

**Pattern:** For cleanup operations, always complete all cleanup steps while preserving first error.

**Implementation:**

```go
func (w *FileWAL) Stop() error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if !w.started {
        return ErrWALNotStarted
    }

    var firstErr error

    // Try to flush
    if err := w.enc.Flush(); err != nil {
        firstErr = err  // Save first error
    }

    // Try to sync (even if flush failed)
    if err := w.file.Sync(); err != nil {
        if firstErr == nil {
            firstErr = err  // Save first error
        }
    }

    // Always close file (even if sync failed)
    if err := w.file.Close(); err != nil {
        if firstErr == nil {
            firstErr = err  // Save first error
        }
    }

    w.started = false

    // Return first error encountered
    return firstErr
}
```

**Why Not Short-Circuit?**

```go
// ANTI-PATTERN - Resource leak!
func (w *FileWAL) Stop() error {
    if err := w.enc.Flush(); err != nil {
        return err  // FILE NEVER CLOSED! ❌
    }
    if err := w.file.Sync(); err != nil {
        return err  // FILE NEVER CLOSED! ❌
    }
    return w.file.Close()
}
```

**Benefits:**
- No resource leaks
- All cleanup attempted
- First error preserved for debugging
- Idempotent (safe to call multiple times)

---

### 2. Lock-Then-Load Pattern

**Pattern:** For double-sign prevention, acquire exclusive lock BEFORE loading state.

**Implementation:**

```go
func LoadFilePV(keyFilePath, stateFilePath string) (*FilePV, error) {
    // 1. Acquire exclusive file lock FIRST
    lockFile, err := os.OpenFile(keyFilePath+".lock", os.O_CREATE|os.O_EXCL, 0600)
    if err != nil {
        return nil, fmt.Errorf("failed to acquire lock (another process using validator?): %w", err)
    }
    if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
        lockFile.Close()
        return nil, fmt.Errorf("failed to acquire exclusive lock: %w", err)
    }

    // 2. ONLY NOW load state (we have exclusive access)
    key, err := loadKey(keyFilePath)
    if err != nil {
        lockFile.Close()
        return nil, err
    }

    state, err := loadState(stateFilePath)
    if err != nil {
        lockFile.Close()
        return nil, err
    }

    return &FilePV{
        lockFile:      lockFile,
        privKey:       key.PrivateKey,
        pubKey:        key.PublicKey,
        lastSignState: state,
    }, nil
}
```

**Attack Prevented (TOCTOU - Time Of Check Time Of Use):**

Without lock-then-load:
```
Process A: Load state (H=99)
Process B: Load state (H=99)
Process A: Sign vote at H=100
Process A: Persist state (H=100)
Process B: Sign vote at H=100 (different block)
Process B: Persist state (H=100)
DOUBLE SIGN! ❌
```

With lock-then-load:
```
Process A: Acquire lock
Process A: Load state (H=99)
Process B: Try to acquire lock → BLOCKED
Process A: Sign vote at H=100
Process A: Persist state (H=100)
Process A: Release lock
Process B: Acquire lock
Process B: Load state (H=100) ← sees latest state
Process B: Try to sign at H=100 → REJECTED ✓
```

---

## Summary

These design patterns form the foundation of Leaderberry's safety and correctness:

**Concurrency:**
- Locked Pattern: Safe internal/external API separation
- Generation Counter: Detect stale references
- Callback Goroutine: Avoid deadlock in callbacks

**Memory Safety:**
- Deep Copy: Prevent aliasing corruption
- Duplicate Detection: Prevent double-counting attacks

**Consensus Safety:**
- Atomic Persistence: Prevent double-signing
- Deterministic Tie-Breaking: Prevent forks
- Pre-Operation Overflow Check: Prevent arithmetic errors

**Error Handling:**
- Error Wrapping: Preserve context and types
- Panic vs Error: Clear failure classification

**Resource Management:**
- First-Error: Complete all cleanup
- Lock-Then-Load: Prevent TOCTOU races

These patterns emerged from 29 code review iterations and represent battle-tested solutions to subtle consensus bugs.

---

## Related Documentation

- [Consensus Protocol](consensus-protocol.md) - Protocol specification
- [Performance](performance.md) - Performance characteristics
- [Troubleshooting](troubleshooting.md) - Common issues
