package engine

import (
	"sync"
	"time"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

// PeerRoundState tracks a peer's consensus state for a specific round
type PeerRoundState struct {
	Height          int64
	Round           int32
	Step            RoundStep
	StartTime       time.Time
	Proposal        bool        // Peer has the proposal
	ProposalBlockID types.Hash  // Block hash of proposal (if known)
	Prevotes        *VoteBitmap // Which prevotes the peer has
	Precommits      *VoteBitmap // Which precommits the peer has
	LastCommitRound int32       // Last commit round peer reported
	CatchingUp      bool        // Peer is catching up (behind on height)
}

// VoteBitmap efficiently tracks which validator indices have voted
type VoteBitmap struct {
	mu     sync.RWMutex
	bits   []uint64
	count  int
	valSet *types.ValidatorSet
}

// NewVoteBitmap creates a bitmap for tracking validator votes
func NewVoteBitmap(valSet *types.ValidatorSet) *VoteBitmap {
	numVals := valSet.Size()
	// Need (numVals + 63) / 64 uint64s to store all bits
	numWords := (numVals + 63) / 64
	return &VoteBitmap{
		bits:   make([]uint64, numWords),
		valSet: valSet,
	}
}

// Set marks a validator index as having voted
func (vb *VoteBitmap) Set(index uint16) {
	vb.mu.Lock()
	defer vb.mu.Unlock()

	if int(index) >= vb.valSet.Size() {
		return
	}

	wordIdx := index / 64
	bitIdx := index % 64
	mask := uint64(1) << bitIdx

	if vb.bits[wordIdx]&mask == 0 {
		vb.bits[wordIdx] |= mask
		vb.count++
	}
}

// Has returns true if validator index has voted
func (vb *VoteBitmap) Has(index uint16) bool {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	if int(index) >= vb.valSet.Size() {
		return false
	}

	wordIdx := index / 64
	bitIdx := index % 64
	return (vb.bits[wordIdx] & (uint64(1) << bitIdx)) != 0
}

// Count returns the number of validators that have voted
func (vb *VoteBitmap) Count() int {
	vb.mu.RLock()
	defer vb.mu.RUnlock()
	return vb.count
}

// Missing returns indices of validators that haven't voted
func (vb *VoteBitmap) Missing() []uint16 {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	missing := make([]uint16, 0, vb.valSet.Size()-vb.count)
	for i := 0; i < vb.valSet.Size(); i++ {
		if !vb.hasUnlocked(uint16(i)) {
			missing = append(missing, uint16(i))
		}
	}
	return missing
}

// hasUnlocked checks if index is set (caller must hold lock)
func (vb *VoteBitmap) hasUnlocked(index uint16) bool {
	wordIdx := index / 64
	bitIdx := index % 64
	return (vb.bits[wordIdx] & (uint64(1) << bitIdx)) != 0
}

// Copy creates a copy of the bitmap
func (vb *VoteBitmap) Copy() *VoteBitmap {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	bits := make([]uint64, len(vb.bits))
	copy(bits, vb.bits)

	return &VoteBitmap{
		bits:   bits,
		count:  vb.count,
		valSet: vb.valSet,
	}
}

// PeerState tracks the consensus state of a single peer
type PeerState struct {
	mu sync.RWMutex

	peerID   string
	valSet   *types.ValidatorSet
	prs      PeerRoundState
	lastSeen time.Time
}

// NewPeerState creates a new PeerState for tracking a peer
func NewPeerState(peerID string, valSet *types.ValidatorSet) *PeerState {
	ps := &PeerState{
		peerID:   peerID,
		valSet:   valSet,
		lastSeen: time.Now(),
	}
	ps.prs = PeerRoundState{
		Height:     0,
		Round:      0,
		Step:       RoundStepNewHeight,
		StartTime:  time.Now(),
		Prevotes:   NewVoteBitmap(valSet),
		Precommits: NewVoteBitmap(valSet),
	}
	return ps
}

// PeerID returns the peer's ID
func (ps *PeerState) PeerID() string {
	return ps.peerID
}

// GetRoundState returns a copy of the peer's round state
func (ps *PeerState) GetRoundState() PeerRoundState {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	// Return copy with copied bitmaps
	prs := ps.prs
	prs.Prevotes = ps.prs.Prevotes.Copy()
	prs.Precommits = ps.prs.Precommits.Copy()
	return prs
}

// SetHasProposal marks that the peer has the proposal
func (ps *PeerState) SetHasProposal(height int64, round int32, blockHash types.Hash) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.prs.Height != height || ps.prs.Round != round {
		return // Stale update
	}

	ps.prs.Proposal = true
	ps.prs.ProposalBlockID = blockHash
	ps.lastSeen = time.Now()
}

// SetHasVote marks that the peer has a specific vote
func (ps *PeerState) SetHasVote(vote *gen.Vote) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if vote.Height != ps.prs.Height {
		return // Wrong height
	}
	if vote.Round != ps.prs.Round {
		return // Wrong round
	}

	switch vote.Type {
	case types.VoteTypePrevote:
		ps.prs.Prevotes.Set(vote.ValidatorIndex)
	case types.VoteTypePrecommit:
		ps.prs.Precommits.Set(vote.ValidatorIndex)
	}

	ps.lastSeen = time.Now()
}

// ApplyNewRoundStep updates the peer's state when they advance to a new step
func (ps *PeerState) ApplyNewRoundStep(height int64, round int32, step RoundStep) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if height < ps.prs.Height {
		return // Peer regressed - ignore
	}

	// If height changed, reset everything
	if height > ps.prs.Height {
		ps.prs = PeerRoundState{
			Height:     height,
			Round:      round,
			Step:       step,
			StartTime:  time.Now(),
			Prevotes:   NewVoteBitmap(ps.valSet),
			Precommits: NewVoteBitmap(ps.valSet),
			CatchingUp: false,
		}
		ps.lastSeen = time.Now()
		return
	}

	// Same height - check round
	if round < ps.prs.Round {
		return // Peer regressed within same height - ignore
	}

	// If round changed within same height, reset vote tracking
	if round > ps.prs.Round {
		ps.prs.Round = round
		ps.prs.Step = step
		ps.prs.Proposal = false
		ps.prs.ProposalBlockID = types.Hash{}
		ps.prs.Prevotes = NewVoteBitmap(ps.valSet)
		ps.prs.Precommits = NewVoteBitmap(ps.valSet)
		ps.lastSeen = time.Now()
		return
	}

	// Same height and round - just update step
	if step > ps.prs.Step {
		ps.prs.Step = step
		ps.lastSeen = time.Now()
	}
}

// ApplyCommit updates state when peer commits a height
func (ps *PeerState) ApplyCommit(height int64, round int32) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if height != ps.prs.Height {
		return
	}

	ps.prs.LastCommitRound = round
	ps.prs.Step = RoundStepCommit
	ps.lastSeen = time.Now()
}

// SetCatchingUp marks that the peer is catching up
func (ps *PeerState) SetCatchingUp(catching bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.prs.CatchingUp = catching
}

// IsCatchingUp returns true if peer is catching up
func (ps *PeerState) IsCatchingUp() bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.prs.CatchingUp
}

// Height returns the peer's current height
func (ps *PeerState) Height() int64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.prs.Height
}

// UpdateValidatorSet updates the validator set for the peer
func (ps *PeerState) UpdateValidatorSet(valSet *types.ValidatorSet) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.valSet = valSet
	// Reset vote tracking with new validator set
	ps.prs.Prevotes = NewVoteBitmap(valSet)
	ps.prs.Precommits = NewVoteBitmap(valSet)
}

// LastSeen returns when we last received data from this peer
func (ps *PeerState) LastSeen() time.Time {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.lastSeen
}

// NeedsVote returns true if the peer needs a specific vote
func (ps *PeerState) NeedsVote(vote *gen.Vote) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if vote.Height != ps.prs.Height || vote.Round != ps.prs.Round {
		return false
	}

	switch vote.Type {
	case types.VoteTypePrevote:
		return !ps.prs.Prevotes.Has(vote.ValidatorIndex)
	case types.VoteTypePrecommit:
		return !ps.prs.Precommits.Has(vote.ValidatorIndex)
	}
	return false
}

// NeedsProposal returns true if the peer needs the proposal
func (ps *PeerState) NeedsProposal(height int64, round int32) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.prs.Height == height && ps.prs.Round == round && !ps.prs.Proposal
}

// PeerSet manages a set of peers and their consensus states
type PeerSet struct {
	mu     sync.RWMutex
	peers  map[string]*PeerState
	valSet *types.ValidatorSet
}

// NewPeerSet creates a new PeerSet
func NewPeerSet(valSet *types.ValidatorSet) *PeerSet {
	return &PeerSet{
		peers:  make(map[string]*PeerState),
		valSet: valSet,
	}
}

// AddPeer adds a new peer to track
func (ps *PeerSet) AddPeer(peerID string) *PeerState {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if existing, ok := ps.peers[peerID]; ok {
		return existing
	}

	peerState := NewPeerState(peerID, ps.valSet)
	ps.peers[peerID] = peerState
	return peerState
}

// RemovePeer removes a peer
func (ps *PeerSet) RemovePeer(peerID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.peers, peerID)
}

// GetPeer returns a peer's state
func (ps *PeerSet) GetPeer(peerID string) *PeerState {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.peers[peerID]
}

// Size returns the number of peers
func (ps *PeerSet) Size() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.peers)
}

// AllPeers returns all peer states
func (ps *PeerSet) AllPeers() []*PeerState {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	peers := make([]*PeerState, 0, len(ps.peers))
	for _, p := range ps.peers {
		peers = append(peers, p)
	}
	return peers
}

// PeersAtHeight returns peers at a specific height
func (ps *PeerSet) PeersAtHeight(height int64) []*PeerState {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	peers := make([]*PeerState, 0)
	for _, p := range ps.peers {
		if p.Height() == height {
			peers = append(peers, p)
		}
	}
	return peers
}

// PeersNeedingVote returns peers that need a specific vote
func (ps *PeerSet) PeersNeedingVote(vote *gen.Vote) []*PeerState {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	peers := make([]*PeerState, 0)
	for _, p := range ps.peers {
		if p.NeedsVote(vote) {
			peers = append(peers, p)
		}
	}
	return peers
}

// PeersNeedingProposal returns peers that need a proposal
func (ps *PeerSet) PeersNeedingProposal(height int64, round int32) []*PeerState {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	peers := make([]*PeerState, 0)
	for _, p := range ps.peers {
		if p.NeedsProposal(height, round) {
			peers = append(peers, p)
		}
	}
	return peers
}

// UpdateValidatorSet updates the validator set for all peers
func (ps *PeerSet) UpdateValidatorSet(valSet *types.ValidatorSet) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.valSet = valSet
	for _, p := range ps.peers {
		p.UpdateValidatorSet(valSet)
	}
}

// CatchingUpPeers returns peers that are behind on height
func (ps *PeerSet) CatchingUpPeers() []*PeerState {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	peers := make([]*PeerState, 0)
	for _, p := range ps.peers {
		if p.IsCatchingUp() {
			peers = append(peers, p)
		}
	}
	return peers
}

// MarkPeerCatchingUp marks a peer as catching up if behind
func (ps *PeerSet) MarkPeerCatchingUp(peerID string, ourHeight int64) {
	ps.mu.RLock()
	peer := ps.peers[peerID]
	ps.mu.RUnlock()

	if peer == nil {
		return
	}

	peer.SetCatchingUp(peer.Height() < ourHeight)
}
