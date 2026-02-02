// Package privval implements private validator functionality with double-sign prevention.
//
// A private validator holds the Ed25519 private key used for signing consensus messages
// (proposals, prevotes, precommits). The key responsibility is preventing double-signing,
// which would constitute Byzantine behavior and violate consensus safety.
//
// # Core Interface
//
// PrivValidator defines the interface for signing consensus messages:
//
//	type PrivValidator interface {
//	    GetPubKey() PublicKey
//	    GetAccountName() types.AccountName
//	    SignVote(chainID string, vote *types.Vote) (types.Signature, error)
//	    SignProposal(chainID string, proposal *types.Proposal) (types.Signature, error)
//	}
//
// # Double-Sign Prevention
//
// LastSignState tracks the last height/round/step/blockhash signed by this validator.
// Before signing any message, the validator checks:
//
//	1. Never sign two different blockhashes at the same height/round/step
//	2. Never regress to a lower height (after restart)
//	3. Persist state BEFORE returning signature (atomic persistence pattern)
//
// This prevents equivocation even across crashes and restarts.
//
// # Implementation
//
// FilePV: File-based private validator with two files:
//
//	- key.json: Ed25519 private key and account name (rarely changes)
//	- state.json: LastSignState (updated on every signature)
//
// The files use atomic write-then-rename to ensure consistency even if
// the process crashes mid-write.
//
// # File Format
//
// key.json:
//
//	{
//	  "account_name": "alice",
//	  "pub_key": "03A2B5...",
//	  "priv_key": "F3C1D2..."
//	}
//
// state.json:
//
//	{
//	  "height": 100,
//	  "round": 2,
//	  "step": 3,
//	  "block_hash": "A1B2C3..."
//	}
//
// # Security Considerations
//
// Private Key Protection:
//	- Key file should have restricted permissions (0600)
//	- Never log or expose the private key
//	- Consider HSM integration for production
//
// Double-Sign Protection:
//	- State file MUST be persisted before returning signature
//	- File operations use fsync() to ensure durability
//	- Lock file prevents concurrent access
//
// # Thread Safety
//
// FilePV uses internal locking to prevent concurrent signing.
// Only one FilePV instance should access the same key/state files.
//
// # Remote Signers
//
// For production deployments, consider:
//	- Hardware security modules (HSMs)
//	- Remote signing services with network protocol
//	- Key sharding and threshold signatures
//
// The PrivValidator interface supports these via custom implementations.
//
// # Usage Example
//
//	// Create or load a private validator
//	pv, err := privval.NewFilePV("key.json", "state.json")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Sign a vote
//	vote := &types.Vote{
//	    Type:           types.VoteTypePrevote,
//	    Height:         100,
//	    Round:          0,
//	    BlockHash:      blockHash,
//	    ValidatorName:  pv.GetAccountName(),
//	}
//	signature, err := pv.SignVote("my-chain", vote)
//	if err != nil {
//	    log.Fatal(err) // Might be ErrDoubleSign
//	}
//	vote.Signature = signature
package privval
