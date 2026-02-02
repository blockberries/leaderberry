// Package evidence implements Byzantine fault detection and evidence management.
//
// The evidence pool collects, validates, and broadcasts proofs of Byzantine behavior
// by validators. The primary type of evidence is duplicate voting (equivocation),
// where a validator signs two conflicting votes at the same height/round/step.
//
// # Evidence Types
//
// DuplicateVoteEvidence: Proof that a validator double-signed.
// Contains two conflicting votes (VoteA and VoteB) with identical height/round/step
// but different block hashes, both signed by the same validator.
//
// # Core Interface
//
// Pool manages the evidence lifecycle:
//
//	type Pool interface {
//	    AddEvidence(ev Evidence) error
//	    GetEvidence(hash Hash) Evidence
//	    ListEvidence() []Evidence
//	    IsCommitted(ev Evidence) bool
//	    MarkCommitted(ev Evidence)
//	}
//
// # Evidence Validation
//
// Before accepting evidence, the pool validates:
//
//	1. Votes are from the same validator (matching ValidatorName/Index)
//	2. Votes are at the same height/round/step
//	3. Votes are for different blocks (conflicting)
//	4. Both signatures are valid
//	5. Evidence is not expired (within MaxEvidenceAge)
//	6. Validator was in the validator set at that height
//
// # Evidence Lifecycle
//
//	1. Detect: Node observes conflicting votes from same validator
//	2. Create: Construct DuplicateVoteEvidence with both votes
//	3. Validate: Verify signatures and check evidence rules
//	4. Broadcast: Gossip evidence to all peers
//	5. Commit: Include in block for on-chain punishment
//	6. Punish: Application layer slashes validator stake
//
// # Byzantine Behavior
//
// Double-signing is the most common Byzantine fault:
//	- Validator prevotes for two different blocks at same height/round
//	- Validator precommits for two different blocks at same height/round
//	- Could be malicious attack or key compromise
//
// Evidence proves the fault cryptographically and enables slashing.
//
// # Expiration
//
// Evidence has a limited validity window (e.g., 100,000 blocks).
// After MaxEvidenceAge, evidence is considered stale and ignored.
// This prevents long-range attacks and limits state growth.
//
// # Punishment
//
// The consensus layer detects and reports evidence, but punishment is
// application-specific. Typical penalties:
//	- Slash validator stake (e.g., 5% penalty)
//	- Remove validator from active set ("jailing")
//	- Blacklist validator from rejoining
//
// # Thread Safety
//
// The Pool implementation uses internal locking for concurrent access.
// Multiple goroutines can safely add and query evidence.
//
// # Usage Example
//
//	// Create evidence pool
//	pool := evidence.NewPool()
//
//	// Add evidence when detecting conflicting votes
//	if vote1.Height == vote2.Height &&
//	   vote1.Round == vote2.Round &&
//	   vote1.Type == vote2.Type &&
//	   vote1.BlockHash != vote2.BlockHash &&
//	   vote1.ValidatorName == vote2.ValidatorName {
//	    ev := &types.DuplicateVoteEvidence{
//	        VoteA: vote1,
//	        VoteB: vote2,
//	    }
//	    err := pool.AddEvidence(ev)
//	}
//
//	// Get pending evidence for next block
//	pending := pool.ListEvidence()
//	block.Evidence = pending
//
//	// Mark evidence as committed after block finalization
//	for _, ev := range block.Evidence {
//	    pool.MarkCommitted(ev)
//	}
package evidence
