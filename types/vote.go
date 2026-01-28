package types

import (
	"crypto/ed25519"
	"errors"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type Vote = gen.Vote
type VoteType = gen.VoteType
type Commit = gen.Commit
type CommitSig = gen.CommitSig

// Vote type constants
const (
	VoteTypeUnknown   = gen.VoteTypeVoteTypeUnknown
	VoteTypePrevote   = gen.VoteTypeVoteTypePrevote
	VoteTypePrecommit = gen.VoteTypeVoteTypePrecommit
)

// Errors
var (
	ErrInvalidVote        = errors.New("invalid vote")
	ErrVoteConflict       = errors.New("conflicting vote")
	ErrDuplicateVote      = errors.New("duplicate vote")
	ErrUnexpectedVoteType = errors.New("unexpected vote type")
)

// VoteSignBytes returns the bytes to sign for a vote
func VoteSignBytes(chainID string, v *Vote) []byte {
	// Create a canonical vote for signing (without signature)
	canonical := &Vote{
		Type:           v.Type,
		Height:         v.Height,
		Round:          v.Round,
		BlockHash:      v.BlockHash,
		Timestamp:      v.Timestamp,
		Validator:      v.Validator,
		ValidatorIndex: v.ValidatorIndex,
		// Signature is nil for signing
	}

	// Prepend chain ID
	data, _ := canonical.MarshalCramberry()
	return append([]byte(chainID), data...)
}

// IsNilVote returns true if the vote is for nil (no block)
func IsNilVote(v *Vote) bool {
	return v.BlockHash == nil || IsHashEmpty(v.BlockHash)
}

// VoteSet tracks votes for a specific height/round/type
type VoteSet struct {
	ChainID    string
	Height     int64
	Round      int32
	Type       VoteType
	valSet     *ValidatorSet
	votes      map[uint16]*Vote // by validator index
	votesByHash map[string][]*Vote // by block hash
	sum        int64
	maj23      *Hash // block hash that has 2/3+ votes
}

// NewVoteSet creates a new VoteSet
func NewVoteSet(chainID string, height int64, round int32, voteType VoteType, valSet *ValidatorSet) *VoteSet {
	return &VoteSet{
		ChainID:     chainID,
		Height:      height,
		Round:       round,
		Type:        voteType,
		valSet:      valSet,
		votes:       make(map[uint16]*Vote),
		votesByHash: make(map[string][]*Vote),
	}
}

// AddVote adds a vote to the set. Returns true if the vote was added.
func (vs *VoteSet) AddVote(vote *Vote) (bool, error) {
	if vote == nil {
		return false, ErrInvalidVote
	}

	// Validate vote matches this set
	if vote.Height != vs.Height || vote.Round != vs.Round || vote.Type != vs.Type {
		return false, ErrInvalidVote
	}

	// Check validator exists
	val := vs.valSet.GetByIndex(vote.ValidatorIndex)
	if val == nil {
		return false, ErrInvalidVote
	}

	// Check for duplicate
	existing := vs.votes[vote.ValidatorIndex]
	if existing != nil {
		// Same vote is OK
		if votesEqual(existing, vote) {
			return false, nil
		}
		// Different vote is a conflict (equivocation)
		return false, ErrVoteConflict
	}

	// Add vote
	vs.votes[vote.ValidatorIndex] = vote
	vs.sum += val.VotingPower

	// Track by block hash
	hashKey := ""
	if vote.BlockHash != nil {
		hashKey = HashString(*vote.BlockHash)
	}
	vs.votesByHash[hashKey] = append(vs.votesByHash[hashKey], vote)

	// Check for 2/3+ majority
	if vs.maj23 == nil {
		hashVotes := vs.votesByHash[hashKey]
		var hashPower int64
		for _, v := range hashVotes {
			val := vs.valSet.GetByIndex(v.ValidatorIndex)
			if val != nil {
				hashPower += val.VotingPower
			}
		}
		if hashPower >= vs.valSet.TwoThirdsMajority() {
			if vote.BlockHash != nil {
				vs.maj23 = vote.BlockHash
			}
		}
	}

	return true, nil
}

// HasTwoThirdsMajority returns true if there's a 2/3+ majority for any block
func (vs *VoteSet) HasTwoThirdsMajority() bool {
	return vs.maj23 != nil
}

// TwoThirdsMajority returns the block hash with 2/3+ votes, or nil
func (vs *VoteSet) TwoThirdsMajority() *Hash {
	return vs.maj23
}

// HasTwoThirdsAny returns true if 2/3+ of voting power has voted (for any block or nil)
func (vs *VoteSet) HasTwoThirdsAny() bool {
	return vs.sum >= vs.valSet.TwoThirdsMajority()
}

// GetVote returns the vote from a validator, if any
func (vs *VoteSet) GetVote(valIndex uint16) *Vote {
	return vs.votes[valIndex]
}

// Size returns the number of votes
func (vs *VoteSet) Size() int {
	return len(vs.votes)
}

// GetVotes returns all votes
func (vs *VoteSet) GetVotes() []*Vote {
	votes := make([]*Vote, 0, len(vs.votes))
	for _, v := range vs.votes {
		votes = append(votes, v)
	}
	return votes
}

// MakeCommit creates a Commit from 2/3+ precommits
func (vs *VoteSet) MakeCommit() *Commit {
	if vs.Type != VoteTypePrecommit || vs.maj23 == nil {
		return nil
	}

	sigs := make([]CommitSig, 0, len(vs.votes))
	for _, vote := range vs.votes {
		sig := CommitSig{
			ValidatorIndex: vote.ValidatorIndex,
			Signature:      vote.Signature,
			Timestamp:      vote.Timestamp,
			BlockHash:      vote.BlockHash,
		}
		sigs = append(sigs, sig)
	}

	return &Commit{
		Height:     vs.Height,
		Round:      vs.Round,
		BlockHash:  *vs.maj23,
		Signatures: sigs,
	}
}

func votesEqual(a, b *Vote) bool {
	if a.Type != b.Type || a.Height != b.Height || a.Round != b.Round {
		return false
	}
	if a.ValidatorIndex != b.ValidatorIndex {
		return false
	}
	// Check block hash
	if a.BlockHash == nil && b.BlockHash == nil {
		return true
	}
	if a.BlockHash == nil || b.BlockHash == nil {
		return false
	}
	return HashEqual(*a.BlockHash, *b.BlockHash)
}

// VerifyVoteSignature verifies the signature on a vote
func VerifyVoteSignature(chainID string, vote *Vote, pubKey PublicKey) error {
	if vote == nil {
		return ErrInvalidVote
	}
	if len(vote.Signature.Data) == 0 {
		return errors.New("vote has no signature")
	}
	if len(pubKey.Data) != ed25519.PublicKeySize {
		return errors.New("invalid public key size")
	}

	signBytes := VoteSignBytes(chainID, vote)
	if !ed25519.Verify(pubKey.Data, signBytes, vote.Signature.Data) {
		return errors.New("invalid vote signature")
	}
	return nil
}
