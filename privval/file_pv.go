package privval

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

const (
	keyFilePerm   = 0600
	stateFilePerm = 0600
)

// FilePV is a file-based private validator
type FilePV struct {
	mu sync.Mutex

	// Key file path
	keyFilePath string
	// State file path
	stateFilePath string

	// Key material
	pubKey  types.PublicKey
	privKey ed25519.PrivateKey

	// Last sign state (for double-sign prevention)
	lastSignState LastSignState
}

// FilePVKey represents the key file structure
type FilePVKey struct {
	PubKey  []byte `json:"pub_key"`
	PrivKey []byte `json:"priv_key"`
}

// FilePVState represents the state file structure
type FilePVState struct {
	Height    int64  `json:"height"`
	Round     int32  `json:"round"`
	Step      int8   `json:"step"`
	Signature []byte `json:"signature,omitempty"`
	BlockHash []byte `json:"block_hash,omitempty"`
	// TWELFTH_REFACTOR: Hash of complete sign bytes for accurate idempotency check
	SignBytesHash []byte `json:"sign_bytes_hash,omitempty"`
	// TWELFTH_REFACTOR: Timestamp for idempotency check
	Timestamp int64 `json:"timestamp,omitempty"`
}

// LoadFilePV loads an existing file-based private validator.
// Returns error if key or state files don't exist.
// Use this for production to ensure no accidental key regeneration.
func LoadFilePV(keyFilePath, stateFilePath string) (*FilePV, error) {
	pv := &FilePV{
		keyFilePath:   keyFilePath,
		stateFilePath: stateFilePath,
	}

	// Load key (must exist)
	if err := pv.loadKeyStrict(); err != nil {
		return nil, fmt.Errorf("failed to load key: %w", err)
	}

	// Load state (must exist)
	if err := pv.loadStateStrict(); err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	return pv, nil
}

// NewFilePV creates or loads a file-based private validator.
// If files don't exist, generates new key and state.
// WARNING: Use LoadFilePV in production to avoid accidental key regeneration.
func NewFilePV(keyFilePath, stateFilePath string) (*FilePV, error) {
	pv := &FilePV{
		keyFilePath:   keyFilePath,
		stateFilePath: stateFilePath,
	}

	// Load or generate key
	if err := pv.loadKey(); err != nil {
		return nil, err
	}

	// Load or initialize state
	if err := pv.loadState(); err != nil {
		return nil, err
	}

	return pv, nil
}

// GenerateFilePV generates a new file-based private validator
func GenerateFilePV(keyFilePath, stateFilePath string) (*FilePV, error) {
	// Generate new key pair
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	pv := &FilePV{
		keyFilePath:   keyFilePath,
		stateFilePath: stateFilePath,
		pubKey:        types.MustNewPublicKey(pubKey), // ed25519 output is always valid
		privKey:       privKey,
	}

	// Save key
	if err := pv.saveKey(); err != nil {
		return nil, err
	}

	// Save initial state
	if err := pv.saveState(); err != nil {
		return nil, err
	}

	return pv, nil
}

// loadKeyStrict loads the key from file, failing if it doesn't exist
func (pv *FilePV) loadKeyStrict() error {
	// SEVENTH_REFACTOR: Check file permissions before loading
	// Key files should have 0600 permissions (owner read/write only)
	info, err := os.Stat(pv.keyFilePath)
	if err != nil {
		return fmt.Errorf("failed to stat key file %s: %w", pv.keyFilePath, err)
	}

	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		// File is readable/writable by group or others
		return fmt.Errorf("key file %s has insecure permissions %o (expected 0600 or stricter)", pv.keyFilePath, perm)
	}

	data, err := os.ReadFile(pv.keyFilePath)
	if err != nil {
		return fmt.Errorf("failed to read key file %s: %w", pv.keyFilePath, err)
	}

	var key FilePVKey
	if err := json.Unmarshal(data, &key); err != nil {
		return fmt.Errorf("failed to parse key file: %w", err)
	}

	if len(key.PubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: %d (expected %d)", len(key.PubKey), ed25519.PublicKeySize)
	}
	if len(key.PrivKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid private key size: %d (expected %d)", len(key.PrivKey), ed25519.PrivateKeySize)
	}

	// TWENTY_FIRST_REFACTOR: Verify public key matches private key
	// In Ed25519, the private key (64 bytes) embeds the public key in bytes [32:64]
	if !bytes.Equal(key.PubKey, key.PrivKey[32:]) {
		return fmt.Errorf("public key mismatch with private key")
	}

	pv.pubKey = types.MustNewPublicKey(key.PubKey)
	pv.privKey = key.PrivKey

	return nil
}

// loadKey loads the key from file, generating if it doesn't exist
func (pv *FilePV) loadKey() error {
	data, err := os.ReadFile(pv.keyFilePath)
	if os.IsNotExist(err) {
		// Generate new key
		pubKey, privKey, err := ed25519.GenerateKey(nil)
		if err != nil {
			return fmt.Errorf("failed to generate key: %w", err)
		}
		pv.pubKey = types.MustNewPublicKey(pubKey) // ed25519 output is always valid
		pv.privKey = privKey
		return pv.saveKey()
	}
	if err != nil {
		return fmt.Errorf("failed to read key file: %w", err)
	}

	var key FilePVKey
	if err := json.Unmarshal(data, &key); err != nil {
		return fmt.Errorf("failed to parse key file: %w", err)
	}

	if len(key.PubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: %d", len(key.PubKey))
	}
	if len(key.PrivKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid private key size: %d", len(key.PrivKey))
	}

	// TWENTY_FIRST_REFACTOR: Verify public key matches private key
	// In Ed25519, the private key (64 bytes) embeds the public key in bytes [32:64]
	if !bytes.Equal(key.PubKey, key.PrivKey[32:]) {
		return fmt.Errorf("public key mismatch with private key")
	}

	// Size already validated above, safe to use Must
	pv.pubKey = types.MustNewPublicKey(key.PubKey)
	pv.privKey = key.PrivKey

	return nil
}

// saveKey saves the key to file using atomic write (temp + rename)
func (pv *FilePV) saveKey() error {
	// Ensure directory exists
	dir := filepath.Dir(pv.keyFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create key directory: %w", err)
	}

	key := FilePVKey{
		PubKey:  pv.pubKey.Data,
		PrivKey: pv.privKey,
	}

	data, err := json.MarshalIndent(key, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal key: %w", err)
	}

	// Atomic write: write to temp file, sync, then rename
	tmpPath := pv.keyFilePath + ".tmp"

	if err := os.WriteFile(tmpPath, data, keyFilePerm); err != nil {
		return fmt.Errorf("failed to write temp key file: %w", err)
	}

	// Sync temp file
	tmpFile, err := os.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath) // L5: Clean up temp file
		return fmt.Errorf("failed to open temp key file for sync: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath) // L5: Clean up temp file
		return fmt.Errorf("failed to sync temp key file: %w", err)
	}
	tmpFile.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, pv.keyFilePath); err != nil {
		os.Remove(tmpPath) // L5: Clean up temp file
		return fmt.Errorf("failed to rename key file: %w", err)
	}

	// Sync directory
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open dir for sync: %w", err)
	}
	if err := dirFile.Sync(); err != nil {
		dirFile.Close()
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	dirFile.Close()

	return nil
}

// loadStateStrict loads the state from file, failing if it doesn't exist
func (pv *FilePV) loadStateStrict() error {
	data, err := os.ReadFile(pv.stateFilePath)
	if err != nil {
		return fmt.Errorf("failed to read state file %s: %w", pv.stateFilePath, err)
	}

	var state FilePVState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to parse state file: %w", err)
	}

	pv.lastSignState = LastSignState{
		Height: state.Height,
		Round:  state.Round,
		Step:   state.Step,
	}

	if len(state.Signature) > 0 {
		sig, err := types.NewSignature(state.Signature)
		if err != nil {
			return fmt.Errorf("invalid signature in state file: %w", err)
		}
		pv.lastSignState.Signature = sig
	}

	if len(state.BlockHash) > 0 {
		hash, err := types.NewHash(state.BlockHash)
		if err != nil {
			return fmt.Errorf("invalid block hash in state file: %w", err)
		}
		pv.lastSignState.BlockHash = &hash
	}

	// TWELFTH_REFACTOR: Load sign bytes hash for accurate idempotency check
	if len(state.SignBytesHash) > 0 {
		hash, err := types.NewHash(state.SignBytesHash)
		if err != nil {
			return fmt.Errorf("invalid sign bytes hash in state file: %w", err)
		}
		pv.lastSignState.SignBytesHash = &hash
	}

	// TWELFTH_REFACTOR: Load timestamp for idempotency check
	pv.lastSignState.Timestamp = state.Timestamp

	return nil
}

// loadState loads the state from file, initializing if it doesn't exist
func (pv *FilePV) loadState() error {
	data, err := os.ReadFile(pv.stateFilePath)
	if os.IsNotExist(err) {
		// Initialize empty state
		pv.lastSignState = LastSignState{}
		return pv.saveState()
	}
	if err != nil {
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var state FilePVState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to parse state file: %w", err)
	}

	pv.lastSignState = LastSignState{
		Height: state.Height,
		Round:  state.Round,
		Step:   state.Step,
	}

	if len(state.Signature) > 0 {
		sig, err := types.NewSignature(state.Signature)
		if err != nil {
			return fmt.Errorf("invalid signature in state file: %w", err)
		}
		pv.lastSignState.Signature = sig
	}

	if len(state.BlockHash) > 0 {
		hash, err := types.NewHash(state.BlockHash)
		if err != nil {
			return fmt.Errorf("invalid block hash in state file: %w", err)
		}
		pv.lastSignState.BlockHash = &hash
	}

	// TWELFTH_REFACTOR: Load sign bytes hash for accurate idempotency check
	if len(state.SignBytesHash) > 0 {
		hash, err := types.NewHash(state.SignBytesHash)
		if err != nil {
			return fmt.Errorf("invalid sign bytes hash in state file: %w", err)
		}
		pv.lastSignState.SignBytesHash = &hash
	}

	// TWELFTH_REFACTOR: Load timestamp for idempotency check
	pv.lastSignState.Timestamp = state.Timestamp

	return nil
}

// saveState saves the state to file using atomic write (temp + rename)
func (pv *FilePV) saveState() error {
	// Ensure directory exists
	dir := filepath.Dir(pv.stateFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	state := FilePVState{
		Height: pv.lastSignState.Height,
		Round:  pv.lastSignState.Round,
		Step:   pv.lastSignState.Step,
	}

	if len(pv.lastSignState.Signature.Data) > 0 {
		state.Signature = pv.lastSignState.Signature.Data
	}

	if pv.lastSignState.BlockHash != nil {
		state.BlockHash = pv.lastSignState.BlockHash.Data
	}

	// TWELFTH_REFACTOR: Save sign bytes hash for accurate idempotency check
	if pv.lastSignState.SignBytesHash != nil {
		state.SignBytesHash = pv.lastSignState.SignBytesHash.Data
	}

	// TWELFTH_REFACTOR: Save timestamp for idempotency check
	state.Timestamp = pv.lastSignState.Timestamp

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Atomic write: write to temp file, sync, then rename
	tmpPath := pv.stateFilePath + ".tmp"

	if err := os.WriteFile(tmpPath, data, stateFilePerm); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	// Sync temp file to ensure data is on disk
	tmpFile, err := os.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath) // SIXTH_REFACTOR: Clean up temp file on error
		return fmt.Errorf("failed to open temp file for sync: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath) // SIXTH_REFACTOR: Clean up temp file on error
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	tmpFile.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, pv.stateFilePath); err != nil {
		os.Remove(tmpPath) // SIXTH_REFACTOR: Clean up temp file on error
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	// Sync directory to ensure rename is persisted
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open dir for sync: %w", err)
	}
	if err := dirFile.Sync(); err != nil {
		dirFile.Close()
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	dirFile.Close()

	return nil
}

// GetPubKey returns the public key
// TWENTY_FIRST_REFACTOR: Returns a deep copy to prevent external modification
func (pv *FilePV) GetPubKey() types.PublicKey {
	return types.CopyPublicKey(pv.pubKey)
}

// GetAddress returns the validator address
// M7: Derives address by hashing public key (standard practice similar to Ethereum/Tendermint)
func (pv *FilePV) GetAddress() []byte {
	hash := sha256.Sum256(pv.pubKey.Data)
	// Return first 20 bytes of hash (standard address length)
	addr := make([]byte, 20)
	copy(addr, hash[:20])
	return addr
}

// SignVote signs a vote, checking for double-sign
func (pv *FilePV) SignVote(chainID string, vote *gen.Vote) error {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	// TWENTIETH_REFACTOR: Validate input to prevent panic
	if vote == nil {
		panic("CONSENSUS CRITICAL: nil vote in SignVote")
	}

	step := VoteStep(vote.Type)

	// Check for double-sign
	if err := pv.lastSignState.CheckHRS(vote.Height, vote.Round, step); err != nil {
		// Check if it's the same vote (idempotent re-signing)
		if err == ErrDoubleSign && pv.isSameVote(vote) {
			// FIFTEENTH_REFACTOR: Deep copy signature to prevent caller from
			// corrupting internal state. Previously returned a reference that
			// shared memory with lastSignState.Signature.
			vote.Signature = types.CopySignature(pv.lastSignState.Signature)
			return nil
		}
		return err
	}

	// Sign the vote
	signBytes := types.VoteSignBytes(chainID, vote)
	sig := ed25519.Sign(pv.privKey, signBytes)
	newSig := types.MustNewSignature(sig) // ed25519 output is always valid

	// TWELFTH_REFACTOR: Compute hash of sign bytes for accurate idempotency check.
	// This ensures isSameVote can verify the entire payload matches, not just BlockHash.
	signBytesHash := sha256.Sum256(signBytes)
	signBytesHashObj := types.MustNewHash(signBytesHash[:])

	// SEVENTH_REFACTOR: Prevent double-sign vulnerability by ensuring we never
	// return a signature until the state is durably persisted.
	// EIGHTH_REFACTOR: Clarified comment - the actual sequence is:
	// 1. Update in-memory state (for saveState to serialize)
	// 2. Persist to disk atomically
	// 3. Only then return signature to caller
	// The key invariant is: vote.Signature is only set AFTER saveState() succeeds.
	// If we crash before persist completes, the signature is never returned.
	oldState := pv.lastSignState // Save for rollback if persist fails

	// Prepare new state (needed for saveState to serialize)
	pv.lastSignState.Height = vote.Height
	pv.lastSignState.Round = vote.Round
	pv.lastSignState.Step = step
	pv.lastSignState.Signature = newSig
	// ELEVENTH_REFACTOR: Deep copy BlockHash to prevent caller modifications
	// from corrupting lastSignState and breaking double-sign detection.
	pv.lastSignState.BlockHash = types.CopyHash(vote.BlockHash)
	// TWELFTH_REFACTOR: Store sign bytes hash and timestamp for accurate idempotency check
	pv.lastSignState.SignBytesHash = &signBytesHashObj
	pv.lastSignState.Timestamp = vote.Timestamp

	// Persist state - PANIC on failure (consensus critical)
	if err := pv.saveState(); err != nil {
		// Rollback in-memory state before panicking
		pv.lastSignState = oldState
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to persist sign state after vote: %v", err))
	}

	// Only set vote signature AFTER successful persist - this is the critical invariant
	vote.Signature = newSig

	return nil
}

// SignProposal signs a proposal, checking for double-sign
func (pv *FilePV) SignProposal(chainID string, proposal *gen.Proposal) error {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	// TWENTIETH_REFACTOR: Validate input to prevent panic
	if proposal == nil {
		panic("CONSENSUS CRITICAL: nil proposal in SignProposal")
	}

	// Check for double-sign
	if err := pv.lastSignState.CheckHRS(proposal.Height, proposal.Round, StepProposal); err != nil {
		// Check if it's the same proposal (idempotent re-signing)
		// SIXTEENTH_REFACTOR: Pass chainID for SignBytesHash comparison
		if err == ErrDoubleSign && pv.isSameProposal(chainID, proposal) {
			// FIFTEENTH_REFACTOR: Deep copy signature to prevent caller from
			// corrupting internal state.
			proposal.Signature = types.CopySignature(pv.lastSignState.Signature)
			return nil
		}
		return err
	}

	// Sign the proposal
	signBytes := types.ProposalSignBytes(chainID, proposal)
	sig := ed25519.Sign(pv.privKey, signBytes)
	newSig := types.MustNewSignature(sig) // ed25519 output is always valid

	// SIXTEENTH_REFACTOR: Compute hash of sign bytes for accurate idempotency check.
	// ProposalSignBytes includes PolRound and PolVotes which aren't checked by
	// isSameProposal's BlockHash/Timestamp checks alone.
	signBytesHash := sha256.Sum256(signBytes)
	signBytesHashObj := types.MustNewHash(signBytesHash[:])

	// SEVENTH_REFACTOR: Prevent double-sign vulnerability. See SignVote for details.
	// EIGHTH_REFACTOR: Clarified - signature only returned after persist succeeds.
	oldState := pv.lastSignState // Save for rollback if persist fails

	// Prepare new state (needed for saveState to serialize)
	blockHash := types.BlockHash(&proposal.Block)
	pv.lastSignState.Height = proposal.Height
	pv.lastSignState.Round = proposal.Round
	pv.lastSignState.Step = StepProposal
	pv.lastSignState.Signature = newSig
	pv.lastSignState.BlockHash = &blockHash
	// TWELFTH_REFACTOR: Store timestamp for accurate idempotency check
	pv.lastSignState.Timestamp = proposal.Timestamp
	// SIXTEENTH_REFACTOR: Store sign bytes hash to detect PolRound/PolVotes differences
	pv.lastSignState.SignBytesHash = &signBytesHashObj

	// Persist state - PANIC on failure (consensus critical)
	if err := pv.saveState(); err != nil {
		// Rollback in-memory state before panicking
		pv.lastSignState = oldState
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to persist sign state after proposal: %v", err))
	}

	// Only set proposal signature AFTER successful persist - critical invariant
	proposal.Signature = newSig

	return nil
}

// isSameProposal checks if a proposal matches the last signed proposal.
// TWELFTH_REFACTOR: Now also checks Timestamp since ProposalSignBytes includes it.
// SIXTEENTH_REFACTOR: Fixed timestamp==0 bug, added SignBytesHash comparison.
// Now takes chainID to compute SignBytesHash for proper comparison including
// PolRound and PolVotes fields.
func (pv *FilePV) isSameProposal(chainID string, proposal *gen.Proposal) bool {
	// SIXTEENTH_REFACTOR: If we have SignBytesHash stored, use it for exact comparison.
	// This catches all differences including PolRound and PolVotes.
	if pv.lastSignState.SignBytesHash != nil {
		signBytes := types.ProposalSignBytes(chainID, proposal)
		signBytesHash := sha256.Sum256(signBytes)
		signBytesHashObj := types.MustNewHash(signBytesHash[:])
		return types.HashEqual(*pv.lastSignState.SignBytesHash, signBytesHashObj)
	}

	// Fallback for state files that don't have SignBytesHash yet (backward compatibility)
	// In this case, check Timestamp and BlockHash
	if pv.lastSignState.Timestamp != proposal.Timestamp {
		return false
	}

	if pv.lastSignState.BlockHash == nil {
		return false
	}
	blockHash := types.BlockHash(&proposal.Block)
	return types.HashEqual(*pv.lastSignState.BlockHash, blockHash)
}

// isSameVote checks if the vote matches the last signed vote.
// TWELFTH_REFACTOR: Now also checks Timestamp since VoteSignBytes includes it.
// THIRTEENTH_REFACTOR: Analysis shows current checks are sufficient because:
// - Type is implicitly checked (same H/R/S means same step, which determines Type)
// - Height/Round/Step are checked by CheckHRS before isSameVote is called
// - BlockHash and Timestamp are checked below
// - Validator/ValidatorIndex are deterministic for a given validator (can't differ)
// The SignBytesHash field is retained for future use but not required for correctness.
// SIXTEENTH_REFACTOR: Fixed timestamp==0 bug - now always checks timestamp match.
func (pv *FilePV) isSameVote(vote *gen.Vote) bool {
	// SIXTEENTH_REFACTOR: Always check timestamp. Previously, when lastSignState.Timestamp==0
	// (initial state or migration), any timestamp would pass, returning a cached signature
	// that wouldn't verify because it was signed with a different timestamp.
	if pv.lastSignState.Timestamp != vote.Timestamp {
		return false
	}

	// Check BlockHash
	if pv.lastSignState.BlockHash == nil && vote.BlockHash == nil {
		return true
	}
	if pv.lastSignState.BlockHash == nil || vote.BlockHash == nil {
		return false
	}
	return types.HashEqual(*pv.lastSignState.BlockHash, *vote.BlockHash)
}

// Reset resets the last sign state (use with caution!)
func (pv *FilePV) Reset() error {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	pv.lastSignState = LastSignState{}
	return pv.saveState()
}

// Ensure FilePV implements PrivValidator
var _ PrivValidator = (*FilePV)(nil)
