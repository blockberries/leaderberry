# Leaderberry Implementation Plan

## Overview

This document provides a comprehensive implementation plan for Leaderberry, a Tendermint-style BFT consensus engine. The implementation uses Cramberry for binary serialization and integrates with Blockberry (node framework) and Looseberry (DAG mempool).

**Key Constraints:**
- All network-serializable types must be defined in `.cram` schema files
- Use Cramberry's generated code for serialization (no reflection)
- Implement blockberry's `BFTConsensus` interface
- Integrate with Looseberry's `DAGMempool` interface
- Support named accounts and named validators

---

## Phase 1: Project Setup and Cramberry Schemas

### 1.1 Project Structure

```
leaderberry/
├── ARCHITECTURE.md
├── CLAUDE.md
├── IMPLEMENTATION_PLAN.md
├── go.mod
├── go.sum
├── Makefile
│
├── schema/                    # Cramberry schemas
│   ├── types.cram            # Core types (Hash, Signature, AccountName)
│   ├── block.cram            # Block, BlockHeader, BlockData
│   ├── transaction.cram      # Transaction wrapper, Authorization (payload opaque)
│   ├── account.cram          # Account, Authority
│   ├── validator.cram        # NamedValidator, ValidatorSet
│   ├── vote.cram             # Vote, VoteSet, Commit
│   ├── proposal.cram         # Proposal
│   ├── evidence.cram         # Evidence types
│   └── wal.cram              # WAL message types
│
├── types/                     # Generated + hand-written types
│   ├── generated/            # Cramberry-generated code
│   │   └── *.go
│   ├── hash.go               # Hash utilities (extends generated)
│   ├── signature.go          # Signature utilities
│   ├── account.go            # Account methods
│   ├── validator.go          # ValidatorSet implementation
│   ├── block.go              # Block methods
│   ├── vote.go               # Vote methods
│   ├── proposal.go           # Proposal methods
│   └── evidence.go           # Evidence methods
│
├── engine/                    # Consensus engine
│   ├── engine.go             # Main Engine struct, BFTConsensus impl
│   ├── state.go              # ConsensusState machine
│   ├── proposer.go           # Block proposal logic
│   ├── vote_tracker.go       # VoteSet, HeightVoteSet
│   ├── timeout.go            # TimeoutScheduler
│   ├── replay.go             # WAL replay for recovery
│   └── metrics.go            # Consensus metrics
│
├── wal/                       # Write-ahead log
│   ├── wal.go                # WAL interface
│   ├── file_wal.go           # File-based implementation
│   └── encoder.go            # WAL message encoding
│
├── privval/                   # Private validator
│   ├── signer.go             # PrivValidator interface
│   ├── file_pv.go            # File-based private validator
│   └── last_sign_state.go    # Double-sign prevention
│
├── evidence/                  # Byzantine evidence
│   ├── pool.go               # EvidencePool
│   ├── verify.go             # Evidence verification
│   └── reactor.go            # Evidence gossip
│
└── tests/                     # Test suites
    ├── engine_test.go
    ├── state_test.go
    ├── vote_test.go
    ├── wal_test.go
    ├── privval_test.go
    ├── evidence_test.go
    └── integration/
        └── consensus_test.go
```

### 1.2 Makefile

```makefile
.PHONY: all generate build test lint clean

CRAMBERRY := cramberry
SCHEMA_DIR := ./schema
GEN_DIR := ./types/generated

all: generate build

generate:
	@echo "Generating cramberry code..."
	$(CRAMBERRY) generate -lang go -out $(GEN_DIR) $(SCHEMA_DIR)/*.cram

build: generate
	go build ./...

test: generate
	go test -race -v ./...

lint:
	golangci-lint run

clean:
	rm -rf $(GEN_DIR)/*.go
	go clean ./...
```

### 1.3 Cramberry Schema Files

#### 1.3.1 `schema/types.cram` - Core Types

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

/// Hash is a 32-byte SHA-256 hash
message Hash {
    data: bytes = 1 [required];
}

/// Signature is a 64-byte Ed25519 signature
message Signature {
    data: bytes = 1 [required];
}

/// PublicKey is a 32-byte Ed25519 public key
message PublicKey {
    data: bytes = 1 [required];
}

/// AccountName is a human-readable account identifier
message AccountName {
    name: string = 1 [required];
}

/// Timestamp in Unix nanoseconds
message Timestamp {
    nanos: int64 = 1;
}
```

#### 1.3.2 `schema/account.cram` - Named Accounts

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";

/// KeyWeight associates a public key with a weight for multi-sig
message KeyWeight {
    public_key: PublicKey = 1 [required];
    weight: uint32 = 2;
}

/// AccountWeight associates an account with a weight for delegation
message AccountWeight {
    account: AccountName = 1 [required];
    weight: uint32 = 2;
}

/// Authority defines multi-sig requirements for an account
message Authority {
    /// Minimum weight required to authorize
    threshold: uint32 = 1;
    /// Direct key authorizations
    keys: []KeyWeight = 2;
    /// Delegated account authorizations
    accounts: []AccountWeight = 3;
}

/// Account represents a named account with multi-sig authority
message Account {
    name: AccountName = 1 [required];
    id: uint64 = 2;
    authority: Authority = 3 [required];
    created_at: int64 = 4;
    updated_at: int64 = 5;
    metadata: map[string]bytes = 6;
}
```

#### 1.3.3 `schema/transaction.cram` - Transaction Wrapper

The consensus engine is transaction-content agnostic. It only handles authorization
verification and ordering. The payload is opaque bytes passed to the application layer.

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";
import "account.cram";

/// SignatureProof is a direct signature from a key
message SignatureProof {
    public_key: PublicKey = 1 [required];
    signature: Signature = 2 [required];
}

/// AccountAuthorization is a delegated authorization from another account
message AccountAuthorization {
    /// The account providing authorization
    account: AccountName = 1 [required];
    /// That account's authorization (recursive)
    authorization: Authorization = 2 [required];
}

/// Authorization provides hierarchical proof of authority
message Authorization {
    /// Direct signatures from keys
    signatures: []SignatureProof = 1;
    /// Delegated authorizations from accounts
    account_authorizations: []AccountAuthorization = 2;
}

/// Transaction wraps an opaque payload with authorization metadata
/// The consensus engine verifies authorizations but does not interpret the payload
message Transaction {
    /// Opaque payload - interpreted by application layer, not consensus
    payload: bytes = 1 [required];
    /// Account authorizing this transaction
    account: AccountName = 2 [required];
    /// Proof of authorization (signatures, delegations)
    authorization: Authorization = 3 [required];
    /// Sequence number for replay protection
    nonce: uint64 = 4;
    /// Unix timestamp after which tx is invalid (optional)
    expiration: int64 = 5;
}
```

#### 1.3.4 `schema/validator.cram` - Named Validators

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";
import "account.cram";

/// NamedValidator represents a validator identified by name
message NamedValidator {
    /// Human-readable validator name
    name: AccountName = 1 [required];
    /// Position in validator set (for efficient indexing)
    index: uint16 = 2;
    /// Ed25519 public key for consensus messages
    public_key: PublicKey = 3 [required];
    /// Voting power
    voting_power: int64 = 4;
    /// Proposer selection priority
    proposer_priority: int64 = 5;
}

/// ValidatorSetData is the serializable form of ValidatorSet
message ValidatorSetData {
    validators: []NamedValidator = 1;
    proposer_index: uint16 = 2;
    total_power: int64 = 3;
}
```

#### 1.3.5 `schema/vote.cram` - Votes and Commits

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";
import "account.cram";

/// VoteType distinguishes prevotes from precommits
enum VoteType {
    VOTE_TYPE_UNKNOWN = 0;
    VOTE_TYPE_PREVOTE = 1;
    VOTE_TYPE_PRECOMMIT = 2;
}

/// Vote represents a consensus vote
message Vote {
    type: VoteType = 1;
    height: int64 = 2;
    round: int32 = 3;
    /// Hash of the block being voted for (empty = nil vote)
    block_hash: Hash = 4;
    timestamp: int64 = 5;
    /// Validator name (not address)
    validator: AccountName = 6 [required];
    /// Validator index for efficient lookup
    validator_index: uint16 = 7;
    signature: Signature = 8;
}

/// CommitSig is a precommit signature in a Commit
message CommitSig {
    validator_index: uint16 = 1;
    signature: Signature = 2;
    timestamp: int64 = 3;
    /// Empty if validator did not commit
    block_hash: Hash = 4;
}

/// Commit aggregates 2/3+ precommits for a block
message Commit {
    height: int64 = 1;
    round: int32 = 2;
    block_hash: Hash = 3 [required];
    signatures: []CommitSig = 4;
}
```

#### 1.3.6 `schema/block.cram` - Blocks

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";
import "account.cram";
import "vote.cram";

/// BatchCertRef references a Looseberry batch certificate
message BatchCertRef {
    certificate_digest: Hash = 1 [required];
    round: uint64 = 2;
    validator: AccountName = 3 [required];
}

/// BlockHeader contains block metadata
message BlockHeader {
    /// Chain identity
    chain_id: string = 1;
    height: int64 = 2;
    time: int64 = 3;

    /// Previous block
    last_block_hash: Hash = 4;
    last_commit_hash: Hash = 5;

    /// State roots
    validators_hash: Hash = 6;
    app_hash: Hash = 7;
    consensus_hash: Hash = 8;

    /// DAG mempool integration
    dag_round: uint64 = 9;
    batch_cert_refs: []BatchCertRef = 10;

    /// Proposer - identified by name
    proposer: AccountName = 11 [required];
}

/// BlockData contains the block body
message BlockData {
    /// References to Looseberry batch certificates
    batch_digests: []Hash = 1;
}

/// Block is a complete consensus block
message Block {
    header: BlockHeader = 1 [required];
    data: BlockData = 2;
    evidence: []bytes = 3;
    last_commit: *Commit = 4;
}
```

#### 1.3.7 `schema/proposal.cram` - Proposals

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";
import "account.cram";
import "block.cram";
import "vote.cram";

/// Proposal is a block proposal from the round's proposer
message Proposal {
    height: int64 = 1;
    round: int32 = 2;
    timestamp: int64 = 3;

    /// The proposed block
    block: Block = 4 [required];

    /// Proof-of-Lock round (-1 if none)
    pol_round: int32 = 5;
    /// 2/3+ prevotes from POLRound (empty if pol_round == -1)
    pol_votes: []Vote = 6;

    /// Proposer identification
    proposer: AccountName = 7 [required];
    signature: Signature = 8;
}
```

#### 1.3.8 `schema/evidence.cram` - Byzantine Evidence

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";
import "account.cram";
import "vote.cram";

/// EvidenceType identifies the type of Byzantine behavior
enum EvidenceType {
    EVIDENCE_TYPE_UNKNOWN = 0;
    EVIDENCE_TYPE_DUPLICATE_VOTE = 1;
    EVIDENCE_TYPE_LIGHT_CLIENT_ATTACK = 2;
}

/// DuplicateVoteEvidence proves a validator voted twice
message DuplicateVoteEvidence {
    vote_a: Vote = 1 [required];
    vote_b: Vote = 2 [required];
    total_voting_power: int64 = 3;
    validator_power: int64 = 4;
    timestamp: int64 = 5;
}

/// Evidence is a polymorphic evidence container
message Evidence {
    type: EvidenceType = 1;
    height: int64 = 2;
    time: int64 = 3;
    /// Serialized evidence data (type-specific)
    data: bytes = 4;
}
```

#### 1.3.9 `schema/wal.cram` - Write-Ahead Log

```cramberry
package leaderberry.types;

option go_package = "github.com/blockberries/leaderberry/types/generated";

import "types.cram";
import "vote.cram";
import "proposal.cram";

/// WALMsgType identifies WAL message types
enum WALMsgType {
    WAL_MSG_UNKNOWN = 0;
    WAL_MSG_PROPOSAL = 1;
    WAL_MSG_VOTE = 2;
    WAL_MSG_COMMIT = 3;
    WAL_MSG_END_HEIGHT = 4;
    WAL_MSG_STATE = 5;
    WAL_MSG_TIMEOUT = 6;
}

/// RoundStepType identifies consensus round steps
enum RoundStepType {
    ROUND_STEP_NEW_HEIGHT = 0;
    ROUND_STEP_NEW_ROUND = 1;
    ROUND_STEP_PROPOSE = 2;
    ROUND_STEP_PREVOTE = 3;
    ROUND_STEP_PREVOTE_WAIT = 4;
    ROUND_STEP_PRECOMMIT = 5;
    ROUND_STEP_PRECOMMIT_WAIT = 6;
    ROUND_STEP_COMMIT = 7;
}

/// WALMessage is a write-ahead log entry
message WALMessage {
    type: WALMsgType = 1;
    height: int64 = 2;
    round: int32 = 3;
    /// Serialized message data (type-specific)
    data: bytes = 4;
}

/// EndHeightMessage marks the end of a height in WAL
message EndHeightMessage {
    height: int64 = 1;
}

/// TimeoutMessage records a timeout event
message TimeoutMessage {
    height: int64 = 1;
    round: int32 = 2;
    step: RoundStepType = 3;
}

/// ConsensusStateData is the serializable consensus state for WAL
message ConsensusStateData {
    height: int64 = 1;
    round: int32 = 2;
    step: RoundStepType = 3;
    locked_round: int32 = 4;
    locked_block_hash: Hash = 5;
    valid_round: int32 = 6;
    valid_block_hash: Hash = 7;
}

/// LastSignState tracks the last signed vote for double-sign prevention
message LastSignState {
    height: int64 = 1;
    round: int32 = 2;
    step: int8 = 3;
    block_hash: Hash = 4;
    signature: Signature = 5;
}
```

---

## Phase 2: Core Types Implementation

### 2.1 Type Extensions

After generating cramberry code, implement method extensions:

#### `types/hash.go`

```go
package types

import (
    "bytes"
    "crypto/sha256"
    "encoding/hex"

    gen "github.com/blockberries/leaderberry/types/generated"
)

// Extend generated Hash type
type Hash = gen.Hash

// NewHash creates a Hash from bytes
func NewHash(data []byte) Hash {
    if len(data) != 32 {
        panic("hash must be 32 bytes")
    }
    return Hash{Data: data}
}

// HashBytes computes SHA-256 hash of data
func HashBytes(data []byte) Hash {
    h := sha256.Sum256(data)
    return Hash{Data: h[:]}
}

// IsEmpty returns true if hash is all zeros
func (h Hash) IsEmpty() bool {
    for _, b := range h.Data {
        if b != 0 {
            return false
        }
    }
    return true
}

// Equal compares two hashes
func (h Hash) Equal(other Hash) bool {
    return bytes.Equal(h.Data, other.Data)
}

// String returns hex-encoded hash
func (h Hash) String() string {
    return hex.EncodeToString(h.Data)
}

// Bytes returns the hash bytes
func (h Hash) Bytes() []byte {
    return h.Data
}
```

#### `types/account.go`

```go
package types

import (
    gen "github.com/blockberries/leaderberry/types/generated"
)

type AccountName = gen.AccountName
type Account = gen.Account
type Authority = gen.Authority
type KeyWeight = gen.KeyWeight
type AccountWeight = gen.AccountWeight

// NewAccountName creates an AccountName
func NewAccountName(name string) AccountName {
    return AccountName{Name: name}
}

// String returns the account name string
func (a AccountName) String() string {
    return a.Name
}

// IsEmpty returns true if account name is empty
func (a AccountName) IsEmpty() bool {
    return a.Name == ""
}

// VerifyAuthorization verifies that an authorization satisfies an authority
// visited tracks accounts already checked (for cycle detection)
func VerifyAuthorization(
    auth *gen.Authorization,
    authority *gen.Authority,
    signBytes []byte,
    getAccount func(AccountName) (*Account, error),
    visited map[string]bool,
) (uint32, error) {
    if visited == nil {
        visited = make(map[string]bool)
    }

    var totalWeight uint32

    // Check direct signatures
    for _, sig := range auth.Signatures {
        // Find the key in authority
        for _, kw := range authority.Keys {
            if bytes.Equal(sig.PublicKey.Data, kw.PublicKey.Data) {
                // Verify signature
                if VerifySignature(kw.PublicKey, signBytes, sig.Signature) {
                    totalWeight += kw.Weight
                }
                break
            }
        }
    }

    // Check delegated authorizations
    for _, accAuth := range auth.AccountAuthorizations {
        accName := accAuth.Account.Name

        // Cycle detection
        if visited[accName] {
            continue
        }
        visited[accName] = true

        // Find the account in authority
        for _, aw := range authority.Accounts {
            if aw.Account.Name == accName {
                // Get the delegating account
                acc, err := getAccount(aw.Account)
                if err != nil {
                    continue
                }

                // Recursively verify
                weight, err := VerifyAuthorization(
                    accAuth.Authorization,
                    acc.Authority,
                    signBytes,
                    getAccount,
                    visited,
                )
                if err != nil {
                    continue
                }

                // If delegation is satisfied, add weight
                if weight >= acc.Authority.Threshold {
                    totalWeight += aw.Weight
                }
                break
            }
        }
    }

    return totalWeight, nil
}
```

#### `types/validator.go`

```go
package types

import (
    "sort"
    "sync"

    gen "github.com/blockberries/leaderberry/types/generated"
)

type NamedValidator = gen.NamedValidator

// ValidatorSet manages a set of named validators
type ValidatorSet struct {
    mu         sync.RWMutex
    validators []*NamedValidator
    byName     map[string]*NamedValidator
    byPubKey   map[string]*NamedValidator
    byIndex    map[uint16]*NamedValidator
    proposer   *NamedValidator
    totalPower int64
}

// NewValidatorSet creates a new validator set
func NewValidatorSet(vals []*NamedValidator) *ValidatorSet {
    vs := &ValidatorSet{
        validators: make([]*NamedValidator, len(vals)),
        byName:     make(map[string]*NamedValidator),
        byPubKey:   make(map[string]*NamedValidator),
        byIndex:    make(map[uint16]*NamedValidator),
    }

    for i, v := range vals {
        vs.validators[i] = v
        vs.byName[v.Name.Name] = v
        vs.byPubKey[string(v.PublicKey.Data)] = v
        vs.byIndex[v.Index] = v
        vs.totalPower += v.VotingPower
        v.ProposerPriority = v.VotingPower
    }

    // Sort by index
    sort.Slice(vs.validators, func(i, j int) bool {
        return vs.validators[i].Index < vs.validators[j].Index
    })

    // Initialize proposer
    vs.incrementProposerPriority()

    return vs
}

// Count returns the number of validators
func (vs *ValidatorSet) Count() int {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return len(vs.validators)
}

// GetByName returns a validator by name
func (vs *ValidatorSet) GetByName(name AccountName) *NamedValidator {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return vs.byName[name.Name]
}

// GetByIndex returns a validator by index
func (vs *ValidatorSet) GetByIndex(index uint16) *NamedValidator {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return vs.byIndex[index]
}

// GetByPubKey returns a validator by public key
func (vs *ValidatorSet) GetByPubKey(pubKey []byte) *NamedValidator {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return vs.byPubKey[string(pubKey)]
}

// TotalVotingPower returns total voting power
func (vs *ValidatorSet) TotalVotingPower() int64 {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return vs.totalPower
}

// Quorum returns 2f+1
func (vs *ValidatorSet) Quorum() int64 {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return vs.totalPower*2/3 + 1
}

// F returns maximum Byzantine validators
func (vs *ValidatorSet) F() int {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return (len(vs.validators) - 1) / 3
}

// GetProposer returns the proposer for a height/round
func (vs *ValidatorSet) GetProposer(height int64, round int32) *NamedValidator {
    vs.mu.Lock()
    defer vs.mu.Unlock()

    if round == 0 {
        return vs.proposer
    }

    // Copy and advance
    vals := vs.Copy()
    for i := int32(0); i < round; i++ {
        vals.incrementProposerPriority()
    }
    return vals.proposer
}

func (vs *ValidatorSet) incrementProposerPriority() {
    // Add voting power to priorities
    for _, v := range vs.validators {
        v.ProposerPriority += v.VotingPower
    }

    // Find max priority
    var maxPriority int64 = -1 << 62
    for _, v := range vs.validators {
        if v.ProposerPriority > maxPriority {
            maxPriority = v.ProposerPriority
            vs.proposer = v
        }
    }

    // Subtract total from proposer
    vs.proposer.ProposerPriority -= vs.totalPower
}

// Copy creates a deep copy
func (vs *ValidatorSet) Copy() *ValidatorSet {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    vals := make([]*NamedValidator, len(vs.validators))
    for i, v := range vs.validators {
        vCopy := *v
        vals[i] = &vCopy
    }
    return NewValidatorSet(vals)
}

// IncrementProposerPriority advances the proposer
func (vs *ValidatorSet) IncrementProposerPriority(times int32) {
    vs.mu.Lock()
    defer vs.mu.Unlock()

    for i := int32(0); i < times; i++ {
        vs.incrementProposerPriority()
    }
}

// Validators returns all validators
func (vs *ValidatorSet) Validators() []*NamedValidator {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    result := make([]*NamedValidator, len(vs.validators))
    copy(result, vs.validators)
    return result
}

// VerifyCommit verifies a commit has 2/3+ valid signatures
func (vs *ValidatorSet) VerifyCommit(height int64, blockHash Hash, commit *gen.Commit) error {
    // Implementation
}
```

### 2.2 Implementation Order for Phase 2

1. **Generate cramberry code** - `make generate`
2. **Implement `types/hash.go`** - Hash utilities
3. **Implement `types/signature.go`** - Signature verification
4. **Implement `types/account.go`** - Account and authorization
5. **Implement `types/validator.go`** - ValidatorSet
6. **Implement `types/vote.go`** - Vote signing and verification
7. **Implement `types/block.go`** - Block hashing
8. **Implement `types/proposal.go`** - Proposal methods
9. **Write tests** for each type

---

## Phase 3: Vote Tracking

### 3.1 Files to Implement

#### `engine/vote_tracker.go`

```go
package engine

import (
    "sync"

    "github.com/blockberries/leaderberry/types"
    gen "github.com/blockberries/leaderberry/types/generated"
)

// VoteSet tracks votes for a single round
type VoteSet struct {
    mu           sync.RWMutex
    height       int64
    round        int32
    voteType     gen.VoteType
    validatorSet *types.ValidatorSet

    votes        map[uint16]*gen.Vote  // by validator index
    votesByBlock map[string]*blockVotes
    sum          int64
    maj23        *blockVotes
}

type blockVotes struct {
    blockHash  types.Hash
    votes      []*gen.Vote
    totalPower int64
}

// NewVoteSet creates a new VoteSet
func NewVoteSet(height int64, round int32, voteType gen.VoteType, valSet *types.ValidatorSet) *VoteSet {
    return &VoteSet{
        height:       height,
        round:        round,
        voteType:     voteType,
        validatorSet: valSet,
        votes:        make(map[uint16]*gen.Vote),
        votesByBlock: make(map[string]*blockVotes),
    }
}

// AddVote adds a vote to the set
func (vs *VoteSet) AddVote(vote *gen.Vote) (added bool, err error) {
    vs.mu.Lock()
    defer vs.mu.Unlock()

    // Validate vote
    if vote.Height != vs.height || vote.Round != vs.round || vote.Type != vs.voteType {
        return false, ErrInvalidVote
    }

    // Check validator
    val := vs.validatorSet.GetByIndex(vote.ValidatorIndex)
    if val == nil {
        return false, ErrUnknownValidator
    }

    // Verify signature
    signBytes := vote.SignBytes()
    if !types.VerifySignature(val.PublicKey, signBytes, vote.Signature) {
        return false, ErrInvalidSignature
    }

    // Check for duplicate
    if existing, ok := vs.votes[vote.ValidatorIndex]; ok {
        if existing.BlockHash.Equal(vote.BlockHash) {
            return false, nil // Duplicate
        }
        return false, ErrConflictingVote // Equivocation!
    }

    // Add vote
    vs.votes[vote.ValidatorIndex] = vote
    vs.sum += val.VotingPower

    // Track by block hash
    key := string(vote.BlockHash.Data)
    bv, ok := vs.votesByBlock[key]
    if !ok {
        bv = &blockVotes{blockHash: vote.BlockHash}
        vs.votesByBlock[key] = bv
    }
    bv.votes = append(bv.votes, vote)
    bv.totalPower += val.VotingPower

    // Check for 2/3+
    quorum := vs.validatorSet.Quorum()
    if bv.totalPower >= quorum && vs.maj23 == nil {
        vs.maj23 = bv
    }

    return true, nil
}

// TwoThirdsMajority returns the block hash with 2/3+ votes
func (vs *VoteSet) TwoThirdsMajority() (types.Hash, bool) {
    vs.mu.RLock()
    defer vs.mu.RUnlock()

    if vs.maj23 != nil {
        return vs.maj23.blockHash, true
    }
    return types.Hash{}, false
}

// HasTwoThirdsAny returns true if any block has 2/3+
func (vs *VoteSet) HasTwoThirdsAny() bool {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return vs.maj23 != nil
}

// HasAll returns true if all validators have voted
func (vs *VoteSet) HasAll() bool {
    vs.mu.RLock()
    defer vs.mu.RUnlock()
    return len(vs.votes) == vs.validatorSet.Count()
}

// HeightVoteSet tracks all votes for a height across rounds
type HeightVoteSet struct {
    mu           sync.RWMutex
    height       int64
    validatorSet *types.ValidatorSet

    prevotes   map[int32]*VoteSet
    precommits map[int32]*VoteSet
}

// NewHeightVoteSet creates a HeightVoteSet
func NewHeightVoteSet(height int64, valSet *types.ValidatorSet) *HeightVoteSet {
    return &HeightVoteSet{
        height:       height,
        validatorSet: valSet,
        prevotes:     make(map[int32]*VoteSet),
        precommits:   make(map[int32]*VoteSet),
    }
}

// AddVote adds a vote to the appropriate VoteSet
func (hvs *HeightVoteSet) AddVote(vote *gen.Vote) (added bool, err error) {
    hvs.mu.Lock()

    var voteSet *VoteSet
    if vote.Type == gen.VoteType_VOTE_TYPE_PREVOTE {
        voteSet = hvs.prevotes[vote.Round]
        if voteSet == nil {
            voteSet = NewVoteSet(hvs.height, vote.Round, gen.VoteType_VOTE_TYPE_PREVOTE, hvs.validatorSet)
            hvs.prevotes[vote.Round] = voteSet
        }
    } else {
        voteSet = hvs.precommits[vote.Round]
        if voteSet == nil {
            voteSet = NewVoteSet(hvs.height, vote.Round, gen.VoteType_VOTE_TYPE_PRECOMMIT, hvs.validatorSet)
            hvs.precommits[vote.Round] = voteSet
        }
    }

    hvs.mu.Unlock()
    return voteSet.AddVote(vote)
}

// Prevotes returns the prevote set for a round
func (hvs *HeightVoteSet) Prevotes(round int32) *VoteSet {
    hvs.mu.RLock()
    defer hvs.mu.RUnlock()
    return hvs.prevotes[round]
}

// Precommits returns the precommit set for a round
func (hvs *HeightVoteSet) Precommits(round int32) *VoteSet {
    hvs.mu.RLock()
    defer hvs.mu.RUnlock()
    return hvs.precommits[round]
}
```

---

## Phase 4: Consensus State Machine

### 4.1 Core State Machine

#### `engine/state.go`

```go
package engine

import (
    "context"
    "sync"
    "time"

    "github.com/blockberries/leaderberry/types"
    gen "github.com/blockberries/leaderberry/types/generated"
)

// RoundStep represents the current step in a consensus round
type RoundStep = gen.RoundStepType

const (
    RoundStepNewHeight     = gen.RoundStepType_ROUND_STEP_NEW_HEIGHT
    RoundStepNewRound      = gen.RoundStepType_ROUND_STEP_NEW_ROUND
    RoundStepPropose       = gen.RoundStepType_ROUND_STEP_PROPOSE
    RoundStepPrevote       = gen.RoundStepType_ROUND_STEP_PREVOTE
    RoundStepPrevoteWait   = gen.RoundStepType_ROUND_STEP_PREVOTE_WAIT
    RoundStepPrecommit     = gen.RoundStepType_ROUND_STEP_PRECOMMIT
    RoundStepPrecommitWait = gen.RoundStepType_ROUND_STEP_PRECOMMIT_WAIT
    RoundStepCommit        = gen.RoundStepType_ROUND_STEP_COMMIT
)

// ConsensusState manages the consensus state machine
type ConsensusState struct {
    mu sync.RWMutex

    // Immutable
    config       *Config
    validatorSet *types.ValidatorSet
    privVal      PrivValidator
    wal          WAL

    // Current state
    height int64
    round  int32
    step   RoundStep

    // Proposal
    proposal      *gen.Proposal
    proposalBlock *gen.Block

    // Locking
    lockedRound     int32
    lockedBlock     *gen.Block
    lockedBlockHash types.Hash

    // Valid block (received valid proposal)
    validRound     int32
    validBlock     *gen.Block
    validBlockHash types.Hash

    // Votes
    votes *HeightVoteSet

    // Commit
    lastCommit *gen.Commit

    // Timeouts
    timeoutScheduler *TimeoutScheduler

    // Channels
    proposalCh  chan *gen.Proposal
    voteCh      chan *gen.Vote
    commitCh    chan *gen.Commit
    timeoutCh   chan timeoutInfo

    // Control
    ctx    context.Context
    cancel context.CancelFunc
}

// NewConsensusState creates a new ConsensusState
func NewConsensusState(
    config *Config,
    valSet *types.ValidatorSet,
    privVal PrivValidator,
    wal WAL,
) *ConsensusState {
    ctx, cancel := context.WithCancel(context.Background())

    return &ConsensusState{
        config:           config,
        validatorSet:     valSet,
        privVal:          privVal,
        wal:              wal,
        timeoutScheduler: NewTimeoutScheduler(config),
        proposalCh:       make(chan *gen.Proposal, 100),
        voteCh:           make(chan *gen.Vote, 1000),
        commitCh:         make(chan *gen.Commit, 10),
        timeoutCh:        make(chan timeoutInfo, 10),
        ctx:              ctx,
        cancel:           cancel,
    }
}

// Start begins the consensus state machine
func (cs *ConsensusState) Start(height int64, lastCommit *gen.Commit) error {
    cs.mu.Lock()
    cs.height = height
    cs.lastCommit = lastCommit
    cs.mu.Unlock()

    go cs.receiveRoutine()
    cs.enterNewHeight(height)
    return nil
}

// Stop halts the consensus state machine
func (cs *ConsensusState) Stop() {
    cs.cancel()
}

// receiveRoutine handles incoming messages
func (cs *ConsensusState) receiveRoutine() {
    for {
        select {
        case <-cs.ctx.Done():
            return
        case proposal := <-cs.proposalCh:
            cs.handleProposal(proposal)
        case vote := <-cs.voteCh:
            cs.handleVote(vote)
        case commit := <-cs.commitCh:
            cs.handleCommit(commit)
        case ti := <-cs.timeoutCh:
            cs.handleTimeout(ti)
        }
    }
}

// enterNewHeight transitions to a new height
func (cs *ConsensusState) enterNewHeight(height int64) {
    cs.mu.Lock()
    defer cs.mu.Unlock()

    cs.height = height
    cs.round = 0
    cs.step = RoundStepNewHeight
    cs.proposal = nil
    cs.proposalBlock = nil
    cs.lockedRound = -1
    cs.lockedBlock = nil
    cs.lockedBlockHash = types.Hash{}
    cs.validRound = -1
    cs.validBlock = nil
    cs.validBlockHash = types.Hash{}
    cs.votes = NewHeightVoteSet(height, cs.validatorSet)

    cs.enterNewRound(height, 0)
}

// enterNewRound transitions to a new round
func (cs *ConsensusState) enterNewRound(height int64, round int32) {
    // Must already hold lock
    if cs.height != height || round < cs.round {
        return
    }

    cs.round = round
    cs.step = RoundStepNewRound
    cs.proposal = nil
    cs.proposalBlock = nil

    // Write to WAL
    cs.walWrite(gen.WALMsgType_WAL_MSG_STATE, cs.stateData())

    cs.enterPropose(height, round)
}

// enterPropose transitions to propose step
func (cs *ConsensusState) enterPropose(height int64, round int32) {
    cs.step = RoundStepPropose

    // Schedule propose timeout
    cs.timeoutScheduler.ScheduleTimeout(
        cs.config.ProposeTimeout(round),
        height, round, RoundStepPropose,
    )

    // Are we the proposer?
    proposer := cs.validatorSet.GetProposer(height, round)
    if proposer.Name.Name == cs.privVal.GetValidatorName().Name {
        cs.doPropose(height, round)
    }
}

// doPropose creates and broadcasts a proposal
func (cs *ConsensusState) doPropose(height int64, round int32) {
    var block *gen.Block
    var polRound int32 = -1
    var polVotes []*gen.Vote

    // If locked, propose locked block
    if cs.lockedBlock != nil {
        block = cs.lockedBlock
        polRound = cs.lockedRound
        polVotes = cs.votes.Prevotes(cs.lockedRound).VotesForBlock(cs.lockedBlockHash)
    } else if cs.validBlock != nil {
        block = cs.validBlock
        polRound = cs.validRound
        polVotes = cs.votes.Prevotes(cs.validRound).VotesForBlock(cs.validBlockHash)
    } else {
        // Build new block
        var err error
        block, err = cs.createBlock(height)
        if err != nil {
            return
        }
    }

    // Create and sign proposal
    proposal := &gen.Proposal{
        Height:    height,
        Round:     round,
        Timestamp: time.Now().UnixNano(),
        Block:     block,
        PolRound:  polRound,
        PolVotes:  polVotes,
        Proposer:  cs.privVal.GetValidatorName(),
    }

    if err := cs.privVal.SignProposal(proposal); err != nil {
        return
    }

    // Write to WAL
    cs.walWrite(gen.WALMsgType_WAL_MSG_PROPOSAL, proposal)

    // Self-deliver
    cs.proposalCh <- proposal
}

// handleProposal processes an incoming proposal
func (cs *ConsensusState) handleProposal(proposal *gen.Proposal) {
    cs.mu.Lock()
    defer cs.mu.Unlock()

    if proposal.Height != cs.height || proposal.Round != cs.round {
        return
    }
    if cs.proposal != nil {
        return // Already have proposal
    }

    // Verify proposer
    expectedProposer := cs.validatorSet.GetProposer(cs.height, cs.round)
    if proposal.Proposer.Name != expectedProposer.Name.Name {
        return
    }

    // Verify signature
    if !cs.verifyProposalSignature(proposal) {
        return
    }

    // Verify POL if present
    if proposal.PolRound >= 0 {
        if !cs.verifyPOL(proposal.PolVotes, proposal.Block.Hash(), proposal.PolRound) {
            return
        }
    }

    cs.proposal = proposal
    cs.proposalBlock = proposal.Block

    // Move to prevote
    cs.enterPrevote(cs.height, cs.round)
}

// enterPrevote transitions to prevote step
func (cs *ConsensusState) enterPrevote(height int64, round int32) {
    cs.step = RoundStepPrevote

    // Decide what to prevote
    var blockHash types.Hash

    if cs.lockedBlock != nil {
        // If locked, only prevote locked block or nil
        if cs.proposal != nil && cs.lockedBlockHash.Equal(types.HashBlock(cs.proposalBlock)) {
            blockHash = cs.lockedBlockHash
        } else if cs.canUnlock(cs.proposal) {
            blockHash = types.HashBlock(cs.proposalBlock)
        }
        // else prevote nil (empty hash)
    } else if cs.proposal != nil && cs.isValidProposal(cs.proposal) {
        blockHash = types.HashBlock(cs.proposalBlock)
    }
    // else prevote nil

    cs.signAndSendVote(gen.VoteType_VOTE_TYPE_PREVOTE, blockHash)
}

func (cs *ConsensusState) canUnlock(proposal *gen.Proposal) bool {
    if proposal == nil || proposal.PolRound < 0 {
        return false
    }
    return proposal.PolRound > cs.lockedRound &&
           cs.verifyPOL(proposal.PolVotes, types.HashBlock(proposal.Block), proposal.PolRound)
}

// handleVote processes an incoming vote
func (cs *ConsensusState) handleVote(vote *gen.Vote) {
    cs.mu.Lock()
    defer cs.mu.Unlock()

    if vote.Height != cs.height {
        return
    }

    added, err := cs.votes.AddVote(vote)
    if err != nil || !added {
        return
    }

    // Write to WAL
    cs.walWrite(gen.WALMsgType_WAL_MSG_VOTE, vote)

    // Check for state transitions based on votes
    switch vote.Type {
    case gen.VoteType_VOTE_TYPE_PREVOTE:
        cs.checkPrevoteQuorum(vote.Round)
    case gen.VoteType_VOTE_TYPE_PRECOMMIT:
        cs.checkPrecommitQuorum(vote.Round)
    }
}

func (cs *ConsensusState) checkPrevoteQuorum(round int32) {
    prevotes := cs.votes.Prevotes(round)
    if prevotes == nil {
        return
    }

    blockHash, ok := prevotes.TwoThirdsMajority()
    if !ok {
        return
    }

    // Lock on the block
    if !blockHash.IsEmpty() {
        if cs.proposalBlock != nil && blockHash.Equal(types.HashBlock(cs.proposalBlock)) {
            cs.lockedRound = round
            cs.lockedBlock = cs.proposalBlock
            cs.lockedBlockHash = blockHash
        }
    }

    if round == cs.round && cs.step == RoundStepPrevote {
        cs.enterPrecommit(cs.height, cs.round)
    }
}

// enterPrecommit transitions to precommit step
func (cs *ConsensusState) enterPrecommit(height int64, round int32) {
    cs.step = RoundStepPrecommit

    prevotes := cs.votes.Prevotes(round)
    blockHash, ok := prevotes.TwoThirdsMajority()

    if !ok {
        // No 2/3+ for anything, precommit nil
        cs.signAndSendVote(gen.VoteType_VOTE_TYPE_PRECOMMIT, types.Hash{})
        return
    }

    if blockHash.IsEmpty() {
        // 2/3+ for nil
        cs.signAndSendVote(gen.VoteType_VOTE_TYPE_PRECOMMIT, types.Hash{})
        return
    }

    // 2/3+ for a block
    cs.signAndSendVote(gen.VoteType_VOTE_TYPE_PRECOMMIT, blockHash)
}

func (cs *ConsensusState) checkPrecommitQuorum(round int32) {
    precommits := cs.votes.Precommits(round)
    if precommits == nil {
        return
    }

    blockHash, ok := precommits.TwoThirdsMajority()
    if !ok {
        return
    }

    if !blockHash.IsEmpty() {
        // Commit!
        cs.enterCommit(cs.height, round, blockHash)
    } else if round == cs.round {
        // 2/3+ nil, go to next round
        cs.enterNewRound(cs.height, cs.round+1)
    }
}

// enterCommit transitions to commit step
func (cs *ConsensusState) enterCommit(height int64, round int32, blockHash types.Hash) {
    cs.step = RoundStepCommit

    // Build commit from precommits
    precommits := cs.votes.Precommits(round)
    commit := cs.buildCommit(precommits, blockHash)

    // Write to WAL
    cs.walWrite(gen.WALMsgType_WAL_MSG_COMMIT, commit)
    cs.walWrite(gen.WALMsgType_WAL_MSG_END_HEIGHT, &gen.EndHeightMessage{Height: height})

    // Notify engine
    cs.commitCh <- commit
}

func (cs *ConsensusState) signAndSendVote(voteType gen.VoteType, blockHash types.Hash) {
    vote := &gen.Vote{
        Type:           voteType,
        Height:         cs.height,
        Round:          cs.round,
        BlockHash:      &blockHash,
        Timestamp:      time.Now().UnixNano(),
        Validator:      cs.privVal.GetValidatorName(),
        ValidatorIndex: cs.privVal.GetValidatorIndex(),
    }

    if err := cs.privVal.SignVote(vote); err != nil {
        return
    }

    // Self-deliver
    cs.voteCh <- vote

    // Broadcast (via engine)
}

// Additional helper methods...
```

---

## Phase 5: WAL Implementation

### 5.1 WAL Interface and File Implementation

#### `wal/wal.go`

```go
package wal

import (
    gen "github.com/blockberries/leaderberry/types/generated"
)

// WAL is the write-ahead log interface
type WAL interface {
    Write(msg *gen.WALMessage) error
    WriteSync(msg *gen.WALMessage) error
    SearchForEndHeight(height int64) (WALReader, error)
    Start() error
    Stop() error
    Flush() error
}

// WALReader reads WAL messages
type WALReader interface {
    Decode() (*gen.WALMessage, error)
    Close() error
}
```

#### `wal/file_wal.go`

```go
package wal

import (
    "bufio"
    "io"
    "os"
    "path/filepath"
    "sync"

    "github.com/blockberries/cramberry"
    gen "github.com/blockberries/leaderberry/types/generated"
)

// FileWAL implements WAL using files
type FileWAL struct {
    mu       sync.Mutex
    dir      string
    file     *os.File
    writer   *bufio.Writer
    height   int64
    encoder  func(msg *gen.WALMessage) ([]byte, error)
}

// NewFileWAL creates a new FileWAL
func NewFileWAL(dir string) (*FileWAL, error) {
    if err := os.MkdirAll(dir, 0755); err != nil {
        return nil, err
    }

    return &FileWAL{
        dir:     dir,
        encoder: cramberry.Marshal,
    }, nil
}

func (w *FileWAL) Start() error {
    return w.openFile()
}

func (w *FileWAL) openFile() error {
    path := filepath.Join(w.dir, "wal")
    f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
    if err != nil {
        return err
    }
    w.file = f
    w.writer = bufio.NewWriter(f)
    return nil
}

func (w *FileWAL) Write(msg *gen.WALMessage) error {
    w.mu.Lock()
    defer w.mu.Unlock()

    data, err := w.encoder(msg)
    if err != nil {
        return err
    }

    // Write length-prefixed
    if err := writeDelimited(w.writer, data); err != nil {
        return err
    }

    return nil
}

func (w *FileWAL) WriteSync(msg *gen.WALMessage) error {
    if err := w.Write(msg); err != nil {
        return err
    }
    return w.Flush()
}

func (w *FileWAL) Flush() error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if err := w.writer.Flush(); err != nil {
        return err
    }
    return w.file.Sync()
}

func (w *FileWAL) Stop() error {
    w.mu.Lock()
    defer w.mu.Unlock()

    if w.file != nil {
        w.writer.Flush()
        return w.file.Close()
    }
    return nil
}

func (w *FileWAL) SearchForEndHeight(height int64) (WALReader, error) {
    path := filepath.Join(w.dir, "wal")
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }

    return &fileWALReader{
        file:         f,
        reader:       bufio.NewReader(f),
        targetHeight: height,
    }, nil
}

type fileWALReader struct {
    file         *os.File
    reader       *bufio.Reader
    targetHeight int64
}

func (r *fileWALReader) Decode() (*gen.WALMessage, error) {
    data, err := readDelimited(r.reader)
    if err != nil {
        return nil, err
    }

    var msg gen.WALMessage
    if err := cramberry.Unmarshal(data, &msg); err != nil {
        return nil, err
    }

    return &msg, nil
}

func (r *fileWALReader) Close() error {
    return r.file.Close()
}
```

---

## Phase 6: Private Validator

### 6.1 PrivValidator Implementation

#### `privval/signer.go`

```go
package privval

import (
    "crypto/ed25519"

    "github.com/blockberries/leaderberry/types"
    gen "github.com/blockberries/leaderberry/types/generated"
)

// PrivValidator signs consensus messages
type PrivValidator interface {
    GetValidatorName() types.AccountName
    GetValidatorIndex() uint16
    GetPublicKey() types.PublicKey
    SignVote(vote *gen.Vote) error
    SignProposal(proposal *gen.Proposal) error
}
```

#### `privval/file_pv.go`

```go
package privval

import (
    "bytes"
    "crypto/ed25519"
    "encoding/json"
    "os"
    "sync"

    "github.com/blockberries/leaderberry/types"
    gen "github.com/blockberries/leaderberry/types/generated"
)

// FilePV is a file-backed PrivValidator
type FilePV struct {
    mu            sync.Mutex
    key           ed25519.PrivateKey
    validatorName types.AccountName
    validatorIdx  uint16
    lastSignState *gen.LastSignState
    stateFile     string
}

// NewFilePV creates a FilePV from a key file
func NewFilePV(keyFile, stateFile string) (*FilePV, error) {
    // Load key
    keyData, err := os.ReadFile(keyFile)
    if err != nil {
        return nil, err
    }

    var keyInfo struct {
        PrivKey       []byte `json:"priv_key"`
        ValidatorName string `json:"validator_name"`
        ValidatorIdx  uint16 `json:"validator_index"`
    }
    if err := json.Unmarshal(keyData, &keyInfo); err != nil {
        return nil, err
    }

    pv := &FilePV{
        key:           ed25519.PrivateKey(keyInfo.PrivKey),
        validatorName: types.NewAccountName(keyInfo.ValidatorName),
        validatorIdx:  keyInfo.ValidatorIdx,
        stateFile:     stateFile,
    }

    // Load state
    if err := pv.loadState(); err != nil {
        pv.lastSignState = &gen.LastSignState{}
    }

    return pv, nil
}

func (pv *FilePV) GetValidatorName() types.AccountName {
    return pv.validatorName
}

func (pv *FilePV) GetValidatorIndex() uint16 {
    return pv.validatorIdx
}

func (pv *FilePV) GetPublicKey() types.PublicKey {
    return types.PublicKey{Data: pv.key.Public().(ed25519.PublicKey)}
}

func (pv *FilePV) SignVote(vote *gen.Vote) error {
    pv.mu.Lock()
    defer pv.mu.Unlock()

    lss := pv.lastSignState
    step := int8(vote.Type)

    // Check for regression
    if vote.Height < lss.Height {
        return ErrVoteHeightTooLow
    }
    if vote.Height == lss.Height && vote.Round < lss.Round {
        return ErrVoteRoundTooLow
    }
    if vote.Height == lss.Height && vote.Round == lss.Round && step < lss.Step {
        return ErrVoteStepTooLow
    }

    // Check for double-sign
    if vote.Height == lss.Height && vote.Round == lss.Round && step == lss.Step {
        if bytes.Equal(vote.BlockHash.Data, lss.BlockHash.Data) {
            // Same vote - return cached signature
            vote.Signature = lss.Signature
            return nil
        }
        return ErrDoubleSign
    }

    // Sign
    signBytes := types.VoteSignBytes(vote)
    sig := ed25519.Sign(pv.key, signBytes)
    vote.Signature = &types.Signature{Data: sig}

    // Update state
    pv.lastSignState = &gen.LastSignState{
        Height:    vote.Height,
        Round:     vote.Round,
        Step:      step,
        BlockHash: vote.BlockHash,
        Signature: vote.Signature,
    }

    return pv.saveState()
}

func (pv *FilePV) SignProposal(proposal *gen.Proposal) error {
    pv.mu.Lock()
    defer pv.mu.Unlock()

    signBytes := types.ProposalSignBytes(proposal)
    sig := ed25519.Sign(pv.key, signBytes)
    proposal.Signature = &types.Signature{Data: sig}

    return nil
}

func (pv *FilePV) loadState() error {
    data, err := os.ReadFile(pv.stateFile)
    if err != nil {
        return err
    }

    var state gen.LastSignState
    if err := json.Unmarshal(data, &state); err != nil {
        return err
    }

    pv.lastSignState = &state
    return nil
}

func (pv *FilePV) saveState() error {
    data, err := json.MarshalIndent(pv.lastSignState, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(pv.stateFile, data, 0600)
}
```

---

## Phase 7: Evidence Pool

### 7.1 Evidence Implementation

#### `evidence/pool.go`

```go
package evidence

import (
    "sync"

    "github.com/blockberries/leaderberry/types"
    gen "github.com/blockberries/leaderberry/types/generated"
)

// Pool manages Byzantine evidence
type Pool struct {
    mu       sync.RWMutex
    evidence []gen.Evidence
    seen     map[string]*gen.Vote // key: validator/height/round/type
}

// NewPool creates a new evidence pool
func NewPool() *Pool {
    return &Pool{
        seen: make(map[string]*gen.Vote),
    }
}

// CheckVote checks a vote for equivocation
func (ep *Pool) CheckVote(vote *gen.Vote) (*gen.DuplicateVoteEvidence, bool) {
    ep.mu.Lock()
    defer ep.mu.Unlock()

    key := voteKey(vote)

    if existing, ok := ep.seen[key]; ok {
        if !types.HashEqual(existing.BlockHash, vote.BlockHash) {
            // Equivocation!
            evidence := &gen.DuplicateVoteEvidence{
                VoteA: existing,
                VoteB: vote,
            }
            return evidence, true
        }
        return nil, false
    }

    ep.seen[key] = vote
    return nil, false
}

// AddEvidence adds verified evidence
func (ep *Pool) AddEvidence(ev *gen.Evidence) error {
    ep.mu.Lock()
    defer ep.mu.Unlock()

    ep.evidence = append(ep.evidence, *ev)
    return nil
}

// PendingEvidence returns evidence to include in blocks
func (ep *Pool) PendingEvidence(maxBytes int64) []gen.Evidence {
    ep.mu.RLock()
    defer ep.mu.RUnlock()

    // Return evidence that fits
    var result []gen.Evidence
    var size int64
    for _, ev := range ep.evidence {
        evSize := int64(len(ev.Data) + 50) // Estimate
        if size+evSize > maxBytes {
            break
        }
        result = append(result, ev)
        size += evSize
    }
    return result
}

func voteKey(vote *gen.Vote) string {
    return fmt.Sprintf("%s/%d/%d/%d",
        vote.Validator.Name, vote.Height, vote.Round, vote.Type)
}
```

---

## Phase 8: Main Engine Implementation

### 8.1 Engine (BFTConsensus Implementation)

#### `engine/engine.go`

```go
package engine

import (
    "context"
    "sync"

    "github.com/blockberries/blockberry/abi"
    "github.com/blockberries/blockberry/consensus"
    "github.com/blockberries/looseberry"

    "github.com/blockberries/leaderberry/evidence"
    "github.com/blockberries/leaderberry/privval"
    "github.com/blockberries/leaderberry/types"
    "github.com/blockberries/leaderberry/wal"
    gen "github.com/blockberries/leaderberry/types/generated"
)

// Engine implements consensus.BFTConsensus
type Engine struct {
    mu sync.RWMutex

    // Configuration
    config *Config

    // Dependencies (from blockberry)
    deps       consensus.ConsensusDependencies
    network    consensus.Network
    app        abi.Application

    // Looseberry integration
    dagMempool looseberry.DAGMempool

    // Consensus components
    state        *ConsensusState
    validatorSet *types.ValidatorSet
    privVal      privval.PrivValidator
    wal          wal.WAL
    evidencePool *evidence.Pool

    // State
    height int64
    round  int32

    // Lifecycle
    running bool
    ctx     context.Context
    cancel  context.CancelFunc
}

// NewEngine creates a new leaderberry consensus engine
func NewEngine(config *Config) *Engine {
    return &Engine{
        config:       config,
        evidencePool: evidence.NewPool(),
    }
}

// Name implements Named
func (e *Engine) Name() string {
    return "leaderberry"
}

// Initialize implements ConsensusEngine
func (e *Engine) Initialize(deps consensus.ConsensusDependencies) error {
    e.mu.Lock()
    defer e.mu.Unlock()

    e.deps = deps
    e.network = deps.Network
    e.app = deps.Application

    // Initialize WAL
    var err error
    e.wal, err = wal.NewFileWAL(e.config.WALDir)
    if err != nil {
        return err
    }

    // Initialize private validator
    e.privVal, err = privval.NewFilePV(
        e.config.PrivValidatorKey,
        e.config.PrivValidatorState,
    )
    if err != nil {
        return err
    }

    // Initialize validator set from genesis or state
    e.validatorSet = e.loadValidatorSet()

    return nil
}

// Start implements Component
func (e *Engine) Start() error {
    e.mu.Lock()
    defer e.mu.Unlock()

    if e.running {
        return ErrAlreadyRunning
    }

    e.ctx, e.cancel = context.WithCancel(context.Background())
    e.running = true

    // Start WAL
    if err := e.wal.Start(); err != nil {
        return err
    }

    // Replay WAL if needed
    height, lastCommit := e.replayWAL()

    // Initialize consensus state
    e.state = NewConsensusState(
        e.config,
        e.validatorSet,
        e.privVal,
        e.wal,
    )

    // Start consensus
    return e.state.Start(height, lastCommit)
}

// Stop implements Component
func (e *Engine) Stop() error {
    e.mu.Lock()
    defer e.mu.Unlock()

    if !e.running {
        return nil
    }

    e.cancel()
    e.state.Stop()
    e.wal.Stop()
    e.running = false

    return nil
}

// IsRunning implements Component
func (e *Engine) IsRunning() bool {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.running
}

// GetHeight implements ConsensusEngine
func (e *Engine) GetHeight() int64 {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.height
}

// GetRound implements ConsensusEngine
func (e *Engine) GetRound() int32 {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.round
}

// IsValidator implements ConsensusEngine
func (e *Engine) IsValidator() bool {
    return e.privVal != nil
}

// ValidatorSet implements ConsensusEngine
func (e *Engine) ValidatorSet() consensus.ValidatorSet {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.validatorSet
}

// ProcessBlock implements ConsensusEngine
func (e *Engine) ProcessBlock(block *consensus.Block) error {
    // Convert and execute block
    return e.executeBlock(block)
}

// ProduceBlock implements BlockProducer
func (e *Engine) ProduceBlock(height int64) (*consensus.Block, error) {
    e.mu.Lock()
    defer e.mu.Unlock()

    return e.buildBlockFromDAG(height)
}

// ShouldPropose implements BlockProducer
func (e *Engine) ShouldPropose(height int64, round int32) bool {
    proposer := e.validatorSet.GetProposer(height, round)
    return proposer.Name.Name == e.privVal.GetValidatorName().Name
}

// HandleProposal implements BFTConsensus
func (e *Engine) HandleProposal(proposal *consensus.Proposal) error {
    // Convert and forward to state machine
    p := e.convertProposal(proposal)
    e.state.proposalCh <- p
    return nil
}

// HandleVote implements BFTConsensus
func (e *Engine) HandleVote(vote *consensus.Vote) error {
    // Check for equivocation
    v := e.convertVote(vote)
    if ev, found := e.evidencePool.CheckVote(v); found {
        e.handleEquivocation(ev)
    }

    e.state.voteCh <- v
    return nil
}

// HandleCommit implements BFTConsensus
func (e *Engine) HandleCommit(commit *consensus.Commit) error {
    c := e.convertCommit(commit)
    e.state.commitCh <- c
    return nil
}

// OnTimeout implements BFTConsensus
func (e *Engine) OnTimeout(height int64, round int32, step consensus.TimeoutStep) error {
    e.state.timeoutCh <- timeoutInfo{
        Height: height,
        Round:  round,
        Step:   RoundStep(step),
    }
    return nil
}

// buildBlockFromDAG creates a block from Looseberry batches
func (e *Engine) buildBlockFromDAG(height int64) (*consensus.Block, error) {
    // Reap certified batches from Looseberry
    batches := e.dagMempool.ReapCertifiedBatches(e.config.MaxBlockBytes)

    // Build batch certificate references
    refs := make([]*gen.BatchCertRef, 0, len(batches))
    for _, batch := range batches {
        refs = append(refs, &gen.BatchCertRef{
            CertificateDigest: types.NewHash(batch.Certificate.Digest()),
            Round:             batch.Certificate.Round(),
            Validator:         types.NewAccountName(batch.Certificate.AuthorName()),
        })
    }

    // Build block header
    header := &gen.BlockHeader{
        ChainId:       e.config.ChainID,
        Height:        height,
        Time:          time.Now().UnixNano(),
        LastBlockHash: e.lastBlockHash,
        DagRound:      e.dagMempool.CurrentRound(),
        BatchCertRefs: refs,
        Proposer:      e.privVal.GetValidatorName(),
    }

    // Build block
    block := &gen.Block{
        Header: header,
        Data: &gen.BlockData{
            BatchDigests: extractDigests(batches),
        },
        Evidence:   e.evidencePool.PendingEvidence(e.config.MaxEvidenceBytes),
        LastCommit: e.lastCommit,
    }

    return e.toConsensusBlock(block), nil
}

// executeBlock executes a committed block
func (e *Engine) executeBlock(block *consensus.Block) error {
    ctx := context.Background()

    // BeginBlock
    header := &abi.BlockHeader{
        Height:   uint64(block.Height),
        Time:     time.Unix(0, block.Timestamp),
        Proposer: e.validatorSet.GetByIndex(block.Proposer).Name.Name,
    }
    if err := e.app.BeginBlock(ctx, header); err != nil {
        return err
    }

    // Execute transactions from Looseberry batches
    for _, txData := range block.Transactions {
        tx := &abi.Transaction{
            Data: txData,
        }
        result := e.app.ExecuteTx(ctx, tx)
        if result.Code != abi.CodeOK {
            // Log but continue - tx was already certified
        }
    }

    // EndBlock
    endResult := e.app.EndBlock(ctx)

    // Apply validator updates
    if len(endResult.ValidatorUpdates) > 0 {
        e.applyValidatorUpdates(endResult.ValidatorUpdates)
    }

    // Commit
    commitResult := e.app.Commit(ctx)
    e.lastAppHash = commitResult.AppHash

    // Notify Looseberry
    e.dagMempool.NotifyCommitted(e.dagRound)

    // Notify callbacks
    if e.deps.Callbacks != nil {
        e.deps.Callbacks.OnBlockCommitted(block.Height, block.Hash)
    }

    return nil
}

// SetDAGMempool sets the Looseberry mempool
func (e *Engine) SetDAGMempool(mempool looseberry.DAGMempool) {
    e.mu.Lock()
    defer e.mu.Unlock()
    e.dagMempool = mempool
}
```

---

## Phase 9: Testing Strategy

### 9.1 Unit Tests

Each component should have comprehensive unit tests:

```
tests/
├── types/
│   ├── hash_test.go
│   ├── signature_test.go
│   ├── account_test.go
│   ├── validator_test.go
│   ├── vote_test.go
│   └── block_test.go
├── engine/
│   ├── vote_tracker_test.go
│   ├── state_test.go
│   ├── timeout_test.go
│   └── proposer_test.go
├── wal/
│   └── file_wal_test.go
├── privval/
│   ├── file_pv_test.go
│   └── last_sign_state_test.go
├── evidence/
│   └── pool_test.go
└── integration/
    ├── consensus_test.go
    ├── looseberry_integration_test.go
    └── byzantine_test.go
```

### 9.2 Test Scenarios

1. **Happy Path**: All validators online, proposals accepted, blocks committed
2. **Round Timeout**: Proposer offline, timeout triggers new round
3. **Locking**: Validator locks on block, maintains lock across rounds
4. **Unlocking**: Validator unlocks with valid POL
5. **Byzantine Proposer**: Different proposals to different validators
6. **Equivocation Detection**: Double-voting detected and evidence collected
7. **Crash Recovery**: Node crashes and recovers from WAL
8. **Validator Set Changes**: Validators added/removed mid-consensus

---

## Phase 10: Implementation Order

### Recommended Implementation Sequence

| Phase | Components | Dependencies | Estimated Effort |
|-------|------------|--------------|------------------|
| 1 | Project setup, Makefile, schemas | None | 1 day |
| 2 | Generate code, type extensions | Phase 1 | 2 days |
| 3 | ValidatorSet, Vote tracking | Phase 2 | 2 days |
| 4 | TimeoutScheduler | Phase 2 | 1 day |
| 5 | WAL implementation | Phase 2 | 2 days |
| 6 | PrivValidator, LastSignState | Phase 2, 5 | 2 days |
| 7 | ConsensusState (state machine) | Phase 3, 4, 5, 6 | 4 days |
| 8 | Evidence pool | Phase 2 | 1 day |
| 9 | Main Engine (BFTConsensus) | Phase 7, 8 | 3 days |
| 10 | Looseberry integration | Phase 9 | 2 days |
| 11 | Unit tests | All phases | 3 days |
| 12 | Integration tests | Phase 11 | 3 days |
| 13 | Byzantine scenario tests | Phase 12 | 2 days |

**Total estimated effort: ~28 days**

---

## Appendix: Cramberry Schema Best Practices

### A.1 Type ID Assignments

```
TypeID Ranges:
  0       - Nil
  1-63    - Reserved (cramberry built-in)
  64-127  - Reserved (stdlib)
  128-199 - Core types (Hash, Signature, AccountName)
  200-299 - Account types (Account, Authority)
  300-399 - Transaction types
  400-499 - Validator types
  500-599 - Vote/Commit types
  600-699 - Block types
  700-799 - Evidence types
  800-899 - WAL types
  900+    - Application-specific
```

### A.2 Field Number Guidelines

- Use 1-15 for frequently serialized fields (single-byte tags)
- Use 16+ for rarely serialized fields
- Reserve field numbers 100-199 for future extensions
- Never reuse field numbers in the same message

### A.3 Versioning Strategy

When evolving schemas:
1. Add new fields with new numbers (never reuse)
2. Mark deprecated fields with comments
3. Use `optional` for new fields to maintain compatibility
4. Bump version in package name for breaking changes
