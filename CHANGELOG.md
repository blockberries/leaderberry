# Changelog

All notable changes to the Leaderberry consensus engine will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

## [0.5.0] - 2026-01-28 - Second Refactor Phase 3

Remaining medium and low severity fixes from comprehensive code review.

### Medium Severity Fixes (M2-M4, M6)

#### M2: POL (Proof of Lock) Validation
- Added `validatePOL()` function in consensus state
- Validates POL votes have correct signatures, height/round, and block hash
- Verifies 2/3+ voting power in POL
- Rejects proposals with invalid POL

#### M3: Evidence Pool Integration
- Added `evidencePool` field to ConsensusState
- `SetEvidencePool()` method to configure evidence pool
- `handleVote()` now checks for equivocation before processing
- Detected equivocation is logged and added to evidence pool

#### M4: Message Length Check
- Fixed minimum length check from `< 2` to `< 1`
- Added explicit empty payload checks for each message type
- Returns descriptive error for empty proposal/vote payloads

#### M6: isSameVote Documentation
- Added documentation explaining why block hash comparison is sufficient
- CheckHRS already validates H/R/S match before isSameVote is called

### Low Severity Fixes (L4-L6)

#### L4: Evidence Size Estimate
- Replaced arbitrary 50-byte overhead with proper calculation
- Added `evidenceSize()` function with documented schema-based overhead
- Evidence overhead: 24 bytes (Type + Height + Time + length prefix)

#### L5: ValidatorSet Nil Name Check
- `NewValidatorSet()` now rejects validators with empty names
- Added `ErrEmptyValidatorName` error
- Prevents potential panics in `Hash()` when sorting by name

#### L6: Document centerPriorities Precision
- Added documentation explaining integer division precision loss
- Precision loss is acceptable for bounded priority maintenance

### Added
- `validatePOL()` method for POL validation
- `SetEvidencePool()` method for evidence pool configuration
- `evidenceSize()` helper function
- `ErrEmptyValidatorName` error constant

## [0.4.0] - 2026-01-28 - Second Refactor Phase 2

Additional high, medium, and low severity fixes from comprehensive code review.

### High Severity Fixes (H3-H6)

#### H3: ScheduleTimeout Non-Blocking
- `ScheduleTimeout` now uses non-blocking send with select
- Drops timeouts if channel is full (logged with counter)
- Prevents caller from hanging indefinitely

#### H4: TimeoutTicker.Stop Waits for Goroutine
- Added `sync.WaitGroup` to track goroutine lifecycle
- `Stop()` now waits for `run()` goroutine to exit
- Prevents use-after-close and dangling callbacks

#### H5: TwoThirdsMajority Overflow Protection
- Reordered calculation to avoid `TotalPower * 2` overflow
- Uses `(total/3 + total/3 + adjustment)` pattern
- Mathematically equivalent but overflow-safe

#### H6: WAL Legacy Migration Error Handling
- `findHighestSegmentIndex` now returns error on migration failure
- Prevents silent data loss if rename fails
- Logs migration success for debugging

### Medium Severity Fixes (M1, M5)

#### M1: Deep Copy in ValidatorSet.Copy
- `CopyAccountName()` helper for deep copying AccountName
- `ValidatorSet.Copy()` now deep copies Name and PublicKey.Data
- Prevents shared references between copies

#### M5: GetAddress Returns Copy
- `FilePV.GetAddress()` now returns a copy of the address bytes
- Prevents callers from modifying internal state

### Low Severity Fixes (L1, L3)

#### L1: Hex Encoding for Block Hash Keys
- `blockHashKey()` now uses `hex.EncodeToString` instead of raw binary
- Improves debuggability of vote tracking
- Returns "nil" for nil/empty hashes instead of empty string

#### L3: Use sort.Ints for WAL Segments
- Replaced O(n²) bubble sort with `sort.Ints()`
- Standard library implementation is more efficient

### Added
- `DroppedSchedules()` method to TimeoutTicker for monitoring
- `CopyAccountName()` helper function in types package

## [0.3.0] - 2026-01-28 - Second Refactor Phase 1

Critical fixes for deadlocks, data races, and consensus safety identified during comprehensive code review.

### Critical Fixes (CR1-CR5)

#### CR1: Deadlock in finalizeCommit → enterNewRound
- Introduced "Locked" pattern for all state transition functions
- Public functions acquire lock, internal "Locked" versions assume lock held
- `finalizeCommit` now calls `enterNewRoundLocked` when `SkipTimeoutCommit=true`

#### CR2: Race Condition in HeightVoteSet.AddVote
- Keep lock held during entire `AddVote` operation
- Same fix applied to `SetPeerMaj23`
- Prevents race with `Reset()` clearing vote sets

#### CR3: Race Condition in ConsensusState.Start
- `Start()` now calls `enterNewRoundLocked` while holding lock
- Ensures no gap where state can be modified by other goroutines

#### CR4: Data Race on ValidatorSet
- Added `WithIncrementedPriority()` immutable method
- Returns new `ValidatorSet` copy with priorities incremented
- Original set is not modified - safe for concurrent access
- Marked `IncrementProposerPriority()` as deprecated

#### CR5: Ignored Vote Error After Signing
- `signAndSendVoteLocked` now PANICs if own vote fails to add
- Own vote must always be tracked - failure indicates consensus corruption

### High Severity Fixes (H1-H2)

#### H1: WAL Writes During Consensus
- Added WAL writes BEFORE processing proposals and votes
- PANICs on WAL write failure (consensus critical)
- Writes `EndHeight` after block commit

#### H2: Pointer to Proposal Block Field (Aliasing Bug)
- `handleProposal` now makes value copy of block before storing pointer
- Prevents issues if proposal is modified or garbage collected

### Medium Severity Fixes (M7-M8)

#### M7: enterCommit Nil Block Handling
- PANICs if `enterCommit` called but no commit found
- Indicates bug in state machine that must be fixed

#### M8: Broadcast Callbacks
- Added `onProposal` and `onVote` callback fields
- `SetBroadcastCallbacks()` method for Engine to register
- Callbacks invoked after creating proposals/votes

### Changed
- All state transitions have public (lock-acquiring) and internal (Locked) versions
- `ValidatorSet.Copy()` used by `WithIncrementedPriority()` for immutable updates
- WAL is now written to during consensus operations

### Added
- SECOND_REFACTOR.md documenting comprehensive code review findings

## [0.2.0] - 2026-01-28 - First Refactor

Major refactoring to address critical, high, and medium severity issues identified during code review.

### Critical Fixes (C1-C7)

#### C1: Proposal Double-Sign Protection
- Added double-sign prevention for proposals in `privval/file_pv.go`
- Proposals now check `LastSignState.CheckHRS()` before signing
- Same proposal re-signing returns cached signature (idempotent)
- State persisted with PANIC on failure

#### C2: Atomic State File Writes
- Implemented atomic write pattern (temp file → sync → rename → dir sync) in `privval/file_pv.go`
- Applied to both `saveKey()` and `saveState()`
- Prevents state file corruption on crash

#### C3: Safe Constructors for Network Input
- `NewHash`, `NewSignature`, `NewPublicKey` now return `(T, error)` for untrusted input
- Added `MustNewHash`, `MustNewSignature`, `MustNewPublicKey` for internal trusted use
- Updated all call sites appropriately

#### C4: Message Dropping Metrics
- Added `droppedProposals` and `droppedVotes` counters in `engine/state.go`
- Logging warnings when messages are dropped due to full channels
- Added `GetDroppedMessageCounts()` for monitoring

#### C5: Nil BlockExecutor Checks
- Added nil checks before all `blockExecutor` calls in `engine/state.go`
- Documented behavior when executor is nil

#### C6: Thread-Safe FlushAndSync
- Split into public `FlushAndSync()` (acquires lock) and internal `flushAndSync()` in `wal/file_wal.go`
- Prevents race conditions on concurrent flush calls

#### C7: Deterministic Validator Set Hash
- Fixed `ValidatorSet.Hash()` to use sorted validators for serialization
- Ensures same validator set produces same hash regardless of insertion order

### High Severity Fixes (H1-H7)

#### H1: MakeCommit Nil Dereference
- `VoteSet.MakeCommit()` now returns nil for nil block commits
- Prevents panic when 2/3+ vote for nil

#### H2: Validator Index Overflow Check
- Added `MaxValidators = 65535` constant
- `NewValidatorSet` returns error if validator count exceeds limit

#### H3: Integer Overflow Protection in Priority Calculations
- Added `MaxTotalVotingPower` and `PriorityWindowSize` constants
- Priority values clamped to prevent overflow in `IncrementProposerPriority`

#### H4: Strict Key File Loading
- Added `LoadFilePV()` that requires existing files (for production)
- `GenerateFilePV()` explicitly creates new keys
- Prevents accidental key regeneration on missing files

#### H5: Safe State File Loading
- Uses error-returning constructors when loading signature/hash from state file
- Corrupted state files now return errors instead of panicking

#### H6: Message Type Discriminator
- Added `ConsensusMessageType` with explicit type prefix byte
- `HandleConsensusMessage` uses type byte to route messages
- Added `EncodeProposalMessage` and `EncodeVoteMessage` helpers

#### H7: Panic on Block Application Failure
- `finalizeCommit` now PANICs if `ApplyBlock` fails
- Consensus has decided; failure is catastrophic

### Medium Severity Fixes (M1-M8)

#### M1: WAL Rotation
- Implemented segment-based WAL with configurable max size (64MB default)
- Files named `wal-00000`, `wal-00001`, etc.
- Automatic rotation when segment exceeds max size
- Legacy `wal` file migrated to `wal-00000` on startup

#### M2: WAL Checksums
- Added CRC32 checksum to each WAL message
- Format: `[4-byte length][data][4-byte CRC32]`
- Decoder verifies checksum, returns `ErrWALCorrupted` on mismatch

#### M3: WAL Height Index
- Added `heightIndex map[int64]int` for O(1) height lookup
- Index built on startup by scanning segments
- `SearchForEndHeight` uses index for fast lookups

#### M4: Peer Maj23 Tracking
- Added `peerMaj23 map[string]*types.Hash` to `VoteSet`
- `SetPeerMaj23()`, `GetPeerMaj23Claims()`, `HasPeerMaj23()` methods
- Applied to both `VoteSet` and `HeightVoteSet`

#### M5: Evidence Key Uniqueness
- Evidence key now includes SHA256 hash prefix of data
- Format: `type/height/time/hash[:8]`
- Prevents collisions with same metadata but different evidence

#### M6: Deduplicated VoteSet
- Removed unused `types.VoteSet` implementation
- `engine.VoteSet` is now the canonical implementation
- Added comment in `types/vote.go` pointing to engine implementation

#### M7: Config Validation
- Added `TimeoutConfig.Validate()` with comprehensive checks
- All timeouts must be positive and ≤ 5 minutes
- Delta values must be non-negative

#### M8: Timeout Handling Improvements
- Increased `timeoutChannelSize` to 100
- Added `droppedTimeouts` counter with logging
- Added `DroppedTimeouts()` method for monitoring

### Low Severity Fixes (L1, L3)

#### L1: ValidatorSet.Copy Error Handling
- `Copy()` now returns `(*ValidatorSet, error)`
- Propagates errors from `NewValidatorSet`

#### L3: Consistent Error Handling Audit
- Fixed ignored marshaling errors in `types/block.go`, `types/vote.go`, `types/proposal.go`
- All consensus-critical marshal failures now PANIC
- Refactored WAL encoder to return `(int, error)` to track bytes written

### Deferred
- L2: AccountName pointer string pattern (requires schema change)

### Changed
- `encoder.Encode()` now returns `(int, error)` instead of `error`
- `ValidatorSet.Copy()` now returns `(*ValidatorSet, error)` instead of `*ValidatorSet`
- Evidence keys include hash prefix for uniqueness
- WAL uses segmented files instead of single file

### Documentation
- Added FIRST_REFACTOR.md documenting all issues and implementation status
- Added comprehensive error handling philosophy (PANIC vs ERROR)

## [0.1.0] - 2026-01-XX - Initial Release

### Added
- Initial implementation of Tendermint-style BFT consensus
- Consensus state machine (Propose → Prevote → Precommit → Commit)
- Write-ahead log (WAL) for crash recovery
- Private validator with double-sign prevention
- Evidence pool for Byzantine fault detection
- Vote tracking with quorum detection
- Proposer selection with priority-based rotation
- Integration tests for basic consensus flow
