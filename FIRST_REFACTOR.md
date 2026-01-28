# First Refactor Plan

This document catalogs all issues identified during code review and defines the refactoring work required before the codebase is production-ready.

## Error Handling Philosophy

### Panic vs Error

**PANIC** when:
- Consensus invariants are violated (would break safety)
- Internal state is corrupted (should never happen with correct code)
- WAL write/sync fails (recovery is impossible)
- State file write fails (double-sign protection compromised)
- Block execution fails after commit decision (irreversible state corruption)
- Signature creation fails (crypto library failure)
- Validator set becomes invalid mid-operation

**ERROR** when:
- Network input is malformed or invalid
- Message is for wrong height/round (stale or future)
- Signature verification fails on received message
- Duplicate or conflicting vote from peer (equivocation evidence)
- Proposal from non-proposer
- Any input validation that doesn't affect our local state

**Rationale**: A consensus node that continues operating with corrupted state is worse than a crashed node. Crashed nodes can be debugged and restarted; nodes with corrupted state can cause chain splits and safety violations.

---

## Critical Issues

### C1. No Double-Sign Protection for Proposals

**Location**: `privval/file_pv.go:278-289`

**Problem**: `SignProposal` signs proposals without checking `LastSignState`. A validator can sign multiple conflicting proposals for the same height/round, which is equivocation and a slashable offense.

**Current Code**:
```go
func (pv *FilePV) SignProposal(chainID string, proposal *gen.Proposal) error {
    pv.mu.Lock()
    defer pv.mu.Unlock()
    signBytes := types.ProposalSignBytes(chainID, proposal)
    sig := ed25519.Sign(pv.privKey, signBytes)
    proposal.Signature = types.NewSignature(sig)
    return nil
}
```

**Required Changes**:
1. Add `StepProposal int8 = 0` constant (proposals come before prevotes)
2. Check `LastSignState.CheckHRS(proposal.Height, proposal.Round, StepProposal)`
3. Store proposal block hash in `LastSignState` for idempotent re-signing
4. Persist state before returning signature
5. **PANIC** if state persistence fails

**Implementation**:
```go
func (pv *FilePV) SignProposal(chainID string, proposal *gen.Proposal) error {
    pv.mu.Lock()
    defer pv.mu.Unlock()

    // Check for double-sign
    if err := pv.lastSignState.CheckHRS(proposal.Height, proposal.Round, StepProposal); err != nil {
        if err == ErrDoubleSign && pv.isSameProposal(proposal) {
            proposal.Signature = pv.lastSignState.Signature
            return nil
        }
        return err
    }

    signBytes := types.ProposalSignBytes(chainID, proposal)
    sig := ed25519.Sign(pv.privKey, signBytes)
    proposal.Signature = types.NewSignature(sig)

    // Update and persist state
    blockHash := types.BlockHash(&proposal.Block)
    pv.lastSignState.Height = proposal.Height
    pv.lastSignState.Round = proposal.Round
    pv.lastSignState.Step = StepProposal
    pv.lastSignState.Signature = proposal.Signature
    pv.lastSignState.BlockHash = &blockHash

    if err := pv.saveState(); err != nil {
        panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to persist sign state: %v", err))
    }

    return nil
}
```

---

### C2. Non-Atomic State File Writes

**Location**: `privval/file_pv.go:153, 196-227`

**Problem**: State is written directly with `os.WriteFile`. If the process crashes mid-write or disk is full, the state file could be corrupted or truncated, compromising double-sign protection.

**Current Code**:
```go
if err := os.WriteFile(pv.stateFilePath, data, stateFilePerm); err != nil {
    return fmt.Errorf("failed to write state file: %w", err)
}
```

**Required Changes**:
1. Write to temporary file first
2. Sync temporary file
3. Rename temporary to final (atomic on POSIX)
4. **PANIC** on any failure (state persistence is critical)

**Implementation**:
```go
func (pv *FilePV) saveState() error {
    dir := filepath.Dir(pv.stateFilePath)
    if err := os.MkdirAll(dir, 0700); err != nil {
        return fmt.Errorf("failed to create state directory: %w", err)
    }

    state := FilePVState{
        Height: pv.lastSignState.Height,
        Round:  pv.lastSignState.Round,
        Step:   pv.lastSignState.Step,
    }
    if len(pv.lastSignState.Signature.Data) > 0 {
        state.Signature = pv.lastSignState.Signature.Data
    }
    if pv.lastSignState.BlockHash != nil {
        state.BlockHash = pv.lastSignState.BlockHash.Data
    }

    data, err := json.MarshalIndent(state, "", "  ")
    if err != nil {
        return fmt.Errorf("failed to marshal state: %w", err)
    }

    // Write to temp file
    tmpPath := pv.stateFilePath + ".tmp"
    if err := os.WriteFile(tmpPath, data, stateFilePerm); err != nil {
        return fmt.Errorf("failed to write temp state file: %w", err)
    }

    // Sync temp file
    f, err := os.Open(tmpPath)
    if err != nil {
        return fmt.Errorf("failed to open temp file for sync: %w", err)
    }
    if err := f.Sync(); err != nil {
        f.Close()
        return fmt.Errorf("failed to sync temp file: %w", err)
    }
    f.Close()

    // Atomic rename
    if err := os.Rename(tmpPath, pv.stateFilePath); err != nil {
        return fmt.Errorf("failed to rename state file: %w", err)
    }

    // Sync directory
    d, err := os.Open(dir)
    if err != nil {
        return fmt.Errorf("failed to open dir for sync: %w", err)
    }
    if err := d.Sync(); err != nil {
        d.Close()
        return fmt.Errorf("failed to sync directory: %w", err)
    }
    d.Close()

    return nil
}
```

Same pattern required for `saveKey()`.

---

### C3. Panics on Network Input

**Location**: `types/hash.go:18-23, 60-65, 68-73`

**Problem**: `NewHash`, `NewSignature`, and `NewPublicKey` panic on invalid input. When processing untrusted network data, this enables denial-of-service attacks.

**Current Code**:
```go
func NewHash(data []byte) Hash {
    if len(data) != 32 {
        panic("hash must be 32 bytes")
    }
    return Hash{Data: data}
}
```

**Required Changes**:
1. Create safe constructors that return errors for untrusted input
2. Keep panicking constructors for internal use (rename to `MustNewHash`)
3. Update all call sites appropriately

**Implementation**:
```go
// NewHash creates a Hash from bytes, returning error if invalid.
// Use for untrusted input (network, files).
func NewHash(data []byte) (Hash, error) {
    if len(data) != 32 {
        return Hash{}, fmt.Errorf("hash must be 32 bytes, got %d", len(data))
    }
    return Hash{Data: data}, nil
}

// MustNewHash creates a Hash, panicking if invalid.
// Use only for trusted internal data.
func MustNewHash(data []byte) Hash {
    h, err := NewHash(data)
    if err != nil {
        panic(err)
    }
    return h
}

// NewSignature creates a Signature from bytes, returning error if invalid.
func NewSignature(data []byte) (Signature, error) {
    if len(data) != 64 {
        return Signature{}, fmt.Errorf("signature must be 64 bytes, got %d", len(data))
    }
    return Signature{Data: data}, nil
}

// MustNewSignature creates a Signature, panicking if invalid.
func MustNewSignature(data []byte) Signature {
    s, err := NewSignature(data)
    if err != nil {
        panic(err)
    }
    return s
}

// NewPublicKey creates a PublicKey from bytes, returning error if invalid.
func NewPublicKey(data []byte) (PublicKey, error) {
    if len(data) != 32 {
        return PublicKey{}, fmt.Errorf("public key must be 32 bytes, got %d", len(data))
    }
    return PublicKey{Data: data}, nil
}

// MustNewPublicKey creates a PublicKey, panicking if invalid.
func MustNewPublicKey(data []byte) PublicKey {
    p, err := NewPublicKey(data)
    if err != nil {
        panic(err)
    }
    return p
}
```

**Call Site Updates Required**:
- `privval/file_pv.go:184, 188` - Use error-returning versions when loading from file
- All network message processing - Use error-returning versions
- Internal consensus code (creating our own signatures) - Use `Must` versions

---

### C4. Silent Message Dropping

**Location**: `engine/state.go:162-177`

**Problem**: Proposals and votes are silently dropped when channels are full. This can cause a validator to miss critical messages, potentially preventing consensus progress.

**Current Code**:
```go
func (cs *ConsensusState) AddProposal(proposal *gen.Proposal) {
    select {
    case cs.proposalCh <- proposal:
    default:
        // Channel full, drop proposal
    }
}
```

**Required Changes**:
1. Increase channel buffer sizes significantly
2. Add metrics for dropped messages
3. Log warnings when dropping
4. Consider blocking with timeout instead of immediate drop
5. Track dropped message counts for monitoring

**Implementation**:
```go
const (
    proposalChannelSize = 100
    voteChannelSize     = 10000
)

type ConsensusState struct {
    // ... existing fields ...

    // Metrics
    droppedProposals uint64
    droppedVotes     uint64
}

func (cs *ConsensusState) AddProposal(proposal *gen.Proposal) {
    select {
    case cs.proposalCh <- proposal:
    default:
        atomic.AddUint64(&cs.droppedProposals, 1)
        // Log at warn level - this indicates backpressure
        log.Warn("dropped proposal due to full channel",
            "height", proposal.Height,
            "round", proposal.Round,
            "proposer", types.AccountNameString(proposal.Proposer),
            "total_dropped", atomic.LoadUint64(&cs.droppedProposals))
    }
}

func (cs *ConsensusState) AddVote(vote *gen.Vote) {
    select {
    case cs.voteCh <- vote:
    default:
        atomic.AddUint64(&cs.droppedVotes, 1)
        log.Warn("dropped vote due to full channel",
            "height", vote.Height,
            "round", vote.Round,
            "type", vote.Type,
            "validator", types.AccountNameString(vote.Validator),
            "total_dropped", atomic.LoadUint64(&cs.droppedVotes))
    }
}

// GetDroppedMessageCounts returns counts for monitoring
func (cs *ConsensusState) GetDroppedMessageCounts() (proposals, votes uint64) {
    return atomic.LoadUint64(&cs.droppedProposals), atomic.LoadUint64(&cs.droppedVotes)
}
```

---

### C5. Nil BlockExecutor Panics

**Location**: `engine/state.go:250, 322, 473`

**Problem**: `blockExecutor` is an optional interface that can be nil, but it's called without nil checks, causing panics.

**Current Code**:
```go
block, err = cs.blockExecutor.CreateProposalBlock(...)  // Panics if nil
```

**Required Changes**:
1. Check for nil before all blockExecutor calls
2. Define behavior when executor is nil (skip block creation, accept all blocks, etc.)
3. Document that executor is required for full consensus participation

**Implementation**:
```go
func (cs *ConsensusState) createAndSendProposal() {
    var block *gen.Block

    if cs.validBlock != nil {
        block = cs.validBlock
    } else if cs.lockedBlock != nil {
        block = cs.lockedBlock
    } else {
        if cs.blockExecutor == nil {
            log.Error("cannot create proposal: no block executor configured")
            return
        }
        var err error
        block, err = cs.blockExecutor.CreateProposalBlock(
            cs.height,
            cs.lastCommit,
            cs.validatorSet.Proposer.Name,
        )
        if err != nil {
            log.Error("failed to create proposal block", "err", err)
            return
        }
    }
    // ... rest of function
}

func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
    // ... validation ...

    // Validate block if executor available
    if cs.blockExecutor != nil {
        if err := cs.blockExecutor.ValidateBlock(&proposal.Block); err != nil {
            log.Debug("proposal block validation failed", "err", err)
            return
        }
    }

    // ... rest of function
}

func (cs *ConsensusState) finalizeCommit(height int64, commit *gen.Commit) {
    block := cs.lockedBlock
    if block == nil {
        block = cs.proposalBlock
    }
    if block == nil {
        panic("CONSENSUS CRITICAL: finalizeCommit called with no block")
    }

    // Apply block
    if cs.blockExecutor != nil {
        if err := cs.blockExecutor.ApplyBlock(block, commit); err != nil {
            panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to apply committed block: %v", err))
        }
    }

    // ... rest of function
}
```

---

### C6. FlushAndSync Not Thread-Safe

**Location**: `wal/file_wal.go:126-131`

**Problem**: `FlushAndSync` is public but doesn't acquire the mutex. Called from `WriteSync` which holds the lock, but if called directly, it races.

**Current Code**:
```go
func (w *FileWAL) FlushAndSync() error {
    if err := w.buf.Flush(); err != nil {
        return err
    }
    return w.file.Sync()
}
```

**Required Changes**:
1. Make internal `flushAndSync` that assumes lock is held
2. Public `FlushAndSync` acquires lock
3. Internal callers use `flushAndSync`

**Implementation**:
```go
// FlushAndSync flushes the buffer and syncs to disk.
// Safe for concurrent use.
func (w *FileWAL) FlushAndSync() error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if !w.started {
        return ErrWALClosed
    }

    return w.flushAndSync()
}

// flushAndSync is the internal version that assumes lock is held.
func (w *FileWAL) flushAndSync() error {
    if err := w.buf.Flush(); err != nil {
        return err
    }
    return w.file.Sync()
}

// WriteSync writes a message and syncs to disk
func (w *FileWAL) WriteSync(msg *Message) error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if !w.started {
        return ErrWALClosed
    }

    if err := w.enc.Encode(msg); err != nil {
        return err
    }

    return w.flushAndSync()
}
```

---

### C7. Validator Set Hash Not Deterministic

**Location**: `types/validator.go:228-239`

**Problem**: The hash function sorts validators but then ignores the sorted slice, computing hash from the original unsorted order.

**Current Code**:
```go
func (vs *ValidatorSet) Hash() Hash {
    sorted := make([]*NamedValidator, len(vs.Validators))
    copy(sorted, vs.Validators)
    sort.Slice(sorted, func(i, j int) bool {
        return AccountNameString(sorted[i].Name) < AccountNameString(sorted[j].Name)
    })

    data := vs.ToData()  // Uses vs.Validators, NOT sorted!
    bytes, _ := data.MarshalCramberry()
    return HashBytes(bytes)
}
```

**Required Changes**:
1. Use the sorted slice for serialization
2. Or sort validators on creation and maintain sorted order

**Implementation** (Option 1 - sort for hash):
```go
func (vs *ValidatorSet) Hash() Hash {
    // Sort validators by name for deterministic ordering
    sorted := make([]*NamedValidator, len(vs.Validators))
    copy(sorted, vs.Validators)
    sort.Slice(sorted, func(i, j int) bool {
        return AccountNameString(sorted[i].Name) < AccountNameString(sorted[j].Name)
    })

    // Create data from sorted validators
    validators := make([]NamedValidator, len(sorted))
    for i, v := range sorted {
        validators[i] = *v
    }

    var proposerIndex uint16
    if vs.Proposer != nil {
        // Find proposer in sorted list
        for i, v := range sorted {
            if AccountNameString(v.Name) == AccountNameString(vs.Proposer.Name) {
                proposerIndex = uint16(i)
                break
            }
        }
    }

    data := &ValidatorSetData{
        Validators:    validators,
        ProposerIndex: proposerIndex,
        TotalPower:    vs.TotalPower,
    }

    bytes, err := data.MarshalCramberry()
    if err != nil {
        panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal validator set: %v", err))
    }
    return HashBytes(bytes)
}
```

---

## High Severity Issues

### H1. MakeCommit Nil Dereference

**Location**: `engine/vote_tracker.go:214`

**Problem**: If 2/3+ vote for nil (no block), `vs.maj23.blockHash` is nil, and dereferencing panics.

**Current Code**:
```go
return &gen.Commit{
    Height:     vs.height,
    Round:      vs.round,
    BlockHash:  *vs.maj23.blockHash,  // PANIC if nil
    Signatures: sigs,
}
```

**Required Changes**:
1. Check for nil blockHash before creating commit
2. Return nil commit if no block has majority (only non-nil blocks can be committed)

**Implementation**:
```go
func (vs *VoteSet) MakeCommit() *gen.Commit {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    if vs.voteType != types.VoteTypePrecommit || vs.maj23 == nil {
        return nil
    }

    // Cannot create commit for nil block
    if vs.maj23.blockHash == nil || types.IsHashEmpty(vs.maj23.blockHash) {
        return nil
    }

    sigs := make([]gen.CommitSig, 0, len(vs.votes))
    for _, vote := range vs.votes {
        sig := gen.CommitSig{
            ValidatorIndex: vote.ValidatorIndex,
            Signature:      vote.Signature,
            Timestamp:      vote.Timestamp,
            BlockHash:      vote.BlockHash,
        }
        sigs = append(sigs, sig)
    }

    return &gen.Commit{
        Height:     vs.height,
        Round:      vs.round,
        BlockHash:  *vs.maj23.blockHash,
        Signatures: sigs,
    }
}
```

---

### H2. Validator Index Overflow

**Location**: `types/validator.go:56`

**Problem**: Validator index is `uint16`, silently truncating if >65535 validators.

**Current Code**:
```go
val := &NamedValidator{
    Index: uint16(i),  // Silent truncation
}
```

**Required Changes**:
1. Check validator count in `NewValidatorSet`
2. Return error if too many validators
3. Define `MaxValidators` constant

**Implementation**:
```go
const MaxValidators = 65535

func NewValidatorSet(validators []*NamedValidator) (*ValidatorSet, error) {
    if len(validators) == 0 {
        return nil, ErrEmptyValidatorSet
    }
    if len(validators) > MaxValidators {
        return nil, fmt.Errorf("too many validators: %d (max %d)", len(validators), MaxValidators)
    }
    // ... rest of function
}
```

---

### H3. Integer Overflow in Priority Calculations

**Location**: `types/validator.go:103-105, 157-163`

**Problem**: Proposer priority arithmetic can overflow int64 with high voting powers.

**Required Changes**:
1. Add overflow-safe arithmetic
2. Clamp priorities to prevent overflow
3. Add `MaxTotalVotingPower` constant

**Implementation**:
```go
const (
    MaxTotalVotingPower = int64(1) << 60  // ~1 quintillion
    PriorityWindowSize  = MaxTotalVotingPower * 2
)

func (vs *ValidatorSet) IncrementProposerPriority(times int32) {
    if len(vs.Validators) == 0 {
        return
    }

    for i := int32(0); i < times; i++ {
        // Increment all priorities by voting power (with overflow check)
        for _, v := range vs.Validators {
            newPriority := v.ProposerPriority + v.VotingPower
            // Clamp to prevent overflow
            if newPriority > PriorityWindowSize/2 {
                newPriority = PriorityWindowSize / 2
            }
            v.ProposerPriority = newPriority
        }

        // Decrease proposer's priority by total power
        proposer := vs.getProposer()
        if proposer != nil {
            newPriority := proposer.ProposerPriority - vs.TotalPower
            // Clamp to prevent underflow
            if newPriority < -PriorityWindowSize/2 {
                newPriority = -PriorityWindowSize / 2
            }
            proposer.ProposerPriority = newPriority
        }
    }

    vs.centerPriorities()
    vs.Proposer = vs.getProposer()
}
```

---

### H4. Auto-Generate Keys on Missing File

**Location**: `privval/file_pv.go:103-111`

**Problem**: If key file is missing (deleted, wrong path), a new key is silently generated. Validator starts with wrong identity.

**Required Changes**:
1. `NewFilePV` should fail if key file doesn't exist
2. `GenerateFilePV` explicitly creates new keys
3. Add `LoadFilePV` that requires existing files

**Implementation**:
```go
// LoadFilePV loads an existing file-based private validator.
// Returns error if files don't exist.
func LoadFilePV(keyFilePath, stateFilePath string) (*FilePV, error) {
    pv := &FilePV{
        keyFilePath:   keyFilePath,
        stateFilePath: stateFilePath,
    }

    // Load key (must exist)
    if err := pv.loadKeyStrict(); err != nil {
        return nil, err
    }

    // Load state (must exist)
    if err := pv.loadStateStrict(); err != nil {
        return nil, err
    }

    return pv, nil
}

func (pv *FilePV) loadKeyStrict() error {
    data, err := os.ReadFile(pv.keyFilePath)
    if err != nil {
        return fmt.Errorf("failed to read key file %s: %w", pv.keyFilePath, err)
    }

    var key FilePVKey
    if err := json.Unmarshal(data, &key); err != nil {
        return fmt.Errorf("failed to parse key file: %w", err)
    }

    if len(key.PubKey) != ed25519.PublicKeySize {
        return fmt.Errorf("invalid public key size: %d", len(key.PubKey))
    }
    if len(key.PrivKey) != ed25519.PrivateKeySize {
        return fmt.Errorf("invalid private key size: %d", len(key.PrivKey))
    }

    pv.pubKey = types.MustNewPublicKey(key.PubKey)
    pv.privKey = key.PrivKey

    return nil
}

func (pv *FilePV) loadStateStrict() error {
    data, err := os.ReadFile(pv.stateFilePath)
    if err != nil {
        return fmt.Errorf("failed to read state file %s: %w", pv.stateFilePath, err)
    }

    var state FilePVState
    if err := json.Unmarshal(data, &state); err != nil {
        return fmt.Errorf("failed to parse state file: %w", err)
    }

    pv.lastSignState = LastSignState{
        Height: state.Height,
        Round:  state.Round,
        Step:   state.Step,
    }

    if len(state.Signature) > 0 {
        sig, err := types.NewSignature(state.Signature)
        if err != nil {
            return fmt.Errorf("invalid signature in state file: %w", err)
        }
        pv.lastSignState.Signature = sig
    }

    if len(state.BlockHash) > 0 {
        hash, err := types.NewHash(state.BlockHash)
        if err != nil {
            return fmt.Errorf("invalid block hash in state file: %w", err)
        }
        pv.lastSignState.BlockHash = &hash
    }

    return nil
}

// NewFilePV is deprecated. Use LoadFilePV or GenerateFilePV.
func NewFilePV(keyFilePath, stateFilePath string) (*FilePV, error) {
    // First try to load existing
    pv, err := LoadFilePV(keyFilePath, stateFilePath)
    if err == nil {
        return pv, nil
    }

    // Check if both files don't exist (fresh start)
    _, keyErr := os.Stat(keyFilePath)
    _, stateErr := os.Stat(stateFilePath)

    if os.IsNotExist(keyErr) && os.IsNotExist(stateErr) {
        log.Warn("no existing validator files found, generating new key pair",
            "key_file", keyFilePath,
            "state_file", stateFilePath)
        return GenerateFilePV(keyFilePath, stateFilePath)
    }

    // Partial state - dangerous
    return nil, fmt.Errorf("validator files in inconsistent state: key=%v, state=%v", keyErr, stateErr)
}
```

---

### H5. State File Corruption Causes Panic

**Location**: `privval/file_pv.go:184, 188`

**Problem**: Loading corrupted state with wrong-length signature/hash causes panic via `NewSignature`/`NewHash`.

**Required Changes**: Use error-returning constructors (covered in C3).

---

### H6. Message Type Detection is Fragile

**Location**: `engine/engine.go:229-239`

**Problem**: Trial-and-error deserialization could misinterpret message types.

**Required Changes**:
1. Add message wrapper type with explicit discriminator
2. Or use Cramberry's union/variant type if supported

**Implementation**:
```go
// ConsensusMessage wraps all consensus message types
type ConsensusMessage struct {
    Type    ConsensusMessageType
    Payload []byte
}

type ConsensusMessageType uint8

const (
    ConsensusMessageTypeProposal ConsensusMessageType = 1
    ConsensusMessageTypeVote     ConsensusMessageType = 2
)

func (e *Engine) HandleConsensusMessage(peerID string, data []byte) error {
    if len(data) < 1 {
        return ErrInvalidMessage
    }

    msgType := ConsensusMessageType(data[0])
    payload := data[1:]

    switch msgType {
    case ConsensusMessageTypeProposal:
        proposal := &gen.Proposal{}
        if err := proposal.UnmarshalCramberry(payload); err != nil {
            return fmt.Errorf("invalid proposal: %w", err)
        }
        return e.AddProposal(proposal)

    case ConsensusMessageTypeVote:
        vote := &gen.Vote{}
        if err := vote.UnmarshalCramberry(payload); err != nil {
            return fmt.Errorf("invalid vote: %w", err)
        }
        return e.AddVote(vote)

    default:
        return fmt.Errorf("unknown message type: %d", msgType)
    }
}
```

---

### H7. Error Silently Swallowed in finalizeCommit

**Location**: `engine/state.go:473-475`

**Problem**: Block application errors are silently ignored.

**Required Changes**: **PANIC** on block application failure (consensus has decided, failure is catastrophic).

**Implementation**:
```go
func (cs *ConsensusState) finalizeCommit(height int64, commit *gen.Commit) {
    block := cs.lockedBlock
    if block == nil {
        block = cs.proposalBlock
    }
    if block == nil {
        panic("CONSENSUS CRITICAL: finalizeCommit called with no block")
    }

    // Apply block - failure is catastrophic
    if cs.blockExecutor != nil {
        if err := cs.blockExecutor.ApplyBlock(block, commit); err != nil {
            panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to apply committed block at height %d: %v", height, err))
        }
    }

    // ... rest of function continues only on success
}
```

---

## Medium Severity Issues

### M1. No WAL Rotation

**Location**: `wal/file_wal.go`

**Problem**: WAL grows indefinitely.

**Required Changes**:
1. Implement WAL rotation when file exceeds threshold
2. Track file sizes in `Group` struct
3. Delete old segments after checkpointing
4. Add `Checkpoint(height int64)` method

**Implementation Sketch**:
```go
const (
    defaultMaxSegmentSize = 64 * 1024 * 1024  // 64MB
)

type FileWAL struct {
    // ... existing fields ...
    maxSegmentSize int64
    currentSize    int64
    segmentIndex   int
}

func (w *FileWAL) Write(msg *Message) error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if !w.started {
        return ErrWALClosed
    }

    // Check if rotation needed
    if w.currentSize >= w.maxSegmentSize {
        if err := w.rotate(); err != nil {
            return err
        }
    }

    n, err := w.enc.Encode(msg)
    if err != nil {
        return err
    }
    w.currentSize += int64(n)

    return nil
}

func (w *FileWAL) rotate() error {
    // Flush and close current segment
    if err := w.flushAndSync(); err != nil {
        return err
    }
    if err := w.file.Close(); err != nil {
        return err
    }

    // Start new segment
    w.segmentIndex++
    path := filepath.Join(w.dir, fmt.Sprintf("wal-%05d", w.segmentIndex))
    file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, walFilePerm)
    if err != nil {
        return err
    }

    w.file = file
    w.buf = bufio.NewWriterSize(file, defaultBufSize)
    w.enc = newEncoder(w.buf)
    w.currentSize = 0

    return nil
}

func (w *FileWAL) Checkpoint(height int64) error {
    // Delete segments that only contain heights <= checkpointed height
    // Implementation depends on tracking height ranges per segment
}
```

---

### M2. No CRC/Checksum on WAL Messages

**Location**: `wal/file_wal.go`

**Problem**: Corrupted WAL messages not detected.

**Required Changes**:
1. Add CRC32 checksum after each message
2. Verify checksum on read
3. Handle checksum failures gracefully (truncate WAL at corruption point)

**Implementation Sketch**:
```go
import "hash/crc32"

func (e *encoder) Encode(msg *Message) (int, error) {
    data, err := msg.MarshalCramberry()
    if err != nil {
        return 0, err
    }

    // Calculate CRC
    crc := crc32.ChecksumIEEE(data)

    // Write length (4 bytes)
    binary.BigEndian.PutUint32(e.buf[:4], uint32(len(data)))
    if _, err := e.w.Write(e.buf[:4]); err != nil {
        return 0, err
    }

    // Write data
    if _, err := e.w.Write(data); err != nil {
        return 0, err
    }

    // Write CRC (4 bytes)
    binary.BigEndian.PutUint32(e.buf[:4], crc)
    if _, err := e.w.Write(e.buf[:4]); err != nil {
        return 0, err
    }

    return 4 + len(data) + 4, nil
}

func (d *decoder) Decode() (*Message, error) {
    // Read length
    if _, err := io.ReadFull(d.r, d.buf[:4]); err != nil {
        return nil, err
    }
    length := binary.BigEndian.Uint32(d.buf[:4])
    if length > maxMsgSize {
        return nil, ErrWALCorrupted
    }

    // Read data
    data := make([]byte, length)
    if _, err := io.ReadFull(d.r, data); err != nil {
        return nil, err
    }

    // Read and verify CRC
    if _, err := io.ReadFull(d.r, d.buf[:4]); err != nil {
        return nil, err
    }
    expectedCRC := binary.BigEndian.Uint32(d.buf[:4])
    actualCRC := crc32.ChecksumIEEE(data)
    if expectedCRC != actualCRC {
        return nil, fmt.Errorf("%w: CRC mismatch (expected %x, got %x)", ErrWALCorrupted, expectedCRC, actualCRC)
    }

    // Deserialize
    msg := &Message{}
    if err := msg.UnmarshalCramberry(data); err != nil {
        return nil, err
    }

    return msg, nil
}
```

---

### M3. O(n) WAL Search

**Location**: `wal/file_wal.go:133-179`

**Problem**: Linear scan from beginning for every search.

**Required Changes**:
1. Maintain in-memory index of height -> file offset
2. Persist index periodically
3. Build index on startup by scanning once

**Implementation Sketch**:
```go
type FileWAL struct {
    // ... existing fields ...
    heightIndex map[int64]int64  // height -> file offset after EndHeight
}

func (w *FileWAL) Start() error {
    // ... existing code ...

    // Build index on startup
    w.heightIndex = make(map[int64]int64)
    if err := w.buildIndex(); err != nil {
        return err
    }

    return nil
}

func (w *FileWAL) buildIndex() error {
    path := filepath.Join(w.dir, "wal")
    file, err := os.Open(path)
    if os.IsNotExist(err) {
        return nil
    }
    if err != nil {
        return err
    }
    defer file.Close()

    dec := newDecoder(bufio.NewReader(file))
    var offset int64

    for {
        msg, err := dec.Decode()
        if err == io.EOF {
            break
        }
        if err != nil {
            // Corrupted - stop indexing here
            log.Warn("WAL corruption detected during indexing", "offset", offset, "err", err)
            break
        }

        if msg.Type == MsgTypeEndHeight {
            currentOffset, _ := file.Seek(0, io.SeekCurrent)
            w.heightIndex[msg.Height] = currentOffset
        }
    }

    return nil
}

func (w *FileWAL) SearchForEndHeight(height int64) (Reader, bool, error) {
    w.mu.Lock()
    defer w.mu.Unlock()

    offset, ok := w.heightIndex[height]
    if !ok {
        return nil, false, nil
    }

    // Open file and seek to offset
    path := filepath.Join(w.dir, "wal")
    file, err := os.Open(path)
    if err != nil {
        return nil, false, err
    }

    if _, err := file.Seek(offset, io.SeekStart); err != nil {
        file.Close()
        return nil, false, err
    }

    return &fileReader{
        file: file,
        dec:  newDecoder(bufio.NewReader(file)),
    }, true, nil
}
```

---

### M4. SetPeerMaj23 is Incomplete

**Location**: `engine/vote_tracker.go:311-334`

**Problem**: Function creates vote sets but doesn't store peer claims.

**Required Changes**:
1. Add peer claim tracking to VoteSet
2. Use claims to request missing votes
3. Validate POL against peer claims

**Implementation Sketch**:
```go
type VoteSet struct {
    // ... existing fields ...
    peerMaj23 map[string]*types.Hash  // peerID -> claimed maj23 block hash
}

func (vs *VoteSet) SetPeerMaj23(peerID string, blockHash *types.Hash) {
    vs.mu.Lock()
    defer vs.mu.Unlock()

    if vs.peerMaj23 == nil {
        vs.peerMaj23 = make(map[string]*types.Hash)
    }
    vs.peerMaj23[peerID] = blockHash
}

func (vs *VoteSet) GetPeerMaj23Claims() map[string]*types.Hash {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    result := make(map[string]*types.Hash, len(vs.peerMaj23))
    for k, v := range vs.peerMaj23 {
        result[k] = v
    }
    return result
}
```

---

### M5. Evidence Key Collisions

**Location**: `evidence/pool.go:324-326`

**Problem**: Nanosecond timestamps can collide.

**Required Changes**: Include more distinguishing data in key.

**Implementation**:
```go
func evidenceKey(ev *gen.Evidence) string {
    // Include hash of data for uniqueness
    dataHash := sha256.Sum256(ev.Data)
    return fmt.Sprintf("%d/%d/%d/%x", ev.Type, ev.Height, ev.Time, dataHash[:8])
}
```

---

### M6. Duplicate VoteSet Implementations

**Location**: `types/vote.go:55-173` and `engine/vote_tracker.go:11-217`

**Problem**: Two different VoteSet implementations with subtly different behavior.

**Required Changes**:
1. Remove `types.VoteSet`
2. Move `engine.VoteSet` to shared location
3. Or clearly document that `types.VoteSet` is for external use only

---

### M7. Config Validation Missing Timeout Checks

**Location**: `engine/config.go:42-51`

**Problem**: No validation of timeout values.

**Required Changes**:
```go
func (cfg *Config) ValidateBasic() error {
    if cfg.ChainID == "" {
        return errors.New("chain_id is required")
    }
    if cfg.WALPath == "" {
        return errors.New("wal_path is required")
    }

    // Validate timeouts
    if cfg.Timeouts.Propose <= 0 {
        return errors.New("propose timeout must be positive")
    }
    if cfg.Timeouts.Prevote <= 0 {
        return errors.New("prevote timeout must be positive")
    }
    if cfg.Timeouts.Precommit <= 0 {
        return errors.New("precommit timeout must be positive")
    }
    if cfg.Timeouts.Commit <= 0 {
        return errors.New("commit timeout must be positive")
    }

    // Sanity checks
    if cfg.Timeouts.Propose > 5*time.Minute {
        return errors.New("propose timeout exceeds 5 minutes")
    }

    return nil
}
```

---

### M8. Dropped Timeouts

**Location**: `engine/timeout.go:140-143`

**Problem**: Timeouts can be silently dropped.

**Required Changes**:
1. Increase channel buffer
2. Log dropped timeouts
3. Consider blocking with reasonable timeout

---

## Low Severity Issues

### L1. Error Ignored in ValidatorSet.Copy

**Location**: `types/validator.go:184`

**Required Changes**:
```go
func (vs *ValidatorSet) Copy() (*ValidatorSet, error) {
    validators := make([]*NamedValidator, len(vs.Validators))
    for i, v := range vs.Validators {
        validators[i] = &NamedValidator{
            Name:             v.Name,
            Index:            v.Index,
            PublicKey:        v.PublicKey,
            VotingPower:      v.VotingPower,
            ProposerPriority: v.ProposerPriority,
        }
    }
    return NewValidatorSet(validators)
}
```

---

### L2. Pointer String Pattern for AccountName

**Location**: `types/account.go:27-29`

**Problem**: Using `*string` creates extra allocations.

**Required Changes**: Consider changing to direct string field in schema, or document why pointer is necessary.

---

### L3. Inconsistent Error Handling

**Problem**: Some functions return errors, others panic, others ignore.

**Required Changes**: Document and enforce the panic vs error philosophy (see top of document) across all packages.

---

## Implementation Order

### Phase 1: Critical Safety (Must Do First)
1. C1 - Proposal double-sign protection
2. C2 - Atomic state file writes
3. C5 - Nil blockExecutor checks
4. C7 - Deterministic validator set hash
5. H7 - Panic on block application failure

### Phase 2: Critical Input Handling
1. C3 - Safe constructors for network input
2. C4 - Message dropping metrics and logging
3. C6 - Thread-safe FlushAndSync
4. H6 - Message type discriminator

### Phase 3: High Severity
1. H1 - MakeCommit nil handling
2. H2 - Validator index overflow check
3. H3 - Priority overflow protection
4. H4 - Strict key file loading
5. H5 - Safe state file loading (covered by C3)

### Phase 4: Medium Severity
1. M1 - WAL rotation
2. M2 - WAL checksums
3. M3 - WAL index
4. M4 - Peer maj23 tracking
5. M5 - Evidence key uniqueness
6. M6 - Deduplicate VoteSet
7. M7 - Config validation
8. M8 - Timeout handling

### Phase 5: Low Severity
1. L1 - Copy error handling
2. L2 - AccountName pattern (deferred - schema change)
3. L3 - Consistent error handling audit

---

## Testing Requirements

Each fix must include:
1. Unit tests for the specific fix
2. Integration test demonstrating the bug is fixed
3. Negative tests (e.g., verify panic occurs when expected)

### Critical Path Tests
- Test that signing same proposal twice returns same signature
- Test that signing conflicting proposal fails
- Test that state file corruption is detected on load
- Test that malformed network messages don't crash node
- Test that dropped messages are logged and counted
- Test that validator set hash is deterministic across orderings

---

## Verification Checklist

Before marking complete:
- [x] All critical issues addressed (C1-C7 implemented)
- [x] All high severity issues addressed (H1-H7 implemented)
- [x] All medium severity issues addressed (M1-M8 implemented)
- [x] Panic philosophy documented and enforced
- [ ] Test coverage >80% for consensus-critical code
- [x] Race detector passes (`go test -race ./...`)
- [x] Lint passes (`golangci-lint run`) - minor test file warnings only
- [ ] Manual review of all panic sites

## Implementation Status (2026-01-28)

### Completed

**Phase 1 - Critical Safety:**
- [x] C1: Proposal double-sign protection added to `privval/file_pv.go`
- [x] C2: Atomic state file writes (temp + rename + sync) in `privval/file_pv.go`
- [x] C5: Nil blockExecutor checks in `engine/state.go`
- [x] C7: Deterministic validator set hash in `types/validator.go`
- [x] H7: Panic on block application failure in `engine/state.go`

**Phase 2 - Input Handling:**
- [x] C3: Safe constructors (`NewHash`, `NewSignature`, `NewPublicKey` return errors; `Must*` versions for internal use)
- [x] C4: Message dropping metrics and logging in `engine/state.go`
- [x] C6: Thread-safe `FlushAndSync` in `wal/file_wal.go`
- [x] H6: Message type discriminator with explicit type prefix in `engine/engine.go`

**Phase 3 - High Severity:**
- [x] H1: MakeCommit nil dereference fix in `engine/vote_tracker.go`
- [x] H2: Validator index overflow check (MaxValidators = 65535) in `types/validator.go`
- [x] H3: Priority overflow protection in `types/validator.go`
- [x] H4: Strict key file loading (`LoadFilePV`) in `privval/file_pv.go`

**Phase 4 - Medium Severity:**
- [x] M1: WAL rotation with configurable segment size (64MB default) in `wal/file_wal.go`
- [x] M2: WAL checksums (CRC32 on each message) for corruption detection in `wal/file_wal.go`
- [x] M3: WAL indexing for O(1) height lookup in `wal/file_wal.go`
- [x] M4: Peer maj23 tracking in `engine/vote_tracker.go` (VoteSet and HeightVoteSet)
- [x] M5: Evidence key uniqueness with SHA256 hash prefix in `evidence/pool.go`
- [x] M6: Deduplicated VoteSet - removed unused `types.VoteSet`, `engine.VoteSet` is canonical
- [x] M7: Config validation in `engine/config.go`
- [x] M8: Timeout handling improvements (larger buffers, logging, metrics) in `engine/timeout.go`

**Phase 5 - Low Severity:**
- [x] L1: ValidatorSet.Copy now returns error in `types/validator.go`
- [x] L3: Consistent error handling audit - fixed ignored marshaling errors:
  - `types/block.go`: BlockHash, BlockHeaderHash, CommitHash panic on marshal failure
  - `types/vote.go`: VoteSignBytes panics on marshal failure
  - `types/proposal.go`: ProposalSignBytes panics on marshal failure
  - `wal/file_wal.go`: Refactored encoder to return (int, error), eliminating ignored errors

### Deferred (Lower Priority)
- [ ] L2: AccountName pointer string pattern (requires schema change - `gen.AccountName` uses `*string`)
