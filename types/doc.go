// Package types defines the core data structures for the Leaderberry consensus protocol.
//
// This package provides both Cramberry-generated types (in types/generated/) and
// hand-written extensions with methods and validation logic.
//
// # Core Types
//
// Block: A finalized block containing batch certificates and metadata.
// Blocks reference Looseberry DAG batches rather than raw transactions.
//
// Vote: A signed prevote or precommit from a validator.
// Votes include height, round, type, block hash, and validator signature.
//
// Proposal: A block proposal with optional proof-of-lock (POL) from prior rounds.
// POL votes demonstrate why a proposer is locked on a specific block.
//
// Account: Named account with weighted multi-signature authorization.
// Supports hierarchical delegation and cycle detection.
//
// NamedValidator: A validator identified by human-readable name with voting power.
// Validators use Ed25519 keys for signing consensus messages.
//
// ValidatorSet: Immutable set of validators with total voting power computation.
// Determines proposer selection via weighted round-robin.
//
// Evidence: Proof of Byzantine behavior (e.g., double-signing).
// Includes conflicting votes signed by the same validator at the same height/round/step.
//
// # Transaction Handling
//
// Consensus treats transaction payloads as opaque bytes. Only authorization metadata
// (signatures, account names, nonces) is interpreted by the consensus layer.
// Actual transaction execution and validation is the application layer's responsibility.
//
// # Serialization
//
// All network-serializable types are defined in Cramberry schemas (../schema/*.cram).
// Generated code provides Encode/Decode methods for deterministic binary serialization.
//
// # Hashing
//
// Blocks, votes, and proposals use SHA-256 hashing. The Hash type wraps [32]byte
// with hex encoding for human readability and JSON compatibility.
//
// # Immutability
//
// Core types like Block, Vote, and ValidatorSet are designed to be immutable.
// Methods return copies rather than exposing internal state for modification.
// This ensures thread-safe sharing and prevents accidental mutation.
//
// # Usage Example
//
//	// Create a validator set
//	vals := []*types.NamedValidator{
//	    {Name: types.NewAccountName("alice"), Index: 0, VotingPower: 100, PublicKey: pubKey1},
//	    {Name: types.NewAccountName("bob"), Index: 1, VotingPower: 100, PublicKey: pubKey2},
//	}
//	valSet, err := types.NewValidatorSet(vals)
//
//	// Create and sign a vote
//	vote := &types.Vote{
//	    Type:             types.VoteTypePrevote,
//	    Height:           1,
//	    Round:            0,
//	    BlockHash:        blockHash,
//	    ValidatorName:    types.NewAccountName("alice"),
//	    ValidatorIndex:   0,
//	}
//	signature, err := privVal.SignVote("chain-id", vote)
//	vote.Signature = signature
//
//	// Verify the vote
//	validator := valSet.GetByIndex(0)
//	err = vote.Verify("chain-id", validator.PublicKey)
package types
