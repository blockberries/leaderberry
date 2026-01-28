# Changelog

All notable changes to the Leaderberry consensus engine will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

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
