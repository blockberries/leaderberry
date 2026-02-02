# Leaderberry Documentation

Comprehensive documentation for the Leaderberry Tendermint-style BFT consensus engine.

---

## Documentation Structure

This documentation is organized into four main categories:

### 📘 [API Documentation](api/)

Complete API reference for all public packages:

- **[Index](api/index.md)** - Overview, quick start, and navigation
- **[types](api/types.md)** - Core data structures (Hash, Vote, Block, Validator)
- **[engine](api/engine.md)** - Consensus engine and state machine
- **[wal](api/wal.md)** - Write-ahead log for crash recovery
- **[privval](api/privval.md)** - Private validator with double-sign prevention
- **[evidence](api/evidence.md)** - Byzantine fault detection and evidence pool

### 📗 [Guides](guides/)

Step-by-step guides for common tasks:

- **[Getting Started](guides/getting-started.md)** - Installation and first consensus engine
- **[Integration](guides/integration.md)** - Integrating with Blockberry and application layer
- **[Testing](guides/testing.md)** - Writing tests and Byzantine scenarios
- **[Deployment](guides/deployment.md)** - Production deployment considerations
- **[Security Best Practices](guides/security-best-practices.md)** - Key management and security

### 📙 [Tutorials](tutorials/)

Hands-on tutorials for learning consensus:

- **[Quickstart](tutorials/quickstart.md)** - 5-minute tutorial with working example
- **[Creating Validators](tutorials/creating-validators.md)** - Validator set creation and management
- **[Consensus Flow](tutorials/consensus-flow.md)** - Walkthrough of Propose → Prevote → Precommit
- **[Crash Recovery](tutorials/crash-recovery.md)** - WAL setup and replay process

### 📕 [Reference](reference/)

In-depth technical documentation:

- **[Consensus Protocol](reference/consensus-protocol.md)** - Complete Tendermint protocol specification
- **[Design Patterns](reference/design-patterns.md)** - Architecture patterns and rationale
- **[Configuration](reference/configuration.md)** - Configuration options and tuning
- **[Performance](reference/performance.md)** - Performance characteristics and optimization
- **[Troubleshooting](reference/troubleshooting.md)** - Common issues and solutions

---

## Quick Links

### For Beginners
1. Start with [API Index](api/index.md) for overview
2. Follow [Getting Started Guide](guides/getting-started.md)
3. Try [Quickstart Tutorial](tutorials/quickstart.md)
4. Read [Consensus Flow Tutorial](tutorials/consensus-flow.md)

### For Integrators
1. Review [Integration Guide](guides/integration.md)
2. Study [API Reference](api/index.md)
3. Check [Configuration Reference](reference/configuration.md)
4. Follow [Deployment Guide](guides/deployment.md)

### For Contributors
1. Understand [Design Patterns](reference/design-patterns.md)
2. Review [Testing Guide](guides/testing.md)
3. Study [Consensus Protocol](reference/consensus-protocol.md)
4. Read codebase documentation (CLAUDE.md, CODE_REVIEW.md)

### For Operators
1. Read [Deployment Guide](guides/deployment.md)
2. Review [Security Best Practices](guides/security-best-practices.md)
3. Study [Performance Reference](reference/performance.md)
4. Keep [Troubleshooting](reference/troubleshooting.md) handy

---

## Documentation Coverage

### Current Status (As of 2026-02-02)

| Section | Files | Status | Coverage |
|---------|-------|--------|----------|
| API | 6 | ✅ Complete | 100% of public APIs |
| Guides | 5 | ⚠️ Partial | Core guides complete |
| Tutorials | 4 | ⚠️ Partial | Essential tutorials complete |
| Reference | 5 | ✅ Complete | Core reference docs done |

**Legend:**
- ✅ Complete - Comprehensive documentation available
- ⚠️ Partial - Core documentation complete, additional content planned
- 🚧 In Progress - Under active development

### Planned Additions

Coming soon:
- Advanced consensus patterns
- Multi-chain deployment guide
- Monitoring and metrics guide
- Upgrade and migration guide

---

## Key Concepts

### What is Leaderberry?

Leaderberry is a Tendermint-style Byzantine Fault Tolerant (BFT) consensus engine designed for the Blockberries ecosystem. It provides:

- **Instant Finality** - Blocks are final when committed (no reorgs)
- **Byzantine Fault Tolerance** - Tolerates up to 1/3 Byzantine validators
- **Safety First** - Never forks, even under network partition
- **Transaction Agnostic** - Treats transaction payloads as opaque bytes

### Core Components

```
┌─────────────────────────────────────────────────────────┐
│                    Application Layer                     │
│            (Your blockchain application)                 │
└────────────────────┬────────────────────────────────────┘
                     │ BlockExecutor interface
┌────────────────────▼────────────────────────────────────┐
│                  Consensus Engine                        │
│         (Tendermint state machine)                       │
├──────────────┬──────────────┬──────────────┬────────────┤
│ Vote Tracker │  Proposer    │  Timeout     │  WAL       │
│              │  Rotation    │  Manager     │  Recovery  │
└──────────────┴──────────────┴──────────────┴────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│                  Network Layer                           │
│           (Glueberry P2P network)                        │
└─────────────────────────────────────────────────────────┘
```

### Consensus Flow

```
Height 1, Round 0:
  ┌─────────────┐
  │  NewHeight  │ ← Start of new height
  └──────┬──────┘
         ▼
  ┌─────────────┐
  │  NewRound   │ ← Start of round 0
  └──────┬──────┘
         ▼
  ┌─────────────┐
  │   Propose   │ ← Proposer broadcasts block
  └──────┬──────┘
         ▼
  ┌─────────────┐
  │   Prevote   │ ← First voting round
  └──────┬──────┘
         ▼
  ┌─────────────┐
  │ PrevoteWait │ ← Collect 2/3+ prevotes
  └──────┬──────┘
         ▼
  ┌─────────────┐
  │  Precommit  │ ← Second voting round
  └──────┬──────┘
         ▼
  ┌─────────────┐
  │PrecommitWait│ ← Collect 2/3+ precommits
  └──────┬──────┘
         ▼
  ┌─────────────┐
  │   Commit    │ ← Block finalized!
  └──────┬──────┘
         ▼
  Height 2, Round 0...
```

---

## Code Examples

### Minimal Consensus Engine

```go
package main

import (
    "github.com/blockberries/leaderberry/engine"
    "github.com/blockberries/leaderberry/types"
    "github.com/blockberries/leaderberry/wal"
    "github.com/blockberries/leaderberry/privval"
)

func main() {
    // Create validator set
    vals := []*types.NamedValidator{
        {Name: types.NewAccountName("alice"), VotingPower: 100, PublicKey: alicePubKey},
    }
    valSet, _ := types.NewValidatorSet(vals)

    // Load private validator
    pv, _ := privval.LoadFilePV("key.json", "state.json")
    defer pv.Close()

    // Create WAL
    wal, _ := wal.NewFileWAL("./data/wal")
    defer wal.Stop()

    // Configure and create engine
    cfg := engine.DefaultConfig()
    cfg.ChainID = "my-chain"
    eng := engine.NewEngine(cfg, valSet, pv, wal, nil)

    // Set broadcast callbacks
    eng.SetProposalBroadcaster(func(p *gen.Proposal) { /* broadcast */ })
    eng.SetVoteBroadcaster(func(v *gen.Vote) { /* broadcast */ })

    // Start consensus
    eng.Start(1, nil)
    defer eng.Stop()

    // Handle incoming messages
    eng.AddProposal(proposal)
    eng.AddVote(vote)
}
```

See [Getting Started](guides/getting-started.md) for complete working example.

---

## Common Tasks

### Creating a Validator

```go
// Generate new keys
pv, err := privval.GenerateFilePV("key.json", "state.json")
if err != nil {
    log.Fatal(err)
}
defer pv.Close()

// Get public key
pubKey := pv.GetPubKey()
fmt.Printf("Public key: %x\n", pubKey.Data)
```

See [Creating Validators Tutorial](tutorials/creating-validators.md) for details.

### Handling Votes

```go
// Receive vote from network
vote := &gen.Vote{ /* ... */ }

// Add to consensus engine
if err := engine.AddVote(vote); err != nil {
    log.Printf("Failed to add vote: %v", err)
}

// Check consensus state
height, round, step, _ := engine.GetState()
fmt.Printf("State: H:%d R:%d S:%v\n", height, round, step)
```

### Implementing BlockExecutor

```go
type MyExecutor struct {
    state *ApplicationState
}

func (e *MyExecutor) CreateProposalBlock(
    height int64,
    lastCommit *gen.Commit,
    proposer types.AccountName,
) (*gen.Block, error) {
    // Build block from application state
    return e.state.BuildBlock(height, lastCommit, proposer)
}

func (e *MyExecutor) ValidateBlock(block *gen.Block) error {
    // Validate according to application rules
    return e.state.ValidateBlock(block)
}

func (e *MyExecutor) ApplyBlock(
    block *gen.Block,
    commit *gen.Commit,
) ([]ValidatorUpdate, error) {
    // Apply block to state, return validator updates
    return e.state.ApplyBlock(block, commit)
}
```

See [Integration Guide](guides/integration.md) for complete integration examples.

---

## FAQ

### General Questions

**Q: What is Byzantine Fault Tolerance?**
A: BFT means the system remains correct even if up to 1/3 of validators act maliciously or fail arbitrarily.

**Q: How is this different from Proof-of-Work?**
A: BFT provides instant finality (no confirmations needed) and deterministic safety (no forks), but requires known validator set.

**Q: Can I use this for a public blockchain?**
A: Yes, but you need a mechanism for managing validator set changes (typically staking).

### Technical Questions

**Q: How do I handle validator updates?**
A: Return validator updates from `ApplyBlock()`. Updates take effect at next height.

**Q: What happens if my node crashes?**
A: WAL replay restores consensus state. Node resumes from last recorded position.

**Q: How do I test Byzantine behavior?**
A: Use the evidence pool's `CheckVote()` to detect equivocation. See [Testing Guide](guides/testing.md).

### Performance Questions

**Q: How many transactions per second?**
A: TPS depends on application layer. Consensus itself processes ~10K votes/second.

**Q: What's the typical block time?**
A: 1-3 seconds under normal conditions. Increases with timeouts if network is slow.

**Q: How much memory does it use?**
A: Baseline ~50MB. Scales with validator count and message buffer sizes.

See [Performance Reference](reference/performance.md) for detailed analysis.

---

## Support and Community

### Getting Help

- **Documentation:** You're reading it!
- **GitHub Issues:** https://github.com/blockberries/leaderberry/issues
- **Discussions:** https://github.com/blockberries/leaderberry/discussions

### Reporting Issues

Found a bug? Please report it with:
1. Leaderberry version
2. Go version
3. Operating system
4. Minimal reproduction steps
5. Logs (if applicable)

### Contributing

Contributions welcome! Please:
1. Read [Design Patterns](reference/design-patterns.md)
2. Follow [Testing Guide](guides/testing.md)
3. Ensure all tests pass: `make test`
4. Run linter: `make lint`

---

## Additional Resources

### Related Projects

- **Blockberry** - Node framework and consensus interfaces
- **Looseberry** - DAG-based mempool for transaction ordering
- **Cramberry** - Binary serialization library
- **Glueberry** - Encrypted P2P networking

### External References

- **Tendermint Paper:** Buchman, E. (2016) - Original Tendermint specification
- **PBFT Paper:** Castro & Liskov (1999) - Foundation of BFT consensus
- **Tendermint Spec:** https://github.com/tendermint/spec

### Codebase Documentation

Located in repository root:
- **CLAUDE.md** - Development guidelines and conventions
- **CODE_REVIEW.md** - Summary of 29 code review iterations
- **ARCHITECTURE.md** - System architecture and component design
- **CHANGELOG.md** - Chronological history of changes

---

## Documentation Maintenance

This documentation is maintained alongside the codebase. When updating code:

1. **Update API docs** if public API changes
2. **Update guides** if workflows change
3. **Update tutorials** if examples break
4. **Update reference** if protocol or patterns change

**Last Updated:** 2026-02-02
**Leaderberry Version:** 1.0.0
**Maintainers:** Blockberries Team

---

## License

Documentation and code are released under the MIT License.

Copyright © 2026 Blockberries Project
