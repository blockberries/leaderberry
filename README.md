# Leaderberry

A Tendermint-style BFT consensus engine for the Blockberries ecosystem.

## Overview

Leaderberry implements a PBFT-derived consensus protocol with deterministic finality. It provides:

- **Tendermint BFT Consensus**: Classic 3-phase commit (Propose → Prevote → Precommit)
- **Transaction-Content Agnostic**: Consensus treats transaction payloads as opaque bytes
- **Named Validators**: Human-readable validator names with weighted voting power
- **Crash Recovery**: Write-ahead log (WAL) for state recovery after crashes
- **Double-Sign Prevention**: LastSignState persistence prevents equivocation
- **Byzantine Evidence**: Detection and reporting of equivocation

## Architecture

```
leaderberry/
├── schema/           # Cramberry schema definitions (.cram)
├── types/            # Generated + hand-written type extensions
│   └── generated/    # Cramberry-generated code (do not edit)
├── engine/           # Consensus state machine and vote tracking
├── wal/              # Write-ahead log for crash recovery
├── privval/          # Private validator with double-sign prevention
├── evidence/         # Byzantine evidence detection
└── tests/            # Integration tests
```

## Building

```bash
# Generate code from Cramberry schemas
make generate

# Build all packages
make build

# Run tests with race detection
make test

# Run linter
make lint
```

## Dependencies

| Package | Purpose |
|---------|---------|
| [Cramberry](../cramberry) | Binary serialization |
| [Blockberry](../blockberry) | Node framework (optional integration) |
| [Looseberry](../looseberry) | DAG mempool (optional integration) |
| [Glueberry](../glueberry) | P2P networking (optional integration) |

## Consensus Protocol

### States

```
NewHeight → NewRound → Propose → Prevote → PrevoteWait → Precommit → PrecommitWait → Commit
```

### Phases

1. **Propose**: Designated proposer broadcasts a block proposal
2. **Prevote**: Validators vote for the proposed block (or nil)
3. **Precommit**: After 2/3+ prevotes, validators precommit
4. **Commit**: After 2/3+ precommits, block is finalized

### Locking Rules

- Lock on seeing 2/3+ prevotes for a block
- Once locked, only prevote for locked block or nil
- Proof-of-lock (POL) allows unlocking for higher rounds

## Usage

```go
import (
    "github.com/blockberries/leaderberry/engine"
    "github.com/blockberries/leaderberry/privval"
    "github.com/blockberries/leaderberry/wal"
    "github.com/blockberries/leaderberry/types"
)

// Create validator set
vals := []*types.NamedValidator{
    {Name: types.NewAccountName("alice"), Index: 0, VotingPower: 100, PublicKey: alicePubKey},
    {Name: types.NewAccountName("bob"), Index: 1, VotingPower: 100, PublicKey: bobPubKey},
    {Name: types.NewAccountName("carol"), Index: 2, VotingPower: 100, PublicKey: carolPubKey},
}
valSet, _ := types.NewValidatorSet(vals)

// Create private validator
pv, _ := privval.NewFilePV("key.json", "state.json")

// Create WAL
w, _ := wal.NewFileWAL("./wal")

// Create engine config
cfg := &engine.Config{
    ChainID:  "my-chain",
    Timeouts: engine.DefaultTimeoutConfig(),
}

// Create and start engine
eng := engine.NewEngine(cfg, valSet, pv, w, nil)
eng.Start(1, nil)

// Handle network messages
eng.AddProposal(proposal)
eng.AddVote(vote)

// Get current state
height, round, step, _ := eng.GetState()
```

## Testing

```bash
# Run all tests
go test -race ./...

# Run specific package tests
go test -race ./engine/...
go test -race ./privval/...
go test -race ./wal/...
go test -race ./evidence/...

# Run integration tests
go test -race ./tests/integration/...
```

## Test Coverage

| Package | Tests |
|---------|-------|
| types | 25 |
| engine | 14 |
| wal | 9 |
| privval | 12 |
| evidence | 11 |
| integration | 12 |
| **Total** | **84** |

## Security Considerations

- **Double-Sign Prevention**: LastSignState is persisted before signing
- **Signature Verification**: Ed25519 signatures on all consensus messages
- **Equivocation Detection**: Evidence pool tracks conflicting votes
- **WAL Integrity**: Length-prefixed messages with sync on critical operations

## License

See LICENSE file for details.
