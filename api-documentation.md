# Leaderberry API Documentation

This document provides comprehensive API documentation for the Leaderberry BFT consensus engine in godoc-compatible format.

## Table of Contents

1. [Package types](#package-types) - Core consensus types
2. [Package engine](#package-engine) - Consensus engine
3. [Package wal](#package-wal) - Write-ahead log
4. [Package privval](#package-privval) - Private validator
5. [Package evidence](#package-evidence) - Evidence pool

---

## Package types

Package types defines the core data structures for the Leaderberry consensus protocol.

This package provides both Cramberry-generated types (in types/generated/) and hand-written extensions with methods and validation logic.

### Overview

The types package is organized around these key concepts:

- **Blocks**: Finalized blocks containing batch certificates and metadata
- **Votes**: Signed prevotes and precommits from validators
- **Proposals**: Block proposals with optional proof-of-lock (POL)
- **Accounts**: Named accounts with weighted multi-signature authorization
- **Validators**: Named validators with voting power and public keys
- **Evidence**: Proof of Byzantine behavior (e.g., double-signing)

All network-serializable types are defined in Cramberry schemas (../schema/*.cram). Generated code provides Encode/Decode methods for deterministic binary serialization.

### Thread Safety

Core types like Block, Vote, and ValidatorSet are designed to be immutable. Methods return copies rather than exposing internal state for modification. This ensures thread-safe sharing and prevents accidental mutation.

### Transaction Handling

**Important**: Consensus treats transaction payloads as opaque bytes. Only authorization metadata (signatures, account names, nonces) is interpreted by the consensus layer. Actual transaction execution and validation is the application layer's responsibility.

---

## Core Types

### Hash

```go
type Hash = gen.Hash  // [32]byte wrapper
```

Hash wraps a [32]byte array with hex encoding for human readability and JSON compatibility. All consensus hashing uses SHA-256.

**Constants:**

```go
const HashSize = 32  // Expected size of a hash in bytes
```

**Functions:**

```go
// NewHash creates a Hash from bytes, returning error if invalid.
// Use for untrusted input (network, files).
// Copies input data to prevent caller from modifying internal state.
func NewHash(data []byte) (Hash, error)

// MustNewHash creates a Hash, panicking if invalid.
// Use only for trusted internal data.
func MustNewHash(data []byte) Hash

// HashBytes computes SHA-256 hash of data
func HashBytes(data []byte) Hash

// HashEmpty returns an empty (zero) hash
func HashEmpty() Hash

// IsHashEmpty returns true if hash is nil or all zeros
func IsHashEmpty(h *Hash) bool

// HashEqual compares two hashes
func HashEqual(a, b Hash) bool

// HashString returns hex-encoded hash
func HashString(h Hash) string

// CopyHash creates a deep copy of a Hash
func CopyHash(h *Hash) *Hash
```

**Thread Safety:** All functions are goroutine-safe. Hash values are immutable once created.

**Example:**

```go
// Compute hash of data
data := []byte("hello world")
hash := types.HashBytes(data)

// Convert to hex string
fmt.Println(types.HashString(hash))  // "b94d27b9..."

// Compare hashes
hash2 := types.HashBytes(data)
if types.HashEqual(hash, hash2) {
    fmt.Println("Hashes match")
}
```

---

### Signature and PublicKey

```go
type Signature = gen.Signature  // [64]byte Ed25519 signature
type PublicKey = gen.PublicKey  // [32]byte Ed25519 public key
```

**Constants:**

```go
const SignatureSize = 64  // Expected size of Ed25519 signature
const PublicKeySize = 32  // Expected size of Ed25519 public key
```

**Functions:**

```go
// NewSignature creates a Signature from bytes, returning error if invalid.
// Copies input data to prevent caller from modifying internal state.
func NewSignature(data []byte) (Signature, error)

// MustNewSignature creates a Signature, panicking if invalid.
func MustNewSignature(data []byte) Signature

// NewPublicKey creates a PublicKey from bytes, returning error if invalid.
// Copies input data to prevent caller from modifying internal state.
func NewPublicKey(data []byte) (PublicKey, error)

// MustNewPublicKey creates a PublicKey, panicking if invalid.
func MustNewPublicKey(data []byte) PublicKey

// PublicKeyEqual compares two public keys
func PublicKeyEqual(a, b PublicKey) bool

// CopySignature creates a deep copy of a Signature
func CopySignature(s Signature) Signature

// CopyPublicKey creates a deep copy of a PublicKey
func CopyPublicKey(pk PublicKey) PublicKey

// VerifySignature verifies an Ed25519 signature
func VerifySignature(pubKey PublicKey, message []byte, sig Signature) bool
```

**Thread Safety:** All functions are goroutine-safe.

**Example:**

```go
// Verify a signature
message := []byte("sign this message")
isValid := types.VerifySignature(pubKey, message, signature)
if !isValid {
    return errors.New("invalid signature")
}
```

---

### AccountName

```go
type AccountName = gen.AccountName
```

AccountName represents a human-readable account identifier (e.g., "alice", "bob").

**Functions:**

```go
// NewAccountName creates an AccountName
func NewAccountName(name string) AccountName

// AccountNameString returns the account name string
func AccountNameString(a AccountName) string

// IsAccountNameEmpty returns true if account name is empty
func IsAccountNameEmpty(a AccountName) bool

// AccountNameEqual compares two account names
func AccountNameEqual(a, b AccountName) bool

// CopyAccountName creates a deep copy of an AccountName
func CopyAccountName(a AccountName) AccountName
```

**Thread Safety:** All functions are goroutine-safe.

**Example:**

```go
name := types.NewAccountName("alice")
if types.AccountNameString(name) == "alice" {
    fmt.Println("Name matches")
}
```

---

### Account and Authorization

```go
type Account = gen.Account
type Authority = gen.Authority
type KeyWeight = gen.KeyWeight
type AccountWeight = gen.AccountWeight
```

Account represents a named account with weighted multi-signature authorization. Supports hierarchical delegation and cycle detection.

**Constants:**

```go
// MaxAuthWeight prevents uint32 overflow in weight accumulation
const MaxAuthWeight = uint32(1<<31 - 1)  // ~2 billion
```

**Errors:**

```go
var (
    ErrInsufficientWeight = errors.New("insufficient authorization weight")
    ErrCycleDetected      = errors.New("authorization cycle detected")
    ErrAccountNotFound    = errors.New("account not found")
    ErrInvalidSignature   = errors.New("invalid signature")
)
```

**Types:**

```go
// AccountGetter is a function that retrieves an account by name
type AccountGetter func(AccountName) (*Account, error)
```

**Functions:**

```go
// VerifyAuthorization verifies that an authorization satisfies an authority.
// Returns the total weight achieved and any error.
// visited tracks accounts already checked (for cycle detection).
//
// Thread Safety: Not goroutine-safe. Caller must ensure AccountGetter is safe.
//
// Error Conditions:
//   - Returns ErrInsufficientWeight if weight < threshold
//   - Returns ErrAccountNotFound if delegated account doesn't exist
//   - Returns ErrInvalidSignature if signature verification fails
//   - Skips cyclic references (no error, but weight not counted)
func VerifyAuthorization(
    auth *gen.Authorization,
    authority *gen.Authority,
    signBytes []byte,
    getAccount AccountGetter,
    visited map[string]bool,
) (uint32, error)

// IsAuthoritySatisfied checks if authorization satisfies authority's threshold
//
// Thread Safety: Not goroutine-safe. Caller must ensure AccountGetter is safe.
func IsAuthoritySatisfied(
    auth *gen.Authorization,
    authority *gen.Authority,
    signBytes []byte,
    getAccount AccountGetter,
) (bool, error)
```

**Example:**

```go
// Define an account with multi-sig authority
account := &types.Account{
    Name: types.NewAccountName("alice"),
    Authority: types.Authority{
        Threshold: 2,
        Keys: []types.KeyWeight{
            {PublicKey: pubKey1, Weight: 1},
            {PublicKey: pubKey2, Weight: 1},
        },
    },
}

// Verify authorization meets threshold
getAccount := func(name types.AccountName) (*types.Account, error) {
    // Look up account from state
    return account, nil
}

satisfied, err := types.IsAuthoritySatisfied(auth, &account.Authority, signBytes, getAccount)
if err != nil {
    return fmt.Errorf("authorization failed: %w", err)
}
```

---

### NamedValidator and ValidatorSet

```go
type NamedValidator = gen.NamedValidator
type ValidatorSetData = gen.ValidatorSetData
```

NamedValidator represents a validator identified by human-readable name with voting power. Validators use Ed25519 keys for signing consensus messages.

**Constants:**

```go
const (
    MaxValidators        = 65535  // Maximum validators in a set
    MaxTotalVotingPower  = 1 << 60  // Prevents overflow in priority calculations
    PriorityWindowSize   = MaxTotalVotingPower * 2
)
```

**Errors:**

```go
var (
    ErrValidatorNotFound   = errors.New("validator not found")
    ErrDuplicateValidator  = errors.New("duplicate validator")
    ErrEmptyValidatorSet   = errors.New("empty validator set")
    ErrInvalidVotingPower  = errors.New("invalid voting power")
    ErrTooManyValidators   = errors.New("too many validators")
    ErrTotalPowerOverflow  = errors.New("total voting power overflow")
    ErrEmptyValidatorName  = errors.New("validator has empty name")
)
```

**Types:**

```go
// ValidatorSet wraps ValidatorSetData with additional methods
type ValidatorSet struct {
    Validators    []*NamedValidator
    Proposer      *NamedValidator
    TotalPower    int64
    // (internal fields omitted)
}
```

**Functions:**

```go
// NewValidatorSet creates a ValidatorSet from validators
//
// Error Conditions:
//   - Returns ErrEmptyValidatorSet if validators is empty
//   - Returns ErrTooManyValidators if len(validators) > MaxValidators
//   - Returns ErrInvalidVotingPower if any validator has power <= 0
//   - Returns ErrDuplicateValidator if duplicate names exist
//   - Returns ErrTotalPowerOverflow if total power exceeds MaxTotalVotingPower
//   - Returns ErrEmptyValidatorName if any validator has empty name
//
// Thread Safety: Goroutine-safe. Creates deep copy of input validators.
func NewValidatorSet(validators []*NamedValidator) (*ValidatorSet, error)

// GetByName returns a validator by name
// Returns nil if not found.
// Thread Safety: Goroutine-safe. Returns deep copy.
func (vs *ValidatorSet) GetByName(name string) *NamedValidator

// GetByIndex returns a validator by index
// Returns nil if not found.
// Thread Safety: Goroutine-safe. Returns deep copy.
func (vs *ValidatorSet) GetByIndex(index uint16) *NamedValidator

// Size returns the number of validators
// Thread Safety: Goroutine-safe (read-only).
func (vs *ValidatorSet) Size() int

// TwoThirdsMajority returns voting power needed for 2/3+ majority
// Thread Safety: Goroutine-safe (read-only).
func (vs *ValidatorSet) TwoThirdsMajority() int64

// GetProposerForRound returns the proposer for a given round
// For round 0, returns the pre-computed proposer.
// For round > 0, computes proposer by advancing priorities.
// Thread Safety: Goroutine-safe. Returns deep copy.
func (vs *ValidatorSet) GetProposerForRound(round int32) *NamedValidator

// Copy creates a deep copy of the validator set
// Thread Safety: Goroutine-safe. Returns independent copy.
func (vs *ValidatorSet) Copy() (*ValidatorSet, error)

// WithIncrementedPriority returns new ValidatorSet with priorities incremented
// This is the immutable pattern for thread-safe proposer rotation.
// The original ValidatorSet is not modified.
// Thread Safety: Goroutine-safe. Returns new instance.
func (vs *ValidatorSet) WithIncrementedPriority(times int32) (*ValidatorSet, error)

// Hash computes a deterministic hash of the validator set
// ProposerPriority is excluded from the hash since it's mutable state.
// Thread Safety: Goroutine-safe (read-only).
func (vs *ValidatorSet) Hash() Hash

// CopyValidator creates a deep copy of a validator
func CopyValidator(v *NamedValidator) *NamedValidator
```

**Example:**

```go
// Create validator set
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
        VotingPower: 100,
        PublicKey:   bobPubKey,
    },
}

valSet, err := types.NewValidatorSet(vals)
if err != nil {
    return fmt.Errorf("invalid validator set: %w", err)
}

// Get 2/3+ majority requirement
required := valSet.TwoThirdsMajority()  // 134

// Get proposer for round 0
proposer := valSet.GetProposerForRound(0)
fmt.Println(types.AccountNameString(proposer.Name))  // "alice" or "bob"

// Get proposer for round 1
proposer = valSet.GetProposerForRound(1)
```

---

### Vote and Commit

```go
type Vote = gen.Vote
type VoteType = gen.VoteType
type Commit = gen.Commit
type CommitSig = gen.CommitSig
```

Vote represents a signed prevote or precommit from a validator.

**Constants:**

```go
const (
    VoteTypeUnknown   = gen.VoteTypeVoteTypeUnknown
    VoteTypePrevote   = gen.VoteTypeVoteTypePrevote
    VoteTypePrecommit = gen.VoteTypeVoteTypePrecommit
)
```

**Errors:**

```go
var (
    ErrInvalidVote        = errors.New("invalid vote")
    ErrVoteConflict       = errors.New("conflicting vote")
    ErrDuplicateVote      = errors.New("duplicate vote")
    ErrUnexpectedVoteType = errors.New("unexpected vote type")

    // Commit verification errors
    ErrInvalidCommit           = errors.New("invalid commit")
    ErrCommitHeightMismatch    = errors.New("commit height mismatch")
    ErrCommitBlockHashMismatch = errors.New("commit block hash mismatch")
    ErrInsufficientVotePower   = errors.New("insufficient voting power in commit")
    ErrInvalidCommitSignature  = errors.New("invalid signature in commit")
    ErrDuplicateCommitSig      = errors.New("duplicate signature in commit")
    ErrUnknownCommitValidator  = errors.New("unknown validator in commit")
)
```

**Functions:**

```go
// VoteSignBytes returns the bytes to sign for a vote
// Panics if vote is nil (CONSENSUS CRITICAL).
// Thread Safety: Goroutine-safe.
func VoteSignBytes(chainID string, v *Vote) []byte

// IsNilVote returns true if the vote is for nil (no block)
// A nil vote pointer is treated as a nil vote.
// Thread Safety: Goroutine-safe.
func IsNilVote(v *Vote) bool

// VerifyVoteSignature verifies the signature on a vote
// Error Conditions:
//   - Returns ErrInvalidVote if vote is nil
//   - Returns error if signature is empty or invalid size
//   - Returns error if public key is invalid size
//   - Returns error if signature verification fails
// Thread Safety: Goroutine-safe.
func VerifyVoteSignature(chainID string, vote *Vote, pubKey PublicKey) error

// CopyVote creates a deep copy of a Vote
// Thread Safety: Goroutine-safe.
func CopyVote(v *Vote) *Vote

// VerifyCommit verifies a commit certificate against a validator set
// Verifies:
//   - All signatures are valid
//   - Signatures are from known validators
//   - No duplicate signatures
//   - 2/3+ of voting power has signed for the block
//
// Error Conditions:
//   - Returns error if valSet is nil
//   - Returns ErrInvalidCommit if commit is nil or has no signatures
//   - Returns ErrCommitHeightMismatch if height doesn't match
//   - Returns ErrCommitBlockHashMismatch if block hash doesn't match
//   - Returns ErrDuplicateCommitSig if duplicate validator signatures
//   - Returns ErrUnknownCommitValidator if signature from unknown validator
//   - Returns ErrInvalidCommitSignature if signature verification fails
//   - Returns ErrInsufficientVotePower if < 2/3+ voting power
//
// Thread Safety: Goroutine-safe.
func VerifyCommit(
    chainID string,
    valSet *ValidatorSet,
    blockHash Hash,
    height int64,
    commit *Commit,
) error

// VerifyCommitLight is lighter version that only checks voting power
// without re-verifying signatures (for use when signatures were already verified).
// Thread Safety: Goroutine-safe.
func VerifyCommitLight(
    valSet *ValidatorSet,
    blockHash Hash,
    height int64,
    commit *Commit,
) error

// CopyCommit creates a deep copy of a Commit
func CopyCommit(c *Commit) *Commit
```

**Example:**

```go
// Create and verify a vote
vote := &types.Vote{
    Type:           types.VoteTypePrevote,
    Height:         1,
    Round:          0,
    BlockHash:      &blockHash,
    Timestamp:      time.Now().UnixNano(),
    Validator:      types.NewAccountName("alice"),
    ValidatorIndex: 0,
}

// Sign the vote (privval.SignVote fills vote.Signature)
err := privVal.SignVote("chain-id", vote)
if err != nil {
    return fmt.Errorf("failed to sign: %w", err)
}

// Verify the signature
validator := valSet.GetByIndex(0)
err = types.VerifyVoteSignature("chain-id", vote, validator.PublicKey)
if err != nil {
    return fmt.Errorf("invalid signature: %w", err)
}

// Verify a commit certificate
err = types.VerifyCommit("chain-id", valSet, blockHash, height, commit)
if err != nil {
    return fmt.Errorf("invalid commit: %w", err)
}
```

---

### Block and BlockHeader

```go
type Block = gen.Block
type BlockHeader = gen.BlockHeader
type BlockData = gen.BlockData
type BatchCertRef = gen.BatchCertRef
```

Block represents a finalized block containing batch certificates and metadata. Blocks reference Looseberry DAG batches rather than raw transactions.

**Functions:**

```go
// BlockHash computes the hash of a block
// Panics if marshaling fails (CONSENSUS CRITICAL).
// Thread Safety: Goroutine-safe.
func BlockHash(b *Block) Hash

// BlockHeaderHash computes the hash of a block header
// Panics if marshaling fails (CONSENSUS CRITICAL).
// Thread Safety: Goroutine-safe.
func BlockHeaderHash(h *BlockHeader) Hash

// CommitHash computes the hash of a commit
// Panics if marshaling fails (CONSENSUS CRITICAL).
// Thread Safety: Goroutine-safe.
func CommitHash(c *Commit) Hash

// NewBlock creates a new block
func NewBlock(header *BlockHeader, data *BlockData, lastCommit *Commit) *Block

// NewBlockHeader creates a new block header
func NewBlockHeader(
    chainID string,
    height int64,
    timestamp int64,
    lastBlockHash *Hash,
    lastCommitHash *Hash,
    validatorsHash *Hash,
    appHash *Hash,
    proposer AccountName,
) *BlockHeader

// CopyBlock creates a deep copy of a Block
// Thread Safety: Goroutine-safe.
func CopyBlock(b *Block) *Block

// CopyBlockHeader creates a deep copy of a BlockHeader
func CopyBlockHeader(h *BlockHeader) BlockHeader

// CopyBlockData creates a deep copy of BlockData
func CopyBlockData(d *BlockData) BlockData
```

**Example:**

```go
// Create a block
header := types.NewBlockHeader(
    "my-chain",
    1,
    time.Now().UnixNano(),
    &lastBlockHash,
    &lastCommitHash,
    &validatorsHash,
    &appHash,
    types.NewAccountName("alice"),
)

data := &types.BlockData{
    BatchDigests: []types.Hash{batchHash1, batchHash2},
}

block := types.NewBlock(header, data, lastCommit)

// Compute block hash
hash := types.BlockHash(block)
fmt.Printf("Block hash: %x\n", hash.Data)
```

---

### Proposal

```go
type Proposal = gen.Proposal
```

Proposal represents a block proposal with optional proof-of-lock (POL) from prior rounds. POL votes demonstrate why a proposer is locked on a specific block.

**Functions:**

```go
// ProposalSignBytes returns the bytes to sign for a proposal
// Panics if proposal is nil (CONSENSUS CRITICAL).
// Thread Safety: Goroutine-safe.
func ProposalSignBytes(chainID string, p *Proposal) []byte

// NewProposal creates a new proposal
func NewProposal(
    height int64,
    round int32,
    timestamp int64,
    block Block,
    polRound int32,
    polVotes []Vote,
    proposer AccountName,
) *Proposal

// HasPOL returns true if this proposal has a proof-of-lock
func HasPOL(p *Proposal) bool

// ProposalBlockHash returns the hash of the proposed block
func ProposalBlockHash(p *Proposal) Hash

// CopyProposal creates a deep copy of a Proposal
func CopyProposal(p *Proposal) *Proposal
```

**Example:**

```go
// Create a proposal
proposal := types.NewProposal(
    1,                           // height
    0,                           // round
    time.Now().UnixNano(),       // timestamp
    *block,                      // block
    -1,                          // polRound (-1 means no POL)
    nil,                         // polVotes
    types.NewAccountName("alice"), // proposer
)

// Check if proposal has POL
if types.HasPOL(proposal) {
    fmt.Printf("Proposal has POL from round %d\n", proposal.PolRound)
}

// Get proposed block hash
hash := types.ProposalBlockHash(proposal)
```

---

### BlockPart and PartSet

```go
// BlockPart represents a single part of a block with Merkle proof
type BlockPart struct {
    Index     uint16  // Part index
    Bytes     []byte  // Part data
    ProofPath []Hash  // Sibling hashes for Merkle proof verification
    ProofRoot Hash    // Expected Merkle root
}

// BlockPartSetHeader describes a set of block parts
type BlockPartSetHeader struct {
    Total uint16  // Total number of parts
    Hash  Hash    // Merkle root of part hashes
}

// PartSet is a set of block parts
type PartSet struct {
    // (internal fields)
}
```

PartSet provides Merkle-tree-based block splitting for efficient gossip of large blocks. Nodes can request and verify individual parts without downloading the entire block.

**Constants:**

```go
const (
    BlockPartSize  = 65536  // 64KB maximum size per part
    MaxBlockParts  = 1024   // Allows blocks up to 64MB
)
```

**Errors:**

```go
var (
    ErrPartSetInvalidIndex = errors.New("invalid part index")
    ErrPartSetInvalidProof = errors.New("invalid part proof")
    ErrPartSetAlreadyHas   = errors.New("part set already has part")
    ErrPartSetFull         = errors.New("part set is full")
    ErrPartSetNotComplete  = errors.New("part set not complete")
    ErrPartSizeTooLarge    = errors.New("part size too large")
    ErrTooManyParts        = errors.New("too many parts")
)
```

**Functions:**

```go
// NewPartSetFromData creates a PartSet by splitting data into parts
// Automatically generates Merkle proofs for each part.
//
// Error Conditions:
//   - Returns error if data is empty
//   - Returns ErrTooManyParts if data requires > MaxBlockParts
//
// Thread Safety: Goroutine-safe after construction.
func NewPartSetFromData(data []byte) (*PartSet, error)

// NewPartSetFromHeader creates an empty PartSet from a header
// Use this to receive parts from the network.
//
// Error Conditions:
//   - Returns ErrTooManyParts if header.Total > MaxBlockParts
//
// Thread Safety: Goroutine-safe after construction.
func NewPartSetFromHeader(header BlockPartSetHeader) (*PartSet, error)

// BlockPartsFromBlock creates a PartSet from a block
func BlockPartsFromBlock(block *Block) (*PartSet, error)

// BlockFromParts reassembles a block from a complete part set
func BlockFromParts(ps *PartSet) (*Block, error)
```

**PartSet Methods:**

```go
// Header returns the part set header
// Thread Safety: Goroutine-safe. Returns deep copy.
func (ps *PartSet) Header() BlockPartSetHeader

// Total returns the total number of parts
// Thread Safety: Goroutine-safe.
func (ps *PartSet) Total() uint16

// Count returns the number of parts we have
// Thread Safety: Goroutine-safe.
func (ps *PartSet) Count() uint16

// IsComplete returns true if we have all parts
// Thread Safety: Goroutine-safe.
func (ps *PartSet) IsComplete() bool

// HasPart returns true if we have the part at index
// Thread Safety: Goroutine-safe.
func (ps *PartSet) HasPart(index uint16) bool

// GetPart returns the part at index, or nil if not present
// Thread Safety: Goroutine-safe. Returns deep copy.
func (ps *PartSet) GetPart(index uint16) *BlockPart

// AddPart adds a part to the set
// Verifies Merkle proof before accepting.
//
// Error Conditions:
//   - Returns error if part is nil
//   - Returns ErrPartSetInvalidIndex if index out of range
//   - Returns ErrPartSizeTooLarge if part size exceeds BlockPartSize
//   - Returns ErrPartSetAlreadyHas if already have this part
//   - Returns ErrPartSetInvalidProof if Merkle proof fails
//
// Thread Safety: Goroutine-safe (internally synchronized).
func (ps *PartSet) AddPart(part *BlockPart) error

// GetData returns the assembled data, or error if not complete
// Thread Safety: Goroutine-safe. Returns copy.
func (ps *PartSet) GetData() ([]byte, error)

// MissingParts returns indices of parts we don't have
// Thread Safety: Goroutine-safe.
func (ps *PartSet) MissingParts() []uint16
```

**Example:**

```go
// Split a block into parts
block := &types.Block{ /* ... */ }
partSet, err := types.BlockPartsFromBlock(block)
if err != nil {
    return fmt.Errorf("failed to create parts: %w", err)
}

// Get part set header (for gossiping)
header := partSet.Header()
fmt.Printf("Block split into %d parts, root: %x\n", header.Total, header.Hash.Data)

// Receive parts from network
receiverPartSet, _ := types.NewPartSetFromHeader(header)
for i := uint16(0); i < header.Total; i++ {
    part := partSet.GetPart(i)
    if err := receiverPartSet.AddPart(part); err != nil {
        return fmt.Errorf("failed to add part %d: %w", i, err)
    }
}

// Reassemble block
if receiverPartSet.IsComplete() {
    reconstructed, err := types.BlockFromParts(receiverPartSet)
    if err != nil {
        return fmt.Errorf("failed to reconstruct: %w", err)
    }
}
```

---

## Package engine

Package engine implements the Tendermint-style BFT consensus state machine.

The engine coordinates the consensus protocol through these key states:

```
NewHeight → NewRound → Propose → Prevote → PrevoteWait → Precommit → PrecommitWait → Commit
```

### Overview

The engine package provides:

- **Engine**: Main consensus coordinator implementing state machine transitions
- **ConsensusState**: Internal state tracking for height, round, step, locked block
- **VoteTracker**: Aggregates prevotes and precommits to detect 2/3+ quorums
- **TimeoutTicker**: Manages round timeouts with exponential backoff
- **PeerState**: Tracks what each peer knows for efficient gossiping
- **BlockSync**: Fast-sync mechanism to catch up nodes that are far behind

### Consensus Properties

**Safety**: Once a block is committed, it is final and immutable. No conflicting blocks can be finalized at the same height.

**Liveness**: Guaranteed under partial synchrony with 2/3+ honest validators. Rounds advance automatically on timeout to ensure progress.

**Byzantine Fault Tolerance**: Tolerates up to 1/3 Byzantine validators. Detects and reports equivocation (double-signing) as evidence.

### Thread Safety

All public Engine methods are thread-safe and use internal locking. The engine maintains a single goroutine for state transitions.

---

## Engine

```go
// Engine is the main consensus engine that implements the BFT consensus protocol
type Engine struct {
    // (internal fields)
}

// ConsensusMessageType identifies the type of consensus message
type ConsensusMessageType uint8

const (
    ConsensusMessageTypeProposal ConsensusMessageType = 1
    ConsensusMessageTypeVote     ConsensusMessageType = 2
)
```

**Functions:**

```go
// NewEngine creates a new consensus engine
//
// Parameters:
//   - config: Engine configuration (ChainID, timeouts, WAL path, etc.)
//   - valSet: Current validator set
//   - pv: Private validator for signing
//   - w: Write-ahead log for crash recovery (can be nil)
//   - executor: Block execution interface (can be nil)
//
// Thread Safety: Goroutine-safe after construction.
func NewEngine(
    config *Config,
    valSet *types.ValidatorSet,
    pv privval.PrivValidator,
    w wal.WAL,
    executor BlockExecutor,
) *Engine

// SetProposalBroadcaster sets the function used to broadcast proposals
// Thread Safety: Goroutine-safe.
func (e *Engine) SetProposalBroadcaster(fn func(*gen.Proposal))

// SetVoteBroadcaster sets the function used to broadcast votes
// Thread Safety: Goroutine-safe.
func (e *Engine) SetVoteBroadcaster(fn func(*gen.Vote))

// Start starts the consensus engine
//
// Parameters:
//   - height: Starting height
//   - lastCommit: Commit from previous height (nil for genesis)
//
// Error Conditions:
//   - Returns ErrAlreadyStarted if already running
//   - Returns error if WAL fails to start
//   - Returns error if consensus state fails to start
//
// Thread Safety: Goroutine-safe.
// Blocking: No, starts background goroutines.
func (e *Engine) Start(height int64, lastCommit *gen.Commit) error

// Stop stops the consensus engine
//
// Error Conditions:
//   - Returns ErrNotStarted if not running
//   - Returns error if state or WAL fails to stop
//
// Thread Safety: Goroutine-safe.
// Blocking: Yes, waits for clean shutdown.
func (e *Engine) Stop() error

// AddProposal adds a proposal received from the network
//
// Error Conditions:
//   - Returns ErrNotStarted if engine not running
//
// Thread Safety: Goroutine-safe.
// Blocking: No, proposal is queued for processing.
func (e *Engine) AddProposal(proposal *gen.Proposal) error

// AddVote adds a vote received from the network
//
// Error Conditions:
//   - Returns ErrNotStarted if engine not running
//
// Thread Safety: Goroutine-safe.
// Blocking: No, vote is queued for processing.
func (e *Engine) AddVote(vote *gen.Vote) error

// GetState returns the current consensus state
// Returns (height, round, step, error).
//
// Thread Safety: Goroutine-safe.
func (e *Engine) GetState() (height int64, round int32, step RoundStep, err error)

// GetValidatorSet returns the current validator set
// Thread Safety: Goroutine-safe. Returns deep copy.
func (e *Engine) GetValidatorSet() *types.ValidatorSet

// UpdateValidatorSet updates the validator set
// Typically called after a block is committed.
// Thread Safety: Goroutine-safe. Deep copies input.
func (e *Engine) UpdateValidatorSet(valSet *types.ValidatorSet)

// IsValidator returns true if the local node is a validator
// Thread Safety: Goroutine-safe.
func (e *Engine) IsValidator() bool

// GetProposer returns the proposer for the current round
// Thread Safety: Goroutine-safe. Returns deep copy.
func (e *Engine) GetProposer() *types.NamedValidator

// ChainID returns the chain ID
// Thread Safety: Goroutine-safe.
func (e *Engine) ChainID() string

// HandleConsensusMessage handles a consensus message from the network
// Messages must be prefixed with a single byte indicating the message type.
//
// Error Conditions:
//   - Returns ErrInvalidMessage if message format is invalid
//   - Returns ErrNotStarted if engine not running
//
// Thread Safety: Goroutine-safe.
func (e *Engine) HandleConsensusMessage(peerID string, data []byte) error
```

**Example:**

```go
// Create validator set
vals := []*types.NamedValidator{
    {Name: types.NewAccountName("alice"), VotingPower: 100, PublicKey: alicePubKey},
    {Name: types.NewAccountName("bob"), VotingPower: 100, PublicKey: bobPubKey},
}
valSet, _ := types.NewValidatorSet(vals)

// Create engine
cfg := &engine.Config{
    ChainID:  "my-chain",
    Timeouts: engine.DefaultTimeoutConfig(),
    WALPath:  "data/cs.wal",
}
eng := engine.NewEngine(cfg, valSet, privVal, wal, nil)

// Set broadcast functions
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

// Process network messages
eng.AddProposal(proposal)
eng.AddVote(vote)

// Query state
height, round, step, _ := eng.GetState()
fmt.Printf("Consensus at H:%d R:%d S:%v\n", height, round, step)

// Stop when done
eng.Stop()
```

---

## Config

```go
// Config holds configuration for the consensus engine
type Config struct {
    ChainID                   string
    Timeouts                  TimeoutConfig
    WALPath                   string
    WALSync                   bool
    MaxBlockBytes             int64
    CreateEmptyBlocks         bool
    CreateEmptyBlocksInterval time.Duration
    SkipTimeoutCommit         bool
}

// TimeoutConfig holds timeout configuration
type TimeoutConfig struct {
    Propose        time.Duration
    ProposeDelta   time.Duration
    Prevote        time.Duration
    PrevoteDelta   time.Duration
    Precommit      time.Duration
    PrecommitDelta time.Duration
    Commit         time.Duration
}
```

**Errors:**

```go
var (
    ErrEmptyChainID         = errors.New("chain_id is required")
    ErrEmptyWALPath         = errors.New("wal_path is required")
    ErrInvalidTimeout       = errors.New("timeout must be positive")
    ErrTimeoutTooLarge      = errors.New("timeout exceeds maximum (5 minutes)")
    ErrInvalidMaxBlockBytes = errors.New("max_block_bytes must be positive")
)
```

**Functions:**

```go
// DefaultConfig returns a default configuration
func DefaultConfig() *Config

// DefaultTimeoutConfig returns default timeout configuration
func DefaultTimeoutConfig() TimeoutConfig

// ValidateBasic performs basic validation of the config
// Error Conditions:
//   - Returns ErrEmptyChainID if chain_id is empty
//   - Returns ErrEmptyWALPath if wal_path is empty
//   - Returns error if timeout validation fails
//   - Returns ErrInvalidMaxBlockBytes if max_block_bytes <= 0
func (cfg *Config) ValidateBasic() error

// Validate validates timeout configuration
// Error Conditions:
//   - Returns error if any timeout is <= 0
//   - Returns error if any timeout > 5 minutes
//   - Returns error if any delta is < 0
func (tc TimeoutConfig) Validate() error
```

**Example:**

```go
// Use default config
cfg := engine.DefaultConfig()
cfg.ChainID = "my-chain"
cfg.WALPath = "/var/consensus/wal"

// Validate
if err := cfg.ValidateBasic(); err != nil {
    log.Fatalf("Invalid config: %v", err)
}

// Custom timeouts
cfg.Timeouts.Propose = 5 * time.Second
cfg.Timeouts.Prevote = 2 * time.Second
```

---

## TimeoutTicker

```go
// TimeoutTicker manages timeouts for the consensus state machine
type TimeoutTicker struct {
    // (internal fields)
}

// TimeoutInfo represents a timeout event
type TimeoutInfo struct {
    Duration time.Duration
    Height   int64
    Round    int32
    Step     RoundStep
}

// RoundStep type aliases
type RoundStep = gen.RoundStepType

const (
    RoundStepNewHeight     = gen.RoundStepTypeRoundStepNewHeight
    RoundStepNewRound      = gen.RoundStepTypeRoundStepNewRound
    RoundStepPropose       = gen.RoundStepTypeRoundStepPropose
    RoundStepPrevote       = gen.RoundStepTypeRoundStepPrevote
    RoundStepPrevoteWait   = gen.RoundStepTypeRoundStepPrevoteWait
    RoundStepPrecommit     = gen.RoundStepTypeRoundStepPrecommit
    RoundStepPrecommitWait = gen.RoundStepTypeRoundStepPrecommitWait
    RoundStepCommit        = gen.RoundStepTypeRoundStepCommit
)
```

**Constants:**

```go
const MaxRoundForTimeout = 10000  // Prevents overflow in timeout calculations
```

**Functions:**

```go
// NewTimeoutTicker creates a new TimeoutTicker
func NewTimeoutTicker(config TimeoutConfig) *TimeoutTicker

// Start starts the timeout ticker
// Thread Safety: Goroutine-safe.
// Blocking: No, starts background goroutine.
func (tt *TimeoutTicker) Start()

// Stop stops the timeout ticker
// Thread Safety: Goroutine-safe.
// Blocking: Yes, waits for goroutine to finish.
func (tt *TimeoutTicker) Stop()

// Chan returns the channel that delivers timeout events
// Thread Safety: Goroutine-safe.
func (tt *TimeoutTicker) Chan() <-chan TimeoutInfo

// ScheduleTimeout schedules a new timeout
// Non-blocking: Drops timeout if channel is full (will be rescheduled).
// Thread Safety: Goroutine-safe.
func (tt *TimeoutTicker) ScheduleTimeout(ti TimeoutInfo)
```

**Example:**

```go
ticker := engine.NewTimeoutTicker(engine.DefaultTimeoutConfig())
ticker.Start()
defer ticker.Stop()

// Schedule a timeout
ticker.ScheduleTimeout(engine.TimeoutInfo{
    Duration: 3 * time.Second,
    Height:   1,
    Round:    0,
    Step:     engine.RoundStepPropose,
})

// Wait for timeout
select {
case ti := <-ticker.Chan():
    fmt.Printf("Timeout at H:%d R:%d S:%v\n", ti.Height, ti.Round, ti.Step)
}
```

---

## Engine Errors

```go
var (
    ErrAlreadyStarted      = errors.New("engine already started")
    ErrNotStarted          = errors.New("engine not started")
    ErrInvalidMessage      = errors.New("invalid consensus message")
    ErrInvalidVote         = errors.New("invalid vote")
    ErrUnknownValidator    = errors.New("unknown validator")
    ErrConflictingVote     = errors.New("conflicting vote from validator")
    ErrInvalidSignature    = errors.New("invalid signature")
    ErrStaleVoteSet        = errors.New("vote set is stale (from previous height)")
)
```

---

## Package wal

Package wal implements a write-ahead log for consensus crash recovery.

The write-ahead log (WAL) provides durability and crash recovery by persisting all consensus messages before they are processed. After a restart, the WAL is replayed to restore the consensus state to its last known position.

### Overview

**File Format**: Each entry is encoded as:

```
[4 bytes: length][N bytes: Cramberry-encoded message][4 bytes: CRC32]
```

The length prefix enables fast seeking and validation. CRC32 detects corruption from incomplete writes or disk errors.

**Rotation and Cleanup**: WAL files are rotated per consensus height to prevent unbounded growth. Old WAL files can be safely deleted after block finalization and persistence.

```
wal-0000000001-height-0000000001.log
wal-0000000002-height-0000000002.log
```

**Recovery Process**:
1. Read all WAL files in order
2. Decode and validate each message
3. Replay messages through the consensus engine
4. Resume consensus from last recorded state

### Thread Safety

FileWAL uses internal locking to ensure thread-safe writes from multiple goroutines. However, only one WAL instance should write to a directory.

### Performance Considerations

- Regular `Write()` calls are buffered for throughput
- `WriteSync()` forces an fsync for critical safety (e.g., before signing votes)
- Balance durability vs performance based on your consistency requirements

---

## WAL Interface

```go
// WAL interface for write-ahead logging
type WAL interface {
    // Write writes a message to the WAL (buffered)
    Write(msg *Message) error

    // WriteSync writes a message and ensures it's synced to disk
    WriteSync(msg *Message) error

    // FlushAndSync flushes and syncs all pending writes
    FlushAndSync() error

    // SearchForEndHeight searches for the end of a height in the WAL
    // Returns a Reader positioned after the EndHeight message, or false if not found
    SearchForEndHeight(height int64) (Reader, bool, error)

    // Start starts the WAL
    Start() error

    // Stop stops the WAL
    Stop() error

    // Group returns the current WAL group (for rotation)
    Group() *Group
}

// Reader interface for reading from WAL
type Reader interface {
    // Read reads the next message from the WAL
    Read() (*Message, error)

    // Close closes the reader
    Close() error
}
```

**Errors:**

```go
var (
    ErrWALClosed     = errors.New("WAL is closed")
    ErrWALCorrupted  = errors.New("WAL is corrupted")
    ErrWALNotFound   = errors.New("WAL file not found")
    ErrInvalidHeight = errors.New("invalid height in WAL")
)
```

---

## Message Types

```go
// Message represents a WAL message with metadata
type Message struct {
    Type   MessageType
    Height int64
    Round  int32
    Data   []byte
}

type MessageType = gen.WalmsgType

const (
    MsgTypeUnknown   = gen.WalmsgTypeWalMsgUnknown
    MsgTypeProposal  = gen.WalmsgTypeWalMsgProposal
    MsgTypeVote      = gen.WalmsgTypeWalMsgVote
    MsgTypeCommit    = gen.WalmsgTypeWalMsgCommit
    MsgTypeEndHeight = gen.WalmsgTypeWalMsgEndHeight
    MsgTypeState     = gen.WalmsgTypeWalMsgState
    MsgTypeTimeout   = gen.WalmsgTypeWalMsgTimeout
)
```

**Functions:**

```go
// Message encoding/decoding
func (m *Message) MarshalCramberry() ([]byte, error)
func (m *Message) UnmarshalCramberry(data []byte) error

// Message constructors
func NewProposalMessage(height int64, round int32, proposal *gen.Proposal) (*Message, error)
func NewVoteMessage(height int64, round int32, vote *gen.Vote) (*Message, error)
func NewCommitMessage(height int64, commit *gen.Commit) (*Message, error)
func NewEndHeightMessage(height int64) *Message
func NewStateMessage(state *gen.ConsensusStateData) (*Message, error)
func NewTimeoutMessage(height int64, round int32, step gen.RoundStepType) (*Message, error)

// Message decoders
func DecodeProposal(data []byte) (*gen.Proposal, error)
func DecodeVote(data []byte) (*gen.Vote, error)
func DecodeCommit(data []byte) (*gen.Commit, error)
func DecodeState(data []byte) (*gen.ConsensusStateData, error)
func DecodeTimeout(data []byte) (*gen.TimeoutMessage, error)
```

---

## FileWAL

```go
// FileWAL is a file-based WAL implementation
// Use NewFileWAL to create instances.
```

**Functions:**

```go
// NewFileWAL creates a new file-based WAL
//
// Parameters:
//   - dirPath: Directory for WAL files (created if doesn't exist)
//
// Error Conditions:
//   - Returns error if directory creation fails
//   - Returns error if initial file creation fails
//
// Thread Safety: Goroutine-safe after construction.
// Resource Management: Call Stop() to close files and flush buffers.
func NewFileWAL(dirPath string) (WAL, error)
```

**Example:**

```go
// Create a new file-based WAL
wal, err := wal.NewFileWAL("./data/wal")
if err != nil {
    log.Fatal(err)
}
defer wal.Stop()

// Start the WAL
if err := wal.Start(); err != nil {
    log.Fatal(err)
}

// Write a proposal (buffered)
msg, err := wal.NewProposalMessage(1, 0, proposal)
if err != nil {
    log.Fatal(err)
}
err = wal.Write(msg)

// Write a critical vote (synced)
msg, err = wal.NewVoteMessage(1, 0, vote)
if err != nil {
    log.Fatal(err)
}
err = wal.WriteSync(msg)

// Search for end of height (for recovery)
reader, found, err := wal.SearchForEndHeight(1)
if found {
    for {
        msg, err := reader.Read()
        if err == io.EOF {
            break
        }
        // Replay message
    }
    reader.Close()
}
```

---

## NopWAL (Testing)

```go
// NopWAL is a no-op WAL implementation for testing
type NopWAL struct{}

func (w *NopWAL) Write(msg *Message) error
func (w *NopWAL) WriteSync(msg *Message) error
func (w *NopWAL) FlushAndSync() error
func (w *NopWAL) SearchForEndHeight(height int64) (Reader, bool, error)
func (w *NopWAL) Start() error
func (w *NopWAL) Stop() error
func (w *NopWAL) Group() *Group
```

**Example:**

```go
// Use NopWAL for testing
wal := &wal.NopWAL{}
eng := engine.NewEngine(cfg, valSet, privVal, wal, nil)
```

---

## Package privval

Package privval implements private validator functionality with double-sign prevention.

A private validator holds the Ed25519 private key used for signing consensus messages (proposals, prevotes, precommits). The key responsibility is preventing double-signing, which would constitute Byzantine behavior and violate consensus safety.

### Overview

**Double-Sign Prevention**: LastSignState tracks the last height/round/step/blockhash signed by this validator. Before signing any message, the validator checks:

1. Never sign two different blockhashes at the same height/round/step
2. Never regress to a lower height (after restart)
3. Persist state BEFORE returning signature (atomic persistence pattern)

This prevents equivocation even across crashes and restarts.

**File Format**:

- `key.json`: Ed25519 private key and account name (rarely changes)
- `state.json`: LastSignState (updated on every signature)

The files use atomic write-then-rename to ensure consistency even if the process crashes mid-write.

### Security Considerations

**Private Key Protection**:
- Key file should have restricted permissions (0600)
- Never log or expose the private key
- Consider HSM integration for production

**Double-Sign Protection**:
- State file MUST be persisted before returning signature
- File operations use fsync() to ensure durability
- Lock file prevents concurrent access

### Thread Safety

FilePV uses internal locking to prevent concurrent signing. Only one FilePV instance should access the same key/state files.

---

## PrivValidator Interface

```go
// PrivValidator interface for signing consensus messages
type PrivValidator interface {
    // GetPubKey returns the public key
    GetPubKey() types.PublicKey

    // SignVote signs a vote, checking for double-sign
    SignVote(chainID string, vote *gen.Vote) error

    // SignProposal signs a proposal
    SignProposal(chainID string, proposal *gen.Proposal) error

    // GetAddress returns the validator address (derived from public key)
    GetAddress() []byte
}
```

**Errors:**

```go
var (
    ErrDoubleSign       = errors.New("double sign attempt")
    ErrSignerNotFound   = errors.New("signer not found")
    ErrInvalidSignature = errors.New("invalid signature")
    ErrHeightRegression = errors.New("height regression")
    ErrRoundRegression  = errors.New("round regression")
    ErrStepRegression   = errors.New("step regression")
)
```

---

## LastSignState

```go
// LastSignState tracks the last signed vote for double-sign prevention
type LastSignState struct {
    Height        int64
    Round         int32
    Step          int8  // 0 = proposal, 1 = prevote, 2 = precommit
    Signature     types.Signature
    BlockHash     *types.Hash
    SignBytesHash *types.Hash  // Hash of complete sign bytes for idempotency
    Timestamp     int64         // Timestamp for idempotency check
}
```

**Constants:**

```go
const (
    StepProposal  int8 = 0
    StepPrevote   int8 = 1
    StepPrecommit int8 = 2
)
```

**Functions:**

```go
// CheckHRS checks if a new vote would be a double sign
// Returns nil if signing is allowed, an error otherwise
//
// Error Conditions:
//   - Returns ErrHeightRegression if height < last height
//   - Returns ErrRoundRegression if same height but round < last round
//   - Returns ErrStepRegression if same H/R but step < last step
//   - Returns ErrDoubleSign if same H/R/S (caller must check if same vote)
func (lss *LastSignState) CheckHRS(height int64, round int32, step int8) error

// VoteStep returns the step value for a vote type
// Panics on invalid vote type (programming error).
func VoteStep(voteType gen.VoteType) int8
```

---

## FilePV

```go
// FilePV is a file-based private validator
// Use LoadFilePV or NewFilePV to create instances.
```

**Functions:**

```go
// LoadFilePV loads an existing file-based private validator
// Returns error if key or state files don't exist.
// Use this for production to ensure no accidental key regeneration.
// Acquires an exclusive file lock to prevent multi-process double-signing.
// Call Close() to release the lock when done.
//
// Error Conditions:
//   - Returns error if key file doesn't exist or has insecure permissions
//   - Returns error if state file doesn't exist
//   - Returns error if lock acquisition fails (another process using validator)
//
// Thread Safety: Not goroutine-safe. Caller must ensure single instance per key.
// Resource Management: Call Close() to release lock and clean up.
func LoadFilePV(keyFilePath, stateFilePath string) (*FilePV, error)

// NewFilePV creates or loads a file-based private validator
// If files don't exist, generates new key and state.
// WARNING: Use LoadFilePV in production to avoid accidental key regeneration.
// Acquires an exclusive file lock to prevent multi-process double-signing.
// Call Close() to release the lock when done.
//
// Thread Safety: Not goroutine-safe. Caller must ensure single instance per key.
// Resource Management: Call Close() to release lock and clean up.
func NewFilePV(keyFilePath, stateFilePath string) (*FilePV, error)

// GenerateFilePV generates a new file-based private validator
// Acquires an exclusive file lock to prevent multi-process double-signing.
// Call Close() to release the lock when done.
//
// Thread Safety: Not goroutine-safe. Caller must ensure single instance per key.
// Resource Management: Call Close() to release lock and clean up.
func GenerateFilePV(keyFilePath, stateFilePath string) (*FilePV, error)
```

**FilePV Methods:**

```go
// GetPubKey returns the public key
// Thread Safety: Goroutine-safe. Returns deep copy.
func (pv *FilePV) GetPubKey() types.PublicKey

// SignVote signs a vote, checking for double-sign
// Mutates vote.Signature field if successful.
// Persists state BEFORE returning to ensure atomic persistence.
//
// Error Conditions:
//   - Returns ErrDoubleSign if signing would violate safety
//   - Returns ErrHeightRegression if height < last height
//   - Returns ErrRoundRegression if regressing in round
//   - Returns ErrStepRegression if regressing in step
//   - Returns error if state persistence fails
//
// Thread Safety: Goroutine-safe (internally synchronized).
// Blocking: Yes, persists state before returning.
func (pv *FilePV) SignVote(chainID string, vote *gen.Vote) error

// SignProposal signs a proposal
// Mutates proposal.Signature field if successful.
// Persists state BEFORE returning to ensure atomic persistence.
//
// Thread Safety: Goroutine-safe (internally synchronized).
// Blocking: Yes, persists state before returning.
func (pv *FilePV) SignProposal(chainID string, proposal *gen.Proposal) error

// GetAddress returns the validator address (SHA-256 of public key)
// Thread Safety: Goroutine-safe.
func (pv *FilePV) GetAddress() []byte

// Close releases the file lock
// Thread Safety: Goroutine-safe.
func (pv *FilePV) Close() error
```

**Example:**

```go
// Load existing validator (production)
pv, err := privval.LoadFilePV("key.json", "state.json")
if err != nil {
    log.Fatal(err)
}
defer pv.Close()

// Or create new validator (testing/initial setup)
pv, err := privval.NewFilePV("key.json", "state.json")
if err != nil {
    log.Fatal(err)
}
defer pv.Close()

// Sign a vote
vote := &types.Vote{
    Type:           types.VoteTypePrevote,
    Height:         100,
    Round:          0,
    BlockHash:      &blockHash,
    Timestamp:      time.Now().UnixNano(),
    Validator:      types.NewAccountName("alice"),
    ValidatorIndex: 0,
}

err = pv.SignVote("my-chain", vote)
if err == privval.ErrDoubleSign {
    log.Fatal("Attempted to double-sign!")
}
if err != nil {
    log.Fatal(err)
}

// vote.Signature now contains the signature
```

---

## Package evidence

Package evidence implements Byzantine fault detection and evidence management.

The evidence pool collects, validates, and broadcasts proofs of Byzantine behavior by validators. The primary type of evidence is duplicate voting (equivocation), where a validator signs two conflicting votes at the same height/round/step.

### Overview

**Evidence Types**:
- **DuplicateVoteEvidence**: Proof that a validator double-signed. Contains two conflicting votes (VoteA and VoteB) with identical height/round/step but different block hashes, both signed by the same validator.

**Evidence Lifecycle**:
1. **Detect**: Node observes conflicting votes from same validator
2. **Create**: Construct DuplicateVoteEvidence with both votes
3. **Validate**: Verify signatures and check evidence rules
4. **Broadcast**: Gossip evidence to all peers
5. **Commit**: Include in block for on-chain punishment
6. **Punish**: Application layer slashes validator stake

**Byzantine Behavior**:

Double-signing is the most common Byzantine fault:
- Validator prevotes for two different blocks at same height/round
- Validator precommits for two different blocks at same height/round
- Could be malicious attack or key compromise

Evidence proves the fault cryptographically and enables slashing.

**Expiration**: Evidence has a limited validity window (e.g., 100,000 blocks). After MaxEvidenceAge, evidence is considered stale and ignored. This prevents long-range attacks and limits state growth.

**Punishment**: The consensus layer detects and reports evidence, but punishment is application-specific. Typical penalties:
- Slash validator stake (e.g., 5% penalty)
- Remove validator from active set ("jailing")
- Blacklist validator from rejoining

---

## Pool

```go
// Pool manages Byzantine evidence
type Pool struct {
    // (internal fields)
}

// Config holds evidence pool configuration
type Config struct {
    MaxAge       time.Duration  // Maximum age of evidence
    MaxAgeBlocks int64          // Maximum block height age
    MaxBytes     int64          // Maximum evidence size in blocks
}
```

**Constants:**

```go
const (
    // MaxSeenVotes limits memory usage for equivocation detection
    MaxSeenVotes = 100000

    // VoteProtectionWindow prevents losing uncommitted evidence
    VoteProtectionWindow = 1000

    // MaxPendingEvidence prevents unbounded memory growth
    MaxPendingEvidence = 10000
)
```

**Errors:**

```go
var (
    ErrInvalidEvidence   = errors.New("invalid evidence")
    ErrDuplicateEvidence = errors.New("duplicate evidence")
    ErrEvidenceExpired   = errors.New("evidence expired")
    ErrEvidenceNotFound  = errors.New("evidence not found")
    ErrInvalidVoteHeight = errors.New("votes have different heights")
    ErrInvalidVoteRound  = errors.New("votes have different rounds")
    ErrInvalidVoteType   = errors.New("votes have different types")
    ErrInvalidValidator  = errors.New("votes from different validators")
    ErrSameBlockHash     = errors.New("votes for same block are not equivocation")
    ErrVotePoolFull      = errors.New("vote pool full")
)
```

**Functions:**

```go
// NewPool creates a new evidence pool
func NewPool(config Config) *Pool

// DefaultConfig returns default evidence pool configuration
func DefaultConfig() Config
```

**Pool Methods:**

```go
// Update updates the pool's knowledge of current height and time
// Prunes expired evidence.
// Thread Safety: Goroutine-safe.
func (p *Pool) Update(height int64, blockTime time.Time)

// CheckVote checks a vote for equivocation and returns evidence if found
// Verifies vote signature before storing.
// If chainID is empty, signature verification is skipped (testing only).
//
// Error Conditions:
//   - Returns error if vote is nil
//   - Returns error if valSet is nil
//   - Returns ErrInvalidValidator if validator not in set
//   - Returns error if signature verification fails
//   - Returns ErrVotePoolFull if cannot track more votes
//
// Thread Safety: Goroutine-safe (internally synchronized).
func (p *Pool) CheckVote(
    vote *gen.Vote,
    valSet *types.ValidatorSet,
    chainID string,
) (*gen.DuplicateVoteEvidence, error)

// AddEvidence adds evidence to the pool
//
// Error Conditions:
//   - Returns ErrInvalidEvidence if evidence is invalid
//   - Returns ErrDuplicateEvidence if already have this evidence
//   - Returns ErrEvidenceExpired if evidence is too old
//
// Thread Safety: Goroutine-safe (internally synchronized).
func (p *Pool) AddEvidence(ev *gen.Evidence) error

// ListEvidence returns all pending evidence
// Thread Safety: Goroutine-safe. Returns deep copy.
func (p *Pool) ListEvidence() []*gen.Evidence

// GetEvidence returns evidence by hash
// Thread Safety: Goroutine-safe.
func (p *Pool) GetEvidence(hash types.Hash) *gen.Evidence

// IsCommitted returns true if evidence has been committed to a block
// Thread Safety: Goroutine-safe.
func (p *Pool) IsCommitted(ev *gen.Evidence) bool

// MarkCommitted marks evidence as committed
// Thread Safety: Goroutine-safe.
func (p *Pool) MarkCommitted(ev *gen.Evidence)
```

**Example:**

```go
// Create evidence pool
pool := evidence.NewPool(evidence.DefaultConfig())
pool.Update(1, time.Now())

// Check votes for equivocation
ev, err := pool.CheckVote(vote, valSet, "chain-id")
if ev != nil {
    fmt.Println("Found equivocation!")

    // Add to pool
    if err := pool.AddEvidence(&gen.Evidence{
        Type: evidence.EvidenceTypeDuplicateVote,
        Data: ev,
    }); err != nil {
        log.Printf("Failed to add evidence: %v", err)
    }
}

// Get pending evidence for next block
pending := pool.ListEvidence()
block.Evidence = pending

// Mark as committed after block finalization
for _, ev := range block.Evidence {
    pool.MarkCommitted(ev)
}
```

---

## Evidence Validation

```go
// VerifyDuplicateVoteEvidence verifies duplicate vote evidence
//
// Checks:
//   - Votes are from same validator
//   - Votes are at same height/round/type
//   - Votes are for different blocks (conflicting)
//   - Both signatures are valid
//   - Validator was in set at that height
//
// Error Conditions:
//   - Returns error if votes are nil
//   - Returns error if votes don't match H/R/T
//   - Returns error if votes are for same block
//   - Returns error if signatures are invalid
//   - Returns error if validator unknown
//
// Thread Safety: Goroutine-safe.
func VerifyDuplicateVoteEvidence(
    ev *gen.DuplicateVoteEvidence,
    chainID string,
    valSet *types.ValidatorSet,
) error
```

**Example:**

```go
// Verify evidence before accepting
err := evidence.VerifyDuplicateVoteEvidence(ev, "chain-id", valSet)
if err != nil {
    log.Printf("Invalid evidence: %v", err)
    return
}

// Evidence is valid - add to pool
pool.AddEvidence(&gen.Evidence{
    Type: evidence.EvidenceTypeDuplicateVote,
    Data: ev,
})
```

---

## Summary

This API documentation covers the five main packages of Leaderberry:

1. **types**: Core data structures (Hash, Vote, Block, Validator, Account)
2. **engine**: Consensus state machine and coordinator
3. **wal**: Write-ahead log for crash recovery
4. **privval**: Private validator with double-sign prevention
5. **evidence**: Byzantine fault detection and evidence management

All packages follow these principles:
- **Immutability**: Core types return deep copies to prevent corruption
- **Thread Safety**: All public APIs are goroutine-safe
- **Error Handling**: Comprehensive error conditions documented
- **Resource Management**: Clear cleanup requirements (Close(), Stop())
- **Security**: Double-sign prevention, signature verification, permission checks

For complete implementation details, see the source code in the respective package directories.
