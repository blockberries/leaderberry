# Leaderberry Architecture Documentation

**Version**: Production Hardened v1.0.1
**Go Version**: 1.25.6
**Module**: `github.com/blockberries/leaderberry`
**Last Updated**: 2026-02-02

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Architecture Principles](#2-architecture-principles)
3. [System Architecture](#3-system-architecture)
4. [Go Package Architecture](#4-go-package-architecture)
5. [Core Components](#5-core-components)
6. [Concurrency Architecture](#6-concurrency-architecture)
7. [Consensus Protocol](#7-consensus-protocol)
8. [Security Architecture](#8-security-architecture)
9. [Crash Recovery](#9-crash-recovery)
10. [Design Patterns](#10-design-patterns)
11. [Performance Considerations](#11-performance-considerations)
12. [Testing Strategy](#12-testing-strategy)
13. [Build and Development](#13-build-and-development)
14. [Integration Points](#14-integration-points)
15. [Future Considerations](#15-future-considerations)

---

## 1. Project Overview

Leaderberry is a production-ready Tendermint-style BFT (Byzantine Fault Tolerant) consensus engine designed for the Blockberries ecosystem. It provides deterministic finality through a PBFT-derived consensus protocol with instant commitment guarantees.

### Key Characteristics

- **Deterministic Finality**: Once a block is committed, it is final and immutable
- **Byzantine Fault Tolerance**: Tolerates up to 1/3 Byzantine validators
- **Transaction Agnostic**: Treats transaction payloads as opaque bytes
- **Named Accounts**: Human-readable account names with hierarchical authorization
- **Instant Finality**: No probabilistic finality period required
- **Production Hardened**: 28+ documented refactoring iterations addressing edge cases

### Design Philosophy

**Key Design Principle**: The consensus engine is transaction-content agnostic. It handles authorization verification and transaction ordering, but treats transaction payloads as opaque bytes passed to the application layer.

This separation of concerns enables:
- Flexible application-layer transaction semantics
- Consensus-layer focus on ordering and finalization
- Clear security boundaries
- Modular system composition

### Module Dependencies

```
module github.com/blockberries/leaderberry

require github.com/blockberries/cramberry v1.5.5  // Binary serialization
```

**Minimal dependency policy**: Only one external dependency to reduce supply chain risk and ensure deterministic behavior.

---

## 2. Architecture Principles

### BFT Consensus Design

Leaderberry implements the Tendermint consensus algorithm with the following properties:

1. **Safety**: At most one block can be committed at any given height
2. **Liveness**: Network makes progress under partial synchrony (2/3+ honest validators online)
3. **Accountability**: Byzantine behavior is detectable and provable via evidence
4. **Determinism**: All honest nodes reach the same decision for same inputs

### Transaction Agnosticism

The consensus layer operates on three levels of abstraction:

```
Application Layer:     Transaction execution, state transitions, fees
                       ↕
Consensus Layer:       Ordering, finalization, authorization verification
                       ↕
Network Layer:         P2P gossip, block propagation, peer management
```

Consensus **knows about**:
- Transaction authorization (signatures, accounts, weights)
- Transaction ordering (via DAG batch certificates)
- Block structure and validation

Consensus **does not know about**:
- Transaction types or semantics
- Fee mechanisms or economics
- Application-specific validation logic
- State execution results (beyond hash commitments)

### Named Accounts and Validators

**Named Accounts**:
- Human-readable identifiers (e.g., "alice", "bob", "treasury")
- Weighted multi-signature authorization
- Hierarchical delegation with cycle detection
- Account-to-account authorization relationships

**Named Validators**:
- Validators are special accounts with single Ed25519 keys
- Voting power determines proposer frequency and quorum weight
- Key rotation handled at application layer
- Consensus only sees current active key per validator

### Deterministic Ordering

All consensus operations must be deterministic to ensure network-wide agreement:

- **No map iteration**: Maps are iterated in sorted key order
- **Tie-breaking rules**: Lexicographic ordering when priorities equal
- **Timestamp validation**: Clock drift tolerance with bounded windows
- **Overflow protection**: Checked arithmetic for all weight/power calculations
- **Canonical serialization**: Cramberry provides deterministic binary encoding

### Instant Finality

Once 2/3+ validators precommit a block:
- Block is immediately final and immutable
- No reorganization or rollback possible
- Application can safely execute state transitions
- Clients receive immediate confirmation (no waiting for "confirmations")

This differs from probabilistic finality (e.g., Bitcoin) where finality probability increases over time.

---

## 3. System Architecture

### High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    Application Layer                         │
│  (Transaction Execution, State, Custom Logic)                │
└────────────────┬────────────────────────────────────────────┘
                 │ BlockExecutor Interface
                 ↓
┌─────────────────────────────────────────────────────────────┐
│                   Consensus Engine                           │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │ State       │  │ Vote Tracker │  │  Proposer    │       │
│  │ Machine     │→ │              │→ │  Selection   │       │
│  └─────────────┘  └──────────────┘  └──────────────┘       │
│         ↓                ↓                   ↓               │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │   WAL       │  │  Evidence    │  │  Timeout     │       │
│  │             │  │  Pool        │  │  Ticker      │       │
│  └─────────────┘  └──────────────┘  └──────────────┘       │
└────────────────┬───────────────────────────────────────────┘
                 │ PrivValidator Interface
                 ↓
┌─────────────────────────────────────────────────────────────┐
│                 Private Validator                            │
│  (Key Management, Double-Sign Prevention)                    │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ↓
┌─────────────────────────────────────────────────────────────┐
│                    Network Layer                             │
│  (P2P Gossip, Peer Management - Glueberry)                   │
└─────────────────────────────────────────────────────────────┘
```

### Consensus State Machine Flow

```
┌──────────────┐
│  NewHeight   │  Height advances after commit
└──────┬───────┘
       ↓
┌──────────────┐
│  NewRound    │  Round 0, set proposer
└──────┬───────┘
       ↓
┌──────────────┐
│   Propose    │  Proposer creates block
└──────┬───────┘  Timeout if no proposal
       ↓
┌──────────────┐
│   Prevote    │  Validators prevote for block
└──────┬───────┘  (or nil if invalid)
       ↓
┌──────────────┐
│ PrevoteWait  │  Wait for 2/3+ prevotes
└──────┬───────┘  Timeout advances round if no quorum
       ↓
┌──────────────┐
│  Precommit   │  Validators precommit for locked block
└──────┬───────┘  Lock on block with 2/3+ prevotes
       ↓
┌──────────────┐
│PrecommitWait │  Wait for 2/3+ precommits
└──────┬───────┘  Timeout advances round
       ↓
┌──────────────┐
│   Commit     │  Block finalized, apply to state
└──────┬───────┘
       ↓
    (NewHeight)
```

**Key State Transitions**:

1. **NewHeight** → **NewRound**: Initialize round 0, reset locked block
2. **NewRound** → **Propose**: Proposer creates block proposal
3. **Propose** → **Prevote**: Validators cast prevotes
4. **Prevote** → **PrevoteWait**: Wait for 2/3+ prevotes or timeout
5. **PrevoteWait** → **Precommit**: Lock on block if 2/3+ prevotes, cast precommit
6. **Precommit** → **PrecommitWait**: Wait for 2/3+ precommits or timeout
7. **PrecommitWait** → **Commit**: Finalize block with 2/3+ precommits
8. **Commit** → **NewHeight**: Advance to next height

**Round Advancement**: If timeout occurs without reaching consensus, advance to next round with new proposer.

### Data Flow Through System

```
1. Transaction Submission
   Application → Looseberry (DAG Mempool) → Batch Certificates

2. Block Proposal
   Proposer → ReapCertifiedBatches() → Block Creation → Sign Proposal

3. Proposal Gossip
   Proposer → Broadcast → All Validators → Validation → Prevote

4. Vote Aggregation
   Validators → Prevotes → Vote Tracker → Quorum Detection

5. Block Commitment
   2/3+ Precommits → Commit Certificate → WAL → Application → State Update

6. Finality Notification
   Application → NotifyCommitted() → Looseberry → Cleanup Old Batches
```

### Integration with Blockberries Ecosystem

```
┌─────────────┐
│ Blockberry  │  Node Framework
│             │  - Consensus Interface
│             │  - Lifecycle Management
└──────┬──────┘
       ↓
┌─────────────┐
│ Leaderberry │  Consensus Engine
│             │  - BFT Protocol
│             │  - Vote Aggregation
└──────┬──────┘
       ↓
┌─────────────┐
│ Looseberry  │  DAG Mempool
│             │  - Transaction Ordering
│             │  - Batch Certification
└─────────────┘

┌─────────────┐
│ Cramberry   │  Serialization
│             │  - Binary Encoding
│             │  - Schema Generation
└─────────────┘

┌─────────────┐
│ Glueberry   │  P2P Networking
│             │  - Encrypted Communication
│             │  - Peer Discovery
└─────────────┘
```

---

## 4. Go Package Architecture

### Package Structure (Flat Layout)

```
leaderberry/
├── schema/              # Build input: Cramberry schemas
│   ├── types.cram       # Hash, Signature, PublicKey, AccountName
│   ├── account.cram     # Account, Authority, KeyWeight
│   ├── validator.cram   # NamedValidator, ValidatorSetData
│   ├── vote.cram        # Vote, VoteType, Commit, CommitSig
│   ├── block.cram       # Block, BlockHeader, BlockData
│   ├── proposal.cram    # Proposal with POL votes
│   ├── evidence.cram    # DuplicateVoteEvidence
│   └── wal.cram         # WAL message types, LastSignState
│
├── types/               # Public API: Core data structures
│   ├── generated/       # Generated code (do not edit)
│   ├── hash.go          # Hash utilities and constructors
│   ├── account.go       # Account and authorization logic
│   ├── validator.go     # ValidatorSet with proposer selection
│   ├── vote.go          # Vote verification
│   ├── block.go         # Block hashing and validation
│   └── block_parts.go   # Merkle-based block splitting
│
├── engine/              # Public API: Consensus state machine
│   ├── engine.go        # Main engine coordinator
│   ├── state.go         # Consensus state transitions
│   ├── vote_tracker.go  # Vote aggregation and quorum detection
│   ├── timeout.go       # Timeout management with backoff
│   ├── peer_state.go    # Peer knowledge tracking
│   └── config.go        # Configuration and validation
│
├── wal/                 # Public API: Write-ahead log
│   ├── wal.go           # WAL interface definition
│   ├── file_wal.go      # File-based implementation
│   ├── encoder.go       # Message encoding with CRC32
│   └── group.go         # File rotation and indexing
│
├── privval/             # Public API: Private validator
│   ├── file_pv.go       # File-based key storage
│   └── last_sign.go     # Double-sign prevention state
│
├── evidence/            # Public API: Byzantine evidence
│   ├── pool.go          # Evidence collection and validation
│   └── verify.go        # Evidence verification logic
│
└── test/                # External test applications
    └── integration/     # End-to-end consensus tests
```

**Design Rationale**:
- **Flat layout**: All packages are top-level for clarity
- **No internal packages**: All code is stable public API
- **Generated code segregation**: `types/generated/` clearly marked as do-not-edit
- **Clear boundaries**: Each package has well-defined responsibility

### Public API Design

All top-level packages (`types`, `engine`, `wal`, `privval`, `evidence`) are **stable public APIs**:

- **Semantic Versioning**: Breaking changes require major version bump
- **Comprehensive Documentation**: All exported symbols documented
- **Thread Safety**: All public functions are goroutine-safe
- **Error Handling**: Clear error conditions and sentinel errors
- **Resource Management**: Explicit lifecycle methods (Start/Stop, Close)

**API Stability Guarantees**:
```go
// Stable: Will not change in breaking way
func (vs *ValidatorSet) GetByName(name string) *NamedValidator

// Generated: Stable interface, implementation may regenerate
type Vote = gen.Vote

// Internal: Not exposed, can change freely
func (cs *ConsensusState) addVoteLocked(vote *Vote)
```

### Generated Code Workflow

```
Schema Definition (schema/*.cram)
        ↓ cramberry generate
Generated Types (types/generated/*.go)
        ↓ type aliases & extensions
Public API (types/*.go)
        ↓ import and use
Application Code
```

**Workflow Steps**:

1. **Define Schema**: Write `.cram` files with type definitions
2. **Generate Code**: Run `make generate` or `cramberry generate`
3. **Extend Types**: Add methods and utilities in `types/*.go`
4. **Use in Packages**: Import and build on generated types
5. **Never Edit Generated**: Regeneration overwrites changes

**Generated Code Features**:
- Deterministic binary serialization (MarshalCramberry/UnmarshalCramberry)
- Zero-copy deserialization where possible
- Bounded memory allocation (no unbounded slices)
- Canonical encoding for consensus-critical data

### Package Dependencies and Layering

```
Dependency Graph (arrows = "depends on"):

┌──────────────┐
│types/generated│  (Foundation: No internal deps)
└───────┬──────┘
        ↓
┌───────────────┐
│    types      │  (Extends generated types)
└───┬───────────┘
    ↓
┌───────────────┬───────────────┬───────────────┐
│     wal       │   privval     │   evidence    │  (Utility packages)
└───────┬───────┴───────┬───────┴───────┬───────┘
        └───────────────┴───────────────┘
                        ↓
                 ┌──────────────┐
                 │    engine    │  (Top-level coordinator)
                 └──────────────┘
```

**Layering Principles**:
1. **No circular dependencies**: Strict acyclic graph
2. **Foundation layer**: `types` has no internal dependencies
3. **Utility layer**: `wal`, `privval`, `evidence` depend only on `types`
4. **Coordination layer**: `engine` orchestrates all components
5. **Interface boundaries**: Higher layers depend on interfaces, not concrete types

### No Circular Dependencies

The package structure enforces zero circular dependencies:

```go
// types: Foundation layer
package types
// No imports from engine, wal, privval, evidence

// wal: Depends only on types
package wal
import "github.com/blockberries/leaderberry/types"
// No imports from engine, privval, evidence

// engine: Coordinates everything
package engine
import (
    "github.com/blockberries/leaderberry/types"
    "github.com/blockberries/leaderberry/wal"
    "github.com/blockberries/leaderberry/privval"
)
```

**Verification**: Run `go list -f '{{ .ImportPath }}: {{ .Imports }}' ./...` to confirm no cycles.

---

## 5. Core Components

### types/ Package

**Purpose**: Core consensus data structures with validation and utilities

**Key Responsibilities**:
- Define Block, Vote, Proposal, Validator types
- Provide hash computation and signature verification
- Implement authorization verification (multi-sig with delegation)
- Handle proposer selection (weighted round-robin)
- Support Merkle-based block splitting for gossip

**Key Types**:
```go
// Cryptographic primitives
Hash          // [32]byte SHA-256 hash
Signature     // [64]byte Ed25519 signature
PublicKey     // [32]byte Ed25519 public key

// Account system
AccountName   // Human-readable account identifier
Account       // Named account with authority
Authority     // Weighted multi-sig + delegation

// Consensus messages
Vote          // Signed prevote or precommit
Proposal      // Block proposal with optional POL
Commit        // Aggregated 2/3+ precommit signatures

// Blocks
Block         // Finalized block with batch certificates
BlockHeader   // Block metadata and hashes
BlockData     // Batch certificate references

// Validators
NamedValidator   // Validator with name, key, power
ValidatorSet     // Set of validators with proposer
```

**Key Algorithms**:

1. **Proposer Selection** (Weighted Round-Robin):
   - Each validator has ProposerPriority (initially = VotingPower)
   - Select validator with highest priority (tie-break lexicographically)
   - Decrease proposer's priority by TotalVotingPower
   - Increase all priorities by their VotingPower
   - Center priorities to prevent unbounded growth

2. **Authorization Verification** (Hierarchical Multi-Sig):
   - Check direct signatures against authority keys
   - Recursively verify delegated account signatures
   - Accumulate weight with overflow protection
   - Detect cycles with visited map (branch-based for diamonds)
   - Return total weight achieved

3. **Block Parts** (Merkle Tree):
   - Split block into 64KB chunks
   - Build binary Merkle tree of part hashes
   - Store proof path with each part
   - Receiver verifies: hash(part) + proof → root hash

**Thread Safety**: All functions return deep copies. Immutable semantics ensure safe concurrent access.

---

### engine/ Package

**Purpose**: Consensus state machine and vote tracking

**Key Responsibilities**:
- Implement Tendermint consensus protocol
- Manage state transitions (NewHeight → NewRound → Propose → ...)
- Aggregate votes and detect 2/3+ quorums
- Handle timeouts with exponential backoff
- Coordinate with PrivValidator for signing
- Track peer knowledge for efficient gossip

**Key Types**:
```go
Engine           // Main consensus coordinator
ConsensusState   // Internal state machine
VoteSet          // Vote collection for H/R/Type
HeightVoteSet    // All votes for a height (all rounds)
TimeoutTicker    // Timeout scheduling with backoff
PeerState        // What each peer knows
```

**Consensus State Fields**:
```go
type ConsensusState struct {
    // Current position
    height  int64
    round   int32
    step    RoundStep

    // Proposal state
    proposal      *Proposal
    proposalBlock *Block

    // Locking state (for safety)
    lockedRound int32
    lockedBlock *Block
    validRound  int32
    validBlock  *Block

    // Vote aggregation
    votes *HeightVoteSet
}
```

**Key Algorithms**:

1. **Vote Quorum Detection** (O(1)):
   - Track per-validator and per-block voting power
   - Check overflow before adding weight
   - Detect 2/3+ majority in constant time
   - Handle equivocation (conflicting votes from same validator)

2. **Timeout Calculation** (Exponential Backoff):
   - Base timeout + round × delta
   - Clamped at MaxRoundForTimeout (prevents overflow)
   - Different timeouts for Propose, Prevote, Precommit

**Thread Safety**: Locked pattern throughout. Public methods acquire lock, internal `*Locked` methods assume lock held.

---

### wal/ Package

**Purpose**: Write-ahead log for crash recovery

**Key Responsibilities**:
- Persist all consensus messages before processing
- Enable replay after crash or restart
- Provide height-indexed seeking for fast recovery
- Detect corruption via CRC32 checksums
- Rotate files by height to prevent unbounded growth

**File Format**:
```
[4 bytes: length][N bytes: Cramberry-encoded message][4 bytes: CRC32]
```

**Key Types**:
```go
WAL              // Interface for write-ahead logging
FileWAL          // File-based implementation
Message          // WAL message envelope
Reader           // Sequential message reading
```

**Message Types**:
- `MsgTypeProposal`: Block proposals
- `MsgTypeVote`: Prevotes and precommits
- `MsgTypeCommit`: Aggregated commit certificates
- `MsgTypeEndHeight`: Height completion marker
- `MsgTypeState`: Consensus state snapshots
- `MsgTypeTimeout`: Timeout events

**Key Features**:

1. **Buffered Writes**: Regular `Write()` calls buffered for throughput
2. **Sync on Demand**: `WriteSync()` forces fsync for critical safety
3. **Height Indexing**: O(1) seek to height via in-memory index
4. **Corruption Detection**: CRC32 validates each message on read
5. **Atomic Rotation**: New file per height with atomic close/open

**Recovery Process**:
1. Open WAL directory and list segments
2. Build height index (maps height → segment)
3. Seek to last committed height
4. Replay messages from that point forward
5. Resume consensus from restored state

**Thread Safety**: Internal locking ensures safe concurrent writes.

---

### privval/ Package

**Purpose**: Private validator with double-sign prevention

**Key Responsibilities**:
- Store Ed25519 private key securely
- Sign votes and proposals on request
- Prevent double-signing via LastSignState tracking
- Persist state atomically before returning signatures
- Enforce file locking to prevent multi-process access

**Key Types**:
```go
PrivValidator    // Interface for signing
FilePV           // File-based implementation
LastSignState    // Tracks last signed H/R/S/BlockHash
```

**Double-Sign Prevention Logic**:
```go
// Check if signing is allowed
func (lss *LastSignState) CheckHRS(height, round, step) error {
    if height < lss.Height {
        return ErrHeightRegression  // Crash recovery rolled back
    }
    if height == lss.Height && round < lss.Round {
        return ErrRoundRegression
    }
    if height == lss.Height && round == lss.Round && step < lss.Step {
        return ErrStepRegression
    }
    if height == lss.Height && round == lss.Round && step == lss.Step {
        return ErrDoubleSign  // Same H/R/S (unless same vote)
    }
    return nil  // OK to sign
}
```

**Atomic Persistence Pattern**:
1. Validate request (check double-sign)
2. Generate signature
3. **Persist state to disk (CRITICAL)**
4. **Only then** return signature to caller

This ensures: If signature is returned → state is on disk → no double-sign after crash.

**File Security**:
- Key file requires 0600 permissions (enforced)
- Lock file prevents concurrent access (flock)
- Atomic write-then-rename for crash safety
- Separate files for key (rarely changes) and state (frequently updated)

**Thread Safety**: Internal mutex protects signing operations.

---

### evidence/ Package

**Purpose**: Byzantine fault detection and evidence management

**Key Responsibilities**:
- Detect equivocation (double-signing)
- Collect and validate Byzantine evidence
- Track committed evidence to prevent duplicates
- Prune expired evidence to limit state growth
- Broadcast evidence for on-chain slashing

**Key Types**:
```go
Pool                     // Evidence collection pool
DuplicateVoteEvidence   // Proof of double-signing
```

**Equivocation Detection**:
```go
// Check vote against previously seen votes
func (p *Pool) CheckVote(vote, valSet, chainID) (*DuplicateVoteEvidence, error) {
    // 1. Verify signature BEFORE storing
    if err := VerifyVoteSignature(chainID, vote, validator.PublicKey); err != nil {
        return nil, err
    }

    // 2. Check if we've seen a conflicting vote
    key := voteKey(vote)  // validator/height/round/type
    if existing := p.seenVotes[key]; existing != nil {
        if !votesForSameBlock(existing, vote) {
            // Found equivocation!
            return &DuplicateVoteEvidence{
                VoteA: *CopyVote(existing),
                VoteB: *CopyVote(vote),
            }, nil
        }
    }

    // 3. Store for future comparison (deep copy)
    p.seenVotes[key] = CopyVote(vote)
    return nil, nil
}
```

**Evidence Validation**:
- Votes must be from same validator (name and index match)
- Votes must be at same height/round/type
- Votes must be for different blocks (equivocation!)
- Both signatures must be valid
- Validator must have been in set at that height

**Memory Management**:
- `MaxSeenVotes = 100,000`: Limits vote history size
- `VoteProtectionWindow = 1000`: Protects recent votes from pruning
- Prune old votes (beyond window) when limit reached
- Evidence expires after MaxEvidenceAge blocks or time

**Thread Safety**: Internal locking for concurrent vote checking.

---

## 6. Concurrency Architecture

### Goroutine Usage Patterns

Leaderberry uses a minimal number of long-lived goroutines:

**Per Engine Instance**:
1. **Consensus routine** (`receiveRoutine`): Main event loop for state transitions
2. **Timeout routine**: Manages round timeout scheduling
3. **WAL writer** (optional): Background WAL flushing

**Characteristics**:
- **Single-threaded state machine**: All state transitions in one goroutine
- **Event-driven**: select on channels for proposals, votes, timeouts
- **Clean shutdown**: WaitGroups ensure graceful termination
- **Context cancellation**: context.Context for lifecycle management

**Example: Main Consensus Loop**
```go
func (cs *ConsensusState) receiveRoutine() {
    defer cs.wg.Done()

    for {
        select {
        case <-cs.ctx.Done():
            return  // Shutdown signal

        case proposal := <-cs.proposalCh:
            cs.handleProposal(proposal)

        case vote := <-cs.voteCh:
            cs.handleVote(vote)

        case ti := <-cs.timeoutTicker.Chan():
            cs.handleTimeout(ti)
        }
    }
}
```

### Channel Communication

**Channel Buffer Strategy**:
```go
const (
    proposalChannelSize = 100      // Low volume, high value
    voteChannelSize     = 10000    // High volume, critical path
    timeoutChannelSize  = 100      // Low volume, timing-sensitive
)
```

**Buffer Sizing Rationale**:
- **Proposals**: ~1 per round × 10 validators × 10 rounds = 100 buffer
- **Votes**: 100 validators × 2 types × 50 rounds = need significant headroom
- **Timeouts**: Single active timeout + burst buffering

**Non-Blocking Send Pattern**:
```go
func (cs *ConsensusState) AddVote(vote *Vote) {
    select {
    case cs.voteCh <- vote:
        // Successfully queued
    default:
        // Channel full - drop and track metric
        dropped := atomic.AddUint64(&cs.droppedVotes, 1)
        log.Warn("Dropped vote", "height", vote.Height, "total", dropped)
    }
}
```

**Benefits**:
- Never blocks caller (prevents deadlock)
- Graceful degradation under load
- Metrics for monitoring
- Vote will be re-sent by gossip if needed

### Mutex Strategy

**RWMutex Usage** (Read-Heavy Workloads):
```go
type Engine struct {
    mu           sync.RWMutex  // Not sync.Mutex
    validatorSet *ValidatorSet
}

// Multiple concurrent readers allowed
func (e *Engine) GetValidatorSet() *ValidatorSet {
    e.mu.RLock()  // Shared read lock
    defer e.mu.RUnlock()
    return e.validatorSet.Copy()
}

// Exclusive writer
func (e *Engine) UpdateValidatorSet(valSet *ValidatorSet) {
    e.mu.Lock()  // Exclusive write lock
    defer e.mu.Unlock()
    e.validatorSet, _ = valSet.Copy()
}
```

**When to use RWMutex**:
- Read/write ratio > 10:1
- Long read operations
- Multiple goroutines reading concurrently

**Regular Mutex Usage** (Write-Heavy Workloads):
```go
type FilePV struct {
    mu sync.Mutex  // Not RWMutex
    // All operations modify lastSignState
}
```

**When to use Mutex**:
- Most operations modify state
- Short critical sections
- Single-threaded access expected

### Race Prevention

**Deep Copy Pattern** (Prevents Aliasing):
```go
// WRONG: Returns pointer to internal state
func (vs *ValidatorSet) GetByIndex(index uint16) *NamedValidator {
    return vs.byIndex[index]  // Caller can modify!
}

// CORRECT: Returns deep copy
func (vs *ValidatorSet) GetByIndex(index uint16) *NamedValidator {
    return CopyValidator(vs.byIndex[index])  // Safe
}
```

All getters return copies, ensuring:
- Thread-safe sharing without locks
- No accidental mutation of internal state
- Clear ownership semantics

**Locked Pattern** (Prevents Double-Locking):
```go
// Public API acquires lock
func (cs *ConsensusState) AddVote(vote *Vote) {
    cs.mu.Lock()
    defer cs.mu.Unlock()
    cs.addVoteLocked(vote)  // Call internal version
}

// Internal API assumes lock held
func (cs *ConsensusState) addVoteLocked(vote *Vote) {
    // Can call other *Locked methods safely
    cs.processVoteLocked(vote)
    cs.checkQuorumLocked()
}
```

**Benefits**:
- Prevents deadlock from recursive locking
- Clear contract: Public = acquires lock, *Locked = assumes lock
- Enables lock-free internal composition

**Generation Counter Pattern** (Prevents Stale References):
```go
type HeightVoteSet struct {
    generation atomic.Uint64  // Incremented on Reset()
}

type VoteSet struct {
    parent       *HeightVoteSet
    myGeneration uint64  // Captured at creation
}

func (vs *VoteSet) AddVote(vote *Vote) (bool, error) {
    // Check if stale
    if vs.parent.generation.Load() != vs.myGeneration {
        return false, ErrStaleVoteSet
    }
    // ...
}
```

After advancing to new height, old VoteSet references are invalidated.

### Deadlock Prevention

**1. Lock Ordering Discipline**:
```
Lock Hierarchy (must acquire in this order):
1. PeerSet.mu
2. PeerState.mu
3. VoteBitmap.mu
```

**Example Safe Pattern**:
```go
func (ps *PeerSet) UpdateAll() {
    ps.mu.Lock()         // Level 1
    defer ps.mu.Unlock()

    for _, peer := range ps.peers {
        peer.mu.Lock()   // Level 2 (OK: lower than Level 1)
        // ... update ...
        peer.mu.Unlock()
    }
}
```

**Example Unsafe Pattern** (DEADLOCK):
```go
func (peer *PeerState) BroadcastToSet() {
    peer.mu.Lock()      // Level 2
    defer peer.mu.Unlock()

    peerSet.mu.Lock()   // Level 1 (WRONG: higher than Level 2)
    // DEADLOCK RISK!
}
```

**2. Non-Reentrant Mutex Awareness**:

Go mutexes are NOT reentrant (same goroutine can't lock twice):
```go
// WRONG (deadlock):
func (e *Engine) GetMetrics() *Metrics {
    e.mu.RLock()
    defer e.mu.RUnlock()
    isVal := e.IsValidator()  // Tries RLock again → deadlock
}

// CORRECT:
func (e *Engine) GetMetrics() *Metrics {
    e.mu.RLock()
    defer e.mu.RUnlock()
    isVal := e.isValidatorLocked()  // Assumes lock held
}
```

**3. Channel-Based Synchronization**:

Prefer channels over shared memory + locks when possible:
```go
// Timeout scheduling via channels (no mutex needed)
func (tt *TimeoutTicker) ScheduleTimeout(ti TimeoutInfo) {
    select {
    case tt.tickCh <- ti:  // Non-blocking send
    default:
        atomic.AddUint64(&tt.droppedTimeouts, 1)
    }
}
```

Philosophy: "Share memory by communicating, don't communicate by sharing memory"

### Atomic Operations

**Lock-Free Metrics**:
```go
type ConsensusState struct {
    droppedVotes uint64  // Accessed via atomic ops only
}

// Increment without lock
func (cs *ConsensusState) AddVote(vote *Vote) {
    select {
    case cs.voteCh <- vote:
    default:
        atomic.AddUint64(&cs.droppedVotes, 1)  // Lock-free
    }
}

// Read without lock
func (cs *ConsensusState) GetMetrics() Metrics {
    return Metrics{
        DroppedVotes: atomic.LoadUint64(&cs.droppedVotes),
    }
}
```

**Benefits**:
- No lock contention on hot path
- Precise metrics without coordination
- Safe concurrent read/write

---

## 7. Consensus Protocol

### Tendermint State Machine

Leaderberry implements the Tendermint consensus protocol with these core properties:

**Round-Based Voting**:
- Each height consists of multiple rounds (Round 0, 1, 2, ...)
- Each round has three phases: Propose → Prevote → Precommit
- Rounds advance automatically on timeout
- Different proposer per round (ensures liveness)

**Locking Rules** (Safety):
- Lock on block when seeing 2/3+ prevotes for it
- Once locked, only prevote for locked block (or nil to unlock)
- Unlock if see 2/3+ prevotes for nil OR enter new round
- Prevents committing two blocks at same height

**Quorum Requirements**:
- 2/3+1 of voting power required for any decision
- Quorum = `(TotalVotingPower * 2 / 3) + 1`
- Tolerates up to 1/3 Byzantine validators

### State Transition Details

**NewHeight → NewRound**:
```go
func (cs *ConsensusState) enterNewHeight(height int64) {
    cs.height = height
    cs.round = 0
    cs.step = RoundStepNewHeight

    // Reset state
    cs.proposal = nil
    cs.lockedRound = -1
    cs.lockedBlock = nil
    cs.validRound = -1
    cs.validBlock = nil

    // Create vote tracker for new height
    cs.votes.Reset(height, cs.validatorSet)

    // Advance to round 0
    cs.enterNewRound(height, 0)
}
```

**NewRound → Propose**:
```go
func (cs *ConsensusState) enterNewRound(height int64, round int32) {
    cs.round = round
    cs.step = RoundStepNewRound

    // Get proposer for this round
    proposer := cs.validatorSet.GetProposerForRound(round)

    // Schedule propose timeout
    cs.scheduleTimeout(RoundStepPropose, round)

    // Advance to propose step
    cs.enterPropose(height, round)
}
```

**Propose → Prevote**:
```go
func (cs *ConsensusState) enterPropose(height int64, round int32) {
    cs.step = RoundStepPropose

    // If we're the proposer, create and broadcast proposal
    if cs.isProposer() {
        cs.createAndBroadcastProposal(height, round)
    }

    // Wait for proposal or timeout
}
```

**On Proposal Receipt**:
```go
func (cs *ConsensusState) handleProposal(proposal *Proposal) {
    // Validate proposal
    if err := cs.validateProposal(proposal); err != nil {
        // Invalid proposal → prevote nil
        cs.signAndBroadcastPrevote(height, round, nil)
        return
    }

    // Valid proposal → prevote for it
    blockHash := ProposalBlockHash(proposal)
    cs.signAndBroadcastPrevote(height, round, &blockHash)
}
```

**Prevote → Precommit** (with locking):
```go
func (cs *ConsensusState) handlePrevoteQuorum(blockHash *Hash) {
    // Saw 2/3+ prevotes for a block

    if blockHash != nil {
        // Lock on this block
        cs.lockedRound = cs.round
        cs.lockedBlock = cs.proposal.Block
        cs.validRound = cs.round
        cs.validBlock = cs.proposal.Block

        // Precommit for locked block
        cs.signAndBroadcastPrecommit(cs.height, cs.round, blockHash)
    } else {
        // 2/3+ prevotes for nil → unlock and precommit nil
        cs.lockedRound = -1
        cs.lockedBlock = nil
        cs.signAndBroadcastPrecommit(cs.height, cs.round, nil)
    }
}
```

**Precommit → Commit**:
```go
func (cs *ConsensusState) handlePrecommitQuorum(blockHash Hash) {
    // Saw 2/3+ precommits for a block

    // Get all precommits for this block
    precommits := cs.votes.Precommits(cs.round).GetVotesForBlock(blockHash)

    // Create commit certificate
    commit := &Commit{
        Height:     cs.height,
        Round:      cs.round,
        BlockHash:  blockHash,
        Signatures: precommitsToSignatures(precommits),
    }

    // Finalize block
    cs.finalizeCommit(cs.height, cs.proposal.Block, commit)
}
```

### POL (Proof-of-Lock) Handling

When a validator is locked on a block in round R, it must include a POL (Proof-of-Lock) when proposing that block in round R+1 or later.

**POL Contents**:
- Round where lock occurred
- 2/3+ prevotes from that round proving lock

**Why POL is needed**:
- Proves to other validators why proposer is locked
- Enables validators to skip validation if they trust POL
- Provides evidence for locking rules

**POL Verification**:
```go
func (cs *ConsensusState) verifyPOL(proposal *Proposal) error {
    if proposal.PolRound < 0 {
        return nil  // No POL
    }

    // Verify 2/3+ prevotes for this block at PolRound
    for _, vote := range proposal.PolVotes {
        if vote.Round != proposal.PolRound {
            return ErrInvalidPOL
        }
        if vote.Type != VoteTypePrevote {
            return ErrInvalidPOL
        }
        // Verify signatures...
    }

    // Check quorum
    totalPower := sumVotingPower(proposal.PolVotes)
    if totalPower < cs.validatorSet.TwoThirdsMajority() {
        return ErrInsufficientPOL
    }

    return nil
}
```

### Proposer Selection

**Weighted Round-Robin Algorithm**:

```
State: Each validator has ProposerPriority (int64)

Initialize:
  For each validator v:
    v.ProposerPriority = v.VotingPower

Select Proposer:
  1. proposer = validator with max(ProposerPriority)
     (tie-break: lexicographic name ordering)

  2. proposer.ProposerPriority -= TotalVotingPower

  3. For each validator v:
       v.ProposerPriority += v.VotingPower

  4. Center priorities:
       avg = sum(all priorities) / count
       For each validator v:
         v.ProposerPriority -= avg
```

**Properties**:
- **Fair**: Proposer frequency proportional to voting power
- **Deterministic**: Same inputs → same proposer on all nodes
- **Rotation**: Different proposer per round ensures liveness
- **Bounded**: Priorities centered to prevent overflow

**Example** (2 validators, equal power):
```
Initial:  alice=100, bob=100
Round 0:  alice selected (tie-break by name)
          alice -= 200  → alice=-100
          All += power  → alice=0, bob=200
          Center       → alice=-100, bob=100

Round 1:  bob selected (100 > -100)
          bob -= 200    → bob=-100
          All += power  → alice=0, bob=0
          Center       → alice=0, bob=0
```

### Timeout Management

**Exponential Backoff**:
```go
func calculateTimeout(step RoundStep, round int32, config TimeoutConfig) time.Duration {
    // Clamp round to prevent overflow
    if round > MaxRoundForTimeout {
        round = MaxRoundForTimeout
    }

    switch step {
    case RoundStepPropose:
        return config.Propose + time.Duration(round) * config.ProposeDelta

    case RoundStepPrevoteWait:
        return config.Prevote + time.Duration(round) * config.PrevoteDelta

    case RoundStepPrecommitWait:
        return config.Precommit + time.Duration(round) * config.PrecommitDelta

    default:
        return 1 * time.Second
    }
}
```

**Default Configuration**:
```
Propose:   3000ms + round × 500ms
Prevote:   1000ms + round × 500ms
Precommit: 1000ms + round × 500ms
Commit:    1000ms (constant)
```

**Example Timeline** (3 rounds):
```
Round 0: Propose 3.0s, Prevote 1.0s, Precommit 1.0s
Round 1: Propose 3.5s, Prevote 1.5s, Precommit 1.5s
Round 2: Propose 4.0s, Precommit 2.0s, Precommit 2.0s
```

**Why exponential backoff**:
- Early rounds have tight timeouts (fast under synchrony)
- Later rounds have looser timeouts (tolerate asynchrony)
- Network eventually synchronizes → consensus reached
- Capped at MaxRoundForTimeout to prevent overflow

---

## 8. Security Architecture

### Double-Sign Prevention

**Multi-Layer Defense**:

1. **LastSignState Tracking**:
   - Records last H/R/S/BlockHash signed
   - Persisted before signature returned
   - Checked before every signature operation

2. **Atomic Persistence**:
   ```go
   func (pv *FilePV) SignVote(chainID string, vote *Vote) error {
       // 1. Check double-sign
       if err := pv.lastSignState.CheckHRS(...); err != nil {
           return ErrDoubleSign
       }

       // 2. Generate signature
       sig := ed25519.Sign(pv.privKey, signBytes)

       // 3. Update state
       pv.lastSignState = newState

       // 4. PERSIST state (CRITICAL)
       if err := pv.saveState(); err != nil {
           panic("CONSENSUS CRITICAL: failed to persist")
       }

       // 5. ONLY NOW return signature
       vote.Signature = sig
       return nil
   }
   ```

3. **File Locking**:
   - Exclusive flock prevents concurrent processes
   - Lock acquired before loading state
   - Released only on Close()

4. **State Validation**:
   - Height regression detection (after crash)
   - Round regression detection (Byzantine)
   - Step regression detection (Byzantine)
   - Same H/R/S with different block (equivocation)

**What is Prevented**:
- Signing two different blocks at same height/round/step
- Signing after crash recovery rolled back height
- Multiple processes using same key simultaneously
- Malicious re-signing attacks

### Byzantine Fault Tolerance

**Threat Model**:
- Up to 1/3 of validators can be Byzantine (malicious)
- Byzantine validators can: send conflicting messages, be offline, collude
- Byzantine validators cannot: forge signatures, break cryptography

**Detection Mechanisms**:

1. **Equivocation Detection**:
   ```go
   // Evidence pool tracks all votes seen
   if existingVote := pool.seenVotes[voteKey]; existingVote != nil {
       if !votesForSameBlock(existingVote, vote) {
           // Found double-sign!
           evidence := &DuplicateVoteEvidence{
               VoteA: existingVote,
               VoteB: vote,
           }
           pool.AddEvidence(evidence)
       }
   }
   ```

2. **Signature Verification**:
   - Every vote signature verified before storage
   - Every commit signature verified before acceptance
   - Invalid signatures rejected immediately

3. **Locking Rules Enforcement**:
   - Validators cannot precommit without 2/3+ prevotes
   - Once locked, cannot prevote for different block
   - Violations create evidence of Byzantine behavior

**Punishment**:
- Evidence broadcast to all nodes
- Included in next block for on-chain slashing
- Application layer applies penalties (e.g., 5% stake slash)
- Validator potentially jailed or removed from set

### Signature Verification

**Ed25519 Everywhere**:
```go
// Vote signature verification
func VerifyVoteSignature(chainID string, vote *Vote, pubKey PublicKey) error {
    signBytes := VoteSignBytes(chainID, vote)
    if !ed25519.Verify(pubKey.Data, signBytes, vote.Signature.Data) {
        return ErrInvalidSignature
    }
    return nil
}
```

**Sign Bytes Construction** (Canonical):
```go
func VoteSignBytes(chainID string, vote *Vote) []byte {
    // Deterministic encoding (Cramberry)
    data, err := vote.MarshalCramberry()
    if err != nil {
        panic("CONSENSUS CRITICAL: marshal failed")
    }

    // Prepend chain ID to prevent cross-chain replay
    return append([]byte(chainID), data...)
}
```

**Why chain ID in sign bytes**:
- Prevents replay attacks across chains
- Same validator can't reuse signatures on different networks
- Chain-specific signatures ensure isolation

### Evidence Collection and Verification

**Evidence Types**:

1. **DuplicateVoteEvidence**:
   ```go
   type DuplicateVoteEvidence struct {
       VoteA Vote  // First vote
       VoteB Vote  // Conflicting vote
       // Must have: same H/R/Type, same validator, different blocks
   }
   ```

**Validation Rules**:
```go
func VerifyDuplicateVoteEvidence(ev *DuplicateVoteEvidence, chainID string, valSet *ValidatorSet) error {
    // 1. Votes from same validator?
    if ev.VoteA.Validator != ev.VoteB.Validator {
        return ErrInvalidEvidence
    }

    // 2. Same height/round/type?
    if ev.VoteA.Height != ev.VoteB.Height ||
       ev.VoteA.Round != ev.VoteB.Round ||
       ev.VoteA.Type != ev.VoteB.Type {
        return ErrInvalidEvidence
    }

    // 3. Different blocks? (actual equivocation)
    if votesForSameBlock(&ev.VoteA, &ev.VoteB) {
        return ErrSameBlockHash
    }

    // 4. Both signatures valid?
    validator := valSet.GetByIndex(ev.VoteA.ValidatorIndex)
    if err := VerifyVoteSignature(chainID, &ev.VoteA, validator.PublicKey); err != nil {
        return err
    }
    if err := VerifyVoteSignature(chainID, &ev.VoteB, validator.PublicKey); err != nil {
        return err
    }

    return nil
}
```

### File Locking (Multi-Process Safety)

**Lock Acquisition**:
```go
func (pv *FilePV) acquireLock() error {
    lockPath := pv.stateFilePath + ".lock"
    lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
    if err != nil {
        return err
    }

    // Exclusive, non-blocking lock
    if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
        lockFile.Close()
        return fmt.Errorf("another process holds validator lock")
    }

    pv.lockFile = lockFile
    return nil
}
```

**Why file locking**:
- Prevents accidental double-signing from running multiple instances
- OS-level protection (survives process crashes)
- Non-blocking detection (immediate error if locked)

### Memory Safety (Deep Copy Pattern)

**Problem**: Aliasing allows unintended mutation
```go
// UNSAFE: Returns pointer to internal state
func (vs *ValidatorSet) GetByIndex(idx uint16) *NamedValidator {
    return vs.validators[idx]  // Caller can modify!
}
```

**Solution**: Always return copies
```go
// SAFE: Returns independent copy
func (vs *ValidatorSet) GetByIndex(idx uint16) *NamedValidator {
    return CopyValidator(vs.validators[idx])
}

func CopyValidator(v *NamedValidator) *NamedValidator {
    if v == nil {
        return nil
    }
    return &NamedValidator{
        Name:             CopyAccountName(v.Name),
        PublicKey:        CopyPublicKey(v.PublicKey),
        VotingPower:      v.VotingPower,
        ProposerPriority: v.ProposerPriority,
        Index:            v.Index,
    }
}
```

**Where applied**:
- All getters return copies
- Store copies of inputs (before adding to internal state)
- Pass copies to callbacks (async execution)
- Copy before returning from WAL replay

---

## 9. Crash Recovery

### WAL Design and Format

**File Format**:
```
Entry ::= [Length:4][Data:N][CRC32:4]

Length: uint32 big-endian (size of Data)
Data:   Cramberry-encoded Message
CRC32:  IEEE checksum of Data
```

**Why this format**:
- **Self-framing**: Length prefix enables seeking
- **Corruption detection**: CRC32 validates each message
- **Bounded allocation**: Length limits prevent DoS
- **Efficient parsing**: Single pass, no backtracking

**Segment Naming**:
```
wal-00001  (segment index 1)
wal-00002  (segment index 2)
...
```

### Message Replay

**Recovery Process**:

1. **Scan Directory**:
   ```go
   files, _ := filepath.Glob(filepath.Join(walDir, "wal-*"))
   sort.Strings(files)  // Ensure chronological order
   ```

2. **Build Height Index**:
   ```go
   heightIndex := make(map[int64]int)  // height → segment index
   for segIdx, file := range files {
       decoder := NewDecoder(file)
       for {
           msg, err := decoder.Decode()
           if err == io.EOF {
               break
           }
           if msg.Type == MsgTypeEndHeight {
               heightIndex[msg.Height] = segIdx
           }
       }
   }
   ```

3. **Seek to Recovery Point**:
   ```go
   // Find last committed height
   startHeight := lastCommittedHeight + 1

   // Seek to beginning of that height
   segIdx, ok := heightIndex[startHeight]
   if !ok {
       // Height not in WAL, start from beginning
       segIdx = 0
   }

   reader := NewSegmentReader(segIdx)
   ```

4. **Replay Messages**:
   ```go
   for {
       msg, err := reader.Read()
       if err == io.EOF {
           break
       }
       if err != nil {
           return fmt.Errorf("WAL corrupted: %w", err)
       }

       // Replay message through consensus
       switch msg.Type {
       case MsgTypeProposal:
           proposal, _ := DecodeProposal(msg.Data)
           engine.AddProposal(proposal)

       case MsgTypeVote:
           vote, _ := DecodeVote(msg.Data)
           engine.AddVote(vote)

       case MsgTypeEndHeight:
           // Height complete, can fast-forward if needed
       }
   }
   ```

### State Restoration

**Consensus State Recovery**:
```go
func (cs *ConsensusState) RecoverFromWAL() error {
    // 1. Start from last committed height
    cs.height = lastCommittedHeight + 1
    cs.round = 0

    // 2. Replay WAL messages
    reader, _, err := cs.wal.SearchForEndHeight(lastCommittedHeight)
    if err != nil {
        return err
    }

    for {
        msg, err := reader.Read()
        if err == io.EOF {
            break
        }

        // Apply message to state
        cs.replayMessage(msg)
    }

    // 3. Resume consensus from recovered state
    return cs.Start(cs.height, nil)
}
```

**PrivValidator State Recovery**:
```go
func LoadFilePV(keyPath, statePath string) (*FilePV, error) {
    pv := &FilePV{}

    // 1. Load key (rarely changes)
    pv.loadKey(keyPath)

    // 2. CRITICAL: Acquire lock BEFORE loading state
    // Prevents TOCTOU race where state changes between load and lock
    if err := pv.acquireLock(); err != nil {
        return nil, err
    }

    // 3. Load state (guaranteed fresh after lock)
    pv.loadState(statePath)

    return pv, nil
}
```

### LastSignState Persistence

**File Format** (JSON for human readability):
```json
{
  "height": 12345,
  "round": 2,
  "step": 1,
  "signature": "base64...",
  "block_hash": "hex...",
  "sign_bytes_hash": "hex...",
  "timestamp": 1234567890
}
```

**Atomic Write Pattern**:
```go
func (pv *FilePV) saveState() error {
    // 1. Serialize state
    data, _ := json.MarshalIndent(pv.lastSignState, "", "  ")

    // 2. Write to temp file
    tmpPath := pv.stateFilePath + ".tmp"
    if err := os.WriteFile(tmpPath, data, 0600); err != nil {
        return err
    }

    // 3. Sync temp file
    tmpFile, _ := os.Open(tmpPath)
    tmpFile.Sync()
    tmpFile.Close()

    // 4. Atomic rename (POSIX guarantee)
    return os.Rename(tmpPath, pv.stateFilePath)
}
```

**Why atomic**:
- Write to temp prevents corruption of live file
- Sync ensures data on disk before rename
- Rename is atomic (all-or-nothing)
- Crash during write → old state intact, temp file orphaned

### Height-Indexed Segments

**Index Structure**:
```go
type FileWAL struct {
    heightIndex map[int64]int  // height → segment index
}
```

**Index Building**:
```go
func (w *FileWAL) buildIndex() error {
    for segIdx := 0; segIdx < w.numSegments; segIdx++ {
        decoder := w.openSegment(segIdx)
        for {
            msg, err := decoder.Decode()
            if err == io.EOF {
                break
            }
            if msg.Type == MsgTypeEndHeight {
                w.heightIndex[msg.Height] = segIdx
            }
        }
    }
    return nil
}
```

**Fast Seek**:
```go
func (w *FileWAL) SearchForEndHeight(height int64) (Reader, bool, error) {
    // O(1) lookup instead of O(N) scan
    segIdx, ok := w.heightIndex[height]
    if !ok {
        return nil, false, nil
    }

    // Open segment and scan to exact position
    reader := w.openSegment(segIdx)
    for {
        msg, err := reader.Read()
        if msg.Type == MsgTypeEndHeight && msg.Height == height {
            return reader, true, nil  // Positioned after EndHeight
        }
    }
}
```

**Performance**:
- Without index: O(N) scan through all segments
- With index: O(1) to find segment + O(M) to scan segment (M << N)
- Typical: 10,000 segments → 10ms seek vs 10s scan

---

## 10. Design Patterns

Leaderberry employs 7+ documented design patterns for safety, performance, and maintainability.

### 1. Deep Copy Pattern

**Problem**: Returning pointers allows caller to corrupt internal state

**Solution**: Always return independent copies
```go
// Implementation
func CopyValidator(v *NamedValidator) *NamedValidator {
    if v == nil {
        return nil
    }
    return &NamedValidator{
        Name:             CopyAccountName(v.Name),
        Index:            v.Index,
        PublicKey:        CopyPublicKey(v.PublicKey),
        VotingPower:      v.VotingPower,
        ProposerPriority: v.ProposerPriority,
    }
}

// Usage
func (vs *ValidatorSet) GetByIndex(idx uint16) *NamedValidator {
    return CopyValidator(vs.validators[idx])
}
```

**When to Copy**:
1. Before storing: Copy input to prevent caller modification
2. Before returning: Copy internal state to prevent caller modification
3. Before callbacks: Copy data passed to async callbacks

**Benefits**:
- Thread-safe sharing without locks
- Clear ownership semantics
- Prevents accidental mutation bugs

**Trade-off**: Higher allocation cost vs safety guarantees

---

### 2. Locked Pattern

**Problem**: Need both external (locking) and internal (lock-free) APIs

**Solution**: Public methods acquire lock, internal `*Locked` methods assume lock held
```go
// Public version - acquires lock
func (cs *ConsensusState) enterNewRound(height int64, round int32) {
    cs.mu.Lock()
    defer cs.mu.Unlock()
    cs.enterNewRoundLocked(height, round)
}

// Internal version - assumes lock held
func (cs *ConsensusState) enterNewRoundLocked(height int64, round int32) {
    // Safe to call other *Locked methods
    if someCondition {
        cs.enterPrevoteLocked(height, round)
    }
}
```

**Naming Convention**:
- No suffix: Public API, acquires lock
- `*Locked` suffix: Internal API, assumes lock held
- Clear documentation comment: "MUST be called with cs.mu held"

**Benefits**:
- Prevents double-locking deadlocks
- Clear API contract
- Efficient internal composition (no repeated lock/unlock)

**Usage Count**: ~15 method pairs in ConsensusState

---

### 3. Generation Counter Pattern

**Problem**: After Reset(), old references might still exist and accept operations, losing data

**Solution**: Increment counter on Reset(), check counter on operations
```go
type HeightVoteSet struct {
    generation atomic.Uint64  // Incremented on Reset()
    prevotes   map[int32]*VoteSet
}

type VoteSet struct {
    parent       *HeightVoteSet
    myGeneration uint64  // Captured at creation
}

func newVoteSet(parent *HeightVoteSet) *VoteSet {
    return &VoteSet{
        parent:       parent,
        myGeneration: parent.generation.Load(),  // Capture current
    }
}

func (vs *VoteSet) AddVote(vote *Vote) (bool, error) {
    // Check if stale
    if vs.parent != nil {
        currentGen := vs.parent.generation.Load()
        if currentGen != vs.myGeneration {
            return false, ErrStaleVoteSet
        }
    }
    // Safe to add
}

func (hvs *HeightVoteSet) Reset(height int64, valSet *ValidatorSet) {
    hvs.generation.Add(1)  // Invalidate all old VoteSets
    hvs.prevotes = make(map[int32]*VoteSet)
}
```

**Benefits**:
- Detects stale references at runtime
- Prevents silent data loss
- Atomic generation check (no deadlock with parent)

---

### 4. Atomic Persistence Pattern

**Problem**: If signature returned before state persisted, crash enables double-sign

**Solution**: Persist state BEFORE returning signature
```go
func (pv *FilePV) SignVote(chainID string, vote *Vote) error {
    pv.mu.Lock()
    defer pv.mu.Unlock()

    // 1. Check for double-sign
    if err := pv.lastSignState.CheckHRS(...); err != nil {
        return ErrDoubleSign
    }

    // 2. Sign the vote
    sig := ed25519.Sign(pv.privKey, signBytes)

    // 3. Update in-memory state
    pv.lastSignState.Height = vote.Height
    pv.lastSignState.Round = vote.Round
    pv.lastSignState.Signature = sig

    // 4. CRITICAL: Persist BEFORE returning
    if err := pv.saveState(); err != nil {
        panic("CONSENSUS CRITICAL: failed to persist")
    }

    // 5. ONLY NOW safe to return signature
    vote.Signature = sig
    return nil
}
```

**Invariant**: Signature returned ⟹ State persisted

**Security Property**: Crash after signing but before persist → node refuses re-sign on restart

---

### 5. First-Error Pattern

**Problem**: Multiple cleanup operations, want to preserve first error

**Solution**: Track first error, always attempt all cleanup
```go
func (w *FileWAL) Stop() error {
    var firstErr error

    // Flush buffer
    if err := w.buf.Flush(); err != nil && firstErr == nil {
        firstErr = err
    }

    // Sync file
    if err := w.file.Sync(); err != nil && firstErr == nil {
        firstErr = err
    }

    // Close file (even if previous operations failed)
    if err := w.file.Close(); err != nil && firstErr == nil {
        firstErr = err
    }

    return firstErr
}
```

**Benefits**:
- Ensures all cleanup attempted
- Preserves first error for diagnostics
- Prevents resource leaks from early return

---

### 6. Non-Blocking Channel Send

**Problem**: Blocking on full channel can deadlock caller

**Solution**: Use select with default case
```go
func (tt *TimeoutTicker) ScheduleTimeout(ti TimeoutInfo) {
    select {
    case tt.tickCh <- ti:
        // Successfully scheduled
    default:
        // Channel full - log and drop
        count := atomic.AddUint64(&tt.droppedSchedules, 1)
        log.Warn("Timeout schedule dropped", "total", count)
    }
}
```

**Rationale**:
- Dropped timeouts are rescheduled on next state transition
- Better to drop one timeout than deadlock the system
- Metrics track dropped messages for monitoring

---

### 7. Immutable Builder Pattern

**Problem**: In-place modification races with concurrent readers

**Solution**: Return new instance with modifications
```go
// DEPRECATED: Mutable (unsafe)
func (vs *ValidatorSet) IncrementProposerPriority(times int32) {
    // Modifies vs in place
}

// PREFERRED: Immutable (safe)
func (vs *ValidatorSet) WithIncrementedPriority(times int32) (*ValidatorSet, error) {
    // Create deep copy
    newVS, err := vs.Copy()
    if err != nil {
        return nil, err
    }

    // Modify copy
    newVS.IncrementProposerPriority(times)

    // Return new instance
    return newVS, nil
}

// Usage
newValSet, _ := cs.validatorSet.WithIncrementedPriority(1)
cs.validatorSet = newValSet  // Atomic pointer swap
```

**Benefits**:
- Thread-safe (copy-on-write)
- Original unmodified (safe for concurrent readers)
- Clear ownership semantics

---

## 11. Performance Considerations

### Memory Pooling

**sync.Pool for Temporary Buffers**:
```go
var decoderPool = sync.Pool{
    New: func() interface{} {
        buf := make([]byte, 0, 65536)  // 64KB initial capacity
        return &buf
    },
}

func (d *decoder) Decode() (*Message, error) {
    // Get buffer from pool
    bufPtr := decoderPool.Get().(*[]byte)
    defer func() {
        // Return to pool (reset length, keep capacity)
        *bufPtr = (*bufPtr)[:0]
        decoderPool.Put(bufPtr)
    }()

    // Use buffer for decoding...
}
```

**Benefits**:
- Reduces GC pressure (reuse allocations)
- Amortizes allocation cost across operations
- Automatic memory reclamation (GC cleans unused pool entries)

**When to use**:
- Temporary buffers in hot paths
- Frequent allocations of same size
- Not for long-lived objects (defeats pooling)

### Atomic Operations

**Lock-Free Counters**:
```go
type VoteSet struct {
    droppedVotes uint64  // Accessed via atomic ops
}

// Increment without lock
func (vs *VoteSet) AddVote(vote *Vote) {
    select {
    case vs.voteCh <- vote:
    default:
        atomic.AddUint64(&vs.droppedVotes, 1)  // Lock-free
    }
}

// Read without lock
func (vs *VoteSet) GetMetrics() Metrics {
    return Metrics{
        DroppedVotes: atomic.LoadUint64(&vs.droppedVotes),
    }
}
```

**Benefits**:
- No lock contention
- Single-instruction operations (fast)
- Safe concurrent read/write

**Use cases**:
- Metrics and counters
- Flags and state bits
- Reference counting

### Pre-Sized Allocations

**When Capacity is Known**:
```go
// GOOD: Pre-size slice
votes := make([]*Vote, 0, len(vs.votes))
for _, v := range vs.votes {
    votes = append(votes, CopyVote(v))
}

// BAD: Repeated reallocation
var votes []*Vote
for _, v := range vs.votes {
    votes = append(votes, CopyVote(v))  // Grows exponentially
}
```

**Growth pattern**:
- Without pre-sizing: 0 → 1 → 2 → 4 → 8 → 16 → ... (log N reallocations)
- With pre-sizing: Single allocation of exact size

**Benefit**: Reduces allocations and copying

### Efficient Algorithms

**O(1) Quorum Detection**:
```go
type VoteSet struct {
    sum          int64              // Total voting power
    votesByBlock map[string]int64  // Per-block power
    maj23        *Hash              // Block with 2/3+, if any
}

func (vs *VoteSet) AddVote(vote *Vote) {
    // Check overflow BEFORE adding
    if vote.VotingPower > math.MaxInt64 - vs.sum {
        return ErrOverflow
    }

    // Update totals
    vs.sum += vote.VotingPower
    vs.votesByBlock[blockKey] += vote.VotingPower

    // Check quorum (O(1))
    quorum := vs.validatorSet.TwoThirdsMajority()
    if vs.votesByBlock[blockKey] >= quorum && vs.maj23 == nil {
        vs.maj23 = vote.BlockHash  // Record quorum
    }
}
```

**Without running sum**: O(N) to recompute total power on every vote
**With running sum**: O(1) quorum check

### Channel Buffer Tuning

**Buffer Sizing Strategy**:
```go
const (
    proposalChannelSize = 100      // Low volume, high value
    voteChannelSize     = 10000    // High volume, critical path
    timeoutChannelSize  = 100      // Low volume, timing-sensitive
)
```

**Trade-off**:
- Larger buffer → less blocking, more memory
- Smaller buffer → more blocking, less memory

**Tuning guidance**:
- Measure peak message rate under load
- Size buffer for 99th percentile burst
- Monitor dropped message metrics

### Read-Optimized Data Structures

**RWMutex for Read-Heavy Workloads**:
```go
type Engine struct {
    mu           sync.RWMutex  // Not sync.Mutex
    validatorSet *ValidatorSet
}

// Read operations (multiple concurrent readers)
func (e *Engine) GetValidatorSet() *ValidatorSet {
    e.mu.RLock()  // Shared lock
    defer e.mu.RUnlock()
    return e.validatorSet.Copy()
}

// Write operations (exclusive access)
func (e *Engine) UpdateValidatorSet(valSet *ValidatorSet) {
    e.mu.Lock()  // Exclusive lock
    defer e.mu.Unlock()
    e.validatorSet = valSet.Copy()
}
```

**When effective**: Read/write ratio > 10:1

**Benchmarks** (hypothetical):
```
Mutex:    10 readers @ 1ms each = 10ms total (sequential)
RWMutex:  10 readers @ 1ms each = 1ms total (parallel)
```

---

## 12. Testing Strategy

### Table-Driven Tests

**Pattern**:
```go
func TestVoteSet_AddVote(t *testing.T) {
    tests := []struct {
        name      string
        vote      *Vote
        existing  *Vote
        wantAdded bool
        wantErr   error
    }{
        {
            name:      "first vote succeeds",
            vote:      &Vote{Height: 1, Round: 0, ValidatorIndex: 0},
            wantAdded: true,
        },
        {
            name:      "duplicate vote rejected",
            vote:      &Vote{Height: 1, Round: 0, ValidatorIndex: 0},
            existing:  &Vote{Height: 1, Round: 0, ValidatorIndex: 0},
            wantAdded: false,
        },
        {
            name:      "equivocation detected",
            vote:      &Vote{Height: 1, Round: 0, ValidatorIndex: 0, BlockHash: &hash1},
            existing:  &Vote{Height: 1, Round: 0, ValidatorIndex: 0, BlockHash: &hash2},
            wantAdded: false,
            wantErr:   ErrConflictingVote,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            vs := NewVoteSet(...)
            if tt.existing != nil {
                vs.AddVote(tt.existing)
            }

            got, err := vs.AddVote(tt.vote)

            if !errors.Is(err, tt.wantErr) {
                t.Errorf("AddVote() error = %v, want %v", err, tt.wantErr)
            }
            if got != tt.wantAdded {
                t.Errorf("AddVote() = %v, want %v", got, tt.wantAdded)
            }
        })
    }
}
```

**Benefits**:
- Clear test case documentation
- Easy to add new cases
- Parallel execution with t.Run()

### Race Detection

**Always run with -race flag**:
```bash
go test -race -v ./...
```

**What it catches**:
- Concurrent map read/write
- Unprotected field access
- Missing mutex guards
- Atomic operation violations

**Example caught**:
```go
// Race detected:
func (hvs *HeightVoteSet) Height() int64 {
    return hvs.height  // READ without lock
}

func (hvs *HeightVoteSet) Reset(height int64) {
    hvs.height = height  // WRITE without lock
}

// Fixed:
func (hvs *HeightVoteSet) Height() int64 {
    hvs.mu.RLock()
    defer hvs.mu.RUnlock()
    return hvs.height
}
```

### Byzantine Scenario Testing

**Equivocation Test**:
```go
func TestEvidence_DoubleSign(t *testing.T) {
    // Setup validator
    pv := privval.GenerateFilePV(...)
    valSet := types.NewValidatorSet(...)

    // Validator signs two different blocks at same H/R
    vote1 := &types.Vote{
        Height: 1, Round: 0, Type: types.VoteTypePrevote,
        BlockHash: &blockHash1,
        ValidatorIndex: 0,
    }
    pv.SignVote("chain-id", vote1)

    vote2 := &types.Vote{
        Height: 1, Round: 0, Type: types.VoteTypePrevote,
        BlockHash: &blockHash2,  // Different!
        ValidatorIndex: 0,
    }
    err := pv.SignVote("chain-id", vote2)

    // Should be rejected as double-sign
    if !errors.Is(err, privval.ErrDoubleSign) {
        t.Fatal("Expected double-sign error")
    }

    // Test evidence detection
    pool := evidence.NewPool(evidence.DefaultConfig())
    ev1, _ := pool.CheckVote(vote1, valSet, "chain-id")
    // Should be nil (first vote)

    ev2, _ := pool.CheckVote(vote2, valSet, "chain-id")
    // Should return DuplicateVoteEvidence
    if ev2 == nil {
        t.Fatal("Expected equivocation to be detected")
    }
}
```

### Integration Tests

**End-to-End Consensus Test**:
```go
func TestConsensus_ThreeValidators(t *testing.T) {
    // Create 3 validators
    vals := []*types.NamedValidator{
        makeValidator("alice", 100),
        makeValidator("bob", 100),
        makeValidator("charlie", 100),
    }
    valSet, _ := types.NewValidatorSet(vals)

    // Create 3 consensus engines
    engines := make([]*engine.Engine, 3)
    for i := 0; i < 3; i++ {
        pv := privval.GenerateFilePV(...)
        wal := wal.NewFileWAL(...)
        engines[i] = engine.NewEngine(config, valSet, pv, wal, nil)
    }

    // Connect engines (simulate network)
    connectEngines(engines)

    // Start consensus
    for _, eng := range engines {
        eng.Start(1, nil)
    }

    // Wait for block 1 commitment
    waitForHeight(engines, 1, 10*time.Second)

    // Verify all committed same block
    block1 := getCommittedBlock(engines[0], 1)
    block2 := getCommittedBlock(engines[1], 1)
    block3 := getCommittedBlock(engines[2], 1)

    if !types.HashEqual(types.BlockHash(block1), types.BlockHash(block2)) {
        t.Fatal("Validators committed different blocks")
    }
    if !types.HashEqual(types.BlockHash(block1), types.BlockHash(block3)) {
        t.Fatal("Validators committed different blocks")
    }
}
```

### Mock Interfaces

**BlockExecutor Mock**:
```go
type MockBlockExecutor struct {
    createBlockFn   func() (*types.Block, error)
    validateBlockFn func() error
    applyBlockFn    func() ([]ValidatorUpdate, error)
}

func (m *MockBlockExecutor) CreateProposalBlock(height int64, lastCommit *types.Commit, proposer types.AccountName) (*types.Block, error) {
    if m.createBlockFn != nil {
        return m.createBlockFn()
    }
    return &types.Block{...}, nil
}

// Usage
func TestEngine_Propose(t *testing.T) {
    executor := &MockBlockExecutor{
        createBlockFn: func() (*types.Block, error) {
            return testBlock, nil
        },
    }

    eng := engine.NewEngine(config, valSet, pv, wal, executor)
    // Test proposal creation...
}
```

---

## 13. Build and Development

### Code Generation Workflow

**Schema-First Development**:
```
1. Define Types (schema/*.cram)
   ↓
2. Generate Code (make generate)
   ↓
3. Extend Types (types/*.go)
   ↓
4. Use in Packages (engine, wal, etc.)
```

**Cramberry Schema Example**:
```cram
// schema/vote.cram
struct Vote {
    type: VoteType [required]
    height: i64 [required]
    round: i32 [required]
    block_hash: Hash? [optional]
    timestamp: i64 [required]
    validator: AccountName [required]
    validator_index: u16 [required]
    signature: Signature [required]
}
```

**Generated Code** (`types/generated/vote.go`):
```go
// DO NOT EDIT - generated by cramberry

type Vote struct {
    Type           VoteType
    Height         int64
    Round          int32
    BlockHash      *Hash
    Timestamp      int64
    Validator      AccountName
    ValidatorIndex uint16
    Signature      Signature
}

func (v *Vote) MarshalCramberry() ([]byte, error) { /* generated */ }
func (v *Vote) UnmarshalCramberry(data []byte) error { /* generated */ }
```

**Hand-Written Extensions** (`types/vote.go`):
```go
// Type alias for convenience
type Vote = gen.Vote

// VoteSignBytes returns bytes to sign
func VoteSignBytes(chainID string, v *Vote) []byte {
    data, _ := v.MarshalCramberry()
    return append([]byte(chainID), data...)
}

// VerifyVoteSignature verifies signature
func VerifyVoteSignature(chainID string, vote *Vote, pubKey PublicKey) error {
    signBytes := VoteSignBytes(chainID, vote)
    if !ed25519.Verify(pubKey.Data, signBytes, vote.Signature.Data) {
        return ErrInvalidSignature
    }
    return nil
}
```

### Build Process

**Makefile**:
```makefile
.PHONY: generate build test lint clean

# Generate code from schemas
generate:
	cramberry generate -lang go -out ./types/generated ./schema/*.cram

# Build all packages
build: generate
	go build ./...

# Run tests with race detection
test: generate
	go test -race -v ./...

# Run linter
lint:
	golangci-lint run

# Clean generated files
clean:
	rm -rf types/generated
```

**Usage**:
```bash
# Full rebuild
make clean generate build test lint

# Development workflow
make generate  # After schema changes
make build     # Check compilation
make test      # Run tests
```

### Test Execution

**Unit Tests**:
```bash
# All tests with race detection
go test -race -v ./...

# Specific package
go test -race -v ./engine

# Specific test
go test -race -v ./engine -run TestEngine_Start

# With coverage
go test -race -cover ./...
```

**Integration Tests**:
```bash
# Run integration tests
go test -v ./test/integration

# With verbose logging
go test -v ./test/integration -args -v
```

### Linting

**golangci-lint Configuration** (`.golangci.yml`):
```yaml
linters:
  enable:
    - errcheck      # Check error returns
    - gosimple      # Simplify code
    - govet         # Vet examination
    - ineffassign   # Detect unused assignments
    - staticcheck   # Static analysis
    - unused        # Detect unused code
    - misspell      # Spelling errors

linters-settings:
  errcheck:
    check-blank: true
```

**Run Linter**:
```bash
golangci-lint run

# With auto-fix
golangci-lint run --fix
```

---

## 14. Integration Points

### Blockberry (Node Framework)

**Interface**: `consensus.ConsensusEngine`
```go
type ConsensusEngine interface {
    Start(height int64, lastCommit *Commit) error
    Stop() error
    AddProposal(proposal *Proposal) error
    AddVote(vote *Vote) error
}
```

**Leaderberry Implementation**:
```go
type Engine struct {
    // Implements consensus.ConsensusEngine
}

func (e *Engine) Start(height int64, lastCommit *Commit) error {
    // Initialize consensus state machine
    return e.state.Start(height, lastCommit)
}
```

**Integration**:
- Blockberry creates Engine instance
- Passes BlockExecutor for application integration
- Handles network message routing
- Manages validator lifecycle

---

### Looseberry (DAG Mempool)

**Interface**: `BlockExecutor` (implemented by application)
```go
type BlockExecutor interface {
    CreateProposalBlock(height int64, lastCommit *Commit, proposer AccountName) (*Block, error)
    ValidateBlock(block *Block) error
    ApplyBlock(block *Block, commit *Commit) ([]ValidatorUpdate, error)
}
```

**Looseberry Integration**:
```go
func (app *Application) CreateProposalBlock(height int64, lastCommit *Commit, proposer AccountName) (*Block, error) {
    // Reap certified batches from DAG
    batches := app.looseberry.ReapCertifiedBatches(maxSize)

    // Create block with batch certificates
    block := &types.Block{
        Header: types.NewBlockHeader(...),
        Data: types.BlockData{
            BatchCertRefs: batchesToRefs(batches),
        },
    }

    return block, nil
}

func (app *Application) ApplyBlock(block *Block, commit *Commit) ([]ValidatorUpdate, error) {
    // Execute transactions from batches
    for _, certRef := range block.Data.BatchCertRefs {
        batch := app.looseberry.GetBatch(certRef)
        app.executeBatch(batch)
    }

    // Notify Looseberry of commitment
    app.looseberry.NotifyCommitted(block.Header.Height, block.Data.BatchCertRefs)

    return nil, nil
}
```

**Key Points**:
- Blocks contain batch certificate references, NOT raw transactions
- Looseberry maintains DAG ordering
- Consensus finalizes batch order
- Application executes batches

---

### Cramberry (Serialization)

**Generated Code**:
```go
// All consensus types implement:
type SerializableType interface {
    MarshalCramberry() ([]byte, error)
    UnmarshalCramberry(data []byte) error
}
```

**Usage**:
```go
// Serialize vote
data, err := vote.MarshalCramberry()

// Deserialize vote
vote := &types.Vote{}
err = vote.UnmarshalCramberry(data)
```

**Properties**:
- **Deterministic**: Same object → same bytes (always)
- **Efficient**: Zero-copy deserialization where possible
- **Bounded**: No unbounded allocations
- **Versioned**: Schema evolution support

---

### Glueberry (P2P Networking)

**Message Broadcasting**:
```go
// Engine callbacks for broadcasting
engine.SetProposalBroadcaster(func(p *Proposal) {
    data, _ := p.MarshalCramberry()
    glueberry.BroadcastMessage(ConsensusChannel, data)
})

engine.SetVoteBroadcaster(func(v *Vote) {
    data, _ := v.MarshalCramberry()
    glueberry.BroadcastMessage(ConsensusChannel, data)
})
```

**Message Receiving**:
```go
// Glueberry calls engine with received messages
func (node *Node) handleConsensusMessage(peerID string, data []byte) {
    engine.HandleConsensusMessage(peerID, data)
}
```

**Features**:
- Encrypted communication (TLS 1.3)
- Peer discovery and management
- Message routing by channel
- Rate limiting and filtering

---

### Application Layer (Transaction Execution)

**Separation of Concerns**:

```
Consensus Layer (Leaderberry):
- Orders transactions
- Finalizes blocks
- Verifies authorization

Application Layer:
- Executes transactions
- Updates state
- Applies validator changes
- Interprets transaction semantics
```

**BlockExecutor Interface**:
```go
type BlockExecutor interface {
    // Create block proposal
    CreateProposalBlock(height int64, lastCommit *Commit, proposer AccountName) (*Block, error)

    // Validate block (application-specific checks)
    ValidateBlock(block *Block) error

    // Apply block to state (returns validator updates)
    ApplyBlock(block *Block, commit *Commit) ([]ValidatorUpdate, error)
}

type ValidatorUpdate struct {
    Name        AccountName
    PublicKey   PublicKey
    VotingPower int64  // 0 to remove
}
```

**Validator Set Updates**:
```go
func (app *Application) ApplyBlock(block *Block, commit *Commit) ([]ValidatorUpdate, error) {
    // Execute transactions, update state...

    // Return validator changes (if any)
    updates := []ValidatorUpdate{
        {Name: "alice", PublicKey: newKey, VotingPower: 100},
        {Name: "bob", PublicKey: bobKey, VotingPower: 0},  // Remove
    }

    return updates, nil
}

// Engine applies updates to validator set
func (e *Engine) applyBlockUpdates(updates []ValidatorUpdate) {
    newValSet, _ := e.validatorSet.ApplyUpdates(updates)
    e.UpdateValidatorSet(newValSet)
}
```

---

## 15. Future Considerations

### Performance Targets

| Metric | Target | Current Status |
|--------|--------|----------------|
| Block time | 1-3 seconds | Configurable (timeout-based) |
| Finality | Instant | Achieved (2/3+ precommit) |
| Recovery time | < 30 seconds | Depends on WAL size |
| Max validators | 100 | Validated up to 100 |

**Optimization Opportunities**:
- Vote aggregation (combine multiple votes per message)
- Proposal compression (reduce block gossip size)
- Parallel vote verification (verify signatures concurrently)
- WAL compression (reduce disk I/O)

### Scalability

**Current Design**:
- Communication: O(N²) where N = validator count
- Vote processing: O(N) per round
- Memory: O(N) for validator set and vote tracking

**Scaling Beyond 100 Validators**:
1. **Vote Aggregation**: Combine signatures (BLS aggregation)
2. **Committee Selection**: Rotate active validator subset
3. **Sharding**: Multiple consensus instances for parallel chains
4. **Optimistic Fast Path**: Skip rounds under synchrony

### Extensibility Points

**1. Custom Proposal Validation**:
```go
// Application can add custom validation
func (app *Application) ValidateBlock(block *Block) error {
    // Check application-specific invariants
    if block.Header.AppHash != app.expectedHash {
        return ErrInvalidAppHash
    }
    return nil
}
```

**2. Custom Evidence Types**:
```go
// Future: Application-specific evidence
type CustomEvidence struct {
    Type string
    Data []byte
}

// Register custom evidence handler
engine.RegisterEvidenceHandler("custom", handleCustomEvidence)
```

**3. Pluggable Cryptography**:
```go
// Future: Support multiple signature schemes
type SignatureScheme interface {
    Sign(privKey []byte, data []byte) ([]byte, error)
    Verify(pubKey []byte, data []byte, sig []byte) bool
}

engine.SetSignatureScheme(NewEd25519Scheme())
```

### Research Directions

**1. Threshold Signatures**:
- BLS signature aggregation
- Reduce vote message size from O(N) to O(1)
- Enable efficient light clients

**2. Optimistic Execution**:
- Execute blocks before finality
- Rollback on different commit
- Reduces latency under normal operation

**3. Dynamic Validator Sets**:
- Validators join/leave without coordination
- PoS-style validator rotation
- Delegation and re-delegation

**4. Cross-Chain Consensus**:
- Inter-blockchain communication (IBC)
- Consensus proofs for other chains
- Atomic cross-chain transactions

---

## Appendix A: File Inventory

```
Total Files: ~50 Go files
├── Schema Files: 9 (.cram files)
├── Generated Code: 10 (types/generated/*.go)
├── Hand-Written: 40
│   ├── Types: 10 (types/*.go)
│   ├── Engine: 16 (engine/*.go)
│   ├── WAL: 4 (wal/*.go)
│   ├── PrivVal: 3 (privval/*.go)
│   ├── Evidence: 2 (evidence/*.go)
│   └── Tests: 11 (*_test.go)

Total Lines of Code: ~15,000
├── Generated: ~3,000
├── Implementation: ~9,000
├── Tests: ~3,000
```

## Appendix B: Glossary

| Term | Definition |
|------|------------|
| **BFT** | Byzantine Fault Tolerant - consensus that works despite malicious nodes |
| **Tendermint** | PBFT-derived consensus protocol used by Cosmos and others |
| **POL** | Proof-of-Lock - evidence showing why validator is locked on a block |
| **Quorum** | 2/3+ of voting power required for any consensus decision |
| **Equivocation** | Signing two conflicting messages (double-signing) |
| **WAL** | Write-Ahead Log - persistent log for crash recovery |
| **Prevote** | First voting round in Tendermint (preliminary commitment) |
| **Precommit** | Second voting round in Tendermint (final commitment) |
| **Validator Set** | Collection of validators with voting power |
| **Named Account** | Human-readable account identifier (e.g., "alice") |
| **Authority** | Weighted multi-sig authorization rules for an account |
| **DAG** | Directed Acyclic Graph - used by Looseberry mempool |
| **Cramberry** | Binary serialization format for consensus messages |

## Appendix C: References

1. **Tendermint Specification**: https://arxiv.org/abs/1807.04938
2. **PBFT Paper**: Practical Byzantine Fault Tolerance (Castro & Liskov, 1999)
3. **Ed25519**: High-speed high-security signatures (Bernstein et al.)
4. **Go Concurrency Patterns**: https://go.dev/blog/pipelines
5. **Blockberries Ecosystem**: https://github.com/blockberries

---

**Document Maintenance**:
- Update this document when making architectural changes
- Keep examples synchronized with actual code
- Review quarterly for accuracy and completeness
- Version document with each major release

**Questions or Feedback**:
- Open GitHub issue for clarifications
- Contribute improvements via pull request
- Join community discussions on Discord

---

**End of Architecture Documentation**
