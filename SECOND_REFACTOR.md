# Second Refactor Plan

This document catalogs all issues identified during the comprehensive code review and defines the refactoring work required before the codebase is production-ready.

## Error Handling Philosophy (Reinforced)

### PANIC When:

**Consensus invariants are violated:**
- Deadlock detected (should never happen with correct code)
- Data race on consensus-critical state
- WAL write/sync fails during consensus
- State file write fails (double-sign protection compromised)
- Block execution fails after commit decision
- Validator set becomes invalid during consensus
- Vote tracking corruption detected
- Proposal/vote signature creation fails
- Any state that would cause consensus to halt or fork

**Key Principle**: A crashed node is better than a node that violates consensus safety or causes a chain split.

### ERROR When:

**Input validation that doesn't affect local consensus state:**
- Malformed network messages
- Invalid signatures on received messages
- Messages for wrong height/round (stale or future)
- Duplicate or conflicting votes from peers (evidence, not our fault)
- Proposal from non-proposer
- Configuration validation failures at startup

**Key Principle**: Bad input from the network should never crash the node, only be rejected.

---

## Critical Issues (Deadlocks & Data Races)

### CR1. DEADLOCK in finalizeCommit → enterNewRound

**Location**: `engine/state.go:534`

**Problem**: When `SkipTimeoutCommit=true`, `finalizeCommit` calls `enterNewRound` while holding `cs.mu`. But `enterNewRound` also tries to acquire `cs.mu`, causing a deadlock.

**Current Code**:
```go
func (cs *ConsensusState) finalizeCommit(height int64, commit *gen.Commit) {
    // ... already holding cs.mu from handlePrecommit → enterCommit ...

    if !cs.config.SkipTimeoutCommit {
        cs.scheduleTimeout(...)
    } else {
        cs.enterNewRound(cs.height, 0)  // DEADLOCK: enterNewRound does cs.mu.Lock()
    }
}

func (cs *ConsensusState) enterNewRound(height int64, round int32) {
    cs.mu.Lock()  // DEADLOCK HERE
    defer cs.mu.Unlock()
    // ...
}
```

**Required Changes**:
1. Create internal `enterNewRoundLocked` that assumes lock is held
2. Public `enterNewRound` acquires lock and calls internal version
3. `finalizeCommit` calls internal version

**Implementation**:
```go
// enterNewRound enters a new round (public, acquires lock)
func (cs *ConsensusState) enterNewRound(height int64, round int32) {
    cs.mu.Lock()
    defer cs.mu.Unlock()
    cs.enterNewRoundLocked(height, round)
}

// enterNewRoundLocked enters a new round (internal, caller must hold lock)
func (cs *ConsensusState) enterNewRoundLocked(height int64, round int32) {
    if cs.height != height || round < cs.round {
        return
    }
    // ... rest of current enterNewRound logic ...
}

func (cs *ConsensusState) finalizeCommit(height int64, commit *gen.Commit) {
    // ... (already holding lock) ...

    if !cs.config.SkipTimeoutCommit {
        cs.scheduleTimeout(...)
    } else {
        cs.enterNewRoundLocked(cs.height, 0)  // Use locked version
    }
}
```

**Apply same pattern to**: `enterPrevote`, `enterPrevoteWait`, `enterPrecommit`, `enterPrecommitWait`, `enterCommit`

---

### CR2. Race Condition in HeightVoteSet.AddVote

**Location**: `engine/vote_tracker.go:337-339`

**Problem**: Mutex is released before calling `voteSet.AddVote()`. Another goroutine could call `Reset()` between the unlock and AddVote, corrupting state.

**Current Code**:
```go
func (hvs *HeightVoteSet) AddVote(vote *gen.Vote) (bool, error) {
    hvs.mu.Lock()

    var voteSet *VoteSet
    if vote.Type == types.VoteTypePrevote {
        voteSet = hvs.prevotes[vote.Round]
        if voteSet == nil {
            voteSet = NewVoteSet(...)
            hvs.prevotes[vote.Round] = voteSet
        }
    } // ... similar for precommit ...

    hvs.mu.Unlock()  // RELEASES LOCK
    return voteSet.AddVote(vote)  // RACE: Reset() could run here
}
```

**Required Changes**:
1. Keep lock held during entire operation
2. Or use a different synchronization strategy (per-round locks)

**Implementation**:
```go
func (hvs *HeightVoteSet) AddVote(vote *gen.Vote) (bool, error) {
    hvs.mu.Lock()
    defer hvs.mu.Unlock()  // Keep lock for entire operation

    if vote.Height != hvs.height {
        return false, ErrInvalidHeight
    }

    var voteSet *VoteSet
    if vote.Type == types.VoteTypePrevote {
        voteSet = hvs.prevotes[vote.Round]
        if voteSet == nil {
            voteSet = NewVoteSet(hvs.chainID, hvs.height, vote.Round, types.VoteTypePrevote, hvs.validatorSet)
            hvs.prevotes[vote.Round] = voteSet
        }
    } else if vote.Type == types.VoteTypePrecommit {
        voteSet = hvs.precommits[vote.Round]
        if voteSet == nil {
            voteSet = NewVoteSet(hvs.chainID, hvs.height, vote.Round, types.VoteTypePrecommit, hvs.validatorSet)
            hvs.precommits[vote.Round] = voteSet
        }
    } else {
        return false, ErrInvalidVote
    }

    // VoteSet has its own mutex, so this is safe
    return voteSet.AddVote(vote)
}
```

---

### CR3. Race Condition in ConsensusState.Start

**Location**: `engine/state.go:148-151`

**Problem**: `Start()` releases the mutex then calls `enterNewRound()` which accesses state fields without synchronization.

**Current Code**:
```go
func (cs *ConsensusState) Start(height int64, lastCommit *gen.Commit) error {
    cs.mu.Lock()
    // ... setup state ...
    cs.mu.Unlock()  // RELEASES LOCK

    // RACE: state can be modified by other goroutines here
    cs.enterNewRound(height, 0)  // Accesses cs.height, cs.round, etc.

    return nil
}
```

**Required Changes**:
1. Call `enterNewRoundLocked` while still holding the lock
2. Or use a started flag to prevent other operations until fully started

**Implementation**:
```go
func (cs *ConsensusState) Start(height int64, lastCommit *gen.Commit) error {
    cs.mu.Lock()
    defer cs.mu.Unlock()

    if cs.started {
        return ErrAlreadyStarted
    }

    cs.ctx, cs.cancel = context.WithCancel(context.Background())
    cs.height = height
    cs.lastCommit = lastCommit
    cs.votes = NewHeightVoteSet(cs.config.ChainID, height, cs.validatorSet)
    cs.started = true

    // Start timeout ticker
    cs.timeoutTicker.Start()

    // Start main loop
    cs.wg.Add(1)
    go cs.receiveRoutine()

    // Enter new round while holding lock
    cs.enterNewRoundLocked(height, 0)

    return nil
}
```

---

### CR4. Data Race on ValidatorSet Between Engine and ConsensusState

**Location**: `engine/state.go:521` + `engine/engine.go:193-197`

**Problem**: `Engine.UpdateValidatorSet` and `ConsensusState` operations both access the validator set, but use different mutexes.

**Current Code**:
```go
// In Engine (holds e.mu):
func (e *Engine) UpdateValidatorSet(valSet *types.ValidatorSet) {
    e.mu.Lock()
    defer e.mu.Unlock()
    e.validatorSet = valSet  // Updates pointer
}

// In ConsensusState (holds cs.mu):
func (cs *ConsensusState) finalizeCommit(...) {
    cs.validatorSet.IncrementProposerPriority(1)  // Modifies same object
}
```

**Required Changes**:
1. ValidatorSet should be immutable - never modify in place
2. Updates should create a new copy
3. ConsensusState should own its validator set, Engine updates via message

**Implementation**:
```go
// ValidatorSet is immutable after creation. To update, create a new one.
// Remove IncrementProposerPriority mutation, replace with:

func (vs *ValidatorSet) WithIncrementedPriority(times int32) (*ValidatorSet, error) {
    // Create deep copy
    copy, err := vs.Copy()
    if err != nil {
        return nil, err
    }

    // Increment on copy
    copy.incrementProposerPriorityInternal(times)

    return copy, nil
}

// In ConsensusState:
func (cs *ConsensusState) finalizeCommit(...) {
    // ...

    // Create new validator set with updated priorities
    newValSet, err := cs.validatorSet.WithIncrementedPriority(1)
    if err != nil {
        panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to update validator set: %v", err))
    }
    cs.validatorSet = newValSet

    // ...
}

// In Engine - remove UpdateValidatorSet or make it send via channel
func (e *Engine) UpdateValidatorSet(valSet *types.ValidatorSet) {
    // Send update via channel to consensus state, don't modify directly
    e.valSetUpdateCh <- valSet
}
```

---

### CR5. Ignored Vote Error After Signing

**Location**: `engine/state.go:672`

**Problem**: After signing a vote, we add it to our vote set but ignore the error. If this fails, we've signed but not tracked the vote.

**Current Code**:
```go
func (cs *ConsensusState) signAndSendVote(...) {
    // ... create and sign vote ...

    cs.votes.AddVote(vote)  // ERROR IGNORED - consensus critical!

    // Broadcast vote
}
```

**Required Changes**:
1. Check error from AddVote
2. PANIC if our own signed vote can't be added (consensus corruption)

**Implementation**:
```go
func (cs *ConsensusState) signAndSendVote(voteType types.VoteType, blockHash *types.Hash) {
    // ... create and sign vote ...

    // Add to our own vote set - PANIC on failure (our own vote must be tracked)
    added, err := cs.votes.AddVote(vote)
    if err != nil {
        panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to add own vote: %v", err))
    }
    if !added {
        // This shouldn't happen for our own fresh vote
        panic("CONSENSUS CRITICAL: own vote was not added (duplicate?)")
    }

    // Broadcast vote
}
```

---

## High Severity Issues

### H1. WAL Never Written During Consensus

**Location**: `engine/state.go` (entire file)

**Problem**: The consensus state machine has a `wal` field but never writes to it. Proposals, votes, and state transitions are not persisted. Crash recovery is impossible.

**Required Changes**:
1. Write proposal to WAL before processing
2. Write our own votes to WAL before sending
3. Write state transitions to WAL
4. Write EndHeight after commit
5. PANIC on WAL write failures

**Implementation**:
```go
func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
    cs.mu.Lock()
    defer cs.mu.Unlock()

    // ... validation ...

    // Write to WAL BEFORE processing - PANIC on failure
    if cs.wal != nil {
        msg, err := wal.NewProposalMessage(proposal.Height, proposal.Round, proposal)
        if err != nil {
            panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to create WAL message: %v", err))
        }
        if err := cs.wal.WriteSync(msg); err != nil {
            panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write proposal to WAL: %v", err))
        }
    }

    // Accept proposal
    cs.proposal = proposal
    cs.proposalBlock = &proposal.Block

    cs.enterPrevoteLocked(cs.height, cs.round)
}

func (cs *ConsensusState) signAndSendVote(...) {
    // ... create and sign vote ...

    // Write to WAL BEFORE adding to vote set
    if cs.wal != nil {
        msg, err := wal.NewVoteMessage(vote.Height, vote.Round, vote)
        if err != nil {
            panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to create WAL message: %v", err))
        }
        if err := cs.wal.WriteSync(msg); err != nil {
            panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write vote to WAL: %v", err))
        }
    }

    // Add to vote set
    // ...
}

func (cs *ConsensusState) finalizeCommit(height int64, commit *gen.Commit) {
    // ... apply block ...

    // Write EndHeight to WAL
    if cs.wal != nil {
        msg := wal.NewEndHeightMessage(height)
        if err := cs.wal.WriteSync(msg); err != nil {
            panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to write EndHeight to WAL: %v", err))
        }
    }

    // ... continue to next height ...
}
```

---

### H2. Pointer to Proposal Block Field (Aliasing Bug)

**Location**: `engine/state.go:357`

**Problem**: `cs.proposalBlock = &proposal.Block` stores a pointer to a field within the proposal struct. If the proposal is modified or garbage collected, `proposalBlock` becomes invalid.

**Current Code**:
```go
func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
    // ...
    cs.proposal = proposal
    cs.proposalBlock = &proposal.Block  // DANGER: pointer into proposal
}
```

**Required Changes**:
1. Make a deep copy of the block
2. Or ensure proposal is never modified after acceptance

**Implementation**:
```go
func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
    // ... validation ...

    // Make a copy of the block to avoid aliasing
    blockCopy := proposal.Block  // Value copy (Block is a struct)

    cs.proposal = proposal
    cs.proposalBlock = &blockCopy

    cs.enterPrevoteLocked(cs.height, cs.round)
}
```

**Note**: Ensure `Block` doesn't contain pointer fields that need deep copying. If it does, implement `Block.Copy()`.

---

### H3. ScheduleTimeout Can Block Forever

**Location**: `engine/timeout.go:124`

**Problem**: `ScheduleTimeout` does a blocking send on `tickCh`. If the channel buffer is full and the run() goroutine is blocked, the caller hangs forever.

**Current Code**:
```go
func (tt *TimeoutTicker) ScheduleTimeout(ti TimeoutInfo) {
    tt.tickCh <- ti  // BLOCKING - can hang forever
}
```

**Required Changes**:
1. Use non-blocking send with logging
2. Or increase buffer size significantly
3. Or use select with context/timeout

**Implementation**:
```go
func (tt *TimeoutTicker) ScheduleTimeout(ti TimeoutInfo) {
    select {
    case tt.tickCh <- ti:
        // Successfully scheduled
    default:
        // Channel full - this indicates a serious problem
        // Log and drop (timeout will be rescheduled on next state transition)
        count := atomic.AddUint64(&tt.droppedSchedules, 1)
        log.Printf("WARN: timeout schedule dropped due to full channel: height=%d round=%d step=%d total_dropped=%d",
            ti.Height, ti.Round, ti.Step, count)
    }
}
```

---

### H4. TimeoutTicker.Stop Doesn't Wait for Goroutine

**Location**: `engine/timeout.go:102-115`

**Problem**: `Stop()` closes `stopCh` and returns immediately. The `run()` goroutine may still be executing, causing use-after-close or timer callbacks running after Stop.

**Current Code**:
```go
func (tt *TimeoutTicker) Stop() {
    tt.mu.Lock()
    defer tt.mu.Unlock()

    if !tt.running {
        return
    }
    tt.running = false

    close(tt.stopCh)  // Signal stop
    if tt.timer != nil {
        tt.timer.Stop()
    }
    // RETURNS IMMEDIATELY - goroutine may still be running
}
```

**Required Changes**:
1. Add a WaitGroup to track the goroutine
2. Wait for goroutine to finish before returning

**Implementation**:
```go
type TimeoutTicker struct {
    // ... existing fields ...
    wg sync.WaitGroup
}

func (tt *TimeoutTicker) Start() {
    tt.mu.Lock()
    defer tt.mu.Unlock()

    if tt.running {
        return
    }
    tt.running = true
    tt.stopCh = make(chan struct{})  // Fresh channel for each start

    tt.wg.Add(1)
    go tt.run()
}

func (tt *TimeoutTicker) Stop() {
    tt.mu.Lock()
    if !tt.running {
        tt.mu.Unlock()
        return
    }
    tt.running = false

    close(tt.stopCh)
    if tt.timer != nil {
        tt.timer.Stop()
    }
    tt.mu.Unlock()

    // Wait for goroutine to finish
    tt.wg.Wait()
}

func (tt *TimeoutTicker) run() {
    defer tt.wg.Done()
    // ... rest of run() ...
}
```

---

### H5. TwoThirdsMajority Potential Overflow

**Location**: `types/validator.go:168-169`

**Problem**: `TotalPower * 2` could overflow for very large voting powers, even though we have MaxTotalVotingPower.

**Current Code**:
```go
func (vs *ValidatorSet) TwoThirdsMajority() int64 {
    return (vs.TotalPower * 2 / 3) + 1  // TotalPower * 2 can overflow
}
```

**Required Changes**:
1. Use overflow-safe arithmetic
2. Reorder operations to avoid overflow

**Implementation**:
```go
func (vs *ValidatorSet) TwoThirdsMajority() int64 {
    // Avoid overflow by dividing first, then adjusting
    // 2/3 majority means > 2/3, so we need (2*total/3) + 1
    // Rewrite as: total/3 + total/3 + 1 + adjustment for remainder

    third := vs.TotalPower / 3
    remainder := vs.TotalPower % 3

    // 2/3 = third + third
    twoThirds := third + third

    // If there's a remainder of 2, we need to add 1 more to get true 2/3
    if remainder == 2 {
        twoThirds++
    }

    // +1 to require strictly greater than 2/3
    return twoThirds + 1
}
```

---

### H6. WAL Legacy Migration Can Lose Data

**Location**: `wal/file_wal.go:139-143`

**Problem**: If renaming legacy WAL file fails, we silently continue with segment-0, losing all WAL data.

**Current Code**:
```go
if err := os.Rename(legacyPath, newPath); err == nil {
    return 0
}
// If rename fails, silently return 0 - DATA LOST
return 0
```

**Required Changes**:
1. Return error on rename failure
2. Or copy file contents instead of renaming
3. Log warning if migration needed

**Implementation**:
```go
func (w *FileWAL) findHighestSegmentIndex() (int, error) {
    legacyPath := filepath.Join(w.dir, "wal")
    if _, err := os.Stat(legacyPath); err == nil {
        newPath := w.segmentPath(0)
        if err := os.Rename(legacyPath, newPath); err != nil {
            return 0, fmt.Errorf("failed to migrate legacy WAL file: %w", err)
        }
        log.Printf("INFO: migrated legacy WAL to segmented format")
        return 0, nil
    }

    // ... rest of function ...
    return highest, nil
}

func (w *FileWAL) Start() error {
    w.mu.Lock()
    defer w.mu.Unlock()

    // ...

    idx, err := w.findHighestSegmentIndex()
    if err != nil {
        return fmt.Errorf("failed to find WAL segments: %w", err)
    }
    w.segmentIndex = idx

    // ...
}
```

---

## Medium Severity Issues

### M1. Shallow Copy of AccountName in ValidatorSet.Copy

**Location**: `types/validator.go:210-216`

**Problem**: `Copy()` creates new validator structs but `Name` contains a pointer to string that is shared.

**Current Code**:
```go
func (vs *ValidatorSet) Copy() (*ValidatorSet, error) {
    validators := make([]*NamedValidator, len(vs.Validators))
    for i, v := range vs.Validators {
        validators[i] = &NamedValidator{
            Name:             v.Name,  // AccountName has *string - shared!
            // ...
        }
    }
    return NewValidatorSet(validators)
}
```

**Required Changes**:
1. Deep copy the AccountName

**Implementation**:
```go
// In types/account.go:
func CopyAccountName(a AccountName) AccountName {
    if a.Name == nil {
        return AccountName{}
    }
    nameCopy := *a.Name
    return AccountName{Name: &nameCopy}
}

// In types/validator.go:
func (vs *ValidatorSet) Copy() (*ValidatorSet, error) {
    validators := make([]*NamedValidator, len(vs.Validators))
    for i, v := range vs.Validators {
        validators[i] = &NamedValidator{
            Name:             CopyAccountName(v.Name),  // Deep copy
            Index:            v.Index,
            PublicKey:        v.PublicKey,  // PublicKey.Data is []byte, needs copy too
            VotingPower:      v.VotingPower,
            ProposerPriority: v.ProposerPriority,
        }
        // Deep copy public key data
        if len(v.PublicKey.Data) > 0 {
            validators[i].PublicKey.Data = make([]byte, len(v.PublicKey.Data))
            copy(validators[i].PublicKey.Data, v.PublicKey.Data)
        }
    }
    return NewValidatorSet(validators)
}
```

---

### M2. No POL (Proof of Lock) Validation

**Location**: `engine/state.go:298-307`, `engine/state.go:321-361`

**Problem**: Proposals with `PolRound >= 0` carry POL votes that prove the proposer was locked. These votes are never verified.

**Required Changes**:
1. Verify POL votes have valid signatures
2. Verify POL votes are from valid validators
3. Verify POL votes total to 2/3+ for the block
4. Reject proposal if POL is invalid

**Implementation**:
```go
func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
    cs.mu.Lock()
    defer cs.mu.Unlock()

    // ... basic validation ...

    // Validate POL if present
    if proposal.PolRound >= 0 {
        if err := cs.validatePOL(proposal); err != nil {
            log.Printf("DEBUG: proposal POL validation failed: %v", err)
            return  // Reject proposal with invalid POL
        }
    }

    // ... rest of handleProposal ...
}

func (cs *ConsensusState) validatePOL(proposal *gen.Proposal) error {
    if len(proposal.PolVotes) == 0 {
        return errors.New("POL round set but no POL votes")
    }

    blockHash := types.BlockHash(&proposal.Block)
    var polPower int64
    seenValidators := make(map[uint16]bool)

    for i, vote := range proposal.PolVotes {
        // Must be prevote
        if vote.Type != types.VoteTypePrevote {
            return fmt.Errorf("POL vote %d is not a prevote", i)
        }

        // Must be for the correct height/round
        if vote.Height != proposal.Height || vote.Round != proposal.PolRound {
            return fmt.Errorf("POL vote %d has wrong height/round", i)
        }

        // Must be for this block
        if vote.BlockHash == nil || !types.HashEqual(*vote.BlockHash, blockHash) {
            return fmt.Errorf("POL vote %d is for different block", i)
        }

        // Validator must exist and not be duplicate
        val := cs.validatorSet.GetByIndex(vote.ValidatorIndex)
        if val == nil {
            return fmt.Errorf("POL vote %d from unknown validator %d", i, vote.ValidatorIndex)
        }
        if seenValidators[vote.ValidatorIndex] {
            return fmt.Errorf("duplicate POL vote from validator %d", vote.ValidatorIndex)
        }
        seenValidators[vote.ValidatorIndex] = true

        // Verify signature
        if err := types.VerifyVoteSignature(cs.config.ChainID, &vote, val.PublicKey); err != nil {
            return fmt.Errorf("POL vote %d has invalid signature: %w", i, err)
        }

        polPower += val.VotingPower
    }

    // Must have 2/3+ power
    if polPower < cs.validatorSet.TwoThirdsMajority() {
        return fmt.Errorf("POL has insufficient power: %d < %d", polPower, cs.validatorSet.TwoThirdsMajority())
    }

    return nil
}
```

---

### M3. Evidence Pool Not Integrated

**Location**: `evidence/pool.go`, `engine/state.go`

**Problem**: Evidence pool exists but is not connected to consensus. Equivocation is never detected or broadcast.

**Required Changes**:
1. Add evidence pool to ConsensusState
2. Check incoming votes for equivocation
3. Add detected evidence to pool
4. Include evidence in block proposals
5. Broadcast evidence to peers

**Implementation**:
```go
// In engine/state.go:

type ConsensusState struct {
    // ... existing fields ...
    evidencePool *evidence.Pool
}

func (cs *ConsensusState) handleVote(vote *gen.Vote) {
    cs.mu.Lock()
    defer cs.mu.Unlock()

    // Check for equivocation BEFORE adding
    if cs.evidencePool != nil {
        dve, err := cs.evidencePool.CheckVote(vote, cs.validatorSet)
        if err != nil {
            log.Printf("DEBUG: evidence check error: %v", err)
        }
        if dve != nil {
            // Found equivocation! Add to evidence pool
            log.Printf("WARN: detected equivocation from validator %s at H=%d R=%d",
                types.AccountNameString(vote.Validator), vote.Height, vote.Round)
            if err := cs.evidencePool.AddDuplicateVoteEvidence(dve); err != nil {
                log.Printf("DEBUG: failed to add evidence: %v", err)
            }
            // TODO: Broadcast evidence to peers
        }
    }

    // Add vote (even if equivocation - we still track it)
    added, err := cs.votes.AddVote(vote)
    // ...
}

func (cs *ConsensusState) createAndSendProposal() {
    // ... create block ...

    // Add pending evidence to block
    if cs.evidencePool != nil && cs.blockExecutor != nil {
        pendingEvidence := cs.evidencePool.PendingEvidence(cs.config.MaxEvidenceBytes)
        // Include evidence in block data
    }

    // ...
}
```

---

### M4. Message Length Check Off-by-One

**Location**: `engine/engine.go:235`

**Problem**: Check is `< 2` but should be `< 1`. A 1-byte message (type only) could be valid.

**Current Code**:
```go
func (e *Engine) HandleConsensusMessage(peerID string, data []byte) error {
    if len(data) < 2 {  // Should be < 1
        return ErrInvalidMessage
    }
```

**Required Changes**:
1. Change to `< 1`
2. Add explicit empty payload handling per message type

**Implementation**:
```go
func (e *Engine) HandleConsensusMessage(peerID string, data []byte) error {
    if len(data) < 1 {
        return ErrInvalidMessage
    }

    msgType := ConsensusMessageType(data[0])
    payload := data[1:]  // May be empty

    switch msgType {
    case ConsensusMessageTypeProposal:
        if len(payload) == 0 {
            return fmt.Errorf("%w: empty proposal payload", ErrInvalidMessage)
        }
        // ...
    case ConsensusMessageTypeVote:
        if len(payload) == 0 {
            return fmt.Errorf("%w: empty vote payload", ErrInvalidMessage)
        }
        // ...
    }
}
```

---

### M5. GetAddress Returns Internal Slice

**Location**: `privval/file_pv.go:392-396`

**Problem**: `GetAddress` returns a slice of the internal public key data. Callers can modify it.

**Current Code**:
```go
func (pv *FilePV) GetAddress() []byte {
    if len(pv.pubKey.Data) >= 20 {
        return pv.pubKey.Data[:20]  // Returns slice of internal data
    }
    return pv.pubKey.Data
}
```

**Required Changes**:
1. Return a copy

**Implementation**:
```go
func (pv *FilePV) GetAddress() []byte {
    var addr []byte
    if len(pv.pubKey.Data) >= 20 {
        addr = make([]byte, 20)
        copy(addr, pv.pubKey.Data[:20])
    } else {
        addr = make([]byte, len(pv.pubKey.Data))
        copy(addr, pv.pubKey.Data)
    }
    return addr
}
```

---

### M6. isSameVote Signature Check Missing

**Location**: `privval/file_pv.go:483-490`

**Problem**: Returns true for matching block hashes but doesn't verify the signatures match. Could return wrong cached signature.

**Current Code**:
```go
func (pv *FilePV) isSameVote(vote *gen.Vote) bool {
    if pv.lastSignState.BlockHash == nil && vote.BlockHash == nil {
        return true  // Both nil - but signatures could differ!
    }
    // ...
}
```

**Required Changes**:
1. Also verify that re-signing would produce the same signature
2. Or store enough information to reconstruct the sign bytes

**Implementation**:
```go
func (pv *FilePV) isSameVote(vote *gen.Vote) bool {
    // Block hash must match
    if pv.lastSignState.BlockHash == nil && vote.BlockHash == nil {
        // Both nil - OK
    } else if pv.lastSignState.BlockHash == nil || vote.BlockHash == nil {
        return false
    } else if !types.HashEqual(*pv.lastSignState.BlockHash, *vote.BlockHash) {
        return false
    }

    // Verify all fields that affect sign bytes match
    // This is implicitly true because CheckHRS already verified H/R/S match
    // and we just verified block hash matches
    return true
}
```

---

### M7. enterCommit Nil Block Handling

**Location**: `engine/state.go:473-487`

**Problem**: If all rounds have 2/3+ precommits for nil, the loop completes without taking action.

**Current Code**:
```go
func (cs *ConsensusState) enterCommit(height int64) {
    // ...
    for round := cs.round; round >= 0; round-- {
        precommits := cs.votes.Precommits(round)
        if precommits == nil {
            continue
        }
        blockHash, ok := precommits.TwoThirdsMajority()
        if ok && blockHash != nil && !types.IsHashEmpty(blockHash) {
            // Only handles non-nil commits
            commit := precommits.MakeCommit()
            if commit != nil {
                cs.finalizeCommit(height, commit)
                return
            }
        }
    }
    // Falls through if 2/3+ committed nil - DOES NOTHING
}
```

**Required Changes**:
1. This function should only be called when there IS a non-nil commit
2. Add panic if no commit found (indicates bug in state machine)

**Implementation**:
```go
func (cs *ConsensusState) enterCommitLocked(height int64) {
    if cs.height != height || cs.step >= RoundStepCommit {
        return
    }

    cs.step = RoundStepCommit

    // Find the round that committed a non-nil block
    for round := cs.round; round >= 0; round-- {
        precommits := cs.votes.Precommits(round)
        if precommits == nil {
            continue
        }
        blockHash, ok := precommits.TwoThirdsMajority()
        if ok && blockHash != nil && !types.IsHashEmpty(blockHash) {
            commit := precommits.MakeCommit()
            if commit != nil {
                cs.finalizeCommitLocked(height, commit)
                return
            }
        }
    }

    // If we reach here, enterCommit was called incorrectly
    // This indicates a bug in the state machine
    panic(fmt.Sprintf("CONSENSUS CRITICAL: enterCommit called at height %d but no commit found", height))
}
```

---

### M8. No Broadcast Implementation

**Location**: `engine/state.go:318`, `engine/state.go:674`

**Problem**: Proposals and votes are created but never broadcast. Comments say "via callback, not implemented here" but there's no callback mechanism.

**Required Changes**:
1. Add broadcast callbacks to ConsensusState
2. Call callbacks after creating proposals/votes
3. Or use channels to emit messages

**Implementation**:
```go
type ConsensusState struct {
    // ... existing fields ...

    // Broadcast callbacks
    onProposal func(*gen.Proposal)
    onVote     func(*gen.Vote)
}

func (cs *ConsensusState) SetBroadcastCallbacks(
    onProposal func(*gen.Proposal),
    onVote func(*gen.Vote),
) {
    cs.mu.Lock()
    defer cs.mu.Unlock()
    cs.onProposal = onProposal
    cs.onVote = onVote
}

func (cs *ConsensusState) createAndSendProposal() {
    // ... create and sign proposal ...

    cs.proposal = proposal
    cs.proposalBlock = block

    // Broadcast to peers
    if cs.onProposal != nil {
        cs.onProposal(proposal)
    }
}

func (cs *ConsensusState) signAndSendVoteLocked(...) {
    // ... create and sign vote ...

    // Broadcast to peers
    if cs.onVote != nil {
        cs.onVote(vote)
    }
}
```

---

## Low Severity Issues

### L1. Binary String Map Keys

**Location**: `engine/vote_tracker.go:270`

**Problem**: Using `string(h.Data)` for binary data as map keys is inefficient and hard to debug.

**Current Code**:
```go
func blockHashKey(h *types.Hash) string {
    if h == nil || types.IsHashEmpty(h) {
        return ""
    }
    return string(h.Data)  // Binary data as string
}
```

**Required Changes**:
1. Use hex encoding for debuggability
2. Or use a proper hash type as map key

**Implementation**:
```go
func blockHashKey(h *types.Hash) string {
    if h == nil || types.IsHashEmpty(h) {
        return "nil"
    }
    return hex.EncodeToString(h.Data)
}
```

---

### L2. GC Pressure in WAL Decoder

**Location**: `wal/file_wal.go:595`

**Problem**: Allocates a new byte slice for every message, creating GC pressure in high-throughput scenarios.

**Current Code**:
```go
func (d *decoder) Decode() (*Message, error) {
    // ...
    data := make([]byte, length)  // Allocates for EVERY message
    // ...
}
```

**Required Changes**:
1. Use a byte pool for message buffers
2. Or reuse decoder buffer for small messages

**Implementation**:
```go
var messagePool = sync.Pool{
    New: func() interface{} {
        // Pre-allocate reasonable size
        return make([]byte, 0, 4096)
    },
}

func (d *decoder) Decode() (*Message, error) {
    // ... read length ...

    // Get buffer from pool
    data := messagePool.Get().([]byte)
    if cap(data) < int(length) {
        // Need larger buffer
        data = make([]byte, length)
    } else {
        data = data[:length]
    }

    // ... read and process ...

    // Message takes ownership of data, so we create a copy for the pool
    msgData := make([]byte, len(data))
    copy(msgData, data)

    // Return original to pool
    messagePool.Put(data[:0])

    // Use msgData in message
    // ...
}
```

---

### L3. Bubble Sort for Segments

**Location**: `wal/file_wal.go:680-686`

**Problem**: O(n²) bubble sort for sorting segment indices.

**Current Code**:
```go
for i := 0; i < len(segments)-1; i++ {
    for j := i + 1; j < len(segments); j++ {
        if segments[i] > segments[j] {
            segments[i], segments[j] = segments[j], segments[i]
        }
    }
}
```

**Required Changes**:
1. Use `sort.Ints()`

**Implementation**:
```go
import "sort"

func findSegments(dir string) []int {
    // ... collect segment indices ...

    sort.Ints(segments)

    return segments
}
```

---

### L4. Arbitrary Evidence Size Estimate

**Location**: `evidence/pool.go:185`

**Problem**: The 50-byte overhead estimate is arbitrary and could be wrong.

**Current Code**:
```go
evSize := int64(len(ev.Data) + 50)  // 50 is arbitrary
```

**Required Changes**:
1. Calculate actual serialized size
2. Or use a more accurate estimate based on schema

**Implementation**:
```go
func evidenceSize(ev *gen.Evidence) int64 {
    // Evidence struct overhead: type (4) + height (8) + time (8) + data length prefix (4)
    const overhead = 24
    return int64(overhead + len(ev.Data))
}

// In PendingEvidence:
evSize := evidenceSize(ev)
```

---

### L5. ValidatorSet Hash Panics on Nil Name

**Location**: `types/validator.go:266-267`

**Problem**: `AccountNameString` dereferences a pointer that could be nil.

**Current Code**:
```go
sort.Slice(sorted, func(i, j int) bool {
    return AccountNameString(sorted[i].Name) < AccountNameString(sorted[j].Name)
})
```

**Required Changes**:
1. `AccountNameString` already handles nil (returns "")
2. But validator creation should prevent nil names

**Implementation**:
```go
// In NewValidatorSet:
func NewValidatorSet(validators []*NamedValidator) (*ValidatorSet, error) {
    // ... existing validation ...

    for i, v := range validators {
        if IsAccountNameEmpty(v.Name) {
            return nil, fmt.Errorf("validator %d has empty name", i)
        }
        // ...
    }

    // ...
}
```

---

### L6. centerPriorities Precision Loss

**Location**: `types/validator.go:129`

**Problem**: Integer division loses precision when centering priorities.

**Current Code**:
```go
avg := sum / int64(len(vs.Validators))
```

**Required Changes**:
1. Document that precision loss is acceptable
2. Or use a different centering algorithm

**Implementation** (document it):
```go
// centerPriorities centers the priorities around zero.
// Note: Integer division means centering is approximate. This is acceptable
// as the important property is that priorities remain bounded, not exact centering.
func (vs *ValidatorSet) centerPriorities() {
    if len(vs.Validators) == 0 {
        return
    }

    var sum int64
    for _, v := range vs.Validators {
        sum += v.ProposerPriority
    }
    // Integer division - some precision loss is acceptable
    avg := sum / int64(len(vs.Validators))

    for _, v := range vs.Validators {
        v.ProposerPriority -= avg
    }
}
```

---

## Missing Functionality

### MF1. Block Parts / Chunking

**Problem**: Real Tendermint breaks blocks into parts for efficient gossip. This implementation only handles whole blocks.

**Impact**: Large blocks cannot be efficiently gossiped; all-or-nothing transmission.

**Required Changes**:
1. Implement BlockParts structure
2. Implement part set for tracking received parts
3. Update gossip to request/send parts

---

### MF2. Catch-up / Fast Sync

**Problem**: No mechanism for a fallen-behind node to catch up.

**Impact**: Nodes that restart or have network issues cannot recover.

**Required Changes**:
1. Implement block sync protocol
2. Add state sync support
3. Implement commit certificate verification for catch-up

---

### MF3. Validator Set Updates from Blocks

**Problem**: No mechanism to update the validator set from block execution results.

**Impact**: Validator set is static; can't add/remove validators.

**Required Changes**:
1. BlockExecutor should return validator set updates
2. Apply updates after block commit
3. Persist validator set changes

---

### MF4. Commit Certificate Verification

**Problem**: No way to verify commits from peers (for catch-up, light clients).

**Impact**: Cannot implement light clients or verify historical blocks.

**Required Changes**:
1. Implement VerifyCommit function
2. Verify all signatures in commit
3. Verify 2/3+ power committed

---

### MF5. Peer Management for Consensus

**Problem**: No tracking of which peers have which messages.

**Impact**: Inefficient gossip; no way to know what to request from whom.

**Required Changes**:
1. Track peer consensus state (height, round, step)
2. Track which peers have which votes
3. Implement efficient vote request/response

---

## Implementation Order

### Phase 1: Critical Fixes (Must Do First)
1. CR1 - Deadlock in finalizeCommit (apply locked pattern to all state transitions)
2. CR2 - Race in HeightVoteSet.AddVote
3. CR3 - Race in ConsensusState.Start
4. CR4 - Data race on ValidatorSet
5. CR5 - Ignored vote error

### Phase 2: WAL & Persistence
1. H1 - WAL writes during consensus
2. H6 - WAL legacy migration fix

### Phase 3: Correctness
1. H2 - Pointer aliasing bug
2. H5 - TwoThirdsMajority overflow
3. M2 - POL validation
4. M7 - enterCommit nil handling

### Phase 4: Infrastructure
1. H3 - ScheduleTimeout blocking
2. H4 - TimeoutTicker.Stop wait
3. M8 - Broadcast implementation

### Phase 5: Integration
1. M3 - Evidence pool integration
2. M1 - Shallow copy fix
3. M4 - Message length check
4. M5 - GetAddress copy
5. M6 - isSameVote fix

### Phase 6: Low Priority
1. L1-L6 - Performance and style issues

### Phase 7: Missing Functionality
1. MF1-MF5 - As needed for production deployment

---

## Testing Requirements

Each fix must include:
1. Unit tests demonstrating the bug is fixed
2. Regression tests preventing reintroduction
3. For race conditions: tests with `-race` flag
4. For deadlocks: tests with timeout detection

### Critical Tests Required
- [ ] Test that SkipTimeoutCommit=true doesn't deadlock
- [ ] Test concurrent AddVote and Reset
- [ ] Test concurrent Start and Stop
- [ ] Test WAL survives crash at any point during consensus
- [ ] Test POL validation rejects invalid POL
- [ ] Test evidence is detected for equivocating validators

---

## Verification Checklist

Before marking complete:
- [ ] All critical issues fixed (CR1-CR5)
- [ ] All high severity issues fixed (H1-H6)
- [ ] WAL writes verified at all consensus transitions
- [ ] Race detector passes (`go test -race ./...`)
- [ ] Deadlock detector passes (test with timeouts)
- [ ] Integration tests pass
- [ ] Lint passes
- [ ] Documentation updated

---

## Summary

| Severity | Count | Status |
|----------|-------|--------|
| Critical | 5 | **DONE** |
| High | 6 | **DONE** |
| Medium | 8 | 4 Done, 4 Pending |
| Low | 6 | 2 Done, 4 Pending |
| Missing Functionality | 5 | Pending |

**Total Issues**: 30
**Completed**: 17

### Implemented in Phase 1
- **CR1**: Deadlock fix - Added `Locked` pattern to all state transition functions
- **CR2**: Race in HeightVoteSet.AddVote - Keep lock during entire operation
- **CR3**: Race in ConsensusState.Start - Call enterNewRoundLocked while holding lock
- **CR4**: Data race on ValidatorSet - Added `WithIncrementedPriority` immutable pattern
- **CR5**: Ignored vote error - PANIC if own vote fails to add
- **H1**: WAL writes during consensus - Write to WAL before processing proposals/votes
- **H2**: Pointer aliasing bug - Make value copy of block before storing pointer
- **M7**: enterCommit nil handling - PANIC if no commit found
- **M8**: Broadcast callbacks - Added onProposal/onVote callback mechanism

### Implemented in Phase 2
- **H3**: ScheduleTimeout non-blocking send with logging
- **H4**: TimeoutTicker.Stop waits for goroutine via WaitGroup
- **H5**: TwoThirdsMajority overflow-safe calculation
- **H6**: WAL legacy migration returns error on failure
- **M1**: Deep copy AccountName in ValidatorSet.Copy
- **M5**: GetAddress returns copy of bytes
- **L1**: Hex encoding for block hash keys (debuggability)
- **L3**: Use sort.Ints instead of bubble sort

### Remaining Work
- M2: POL validation
- M3: Evidence pool integration
- M4: Message length check
- M6: isSameVote signature check
- L2: GC pressure in WAL decoder
- L4: Evidence size estimate
- L5: ValidatorSet hash nil name check
- L6: centerPriorities precision loss (document only)
- MF1-MF5: Missing functionality
