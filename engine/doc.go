// Package engine implements the Tendermint-style BFT consensus state machine.
//
// The engine coordinates the consensus protocol through these key states:
//
//	NewHeight → NewRound → Propose → Prevote → PrevoteWait → Precommit → PrecommitWait → Commit
//
// # Core Components
//
// Engine: Main consensus coordinator implementing the state machine transitions.
// Manages the full lifecycle of block production and finalization.
//
// State: Internal state tracking for height, round, step, locked block, and valid block.
// Implements Tendermint locking rules and proof-of-lock (POL) mechanics.
//
// VoteTracker: Aggregates prevotes and precommits to detect 2/3+ quorums.
// Tracks voting power per block hash to determine consensus decisions.
//
// TimeoutTicker: Manages round timeouts with exponential backoff.
// Ensures liveness by advancing rounds when progress stalls.
//
// PeerState: Tracks what each peer knows (height/round/votes) for efficient gossiping.
// Enables smart vote and proposal forwarding based on peer knowledge.
//
// BlockSync: Fast-sync mechanism to catch up nodes that are far behind.
// Downloads and verifies historical blocks in parallel.
//
// Replay: Crash recovery from write-ahead log (WAL).
// Restores consensus state and replays messages after restart.
//
// # Usage Example
//
//	// Create validator set
//	vals := []*types.NamedValidator{
//	    {Name: types.NewAccountName("alice"), VotingPower: 100, PublicKey: alicePubKey},
//	    {Name: types.NewAccountName("bob"), VotingPower: 100, PublicKey: bobPubKey},
//	}
//	valSet, _ := types.NewValidatorSet(vals)
//
//	// Create engine
//	cfg := &engine.Config{ChainID: "my-chain", Timeouts: engine.DefaultTimeoutConfig()}
//	eng := engine.NewEngine(cfg, valSet, privVal, wal, nil)
//
//	// Start consensus at height 1
//	eng.Start(1, nil)
//
//	// Process network messages
//	eng.AddProposal(proposal)
//	eng.AddVote(vote)
//
// # Thread Safety
//
// All public methods are thread-safe and use internal locking.
// The engine maintains a single goroutine for state transitions.
//
// # Consensus Properties
//
// Safety: Once a block is committed, it is final and immutable.
// No conflicting blocks can be finalized at the same height.
//
// Liveness: Guaranteed under partial synchrony with 2/3+ honest validators.
// Rounds advance automatically on timeout to ensure progress.
//
// Byzantine Fault Tolerance: Tolerates up to 1/3 Byzantine validators.
// Detects and reports equivocation (double-signing) as evidence.
package engine
