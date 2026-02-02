# Getting Started with Leaderberry

This guide walks you through setting up and running your first Leaderberry consensus engine.

---

## Prerequisites

### System Requirements

- **Operating System:** Linux, macOS, or BSD
- **Go Version:** 1.25.6 or later
- **Disk Space:** 100MB for consensus data, 10GB+ recommended for production
- **Memory:** 512MB minimum, 2GB+ recommended for production
- **CPU:** 2+ cores recommended

### Dependencies

```bash
# Install Go (if not already installed)
wget https://go.dev/dl/go1.25.6.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.25.6.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# Verify installation
go version  # Should show go1.25.6 or later
```

---

## Installation

### Option 1: Go Module (Recommended)

```bash
# Initialize your project
mkdir my-consensus-node
cd my-consensus-node
go mod init example.com/my-node

# Install leaderberry
go get github.com/blockberries/leaderberry@latest
```

### Option 2: From Source

```bash
# Clone repository
git clone https://github.com/blockberries/leaderberry.git
cd leaderberry

# Build all packages
make build

# Run tests
make test
```

---

## First Consensus Engine

### Step 1: Generate Validator Keys

Create a private validator with cryptographic keys:

```go
package main

import (
    "log"
    "github.com/blockberries/leaderberry/privval"
)

func main() {
    // Generate new validator keys
    pv, err := privval.GenerateFilePV("./data/priv_validator_key.json", "./data/priv_validator_state.json")
    if err != nil {
        log.Fatal(err)
    }
    defer pv.Close()

    pubKey := pv.GetPubKey()
    log.Printf("Generated validator public key: %x", pubKey.Data)
}
```

Run this once to generate your keys:

```bash
go run generate_keys.go
```

**Output:**
```
Generated validator public key: a1b2c3d4e5f6...
```

**IMPORTANT:** Store these files securely. Never commit private keys to version control!

```bash
chmod 600 ./data/priv_validator_key.json
chmod 600 ./data/priv_validator_state.json
```

---

### Step 2: Create Validator Set

Define the validators participating in consensus:

```go
package main

import (
    "log"
    "github.com/blockberries/leaderberry/types"
)

func createValidatorSet() (*types.ValidatorSet, error) {
    // Define validators
    validators := []*types.NamedValidator{
        {
            Name:        types.NewAccountName("alice"),
            Index:       0,
            PublicKey:   alicePublicKey,  // From Step 1
            VotingPower: 100,
        },
        {
            Name:        types.NewAccountName("bob"),
            Index:       1,
            PublicKey:   bobPublicKey,    // From another node
            VotingPower: 100,
        },
        {
            Name:        types.NewAccountName("charlie"),
            Index:       2,
            PublicKey:   charliePublicKey, // From another node
            VotingPower: 50,
        },
    }

    // Create validator set
    valSet, err := types.NewValidatorSet(validators)
    if err != nil {
        return nil, err
    }

    log.Printf("Created validator set with %d validators", valSet.Size())
    log.Printf("Total voting power: %d", valSet.TotalPower)
    log.Printf("2/3+ majority requires: %d", valSet.TwoThirdsMajority())

    return valSet, nil
}
```

**Key Concepts:**

- **VotingPower:** Validators can have different voting weights
- **2/3+ Majority:** Need 2/3 of total voting power for consensus
- **Index:** Must be unique and sequential (0, 1, 2, ...)

---

### Step 3: Configure Engine

Create a configuration for your consensus engine:

```go
package main

import (
    "time"
    "github.com/blockberries/leaderberry/engine"
)

func createConfig() *engine.Config {
    cfg := engine.DefaultConfig()

    // Required settings
    cfg.ChainID = "my-test-chain"
    cfg.WALPath = "./data/wal"

    // Optional: Customize timeouts
    cfg.Timeouts.Propose = 3 * time.Second
    cfg.Timeouts.ProposeDelta = 500 * time.Millisecond
    cfg.Timeouts.Prevote = 1 * time.Second
    cfg.Timeouts.PrevoteDelta = 500 * time.Millisecond
    cfg.Timeouts.Precommit = 1 * time.Second
    cfg.Timeouts.PrecommitDelta = 500 * time.Millisecond
    cfg.Timeouts.Commit = 1 * time.Second

    // Optional: Block creation settings
    cfg.CreateEmptyBlocks = true
    cfg.CreateEmptyBlocksInterval = 5 * time.Second

    // Validate configuration
    if err := cfg.ValidateBasic(); err != nil {
        log.Fatal("Invalid config:", err)
    }

    return cfg
}
```

---

### Step 4: Start Consensus Engine

Put it all together:

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/blockberries/leaderberry/engine"
    "github.com/blockberries/leaderberry/privval"
    "github.com/blockberries/leaderberry/wal"
    "github.com/blockberries/leaderberry/types"
    gen "github.com/blockberries/leaderberry/types/generated"
)

func main() {
    // Load configuration
    cfg := createConfig()

    // Load validator set
    valSet, err := createValidatorSet()
    if err != nil {
        log.Fatal("Failed to create validator set:", err)
    }

    // Load private validator
    pv, err := privval.LoadFilePV(
        "./data/priv_validator_key.json",
        "./data/priv_validator_state.json",
    )
    if err != nil {
        log.Fatal("Failed to load private validator:", err)
    }
    defer pv.Close()

    // Create WAL
    wal, err := wal.NewFileWAL(cfg.WALPath)
    if err != nil {
        log.Fatal("Failed to create WAL:", err)
    }
    defer wal.Stop()

    if err := wal.Start(); err != nil {
        log.Fatal("Failed to start WAL:", err)
    }

    // Create consensus engine
    eng := engine.NewEngine(cfg, valSet, pv, wal, nil)

    // Set broadcast callbacks
    eng.SetProposalBroadcaster(func(p *gen.Proposal) {
        log.Printf("Broadcasting proposal: height=%d round=%d", p.Height, p.Round)
        // TODO: Broadcast to network
    })

    eng.SetVoteBroadcaster(func(v *gen.Vote) {
        log.Printf("Broadcasting vote: height=%d round=%d type=%v", v.Height, v.Round, v.Type)
        // TODO: Broadcast to network
    })

    // Start consensus at height 1
    if err := eng.Start(1, nil); err != nil {
        log.Fatal("Failed to start engine:", err)
    }

    log.Println("Consensus engine started successfully")

    // Wait for interrupt signal
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
    <-sigCh

    log.Println("Shutting down...")
    if err := eng.Stop(); err != nil {
        log.Printf("Error stopping engine: %v", err)
    }
}
```

---

### Step 5: Run Your Node

```bash
# Create data directory
mkdir -p ./data/wal

# Run the node
go run main.go
```

**Expected Output:**

```
2026/02/02 10:00:00 Created validator set with 3 validators
2026/02/02 10:00:00 Total voting power: 250
2026/02/02 10:00:00 2/3+ majority requires: 167
2026/02/02 10:00:00 Consensus engine started successfully
2026/02/02 10:00:01 Broadcasting proposal: height=1 round=0
2026/02/02 10:00:01 Broadcasting vote: height=1 round=0 type=PREVOTE
2026/02/02 10:00:02 Broadcasting vote: height=1 round=0 type=PRECOMMIT
```

---

## Running Multiple Nodes

To run a real consensus network, you need multiple nodes.

### Node 1 (alice)

```bash
# On machine 1 (or port 26656)
./node --name=alice \
       --listen=0.0.0.0:26656 \
       --peers=node2:26656,node3:26656
```

### Node 2 (bob)

```bash
# On machine 2 (or port 26657)
./node --name=bob \
       --listen=0.0.0.0:26657 \
       --peers=node1:26656,node3:26656
```

### Node 3 (charlie)

```bash
# On machine 3 (or port 26658)
./node --name=charlie \
       --listen=0.0.0.0:26658 \
       --peers=node1:26656,node2:26657
```

---

## Common Pitfalls

### 1. File Permissions

**Problem:** Private key files must have restricted permissions.

```bash
# Fix permissions
chmod 600 ./data/priv_validator_key.json
chmod 600 ./data/priv_validator_state.json
```

### 2. Port Conflicts

**Problem:** Multiple nodes on same machine need different ports.

**Solution:** Configure different ports for each node:

```go
cfg.ListenAddress = "tcp://0.0.0.0:26656"  // Node 1
cfg.ListenAddress = "tcp://0.0.0.0:26657"  // Node 2
```

### 3. WAL Directory

**Problem:** WAL directory must exist and be writable.

```bash
# Create directory with proper permissions
mkdir -p ./data/wal
chmod 755 ./data/wal
```

### 4. ChainID Mismatch

**Problem:** All nodes must use the same ChainID.

**Solution:** Ensure all nodes have identical configuration:

```go
cfg.ChainID = "my-test-chain"  // MUST be identical on all nodes
```

---

## Next Steps

Now that you have a running consensus engine, explore:

1. **[Integration Guide](integration.md)** - Connect to application layer
2. **[Testing Guide](testing.md)** - Write tests for your consensus logic
3. **[Deployment Guide](deployment.md)** - Deploy to production
4. **[Security Best Practices](security-best-practices.md)** - Secure your validator

---

## Troubleshooting

### Engine Won't Start

```bash
# Check logs for errors
tail -f ./logs/consensus.log

# Common issues:
# - WAL directory doesn't exist
# - Invalid configuration
# - Port already in use
```

### No Proposals Being Made

**Possible causes:**
- Not the proposer for this round
- Clock skew between nodes
- Network connectivity issues

**Check:**
```go
height, round, step, _ := eng.GetState()
log.Printf("Current state: H:%d R:%d S:%v", height, round, step)

proposer := eng.GetProposer()
log.Printf("Proposer: %s", types.AccountNameString(proposer.Name))
```

### Votes Not Arriving

**Check:**
- Network connectivity between nodes
- Firewall rules
- Message serialization

**Debug:**
```go
eng.SetVoteBroadcaster(func(v *gen.Vote) {
    data, _ := v.MarshalCramberry()
    log.Printf("Broadcasting vote: %d bytes", len(data))
    // ... send to network
})
```

---

## Additional Resources

- **API Documentation:** [docs/api/index.md](../api/index.md)
- **Tutorials:** [docs/tutorials/](../tutorials/)
- **Example Code:** [test/integration/](../../test/integration/)
- **GitHub Issues:** https://github.com/blockberries/leaderberry/issues
