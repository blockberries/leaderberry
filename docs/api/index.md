# Leaderberry API Documentation

**Version:** 1.0.0
**License:** MIT
**Repository:** https://github.com/blockberries/leaderberry

---

## Overview

Leaderberry is a Tendermint-style BFT consensus engine for the Blockberries ecosystem. It provides deterministic finality through a PBFT-derived consensus protocol with full Byzantine fault tolerance.

This documentation covers all stable public APIs across five main packages:

- **[types](types.md)** - Core consensus data structures
- **[engine](engine.md)** - Consensus state machine
- **[wal](wal.md)** - Write-ahead log for crash recovery
- **[privval](privval.md)** - Private validator with double-sign prevention
- **[evidence](evidence.md)** - Byzantine fault detection

---

## Quick Navigation

### Getting Started
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Import Paths](#import-paths)

### Core Concepts
- [Consensus States](#consensus-states)
- [Vote Types](#vote-types)
- [Block Structure](#block-structure)
- [Validator Sets](#validator-sets)

### Integration
- [Implementing BlockExecutor](#blockexecutor-interface)
- [Network Integration](#network-integration)
- [Application Layer](#application-layer)

---

## Installation

```bash
go get github.com/blockberries/leaderberry@latest
```

**Requirements:**
- Go 1.25.6 or later
- Unix-like OS (Linux, macOS, BSD)
- Ed25519 cryptographic support

---

## Import Paths

```go
import (
    "github.com/blockberries/leaderberry/engine"
    "github.com/blockberries/leaderberry/types"
    "github.com/blockberries/leaderberry/wal"
    "github.com/blockberries/leaderberry/privval"
    "github.com/blockberries/leaderberry/evidence"

    gen "github.com/blockberries/leaderberry/types/generated"
)
```

---

## Quick Start

### Minimal Consensus Engine

```go
package main

import (
    "log"

    "github.com/blockberries/leaderberry/engine"
    "github.com/blockberries/leaderberry/types"
    "github.com/blockberries/leaderberry/wal"
    "github.com/blockberries/leaderberry/privval"
)

func main() {
    // Create validator set
    vals := []*types.NamedValidator{
        {
            Name:        types.NewAccountName("alice"),
            Index:       0,
            VotingPower: 100,
            PublicKey:   alicePubKey,
        },
    }
    valSet, _ := types.NewValidatorSet(vals)

    // Load private validator
    pv, _ := privval.LoadFilePV("key.json", "state.json")
    defer pv.Close()

    // Create WAL
    wal, _ := wal.NewFileWAL("./data/wal")
    defer wal.Stop()

    // Configure engine
    cfg := engine.DefaultConfig()
    cfg.ChainID = "my-chain"
    cfg.WALPath = "./data/wal"

    // Create engine
    eng := engine.NewEngine(cfg, valSet, pv, wal, nil)

    // Set broadcast callbacks
    eng.SetProposalBroadcaster(func(p *gen.Proposal) {
        // Broadcast to network
    })
    eng.SetVoteBroadcaster(func(v *gen.Vote) {
        // Broadcast to network
    })

    // Start consensus at height 1
    if err := eng.Start(1, nil); err != nil {
        log.Fatal(err)
    }

    // Wait for signal...

    eng.Stop()
}
```

---

## Consensus States

Leaderberry implements the Tendermint consensus state machine:

```
NewHeight → NewRound → Propose → Prevote → PrevoteWait → Precommit → PrecommitWait → Commit
```

### State Descriptions

| State | Description | Duration |
|-------|-------------|----------|
| **NewHeight** | Entered after committing block at previous height | Immediate transition |
| **NewRound** | Start of new round, reset state for this round | Immediate transition |
| **Propose** | Wait for proposal from designated proposer | Timeout: 3s + 500ms×round |
| **Prevote** | Validators broadcast first vote (prevote) | Immediate transition |
| **PrevoteWait** | Wait to collect 2/3+ prevotes | Timeout: 1s + 500ms×round |
| **Precommit** | Validators broadcast second vote (precommit) | Immediate transition |
| **PrecommitWait** | Wait to collect 2/3+ precommits | Timeout: 1s + 500ms×round |
| **Commit** | Block finalized, applying to state | Timeout: 1s |

---

## Vote Types

### VoteTypePrevote

First round of voting. Validators prevote for:
- The proposed block (if valid)
- `nil` (if no valid proposal or timeout)

### VoteTypePrecommit

Second round of voting. Validators precommit for:
- The block that received 2/3+ prevotes
- `nil` (if no block received 2/3+ prevotes)

### Commit

Not a vote type, but a collection of 2/3+ precommits for the same block, forming a commit certificate that finalizes the block.

---

## Block Structure

```go
type Block struct {
    Header     BlockHeader       // Metadata and hashes
    Data       BlockData         // Batch certificate references
    Evidence   [][]byte          // Byzantine evidence
    LastCommit *Commit           // Commit from previous height
}

type BlockHeader struct {
    ChainId        string
    Height         int64
    Timestamp      int64
    LastBlockHash  *Hash
    LastCommitHash *Hash
    ValidatorsHash *Hash
    AppHash        *Hash          // Application state hash
    Proposer       AccountName
}
```

**Key Design:** Blocks contain batch certificate references, NOT raw transactions. This integrates with Looseberry's DAG-based mempool.

---

## Validator Sets

```go
type ValidatorSet struct {
    Validators []*NamedValidator
    Proposer   *NamedValidator
    TotalPower int64
}

type NamedValidator struct {
    Name             AccountName
    Index            uint16
    PublicKey        PublicKey
    VotingPower      int64
    ProposerPriority int64
}
```

### Key Features

- **Named Validators:** Human-readable account names (e.g., "alice", "bob")
- **Weighted Voting:** Validators have voting power (typically 1-1000)
- **Proposer Rotation:** Weighted round-robin based on voting power
- **Deterministic:** Same inputs produce same proposer selection

### Creating a Validator Set

```go
vals := []*types.NamedValidator{
    {
        Name:        types.NewAccountName("alice"),
        Index:       0,
        VotingPower: 100,
        PublicKey:   alicePubKey,
    },
    {
        Name:        types.NewAccountName("bob"),
        Index:       1,
        VotingPower: 50,
        PublicKey:   bobPubKey,
    },
}

valSet, err := types.NewValidatorSet(vals)
if err != nil {
    log.Fatal(err)
}

// Get 2/3+ majority threshold
required := valSet.TwoThirdsMajority()  // 101 (2/3 of 150 + 1)

// Get proposer for specific round
proposer := valSet.GetProposerForRound(0)
```

---

## BlockExecutor Interface

The application layer implements this interface to integrate with consensus:

```go
type BlockExecutor interface {
    // CreateProposalBlock creates a new block to propose
    CreateProposalBlock(
        height int64,
        lastCommit *gen.Commit,
        proposer types.AccountName,
    ) (*gen.Block, error)

    // ValidateBlock validates a proposed block
    ValidateBlock(block *gen.Block) error

    // ApplyBlock applies a committed block to application state
    // Returns validator updates for next height
    ApplyBlock(
        block *gen.Block,
        commit *gen.Commit,
    ) ([]ValidatorUpdate, error)
}

type ValidatorUpdate struct {
    Name        types.AccountName
    PublicKey   types.PublicKey
    VotingPower int64  // 0 to remove validator
}
```

### Implementation Requirements

1. **CreateProposalBlock:** Must return a valid block within timeout (3s)
2. **ValidateBlock:** Should validate transactions, state transitions, and application rules
3. **ApplyBlock:** Must be deterministic - same block always produces same state
4. **Transaction Handling:** Consensus treats payloads as opaque bytes

### Example Implementation

```go
type MyExecutor struct {
    state *ApplicationState
    pool  *MempoolInterface
}

func (e *MyExecutor) CreateProposalBlock(
    height int64,
    lastCommit *gen.Commit,
    proposer types.AccountName,
) (*gen.Block, error) {
    // Get certified batches from Looseberry DAG mempool
    batches := e.pool.ReapCertifiedBatches()

    // Create block header
    header := types.NewBlockHeader(
        e.chainID,
        height,
        time.Now().UnixNano(),
        &lastBlockHash,
        types.CommitHash(lastCommit),
        e.state.ValidatorsHash(),
        e.state.AppHash(),
        proposer,
    )

    // Create block data with batch references
    data := &gen.BlockData{
        BatchDigests: batches,
    }

    return types.NewBlock(header, data, lastCommit), nil
}

func (e *MyExecutor) ValidateBlock(block *gen.Block) error {
    // Verify batch certificates exist in DAG
    for _, digest := range block.Data.BatchDigests {
        if !e.pool.HasBatch(digest) {
            return fmt.Errorf("unknown batch: %x", digest)
        }
    }

    // Application-specific validation
    return e.state.ValidateTransitions(block)
}

func (e *MyExecutor) ApplyBlock(
    block *gen.Block,
    commit *gen.Commit,
) ([]ValidatorUpdate, error) {
    // Apply block to state
    if err := e.state.ApplyBlock(block); err != nil {
        return nil, err
    }

    // Notify mempool of committed batches
    for _, digest := range block.Data.BatchDigests {
        e.pool.NotifyCommitted(digest, block.Header.Height)
    }

    // Return any validator updates
    return e.state.GetValidatorUpdates(), nil
}
```

---

## Network Integration

### Broadcasting

```go
// Set callbacks for broadcasting messages
eng.SetProposalBroadcaster(func(p *gen.Proposal) {
    data, _ := p.MarshalCramberry()
    network.Broadcast(ProposalMessage, data)
})

eng.SetVoteBroadcaster(func(v *gen.Vote) {
    data, _ := v.MarshalCramberry()
    network.Broadcast(VoteMessage, data)
})
```

### Receiving

```go
// Handle incoming consensus messages
func handleConsensusMessage(peerID string, msgType byte, data []byte) {
    switch msgType {
    case ProposalMessage:
        var proposal gen.Proposal
        proposal.UnmarshalCramberry(data)
        eng.AddProposal(&proposal)

    case VoteMessage:
        var vote gen.Vote
        vote.UnmarshalCramberry(data)
        eng.AddVote(&vote)
    }
}
```

---

## Application Layer

### Transaction Content Agnosticism

**Critical Design Principle:** Consensus treats transaction payloads as opaque bytes.

```go
type Transaction struct {
    Payload []byte  // Opaque to consensus
    // Authorization metadata interpreted by consensus
    Auth    *Authorization
}
```

**What Consensus Does:**
- Verify authorization signatures
- Order transactions deterministically
- Include transactions in blocks

**What Consensus Does NOT Do:**
- Parse transaction types
- Validate business logic
- Calculate fees
- Execute smart contracts

**Application Responsibility:**
- Define transaction formats
- Implement transaction execution
- Manage application state
- Handle fee logic

---

## Common Patterns

### Deep Copy Pattern

All public APIs return deep copies to prevent memory corruption:

```go
// WRONG - Returns internal pointer
func (e *Engine) GetValidatorSet() *types.ValidatorSet {
    return e.validatorSet  // Caller can corrupt!
}

// CORRECT - Returns deep copy
func (e *Engine) GetValidatorSet() *types.ValidatorSet {
    return e.validatorSet.Copy()
}
```

### Error Handling

Always wrap errors with context:

```go
if err := operation(); err != nil {
    return fmt.Errorf("failed to perform operation: %w", err)
}
```

Check for specific errors:

```go
if errors.Is(err, privval.ErrDoubleSign) {
    // Handle double-sign specifically
}
```

### Thread Safety

All public methods are thread-safe. No external locking required:

```go
// Safe to call from multiple goroutines
go func() { eng.AddVote(vote1) }()
go func() { eng.AddVote(vote2) }()
```

---

## Performance Considerations

### Channel Buffer Sizes

Consensus uses buffered channels for message processing:

```go
const (
    proposalChannelSize = 100      // Proposals are low volume
    voteChannelSize     = 10000    // Votes are high volume
)
```

**Impact:** Under extreme load, messages may be dropped. Monitor metrics for dropped message counts.

### Memory Usage

Deep copying has a performance cost. For large validator sets (> 100 validators):

- Block gossiping: ~1MB per block
- Vote processing: ~10K votes/sec per core
- WAL writes: ~100MB/hour for typical traffic

### Profiling

Enable Go profiling to monitor consensus performance:

```go
import _ "net/http/pprof"

go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

---

## Package Summaries

| Package | Purpose | Key Types |
|---------|---------|-----------|
| **[types](types.md)** | Core data structures | Hash, Vote, Block, Validator, Account |
| **[engine](engine.md)** | Consensus coordinator | Engine, Config, TimeoutTicker |
| **[wal](wal.md)** | Crash recovery | WAL, FileWAL, Message, Reader |
| **[privval](privval.md)** | Key management | PrivValidator, FilePV, LastSignState |
| **[evidence](evidence.md)** | Byzantine detection | Pool, DuplicateVoteEvidence |

---

## Next Steps

- **Deep Dive:** Read individual package documentation
- **Tutorials:** Follow [step-by-step tutorials](../tutorials/quickstart.md)
- **Integration:** Check [integration guide](../guides/integration.md)
- **Security:** Review [security best practices](../guides/security-best-practices.md)

---

## Support

- **Issues:** https://github.com/blockberries/leaderberry/issues
- **Discussions:** https://github.com/blockberries/leaderberry/discussions
- **Documentation:** https://docs.blockberries.io/leaderberry
