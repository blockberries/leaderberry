package privval

import (
	"crypto/ed25519"
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
}

// NewFilePV creates a new file-based private validator
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
		pubKey:        types.NewPublicKey(pubKey),
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

// loadKey loads the key from file, generating if it doesn't exist
func (pv *FilePV) loadKey() error {
	data, err := os.ReadFile(pv.keyFilePath)
	if os.IsNotExist(err) {
		// Generate new key
		pubKey, privKey, err := ed25519.GenerateKey(nil)
		if err != nil {
			return fmt.Errorf("failed to generate key: %w", err)
		}
		pv.pubKey = types.NewPublicKey(pubKey)
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
		return fmt.Errorf("invalid public key size")
	}
	if len(key.PrivKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid private key size")
	}

	pv.pubKey = types.NewPublicKey(key.PubKey)
	pv.privKey = key.PrivKey

	return nil
}

// saveKey saves the key to file
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

	if err := os.WriteFile(pv.keyFilePath, data, keyFilePerm); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	return nil
}

// loadState loads the state from file
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
		pv.lastSignState.Signature = types.NewSignature(state.Signature)
	}

	if len(state.BlockHash) > 0 {
		hash := types.NewHash(state.BlockHash)
		pv.lastSignState.BlockHash = &hash
	}

	return nil
}

// saveState saves the state to file
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

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(pv.stateFilePath, data, stateFilePerm); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// GetPubKey returns the public key
func (pv *FilePV) GetPubKey() types.PublicKey {
	return pv.pubKey
}

// GetAddress returns the validator address
func (pv *FilePV) GetAddress() []byte {
	// For now, just return the first 20 bytes of the public key
	// In production, this would typically be a hash
	if len(pv.pubKey.Data) >= 20 {
		return pv.pubKey.Data[:20]
	}
	return pv.pubKey.Data
}

// SignVote signs a vote, checking for double-sign
func (pv *FilePV) SignVote(chainID string, vote *gen.Vote) error {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	step := VoteStep(vote.Type)

	// Check for double-sign
	if err := pv.lastSignState.CheckHRS(vote.Height, vote.Round, step); err != nil {
		// Check if it's the same vote (idempotent re-signing)
		if err == ErrDoubleSign && pv.isSameVote(vote) {
			// Return existing signature
			vote.Signature = pv.lastSignState.Signature
			return nil
		}
		return err
	}

	// Sign the vote
	signBytes := types.VoteSignBytes(chainID, vote)
	sig := ed25519.Sign(pv.privKey, signBytes)
	vote.Signature = types.NewSignature(sig)

	// Update state
	pv.lastSignState.Height = vote.Height
	pv.lastSignState.Round = vote.Round
	pv.lastSignState.Step = step
	pv.lastSignState.Signature = vote.Signature
	pv.lastSignState.BlockHash = vote.BlockHash

	// Persist state
	return pv.saveState()
}

// SignProposal signs a proposal
func (pv *FilePV) SignProposal(chainID string, proposal *gen.Proposal) error {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	// Sign the proposal
	signBytes := types.ProposalSignBytes(chainID, proposal)
	sig := ed25519.Sign(pv.privKey, signBytes)
	proposal.Signature = types.NewSignature(sig)

	return nil
}

// isSameVote checks if a vote matches the last signed vote
func (pv *FilePV) isSameVote(vote *gen.Vote) bool {
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
