# Leaderberry

**Production-ready Tendermint-style BFT consensus engine for deterministic finality**

[![Go Version](https://img.shields.io/badge/go-1.25.6-blue.svg)](https://golang.org)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)](#)
[![Go Report Card](https://img.shields.io/badge/go%20report-A%2B-brightgreen.svg)](#)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](#)

---

## Overview

Leaderberry is a Byzantine Fault Tolerant (BFT) consensus engine implementing the Tendermint protocol for the Blockberries ecosystem. It provides instant finality, deterministic block ordering, and robust fault tolerance for distributed systems requiring strong consistency guarantees.

**Key Design Principle:** The consensus engine is transaction-content agnostic. It handles authorization verification and transaction ordering but treats transaction payloads as opaque bytes passed to the application layer. This separation enables Leaderberry to serve as a consensus foundation for any state machine application.

Leaderberry integrates with Looseberry's DAG-based mempool for high-throughput transaction batching, uses Cramberry for deterministic binary serialization, and leverages Glueberry for secure P2P networking. The result is a production-hardened consensus layer with instant finality and Byzantine fault tolerance.

---

## Key Features

- **BFT Consensus with Instant Finality** - Tendermint-derived protocol providing immediate block finalization with no reversion risk
- **Named Accounts with Weighted Multi-Sig** - Human-readable account names supporting hierarchical delegation and weighted authorization
- **Transaction-Content Agnostic** - Consensus layer treats transaction payloads as opaque bytes, enabling application flexibility
- **Crash Recovery with WAL** - Write-ahead log with height indexing and CRC32 corruption detection for fast, reliable recovery
- **Double-Sign Prevention** - Atomic persistence pattern and file locking ensure validators never equivocate
- **Byzantine Evidence Detection** - Automatic detection and reporting of equivocation with on-chain evidence
- **Production Hardened (v1.0.1)** - Battle-tested with 28+ documented security and correctness refinements
- **Thread-Safe Deep Copy Pattern** - Comprehensive memory safety preventing aliasing bugs in concurrent access
- **Deterministic Execution** - Careful handling of non-determinism sources (map iteration, overflow, tie-breaking)
- **High Performance** - 1-3 second block times with sub-30-second crash recovery

---

## Installation

```bash
go get github.com/blockberries/leaderberry@latest
```

**Requirements:**
- Go 1.25.6 or later
- cramberry code generator (for building from source)

---

## Quick Start

Here's a minimal example demonstrating core Leaderberry functionality:

```go
package main

import (
    "log"
    "time"

    "github.com/blockberries/leaderberry/engine"
    "github.com/blockberries/leaderberry/types"
    "github.com/blockberries/leaderberry/privval"
    "github.com/blockberries/leaderberry/wal"
)

func main() {
    // 1. Create validator set
    validators := []*types.NamedValidator{
        {
            Name:        types.NewAccountName("alice"),
            Index:       0,
            VotingPower: 100,
            PublicKey:   alicePubKey,
        },
        {
            Name:        types.NewAccountName("bob"),
            Index:       1,
            VotingPower: 100,
            PublicKey:   bobPubKey,
        },
    }

    valSet, err := types.NewValidatorSet(validators)
    if err != nil {
        log.Fatalf("Failed to create validator set: %v", err)
    }

    // 2. Load private validator (with double-sign prevention)
    pv, err := privval.LoadFilePV("key.json", "state.json")
    if err != nil {
        log.Fatalf("Failed to load private validator: %v", err)
    }
    defer pv.Close()

    // 3. Create write-ahead log (for crash recovery)
    wal, err := wal.NewFileWAL("./data/wal")
    if err != nil {
        log.Fatalf("Failed to create WAL: %v", err)
    }
    defer wal.Stop()

    // 4. Configure consensus engine
    cfg := &engine.Config{
        ChainID:  "my-chain",
        Timeouts: engine.DefaultTimeoutConfig(),
        WALPath:  "./data/wal",
    }

    // 5. Create consensus engine
    eng := engine.NewEngine(cfg, valSet, pv, wal, nil)

    // 6. Set up network broadcast callbacks
    eng.SetProposalBroadcaster(func(proposal *types.Proposal) {
        // Broadcast proposal to network peers
        log.Printf("Broadcasting proposal for height %d round %d",
            proposal.Height, proposal.Round)
    })

    eng.SetVoteBroadcaster(func(vote *types.Vote) {
        // Broadcast vote to network peers
        log.Printf("Broadcasting %s vote for height %d",
            vote.Type, vote.Height)
    })

    // 7. Start consensus at height 1
    if err := eng.Start(1, nil); err != nil {
        log.Fatalf("Failed to start consensus: %v", err)
    }

    // 8. Process incoming network messages
    go func() {
        // When receiving proposal from network
        eng.AddProposal(proposal)
    }()

    go func() {
        // When receiving vote from network
        eng.AddVote(vote)
    }()

    // 9. Query consensus state
    height, round, step, _ := eng.GetState()
    log.Printf("Consensus at H:%d R:%d S:%v", height, round, step)

    // Keep running...
    select {}
}
```

---

## Architecture Overview

Leaderberry is organized into five core packages with well-defined responsibilities:

```
leaderberry/
├── types/         - Core data structures (Hash, Vote, Block, Validator, Account)
├── engine/        - Consensus state machine and coordinator
├── wal/           - Write-ahead log for crash recovery
├── privval/       - Private validator with double-sign prevention
└── evidence/      - Byzantine fault detection and evidence management
```

### Consensus Flow

The Tendermint consensus protocol proceeds through these states:

```
NewHeight → NewRound → Propose → Prevote → PrevoteWait → Precommit → PrecommitWait → Commit
```

**State Transitions:**

1. **NewHeight**: Enter new height, reset round to 0
2. **NewRound**: Select proposer for current round
3. **Propose**: Proposer creates and broadcasts block proposal
4. **Prevote**: Validators vote for proposed block (or nil if invalid/timeout)
5. **PrevoteWait**: Wait for 2/3+ prevotes (or timeout)
6. **Precommit**: Validators commit to block if 2/3+ prevoted for it
7. **PrecommitWait**: Wait for 2/3+ precommits (or timeout)
8. **Commit**: Finalize block and advance to next height

**Locking Rules:**
- Lock on block when 2/3+ prevotes seen
- Once locked, only prevote for locked block or nil
- Valid round tracking prevents re-locking on older blocks

**Timeout Management:**
- Exponential backoff per round (prevents indefinite blocking)
- Different timeouts for propose, prevote, and precommit phases
- Automatic round advancement on timeout

For detailed architecture, see [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Package Reference

### types

Core data structures for the consensus protocol.

**Key Types:**
- `Hash`, `Signature`, `PublicKey` - Cryptographic primitives (Ed25519)
- `AccountName`, `Account`, `Authority` - Named accounts with weighted multi-sig
- `NamedValidator`, `ValidatorSet` - Validator management with weighted voting power
- `Vote`, `VoteType`, `Commit` - Prevotes and precommits with aggregated commit certificates
- `Block`, `BlockHeader`, `BlockData` - Finalized blocks containing batch certificates
- `Proposal` - Block proposals with optional proof-of-lock (POL)
- `BlockPart`, `PartSet` - Merkle-tree-based block splitting for efficient gossip

**Key Functions:**
```go
// Validator set management
func NewValidatorSet(validators []*NamedValidator) (*ValidatorSet, error)
func (vs *ValidatorSet) GetProposerForRound(round int32) *NamedValidator
func (vs *ValidatorSet) TwoThirdsMajority() int64

// Vote verification
func VerifyVoteSignature(chainID string, vote *Vote, pubKey PublicKey) error
func VerifyCommit(chainID string, valSet *ValidatorSet, blockHash Hash, height int64, commit *Commit) error

// Authorization verification (hierarchical multi-sig)
func VerifyAuthorization(auth *Authorization, authority *Authority, signBytes []byte, getAccount AccountGetter, visited map[string]bool) (uint32, error)

// Block operations
func BlockHash(b *Block) Hash
func NewPartSetFromData(data []byte) (*PartSet, error)
```

### engine

Consensus state machine implementing the BFT protocol.

**Key Types:**
- `Engine` - Main consensus coordinator
- `Config` - Engine configuration (ChainID, timeouts, WAL path)
- `TimeoutTicker` - Manages round timeouts with exponential backoff
- `RoundStep` - Consensus state enumeration

**Key Functions:**
```go
// Engine lifecycle
func NewEngine(config *Config, valSet *ValidatorSet, pv privval.PrivValidator, w wal.WAL, executor BlockExecutor) *Engine
func (e *Engine) Start(height int64, lastCommit *Commit) error
func (e *Engine) Stop() error

// Message handling
func (e *Engine) AddProposal(proposal *Proposal) error
func (e *Engine) AddVote(vote *Vote) error

// State queries
func (e *Engine) GetState() (height int64, round int32, step RoundStep, err error)
func (e *Engine) GetValidatorSet() *ValidatorSet
func (e *Engine) IsValidator() bool

// Network integration
func (e *Engine) HandleConsensusMessage(peerID string, data []byte) error
func (e *Engine) SetProposalBroadcaster(fn func(*Proposal))
func (e *Engine) SetVoteBroadcaster(fn func(*Vote))
```

### wal

Write-ahead log for crash recovery.

**Key Types:**
- `WAL` - Write-ahead log interface
- `FileWAL` - File-based WAL implementation with segmentation
- `Message` - WAL message with type, height, round, and data
- `Reader` - Sequential message reading

**Key Functions:**
```go
// WAL lifecycle
func NewFileWAL(dirPath string) (WAL, error)
func (w *WAL) Start() error
func (w *WAL) Stop() error

// Writing messages
func (w *WAL) Write(msg *Message) error
func (w *WAL) WriteSync(msg *Message) error
func (w *WAL) FlushAndSync() error

// Recovery
func (w *WAL) SearchForEndHeight(height int64) (Reader, bool, error)

// Message construction
func NewProposalMessage(height int64, round int32, proposal *Proposal) (*Message, error)
func NewVoteMessage(height int64, round int32, vote *Vote) (*Message, error)
func NewCommitMessage(height int64, commit *Commit) (*Message, error)
```

### privval

Private validator with double-sign prevention.

**Key Types:**
- `PrivValidator` - Private key operations interface
- `FilePV` - File-based private validator with atomic persistence
- `LastSignState` - Tracks last signed vote for double-sign detection

**Key Functions:**
```go
// Validator lifecycle
func LoadFilePV(keyFilePath, stateFilePath string) (*FilePV, error)
func NewFilePV(keyFilePath, stateFilePath string) (*FilePV, error)
func GenerateFilePV(keyFilePath, stateFilePath string) (*FilePV, error)
func (pv *FilePV) Close() error

// Signing with double-sign prevention
func (pv *FilePV) SignVote(chainID string, vote *Vote) error
func (pv *FilePV) SignProposal(chainID string, proposal *Proposal) error

// Queries
func (pv *FilePV) GetPubKey() PublicKey
func (pv *FilePV) GetAddress() []byte

// Double-sign checking
func (lss *LastSignState) CheckHRS(height int64, round int32, step int8) error
```

### evidence

Byzantine fault detection and evidence management.

**Key Types:**
- `Pool` - Evidence pool for collecting and managing Byzantine evidence
- `DuplicateVoteEvidence` - Proof of validator double-signing
- `Config` - Evidence pool configuration (expiration, size limits)

**Key Functions:**
```go
// Pool lifecycle
func NewPool(config Config) *Pool
func (p *Pool) Update(height int64, blockTime time.Time)

// Equivocation detection
func (p *Pool) CheckVote(vote *Vote, valSet *ValidatorSet, chainID string) (*DuplicateVoteEvidence, error)

// Evidence management
func (p *Pool) AddEvidence(ev *Evidence) error
func (p *Pool) ListEvidence() []*Evidence
func (p *Pool) MarkCommitted(ev *Evidence)

// Evidence verification
func VerifyDuplicateVoteEvidence(dve *DuplicateVoteEvidence, chainID string, valSet *ValidatorSet) error
```

---

## Usage Examples

### Creating a Validator Set

```go
// Create validators with voting power
validators := []*types.NamedValidator{
    {
        Name:             types.NewAccountName("alice"),
        Index:            0,
        VotingPower:      100,
        PublicKey:        alicePubKey,
        ProposerPriority: 0,
    },
    {
        Name:             types.NewAccountName("bob"),
        Index:            1,
        VotingPower:      50,
        PublicKey:        bobPubKey,
        ProposerPriority: 0,
    },
    {
        Name:             types.NewAccountName("charlie"),
        Index:            2,
        VotingPower:      50,
        PublicKey:        charliePubKey,
        ProposerPriority: 0,
    },
}

// Create validator set with validation
valSet, err := types.NewValidatorSet(validators)
if err != nil {
    log.Fatalf("Invalid validator set: %v", err)
}

// Query validator set properties
quorum := valSet.TwoThirdsMajority()  // 134 (2/3 of 200 + 1)
fmt.Printf("2/3+ majority requires: %d voting power\n", quorum)

// Get proposer for round 0
proposer := valSet.GetProposerForRound(0)
fmt.Printf("Round 0 proposer: %s\n", types.AccountNameString(proposer.Name))

// Get proposer for round 1 (different due to round-robin)
proposer = valSet.GetProposerForRound(1)
fmt.Printf("Round 1 proposer: %s\n", types.AccountNameString(proposer.Name))
```

### Starting the Consensus Engine

```go
// Configure engine
cfg := &engine.Config{
    ChainID:                   "production-chain",
    Timeouts:                  engine.DefaultTimeoutConfig(),
    WALPath:                   "/var/consensus/wal",
    WALSync:                   true,  // fsync every write (safety)
    MaxBlockBytes:             10 * 1024 * 1024,  // 10MB blocks
    CreateEmptyBlocks:         true,
    CreateEmptyBlocksInterval: 3 * time.Second,
    SkipTimeoutCommit:         false,
}

// Validate configuration
if err := cfg.ValidateBasic(); err != nil {
    log.Fatalf("Invalid config: %v", err)
}

// Create engine with dependencies
eng := engine.NewEngine(cfg, valSet, privVal, wal, executor)

// Set up callbacks for network integration
eng.SetProposalBroadcaster(func(proposal *types.Proposal) {
    // Serialize and broadcast to network
    data, _ := proposal.MarshalCramberry()
    network.Broadcast(data)
})

eng.SetVoteBroadcaster(func(vote *types.Vote) {
    // Serialize and broadcast to network
    data, _ := vote.MarshalCramberry()
    network.Broadcast(data)
})

// Start consensus from height 1 (genesis)
if err := eng.Start(1, nil); err != nil {
    log.Fatalf("Failed to start: %v", err)
}

// Or resume from checkpoint
lastCommit := loadLastCommit()
if err := eng.Start(lastHeight+1, lastCommit); err != nil {
    log.Fatalf("Failed to resume: %v", err)
}
```

### Handling Proposals and Votes

```go
// Receive proposal from network
func handleNetworkProposal(data []byte) {
    proposal := &types.Proposal{}
    if err := proposal.UnmarshalCramberry(data); err != nil {
        log.Printf("Invalid proposal: %v", err)
        return
    }

    // Add to consensus engine (non-blocking)
    if err := eng.AddProposal(proposal); err != nil {
        log.Printf("Failed to add proposal: %v", err)
    }
}

// Receive vote from network
func handleNetworkVote(data []byte) {
    vote := &types.Vote{}
    if err := vote.UnmarshalCramberry(data); err != nil {
        log.Printf("Invalid vote: %v", err)
        return
    }

    // Check for equivocation
    evidence, err := evidencePool.CheckVote(vote, valSet, chainID)
    if err != nil {
        log.Printf("Failed to check vote: %v", err)
    }
    if evidence != nil {
        log.Printf("Detected equivocation from %s", vote.Validator)
        evidencePool.AddEvidence(evidence)
    }

    // Add to consensus engine (non-blocking)
    if err := eng.AddVote(vote); err != nil {
        log.Printf("Failed to add vote: %v", err)
    }
}

// Query consensus state
func monitorConsensus() {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        height, round, step, _ := eng.GetState()
        proposer := eng.GetProposer()

        fmt.Printf("H:%d R:%d S:%v Proposer:%s\n",
            height, round, step,
            types.AccountNameString(proposer.Name))
    }
}
```

---

## API Documentation

**Go Documentation:** [https://pkg.go.dev/github.com/blockberries/leaderberry](https://pkg.go.dev/github.com/blockberries/leaderberry)

**Detailed API Reference:** [api-documentation.md](api-documentation.md)

**Package Organization:**
- `types` - Core consensus types and validation functions
- `types/generated` - Cramberry-generated serialization code (do not edit)
- `engine` - Consensus state machine and coordinator
- `wal` - Write-ahead log implementation
- `privval` - Private validator with double-sign prevention
- `evidence` - Byzantine evidence pool

All public APIs follow these principles:
- **Thread Safety**: All exported functions are goroutine-safe
- **Immutability**: Functions return deep copies to prevent corruption
- **Error Handling**: Comprehensive error conditions documented
- **Resource Management**: Clear cleanup requirements (Close(), Stop())

---

## Development

### Prerequisites

- **Go 1.25.6 or later** - [Installation guide](https://golang.org/doc/install)
- **cramberry** - Code generator for schema definitions

### Building from Source

```bash
# Clone repository
git clone https://github.com/blockberries/leaderberry.git
cd leaderberry

# Generate code from schemas
make generate

# Build all packages
make build

# Run tests with race detection
make test

# Run linter
make lint

# Clean generated files
make clean
```

### Manual Build Commands

```bash
# Generate Cramberry code
cramberry generate -lang go -out ./types/generated ./schema/*.cram

# Build packages
go build ./...

# Run tests
go test -race -v ./...

# Run linter
golangci-lint run
```

### Running Tests

```bash
# Unit tests with race detection
go test -race -v ./...

# Specific package
go test -race -v ./engine

# With coverage
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Integration tests
go test -race -v ./test/integration
```

### Code Generation

Leaderberry uses Cramberry for deterministic binary serialization:

```bash
# Schema files (input)
schema/
├── types.cram       # Hash, Signature, PublicKey, AccountName
├── account.cram     # Account, Authority, KeyWeight
├── validator.cram   # NamedValidator, ValidatorSetData
├── vote.cram        # Vote, VoteType, Commit, CommitSig
├── block.cram       # Block, BlockHeader, BlockData
├── proposal.cram    # Proposal with POLVotes
├── evidence.cram    # DuplicateVoteEvidence
└── wal.cram         # WAL message types, LastSignState

# Generated code (output - do not edit)
types/generated/
├── types.go
├── account.go
├── validator.go
├── vote.go
├── block.go
├── proposal.go
├── evidence.go
└── wal.go

# Hand-written extensions
types/
├── hash.go          # Helper functions for Hash
├── vote.go          # Vote verification functions
├── validator.go     # ValidatorSet methods
└── ...
```

**Important:** Never edit generated code directly. Always modify schema files and regenerate.

---

## Testing

Leaderberry has comprehensive test coverage:

### Unit Tests

- **Package-level tests**: `*_test.go` files in each package
- **Table-driven tests**: Systematic coverage of edge cases
- **Race detection**: All tests run with `-race` flag
- **Mocking**: Interface-based mocking for dependencies

### Integration Tests

Located in `test/integration/`:
- Full consensus flow tests
- Multi-validator scenarios
- Byzantine behavior tests (equivocation detection)
- Crash recovery tests
- Network partition simulations

### Byzantine Scenario Tests

```go
// Example: Testing equivocation detection
func TestByzantineEquivocation(t *testing.T) {
    // Create two conflicting votes
    vote1 := &types.Vote{
        Height:         1,
        Round:          0,
        Type:           types.VoteTypePrevote,
        BlockHash:      &blockHash1,
        ValidatorIndex: 0,
    }

    vote2 := &types.Vote{
        Height:         1,
        Round:          0,
        Type:           types.VoteTypePrevote,
        BlockHash:      &blockHash2,  // Different!
        ValidatorIndex: 0,
    }

    // Sign both votes
    pv.SignVote(chainID, vote1)

    // Second signature should fail (double-sign detection)
    err := pv.SignVote(chainID, vote2)
    if !errors.Is(err, privval.ErrDoubleSign) {
        t.Fatal("Expected double-sign error")
    }

    // Evidence pool should detect equivocation
    pool := evidence.NewPool(evidence.DefaultConfig())
    pool.CheckVote(vote1, valSet, chainID)

    ev, _ := pool.CheckVote(vote2, valSet, chainID)
    if ev == nil {
        t.Fatal("Expected equivocation evidence")
    }
}
```

---

## Integration with Blockberries

Leaderberry is part of the Blockberries ecosystem:

### Related Projects

| Project | Purpose | Integration Point |
|---------|---------|-------------------|
| **[Blockberry](https://github.com/blockberries/blockberry)** | Node framework | Consensus engine interface implementation |
| **[Looseberry](https://github.com/blockberries/looseberry)** | DAG mempool | Block building from certified batches |
| **[Cramberry](https://github.com/blockberries/cramberry)** | Binary serialization | Deterministic encoding for all consensus messages |
| **[Glueberry](https://github.com/blockberries/glueberry)** | P2P networking | Encrypted message transport |

### Integration Overview

```
┌──────────────┐
│  Blockberry  │  (Node orchestration)
└──────┬───────┘
       │
       ├─── Leaderberry  (BFT consensus)
       │         ├─── Types (messages, blocks, votes)
       │         ├─── Engine (state machine)
       │         ├─── WAL (crash recovery)
       │         └─── PrivVal (key management)
       │
       ├─── Looseberry   (DAG mempool)
       │         └─── Certified batches → Block.Data
       │
       ├─── Cramberry    (Serialization)
       │         └─── Deterministic encoding
       │
       └─── Glueberry    (P2P network)
                 └─── Encrypted transport
```

**Data Flow:**
1. Transactions enter Looseberry DAG mempool
2. Looseberry builds certified batches with DAG ordering
3. Leaderberry consensus builds blocks containing batch certificates
4. Leaderberry finalizes blocks through BFT voting
5. Blockberry applies committed blocks to application state

---

## Performance Targets

| Metric | Target | Description |
|--------|--------|-------------|
| **Block time** | 1-3 seconds | Time between consecutive blocks under normal operation |
| **Finality** | Instant | Blocks are final once committed (no reversion) |
| **Recovery time** | < 30 seconds | Time to recover from crash using WAL replay |
| **Max validators** | 100 | Maximum recommended validator set size |
| **Throughput** | 5,000+ tx/sec | Transaction throughput (depends on mempool) |
| **Latency** | < 3 seconds | Transaction confirmation latency (99th percentile) |

**Scalability Notes:**
- Validator set size impacts voting overhead (O(n) communication)
- Looseberry mempool provides high throughput via parallel DAG
- Block size configurable (default 10MB, supports up to 100MB)
- WAL replay is O(messages) with height indexing for fast seeking

---

## Security

Leaderberry implements multiple layers of security:

### Double-Sign Prevention

**Atomic Persistence Pattern:**
1. Check last signed state (height/round/step)
2. Sign vote/proposal with private key
3. **Persist state to disk (fsync)**
4. Only then return signature to caller

**Invariant:** Signature is returned ⟹ State is persisted

If the process crashes after signing but before persisting, the node restarts with old state and refuses to re-sign (detecting potential double-sign).

### Byzantine Fault Tolerance

- **Tolerates up to 1/3 Byzantine validators**
- Automatic equivocation detection (duplicate voting)
- Evidence collection and on-chain reporting
- Cryptographic proof of misbehavior (two signed votes)

### Memory Safety

**Deep Copy Pattern:**
- All getters return deep copies of internal state
- All setters accept deep copies of input data
- Prevents caller corruption of consensus state
- Thread-safe sharing without synchronization

### File System Security

- **Private keys**: 0600 permissions enforced
- **State files**: Atomic write-then-rename pattern
- **WAL files**: CRC32 checksums detect corruption
- **File locking**: Prevents multi-process double-signing

### Cryptographic Guarantees

- **Ed25519 signatures** for all consensus messages
- **SHA-256 hashing** for blocks, votes, and merkle trees
- **Signature verification** before accepting votes/proposals
- **Merkle proofs** for block part verification

---

## Contributing

Contributions are welcome! Please follow these guidelines:

1. **Read CLAUDE.md** - Development guidelines and coding standards
2. **Follow Go conventions** - gofmt, golangci-lint
3. **Write tests** - Comprehensive unit tests with race detection
4. **Test Byzantine scenarios** - Not just happy paths
5. **Document design decisions** - Inline comments explaining "why"
6. **Run linter** - Fix all warnings before submitting

**Before submitting:**
```bash
make generate  # Regenerate code from schemas
make test      # Run tests with race detection
make lint      # Run golangci-lint
```

---

## Documentation

### Architecture and Design

- **[CLAUDE.md](CLAUDE.md)** - Development guidelines and implementation workflow
- **[CODE_REVIEW.md](CODE_REVIEW.md)** - Design patterns and code review findings
- **[code-analysis.md](code-analysis.md)** - Deep code analysis and algorithm documentation
- **[go-architecture-analysis.md](go-architecture-analysis.md)** - Go-specific patterns and idioms

### API and Usage

- **[api-documentation.md](api-documentation.md)** - Comprehensive API reference
- **[README.md](README.md)** - This document

### History

- **[CHANGELOG.md](CHANGELOG.md)** - Version history and release notes

---

## License

Apache License 2.0

Copyright (c) 2024 Blockberries Project

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

---

## Links

- **Godoc**: [https://pkg.go.dev/github.com/blockberries/leaderberry](https://pkg.go.dev/github.com/blockberries/leaderberry)
- **Issues**: [https://github.com/blockberries/leaderberry/issues](https://github.com/blockberries/leaderberry/issues)
- **Blockberries Ecosystem**: [https://github.com/blockberries](https://github.com/blockberries)
- **Tendermint Specification**: [https://github.com/tendermint/spec](https://github.com/tendermint/spec)

---

## Acknowledgments

Leaderberry is inspired by [Tendermint Core](https://github.com/tendermint/tendermint) and implements the Tendermint consensus protocol with modifications for named accounts, hierarchical authorization, and integration with the Blockberries ecosystem.

**Key References:**
- [Tendermint: Consensus without Mining](https://tendermint.com/static/docs/tendermint.pdf)
- [The latest gossip on BFT consensus](https://arxiv.org/abs/1807.04938)
- [PBFT: Practical Byzantine Fault Tolerance](http://pmg.csail.mit.edu/papers/osdi99.pdf)
