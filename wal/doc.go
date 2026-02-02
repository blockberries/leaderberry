// Package wal implements a write-ahead log for consensus crash recovery.
//
// The write-ahead log (WAL) provides durability and crash recovery by persisting
// all consensus messages before they are processed. After a restart, the WAL is
// replayed to restore the consensus state to its last known position.
//
// # Core Interface
//
// WAL defines the interface for writing consensus messages and state changes:
//
//	type WAL interface {
//	    Write(msg WALMessage) error
//	    WriteSync(msg WALMessage) error
//	    Close() error
//	}
//
// # Implementation
//
// FileWAL: Disk-based WAL using length-prefixed messages with CRC32 checksums.
// Messages are buffered for performance and fsync'd on critical operations.
//
// # Message Types
//
// The WAL records different message types as defined in schema/wal.cram:
//
//	- WALMsgEndHeight: Marks successful block commit at a height
//	- WALMsgProposal: Incoming block proposal
//	- WALMsgVote: Incoming prevote or precommit
//	- WALMsgTimeout: Timeout trigger
//
// # File Format
//
// Each entry is encoded as:
//
//	[4 bytes: length][N bytes: Cramberry-encoded message][4 bytes: CRC32]
//
// The length prefix enables fast seeking and validation.
// CRC32 detects corruption from incomplete writes or disk errors.
//
// # Rotation and Cleanup
//
// WAL files are rotated per consensus height to prevent unbounded growth.
// Old WAL files can be safely deleted after block finalization and persistence.
//
//	wal-0000000001-height-0000000001.log
//	wal-0000000002-height-0000000002.log
//
// # Recovery Process
//
// On startup:
//	1. Read all WAL files in order
//	2. Decode and validate each message
//	3. Replay messages through the consensus engine
//	4. Resume consensus from last recorded state
//
// # Thread Safety
//
// FileWAL uses internal locking to ensure thread-safe writes from multiple
// goroutines. However, only one WAL instance should write to a directory.
//
// # Performance Considerations
//
// Regular Write() calls are buffered for throughput.
// WriteSync() forces an fsync for critical safety (e.g., before signing votes).
// Balance durability vs performance based on your consistency requirements.
//
// # Usage Example
//
//	// Create a new file-based WAL
//	wal, err := wal.NewFileWAL("./data/wal")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer wal.Close()
//
//	// Write a proposal (buffered)
//	msg := &generated.WALMsgProposal{Proposal: proposal}
//	err = wal.Write(msg)
//
//	// Write a critical vote (synced)
//	msg = &generated.WALMsgVote{Vote: vote}
//	err = wal.WriteSync(msg)
//
//	// Replay WAL after crash
//	messages, err := wal.ReadAll()
//	for _, msg := range messages {
//	    engine.ReplayMessage(msg)
//	}
package wal
