# Leaderberry Architecture

## Overview

Leaderberry is a Tendermint-style BFT consensus engine designed to integrate with the Blockberries ecosystem:
- **Blockberry**: Node framework providing infrastructure
- **Looseberry**: DAG-based mempool for high-throughput transaction ordering
- **Glueberry**: Encrypted P2P networking
- **Cramberry**: Binary serialization

### Key Design Decisions

1. **Looseberry Integration**: Blocks contain batch certificate references, not individual transactions
2. **Named Accounts**: Human-readable account names with weighted multi-sig authorization
3. **Tendermint BFT**: Classic PBFT-derived consensus with deterministic finality
4. **Pluggable Application**: Implements blockberry's ABI for application layer separation

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              LEADERBERRY                                     │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                        CONSENSUS STATE MACHINE                          │ │
│  │                                                                          │ │
│  │   NewHeight ──► NewRound ──► Propose ──► Prevote ──► Precommit ──► Commit│ │
│  │       │            │                                                     │ │
│  │       │            └──────── timeout ────────────────────────────────┘  │ │
│  │       └───────────────────────────────────────────────────────────────┘ │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                    │                                         │
│         ┌──────────────────────────┼──────────────────────────┐             │
│         │                          │                          │             │
│         ▼                          ▼                          ▼             │
│  ┌─────────────┐          ┌─────────────┐          ┌─────────────┐         │
│  │ Vote Tracker│          │   Proposer  │          │     WAL     │         │
│  │             │          │             │          │             │         │
│  │ Prevotes    │          │ Block Build │          │ Crash       │         │
│  │ Precommits  │          │ From DAG    │          │ Recovery    │         │
│  │ Quorum      │          │ Batches     │          │             │         │
│  └─────────────┘          └─────────────┘          └─────────────┘         │
│                                    │                                         │
├────────────────────────────────────┼────────────────────────────────────────┤
│                                    │                                         │
│  ┌─────────────┐          ┌───────▼───────┐          ┌─────────────┐        │
│  │ TYPES       │          │ BLOCK BUILDER │          │ VALIDATOR   │        │
│  │             │          │               │          │ SET         │        │
│  │ Block       │◄─────────│ ReapBatches   │          │             │        │
│  │ Transaction │          │ BuildHeader   │          │ Proposer    │        │
│  │ Account     │          │ ComputeHash   │          │ Selection   │        │
│  │ Permission  │          │               │          │             │        │
│  └─────────────┘          └───────────────┘          └─────────────┘        │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┼───────────────┐
                    │               │               │
                    ▼               ▼               ▼
             ┌───────────┐   ┌───────────┐   ┌───────────┐
             │LOOSEBERRY │   │BLOCKBERRY │   │ GLUEBERRY │
             │  (DAG     │   │  (Node    │   │   (P2P)   │
             │  Mempool) │   │ Framework)│   │           │
             └───────────┘   └───────────┘   └───────────┘
```

---

## Core Components

### 1. Consensus State Machine

The state machine implements Tendermint BFT with the following states:

```go
type RoundStep uint8

const (
    RoundStepNewHeight     RoundStep = iota + 1  // Waiting for new block height
    RoundStepNewRound                             // Starting a new round
    RoundStepPropose                              // Proposer creates/waits for proposal
    RoundStepPrevote                              // Vote on proposal validity
    RoundStepPrevoteWait                          // Waiting for 2/3 prevotes
    RoundStepPrecommit                            // Vote to commit
    RoundStepPrecommitWait                        // Waiting for 2/3 precommits
    RoundStepCommit                               // Finalize and commit block
)
```

#### State Transitions

```
NewHeight
    └─► NewRound(0)
           └─► Propose
                  │
                  ├─ (valid proposal) ─► Prevote(block)
                  │
                  └─ (invalid/timeout) ─► Prevote(nil)
                                              │
                  ┌───────────────────────────┘
                  │
                  ├─ (2/3+ prevotes for block) ─► Lock on block ─► Precommit(block)
                  │
                  ├─ (2/3+ prevotes for nil) ─► Precommit(nil)
                  │
                  └─ (timeout, no 2/3+) ─► Precommit(nil)
                                              │
                  ┌───────────────────────────┘
                  │
                  ├─ (2/3+ precommits for block) ─► Commit ─► NewHeight
                  │
                  └─ (2/3+ precommits for nil or timeout) ─► NewRound(n+1)
```

#### Proposal Structure

```go
type Proposal struct {
    Height    int64
    Round     int32
    Timestamp time.Time

    // The proposed block
    Block     *Block

    // Proof-of-Lock: if proposer is locked, includes the POL round
    // and the votes that prove the lock. Required when re-proposing
    // a block that validators may be locked against.
    POLRound  int32    // -1 if no POL
    POLVotes  []*Vote  // 2/3+ prevotes from POLRound (nil if POLRound == -1)

    // Proposer identification
    Proposer  AccountName
    Signature []byte
}

// When creating a proposal with POL:
// 1. Proposer must be locked on the block (saw 2/3+ prevotes in a previous round)
// 2. POLRound = the round where the lock was acquired
// 3. POLVotes = the 2/3+ prevotes that caused the lock
// 4. Other validators verify POLVotes before accepting the proposal
```

### 2. Block Structure

Blocks in leaderberry reference Looseberry batch certificates instead of containing raw transactions:

```go
type Block struct {
    Header     BlockHeader
    Data       BlockData
    Evidence   EvidenceList
    LastCommit *Commit
}

type BlockHeader struct {
    // Chain identity
    ChainID     string
    Height      int64
    Time        time.Time

    // Previous block
    LastBlockHash    Hash
    LastCommitHash   Hash

    // State roots
    ValidatorsHash   Hash   // Merkle root of validator set
    AppHash          Hash   // Application state hash (from IAVL)
    ConsensusHash    Hash   // Hash of consensus parameters

    // DAG mempool integration
    DAGRound         uint64             // Highest DAG round included
    BatchCertRefs    []BatchCertRef     // References to certified batches

    // Proposer - identified by name, not address
    Proposer         AccountName
}

type BlockData struct {
    // For Looseberry: We reference batch certificates, not raw txs
    // Transactions are extracted from batches during execution
    BatchCertificates []BatchCertificate
}

type BatchCertRef struct {
    CertificateDigest Hash        // Looseberry certificate digest
    Round             uint64      // DAG round
    Validator         AccountName // Which validator created the batch
}

type BatchCertificate struct {
    Certificate *looseberry.Certificate
    Batches     []*looseberry.Batch
}
```

### 3. Transaction Structure

The consensus engine is transaction-content agnostic. It wraps an opaque payload
with authorization metadata for signature verification and ordering:

```go
type Transaction struct {
    // Opaque payload - interpreted by application layer, not consensus
    Payload     []byte

    // Account identification (named accounts)
    Account     AccountName  // e.g., "alice", "bob"

    // Authorization chain - hierarchical proof of authority
    Authorization Authorization

    // Metadata for replay protection
    Nonce       uint64
    Expiration  time.Time   // Optional
}

// Note: Transaction types, fees, and payload interpretation are handled
// by the application layer, not the consensus engine.

type AccountName string  // Human-readable, e.g., "alice", "bob"

// Authorization provides hierarchical proof that signatures satisfy
// the account's authority requirements. The hierarchy allows for
// efficient verification by matching the delegation chain.
type Authorization struct {
    // Direct signatures from keys in the account's authority
    Signatures []Signature

    // Delegated authorizations from other accounts
    // Each AccountAuthorization provides proof that another account
    // (which has authority over this account) has authorized the tx
    AccountAuthorizations []AccountAuthorization
}

type Signature struct {
    PublicKey []byte    // Which key signed
    Signature []byte    // Ed25519 signature
}

type AccountAuthorization struct {
    // The account providing authorization
    Account       AccountName

    // That account's authorization (recursive - can include
    // their own signatures and delegations)
    Authorization Authorization
}
```

#### Hierarchical Authorization Example

```
Transaction: alice wants to transfer funds
Account: alice

alice's Authority: threshold=2
  - key1 (weight=1)
  - bob (weight=1)
  - charlie (weight=1)

Authorization Proof (2 of 3):
{
    Signatures: [
        { PublicKey: key1, Signature: ... }  // weight=1
    ],
    AccountAuthorizations: [
        {
            Account: "bob",
            Authorization: {
                Signatures: [
                    { PublicKey: bob_key, Signature: ... }
                ]
            }
        }  // weight=1
    ]
}
// Total weight: 2 >= threshold 2 ✓

// This hierarchical structure allows:
// 1. Quick rejection if top-level weight is insufficient
// 2. Lazy verification of nested account authorities
// 3. Clear audit trail of who authorized what

// IMPORTANT: Verification must track visited accounts to detect cycles.
// If alice delegates to bob and bob delegates to alice, naive verification
// would loop forever. Use a visited set:
//
// func verifyAuth(auth Authorization, account AccountName, visited map[AccountName]bool) bool {
//     if visited[account] {
//         return false  // Cycle detected
//     }
//     visited[account] = true
//     // ... verify signatures and recurse into AccountAuthorizations
// }
```

### 4. Named Accounts

Simple named account system with single authority:

```go
type Account struct {
    // Identity
    Name        AccountName      // Human-readable name (e.g., "alice", "bob")
    ID          uint64           // Numeric ID for efficiency

    // Single authority - multi-sig with weighted threshold
    Authority   Authority

    // Metadata
    CreatedAt   time.Time
    UpdatedAt   time.Time
    Metadata    map[string][]byte
}

type Authority struct {
    // Multi-sig threshold
    Threshold   uint32

    // Authorized keys with weights
    Keys        []KeyWeight

    // Authorized accounts (delegation) with weights
    Accounts    []AccountWeight
}

type KeyWeight struct {
    PublicKey   []byte
    Weight      uint32
}

type AccountWeight struct {
    Account     AccountName
    Weight      uint32
}
```

#### Account Examples

```
alice (Account)
└── Authority [threshold=1]
    └── key1 (weight=1)

bob (Account) - Multi-sig
└── Authority [threshold=2]
    ├── key1 (weight=1)
    ├── key2 (weight=1)
    └── alice (weight=1)  // Account delegation

escrow (Account) - 2-of-3
└── Authority [threshold=2]
    ├── alice (weight=1)
    ├── bob (weight=1)
    └── charlie (weight=1)
```

### 5. Named Validators

Validators are identified by their account name, with a single linked public key:

```go
type NamedValidator struct {
    // Identity - validator is identified by name, not address
    Name          AccountName    // Human-readable validator name (e.g., "validator.alice")
    Index         uint16         // Position in validator set (for efficient lookups)

    // Single consensus key (can be changed via application layer)
    PublicKey     []byte         // Ed25519 public key for signing consensus messages

    // Voting power
    VotingPower   int64

    // Proposer selection priority (for round-robin with priority)
    ProposerPriority int64
}

// ValidatorSet uses names as primary identifiers
type ValidatorSet struct {
    validators    []*NamedValidator          // Ordered by index
    byName        map[AccountName]*NamedValidator  // Fast lookup by name
    byPubKey      map[string]*NamedValidator       // Fast lookup by public key

    totalPower    int64
    proposer      *NamedValidator            // Current proposer (updated each round)
}

// NewValidatorSet creates a validator set and initializes proposer priorities
func NewValidatorSet(vals []*NamedValidator) *ValidatorSet {
    vs := &ValidatorSet{
        validators: vals,
        byName:     make(map[AccountName]*NamedValidator),
        byPubKey:   make(map[string]*NamedValidator),
    }
    for _, v := range vals {
        vs.byName[v.Name] = v
        vs.byPubKey[string(v.PublicKey)] = v
        vs.totalPower += v.VotingPower
        // Initialize priorities to voting power
        v.ProposerPriority = v.VotingPower
    }
    // Set initial proposer (highest priority)
    vs.incrementProposerPriority()
    return vs
}

func (vs *ValidatorSet) GetByName(name AccountName) *NamedValidator
func (vs *ValidatorSet) GetByIndex(index uint16) *NamedValidator
func (vs *ValidatorSet) GetByPubKey(pubKey []byte) *NamedValidator
func (vs *ValidatorSet) GetProposer(height int64, round int32) *NamedValidator
```

Note: Key rotation (switching from one key to another) is handled by the application layer, not the consensus engine. The consensus engine only sees the current active key for each validator. Proposals and votes reference validators by name, not by key-derived address.

### 6. Vote Tracking

```go
type VoteType uint8

const (
    VoteTypePrevote   VoteType = 1
    VoteTypePrecommit VoteType = 2
)

type Vote struct {
    Type      VoteType
    Height    int64
    Round     int32
    BlockHash Hash          // nil for nil votes
    Timestamp time.Time

    // Validator identification by name, not address
    Validator AccountName

    // Signature
    Signature []byte
}

func (v *Vote) SignBytes() []byte  // Canonical bytes for signing

type VoteSet struct {
    height       int64
    round        int32
    voteType     VoteType
    validatorSet ValidatorSet

    // Votes indexed by validator index (for efficient array access)
    votes        map[uint16]*Vote

    // Vote totals by block hash
    votesByBlock map[string]*BlockVotes

    // Quorum tracking
    maj23        *BlockVotes  // nil until 2/3 reached
}

type BlockVotes struct {
    BlockHash   Hash
    Votes       []*Vote
    TotalPower  int64
}

// Hash is a 32-byte SHA256 hash
type Hash [32]byte

// Commit aggregates precommits for a block
type Commit struct {
    Height     int64
    Round      int32
    BlockHash  Hash
    Signatures []CommitSig  // Precommit signatures from 2/3+ validators
}

type CommitSig struct {
    ValidatorIndex uint16
    Signature      []byte
    Timestamp      time.Time
}

// Evidence types for Byzantine behavior
type Evidence interface {
    Height() int64
    Validate(valSet *ValidatorSet) error
    Hash() Hash
}

type EvidenceList []Evidence

type DuplicateVoteEvidence struct {
    VoteA *Vote
    VoteB *Vote
}

func (e *DuplicateVoteEvidence) Height() int64 { return e.VoteA.Height }
```

### 7. Write-Ahead Log (WAL)

For crash recovery:

```go
type WAL interface {
    // Write operations
    Write(msg WALMessage) error
    WriteSync(msg WALMessage) error

    // Recovery
    SearchForEndHeight(height int64) (WALReader, error)

    // Lifecycle
    Start() error
    Stop() error
    Flush() error
}

type WALMessage struct {
    Type    WALMsgType
    Height  int64
    Round   int32
    Data    []byte  // Serialized message
}

type WALMsgType uint8

const (
    WALMsgProposal    WALMsgType = 1
    WALMsgVote        WALMsgType = 2
    WALMsgCommit      WALMsgType = 3
    WALMsgEndHeight   WALMsgType = 4
    WALMsgState       WALMsgType = 5
)
```

---

## Integration Points

### Raspberry Integration

Leaderberry serves as the consensus engine for the Raspberry blockchain node, working in conjunction with:

- **Blockberry**: Node framework providing networking, storage, and peer management
- **Looseberry**: DAG mempool providing certified transaction batches
- **Application**: User-defined state machine for transaction execution

The integration flow:

```
Application
    ↓ (CheckTx for validation)
Looseberry (DAG Mempool)
    ↓ (ReapCertifiedBatches)
Leaderberry (BFT Consensus)
    ↓ (ExecuteTx for each batch)
Application
    ↓ (Commit for finalization)
Blockberry (Storage)
```

### Blockberry Integration

Leaderberry implements the `BFTConsensus` interface from blockberry:

```go
// Implements consensus.BFTConsensus
type Engine struct {
    // From ConsensusEngine
    deps     consensus.ConsensusDependencies
    height   int64
    round    int32

    // BFT-specific
    state    *ConsensusState
    votes    *HeightVoteSet
    proposer *Proposer
    wal      WAL

    // Looseberry integration
    dagMempool looseberry.DAGMempool
}

// Initialize implements ConsensusEngine
func (e *Engine) Initialize(deps consensus.ConsensusDependencies) error

// ProcessBlock implements ConsensusEngine
func (e *Engine) ProcessBlock(block *consensus.Block) error

// ProduceBlock implements BlockProducer
func (e *Engine) ProduceBlock(height int64) (*consensus.Block, error)

// HandleProposal implements BFTConsensus
func (e *Engine) HandleProposal(proposal *consensus.Proposal) error

// HandleVote implements BFTConsensus
func (e *Engine) HandleVote(vote *consensus.Vote) error

// HandleCommit implements BFTConsensus
func (e *Engine) HandleCommit(commit *consensus.Commit) error
```

### Looseberry Integration

Block building from DAG mempool:

```go
func (e *Engine) buildBlockFromDAG(height int64) (*Block, error) {
    // Reap certified batches from Looseberry
    batches := e.dagMempool.ReapCertifiedBatches(e.config.MaxBlockBytes)

    // Build batch certificate references
    refs := make([]BatchCertRef, 0, len(batches))
    for _, batch := range batches {
        refs = append(refs, BatchCertRef{
            CertificateDigest: batch.Certificate.Digest(),
            Round:             batch.Certificate.Round(),
            Validator:         batch.Certificate.AuthorName(),
        })
    }

    // Compute block header
    header := &BlockHeader{
        ChainID:           e.config.ChainID,
        Height:            height,
        Time:              time.Now(),
        LastBlockHash:     e.lastBlockHash,
        DAGRound:          e.dagMempool.CurrentRound(),
        BatchCertRefs:     refs,
        Proposer:          e.validatorName,
    }

    return &Block{
        Header: *header,
        Data: BlockData{
            BatchCertificates: batches,
        },
    }, nil
}
```

### Application Integration

The Application interface defines how Leaderberry interacts with the user-defined state machine:

```go
type Application interface {
    // Transaction validation (called by Looseberry before batching)
    // MUST be deterministic across all nodes
    // Leaderberry does NOT call this - it's called by Looseberry during AddTx()
    CheckTx(ctx context.Context, tx []byte) error

    // Block lifecycle (called by Leaderberry during consensus)
    BeginBlock(ctx context.Context, header *BlockHeader) error
    ExecuteTx(ctx context.Context, tx []byte) (*TxResult, error)
    EndBlock(ctx context.Context) (*EndBlockResult, error)
    Commit(ctx context.Context) (*CommitResult, error)

    // Queries (called by RPC layer, not by consensus)
    Query(ctx context.Context, path string, data []byte, height int64) (*QueryResult, error)

    // Initialization
    InitChain(ctx context.Context, validators []Validator, appState []byte) error
}

type TxResult struct {
    Code      uint32            // 0 = success, non-zero = error
    Data      []byte            // Result data
    Log       string            // Human-readable log
    GasWanted int64
    GasUsed   int64
    Events    []Event
}

type EndBlockResult struct {
    ValidatorUpdates []ValidatorUpdate   // Changes to validator set
    ConsensusParams  *ConsensusParams    // Updated consensus parameters
    Events           []Event
}

type CommitResult struct {
    AppHash []byte              // New application state root
}
```

**Important**: Leaderberry treats transaction payloads as opaque `[]byte`. The consensus engine only handles:
- Transaction ordering (via Looseberry batches)
- Authorization verification (for named accounts)
- Block finalization

Transaction content interpretation, execution logic, and state transitions are the Application's responsibility.

Block execution flow:

```go
func (e *Engine) executeBlock(block *Block) error {
    ctx := context.Background()

    // 1. Begin block
    if err := e.app.BeginBlock(ctx, &block.Header); err != nil {
        return err
    }

    // 2. Execute transactions from batch certificates
    // Note: Transactions were already validated by Looseberry's CheckTx
    for _, batchCert := range block.Data.BatchCertificates {
        for _, batch := range batchCert.Batches {
            for _, tx := range batch.Transactions {
                result, err := e.app.ExecuteTx(ctx, tx)
                if err != nil {
                    return fmt.Errorf("ExecuteTx failed: %w", err)
                }

                if result.Code != 0 {
                    // Non-zero code indicates execution failure
                    // This is a consensus failure since CheckTx passed
                    e.logger.Warn("Transaction execution failed",
                        "code", result.Code,
                        "log", result.Log)
                }
            }
        }
    }

    // 3. End block
    endResult, err := e.app.EndBlock(ctx)
    if err != nil {
        return fmt.Errorf("EndBlock failed: %w", err)
    }

    // 4. Apply validator updates (if any)
    if len(endResult.ValidatorUpdates) > 0 {
        if err := e.applyValidatorUpdates(endResult.ValidatorUpdates); err != nil {
            return fmt.Errorf("validator update failed: %w", err)
        }

        // Notify Looseberry of validator set change
        e.looseberry.UpdateValidatorSet(e.validatorSet)
    }

    // 5. Commit application state
    commitResult, err := e.app.Commit(ctx)
    if err != nil {
        return fmt.Errorf("Commit failed: %w", err)
    }

    e.lastAppHash = commitResult.AppHash

    // 6. Notify Looseberry for garbage collection
    e.looseberry.NotifyCommitted(block.Header.DAGRound)

    // 7. Save block (handled by blockberry)
    // Blockberry's OnBlockCommit callback saves to blockstore

    return nil
}
```

---

## Consensus Algorithm

### Proposer Selection

Proposer selection uses weighted round-robin with priority adjustment. This ensures fair distribution proportional to voting power over time.

```go
// GetProposer returns the proposer for a given height and round
func (vs *ValidatorSet) GetProposer(height int64, round int32) *NamedValidator {
    // For round 0, use pre-computed proposer
    // For round > 0, advance through validators deterministically
    if round == 0 {
        return vs.proposer
    }

    // Copy and increment priorities 'round' times
    vals := vs.Copy()
    for i := int32(0); i < round; i++ {
        vals.incrementProposerPriority()
    }
    return vals.proposer
}

// incrementProposerPriority adjusts priorities and selects next proposer
// Algorithm ensures proposer selection is proportional to voting power
func (vs *ValidatorSet) incrementProposerPriority() {
    // 1. Add voting power to each validator's priority
    for _, v := range vs.validators {
        v.ProposerPriority += v.VotingPower
    }

    // 2. Select proposer with highest priority
    var maxPriority int64 = math.MinInt64
    var proposer *NamedValidator
    for _, v := range vs.validators {
        if v.ProposerPriority > maxPriority {
            maxPriority = v.ProposerPriority
            proposer = v
        }
    }

    // 3. Subtract total voting power from selected proposer's priority
    // This ensures they won't be selected again until others "catch up"
    proposer.ProposerPriority -= vs.totalPower
    vs.proposer = proposer
}

// Called at end of each height to prepare for next height
func (vs *ValidatorSet) IncrementProposerPriority(times int32) {
    for i := int32(0); i < times; i++ {
        vs.incrementProposerPriority()
    }
}
```

**Example** (3 validators with powers 4, 3, 3):

| Round | Priorities Before | Proposer | Priorities After |
|-------|-------------------|----------|------------------|
| 0 | [4, 3, 3] | V1 (4) | [-6, 3, 3] |
| 1 | [-2, 6, 6] | V2 (6) | [-2, -4, 6] |
| 2 | [2, -1, 9] | V3 (9) | [2, -1, -1] |
| 3 | [6, 2, 2] | V1 (6) | [-4, 2, 2] |

Over time, each validator proposes proportionally to their voting power.

### Locking Rules

Locking is a key safety mechanism that prevents validators from voting for conflicting blocks across rounds.

#### Lock Acquisition

A validator locks on a block when it sees 2/3+ prevotes for that block:

```go
func (cs *ConsensusState) addPrevote(vote *Vote) {
    cs.Prevotes.AddVote(vote)

    // Lock when we see 2/3+ prevotes for a block
    if blockHash, ok := cs.Prevotes.TwoThirdsMajority(); ok && blockHash != nil {
        cs.LockedRound = cs.Round
        cs.LockedBlock = cs.ProposalBlock
        cs.LockedBlockHash = blockHash
    }
}
```

#### Prevote Decision

Once locked, a validator follows strict rules about what it can prevote for:

```go
func (cs *ConsensusState) decidePrevote(proposal *Proposal) Hash {
    // If not locked, prevote for valid proposal
    if cs.LockedBlock == nil {
        if cs.isValidProposal(proposal) {
            return proposal.Block.Hash()
        }
        return nil // Prevote nil
    }

    // If locked, only prevote for:
    // 1. The locked block itself
    if bytes.Equal(proposal.Block.Hash(), cs.LockedBlockHash) {
        return cs.LockedBlockHash
    }

    // 2. A different block if proposal has valid POL from a later round
    //    NOTE: We do NOT update our lock here. Lock is only updated when
    //    we see 2/3+ prevotes in addPrevote(). This just allows us to
    //    prevote for a different block.
    if cs.canUnlock(proposal) {
        return proposal.Block.Hash()
    }

    // Otherwise, prevote nil
    return nil
}

func (cs *ConsensusState) canUnlock(proposal *Proposal) bool {
    // Can only unlock if:
    // 1. Proposal includes a POL (Proof-of-Lock) from a round > our locked round
    // 2. The POL is valid (contains 2/3+ prevotes for the proposed block)
    return proposal.POLRound > cs.LockedRound &&
           cs.verifyPOL(proposal.POLVotes, proposal.Block.Hash(), proposal.POLRound)
}
```

#### Why Locking Works

The locking mechanism ensures safety through these invariants:

1. **Lock requires quorum**: You only lock after seeing 2/3+ prevotes
2. **No conflicting locks**: Since 2/3+ is required, at most one block can be locked per round
3. **Locks persist across rounds**: Once locked, you only vote for that block (or unlock via POL)
4. **Unlock requires proof**: To unlock, you need evidence that 2/3+ prevoted for a different block in a later round

```
Safety proof sketch:
- If validator V commits block A, V saw 2/3+ precommits for A
- Those 2/3+ validators saw 2/3+ prevotes for A (to precommit)
- Those 2/3+ validators are now locked on A
- In any future round, those locked validators won't prevote for B ≠ A
- Therefore B cannot get 2/3+ prevotes → cannot get 2/3+ precommits → cannot be committed
```

### Timeout Management

Timeouts increase with each round to handle varying network conditions:

```go
type TimeoutScheduler struct {
    propose   time.Duration  // Base propose timeout
    prevote   time.Duration  // Base prevote timeout
    precommit time.Duration  // Base precommit timeout
    commit    time.Duration  // Time to wait after commit before starting next height

    delta     time.Duration  // Timeout increase per round
}

func (ts *TimeoutScheduler) ProposeTimeout(round int32) time.Duration {
    return ts.propose + time.Duration(round)*ts.delta
}

func (ts *TimeoutScheduler) PrevoteTimeout(round int32) time.Duration {
    return ts.prevote + time.Duration(round)*ts.delta
}

func (ts *TimeoutScheduler) PrecommitTimeout(round int32) time.Duration {
    return ts.precommit + time.Duration(round)*ts.delta
}

func (ts *TimeoutScheduler) CommitTimeout() time.Duration {
    return ts.commit
}
```

**Default timeouts**:

| Parameter | Default | Description |
|-----------|---------|-------------|
| propose | 3s | Wait for proposal from proposer |
| prevote | 1s | Wait for 2/3+ prevotes after first prevote |
| precommit | 1s | Wait for 2/3+ precommits after first precommit |
| commit | 1s | Wait after commit before next height |
| delta | 500ms | Increase per round |

**Round timing** (with defaults):

| Round | Propose | Prevote | Precommit | Total |
|-------|---------|---------|-----------|-------|
| 0 | 3.0s | 1.0s | 1.0s | 5.0s |
| 1 | 3.5s | 1.5s | 1.5s | 6.5s |
| 2 | 4.0s | 2.0s | 2.0s | 8.0s |

---

## Security Considerations

### Byzantine Fault Tolerance

- Tolerates up to f = (n-1)/3 Byzantine validators
- Requires 2f+1 = 2/3+1 votes for consensus
- Finality is instant - committed blocks never revert

### Double-Sign Prevention

The private validator tracks the last signed vote to prevent signing conflicting votes:

```go
type PrivValidator struct {
    key           ed25519.PrivateKey
    lastSignState *LastSignState
}

// LastSignState tracks the last vote signed at each step
// Step values: 1 = prevote, 2 = precommit
type LastSignState struct {
    Height    int64
    Round     int32
    Step      int8      // 1=prevote, 2=precommit
    BlockHash []byte    // Hash of block voted for (nil for nil votes)
    Signature []byte

    filePath  string    // File-backed for persistence across restarts
}

func (pv *PrivValidator) SignVote(vote *Vote) error {
    lss := pv.lastSignState
    step := int8(vote.Type)  // VoteTypePrevote=1, VoteTypePrecommit=2

    // Reject votes from the past
    if vote.Height < lss.Height {
        return ErrVoteHeightTooLow
    }
    if vote.Height == lss.Height && vote.Round < lss.Round {
        return ErrVoteRoundTooLow
    }
    if vote.Height == lss.Height && vote.Round == lss.Round && step < lss.Step {
        return ErrVoteStepTooLow
    }

    // Check for double-sign at same H/R/S
    if vote.Height == lss.Height && vote.Round == lss.Round && step == lss.Step {
        // Same H/R/S - only allow if voting for the same block
        if bytes.Equal(vote.BlockHash[:], lss.BlockHash) {
            // Re-signing same vote is OK (idempotent)
            vote.Signature = lss.Signature
            return nil
        }
        // Different block at same H/R/S = double sign!
        return ErrDoubleSign
    }

    // New vote - sign and update state
    sig := ed25519.Sign(pv.key, vote.SignBytes())
    vote.Signature = sig

    pv.lastSignState = &LastSignState{
        Height:    vote.Height,
        Round:     vote.Round,
        Step:      step,
        BlockHash: vote.BlockHash[:],
        Signature: sig,
    }

    return pv.lastSignState.Save()
}
```

### Equivocation Detection

```go
type EvidencePool struct {
    evidence []Evidence

    // Track potential equivocation
    seen map[string]*Vote  // validator+height+round+type -> vote
}

func (ep *EvidencePool) CheckVote(vote *Vote) (Evidence, bool) {
    key := fmt.Sprintf("%s/%d/%d/%d",
        vote.Validator, vote.Height, vote.Round, vote.Type)

    if existing, ok := ep.seen[key]; ok {
        if !bytes.Equal(existing.BlockHash, vote.BlockHash) {
            // Equivocation detected!
            return &DuplicateVoteEvidence{
                VoteA: existing,
                VoteB: vote,
            }, true
        }
    }

    ep.seen[key] = vote
    return nil, false
}
```

---

## Configuration

```go
type Config struct {
    // Chain
    ChainID     string

    // Timeouts
    TimeoutPropose   time.Duration  // Default: 3s
    TimeoutPrevote   time.Duration  // Default: 1s
    TimeoutPrecommit time.Duration  // Default: 1s
    TimeoutCommit    time.Duration  // Default: 1s
    TimeoutDelta     time.Duration  // Default: 500ms (per round)

    // Block
    MaxBlockBytes    int64          // Default: 21MB
    MaxBlockTxs      int            // Default: 10000

    // DAG integration
    MaxDAGRoundGap   uint64         // Default: 100 rounds

    // WAL
    WALDir           string

    // Validator
    PrivValidatorKey string
}
```

---

## File Structure

```
leaderberry/
├── ARCHITECTURE.md
├── CODE_REVIEW.md
├── go.mod
├── go.sum
├── Makefile
│
├── engine/
│   ├── engine.go           # Main consensus engine
│   ├── state.go            # Consensus state machine
│   ├── proposer.go         # Block proposal logic
│   ├── vote_tracker.go     # Vote collection and quorum
│   ├── timeout.go          # Timeout management
│   └── replay.go           # WAL replay for recovery
│
├── types/
│   ├── block.go            # Block and header types
│   ├── transaction.go      # Transaction with named accounts
│   ├── account.go          # Named account structure
│   ├── permission.go       # Hierarchical permissions
│   ├── vote.go             # Vote types
│   ├── commit.go           # Commit structure
│   ├── evidence.go         # Byzantine evidence
│   ├── validator.go        # Validator types
│   └── hash.go             # Hash utilities
│
├── wal/
│   ├── wal.go              # Write-ahead log interface
│   ├── file_wal.go         # File-based WAL implementation
│   └── encoder.go          # WAL message encoding
│
├── privval/
│   ├── signer.go           # Private validator signer
│   ├── file_pv.go          # File-based private validator
│   └── double_sign.go      # Double-sign prevention
│
├── evidence/
│   ├── pool.go             # Evidence pool
│   ├── types.go            # Evidence types
│   └── verify.go           # Evidence verification
│
├── schema/
│   ├── leaderberry.cram    # Cramberry schema
│   └── *.go                # Generated code
│
└── tests/
    ├── engine_test.go
    ├── state_test.go
    ├── vote_test.go
    ├── permission_test.go
    └── integration_test.go
```

---

## Dependencies

```go
module github.com/blockberries/leaderberry

go 1.23

require (
    github.com/blockberries/blockberry v0.1.0
    github.com/blockberries/looseberry v0.1.0
    github.com/blockberries/cramberry v1.0.0
    github.com/blockberries/glueberry v1.0.0
)

replace (
    github.com/blockberries/blockberry => ../blockberry
    github.com/blockberries/looseberry => ../looseberry
    github.com/blockberries/cramberry => ../cramberry
    github.com/blockberries/glueberry => ../glueberry
)
```

---

## Performance Targets

| Metric | Target |
|--------|--------|
| Block time | 1-3 seconds |
| Transactions per block | 10,000+ |
| Finality | Instant (same block) |
| Validators | Up to 100 |
| Recovery time | < 30 seconds |

---

## Future Considerations

1. **Vote Extensions**: Support for application-specific data in votes
2. **Threshold Signatures**: BLS aggregation for commit signatures
3. **Light Client Proofs**: Merkle proofs for block verification
4. **State Sync**: Snapshot-based fast sync for new nodes
5. **Pipelining**: Overlapping consensus rounds for higher throughput
