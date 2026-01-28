package types

import (
	"errors"
	"sort"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type NamedValidator = gen.NamedValidator
type ValidatorSetData = gen.ValidatorSetData

// Errors
var (
	ErrValidatorNotFound  = errors.New("validator not found")
	ErrDuplicateValidator = errors.New("duplicate validator")
	ErrEmptyValidatorSet  = errors.New("empty validator set")
	ErrInvalidVotingPower = errors.New("invalid voting power")
)

// ValidatorSet wraps ValidatorSetData with additional methods
type ValidatorSet struct {
	Validators    []*NamedValidator
	Proposer      *NamedValidator
	TotalPower    int64
	byName        map[string]*NamedValidator
	byIndex       map[uint16]*NamedValidator
}

// NewValidatorSet creates a ValidatorSet from validators
func NewValidatorSet(validators []*NamedValidator) (*ValidatorSet, error) {
	if len(validators) == 0 {
		return nil, ErrEmptyValidatorSet
	}

	vs := &ValidatorSet{
		Validators: make([]*NamedValidator, len(validators)),
		byName:     make(map[string]*NamedValidator),
		byIndex:    make(map[uint16]*NamedValidator),
	}

	// Copy and validate
	for i, v := range validators {
		if v.VotingPower <= 0 {
			return nil, ErrInvalidVotingPower
		}
		name := AccountNameString(v.Name)
		if _, exists := vs.byName[name]; exists {
			return nil, ErrDuplicateValidator
		}

		// Create copy with correct index
		val := &NamedValidator{
			Name:             v.Name,
			Index:            uint16(i),
			PublicKey:        v.PublicKey,
			VotingPower:      v.VotingPower,
			ProposerPriority: v.ProposerPriority,
		}
		vs.Validators[i] = val
		vs.byName[name] = val
		vs.byIndex[uint16(i)] = val
		vs.TotalPower += v.VotingPower
	}

	// Initialize proposer priorities if all zero
	allZero := true
	for _, v := range vs.Validators {
		if v.ProposerPriority != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		vs.initProposerPriorities()
	}

	// Set initial proposer
	vs.Proposer = vs.getProposer()

	return vs, nil
}

// initProposerPriorities initializes proposer priorities
func (vs *ValidatorSet) initProposerPriorities() {
	// Initialize all to voting power (will be centered)
	for _, v := range vs.Validators {
		v.ProposerPriority = v.VotingPower
	}
	vs.centerPriorities()
}

// centerPriorities centers the priorities around zero
func (vs *ValidatorSet) centerPriorities() {
	if len(vs.Validators) == 0 {
		return
	}

	// Calculate average
	var sum int64
	for _, v := range vs.Validators {
		sum += v.ProposerPriority
	}
	avg := sum / int64(len(vs.Validators))

	// Subtract average from all
	for _, v := range vs.Validators {
		v.ProposerPriority -= avg
	}
}

// getProposer returns the validator with highest priority
func (vs *ValidatorSet) getProposer() *NamedValidator {
	if len(vs.Validators) == 0 {
		return nil
	}

	var proposer *NamedValidator
	for _, v := range vs.Validators {
		if proposer == nil || v.ProposerPriority > proposer.ProposerPriority {
			proposer = v
		}
	}
	return proposer
}

// GetByName returns a validator by name
func (vs *ValidatorSet) GetByName(name string) *NamedValidator {
	return vs.byName[name]
}

// GetByIndex returns a validator by index
func (vs *ValidatorSet) GetByIndex(index uint16) *NamedValidator {
	return vs.byIndex[index]
}

// Size returns the number of validators
func (vs *ValidatorSet) Size() int {
	return len(vs.Validators)
}

// TwoThirdsMajority returns the voting power needed for 2/3+ majority
func (vs *ValidatorSet) TwoThirdsMajority() int64 {
	return (vs.TotalPower * 2 / 3) + 1
}

// IncrementProposerPriority updates priorities and selects next proposer
func (vs *ValidatorSet) IncrementProposerPriority(times int32) {
	if len(vs.Validators) == 0 {
		return
	}

	for i := int32(0); i < times; i++ {
		// Increment all priorities by voting power
		for _, v := range vs.Validators {
			v.ProposerPriority += v.VotingPower
		}

		// Decrease proposer's priority by total power
		proposer := vs.getProposer()
		if proposer != nil {
			proposer.ProposerPriority -= vs.TotalPower
		}
	}

	vs.centerPriorities()
	vs.Proposer = vs.getProposer()
}

// Copy creates a deep copy of the validator set
func (vs *ValidatorSet) Copy() *ValidatorSet {
	validators := make([]*NamedValidator, len(vs.Validators))
	for i, v := range vs.Validators {
		validators[i] = &NamedValidator{
			Name:             v.Name,
			Index:            v.Index,
			PublicKey:        v.PublicKey,
			VotingPower:      v.VotingPower,
			ProposerPriority: v.ProposerPriority,
		}
	}

	copy, _ := NewValidatorSet(validators)
	return copy
}

// ToData converts to serializable form
func (vs *ValidatorSet) ToData() *ValidatorSetData {
	validators := make([]NamedValidator, len(vs.Validators))
	for i, v := range vs.Validators {
		validators[i] = *v
	}

	var proposerIndex uint16
	if vs.Proposer != nil {
		proposerIndex = vs.Proposer.Index
	}

	return &ValidatorSetData{
		Validators:    validators,
		ProposerIndex: proposerIndex,
		TotalPower:    vs.TotalPower,
	}
}

// ValidatorSetFromData creates a ValidatorSet from serialized data
func ValidatorSetFromData(data *ValidatorSetData) (*ValidatorSet, error) {
	validators := make([]*NamedValidator, len(data.Validators))
	for i := range data.Validators {
		validators[i] = &data.Validators[i]
	}

	vs, err := NewValidatorSet(validators)
	if err != nil {
		return nil, err
	}

	// Restore proposer
	if int(data.ProposerIndex) < len(vs.Validators) {
		vs.Proposer = vs.Validators[data.ProposerIndex]
	}

	return vs, nil
}

// Hash computes a hash of the validator set
func (vs *ValidatorSet) Hash() Hash {
	// Sort validators by name for deterministic ordering
	sorted := make([]*NamedValidator, len(vs.Validators))
	copy(sorted, vs.Validators)
	sort.Slice(sorted, func(i, j int) bool {
		return AccountNameString(sorted[i].Name) < AccountNameString(sorted[j].Name)
	})

	// Serialize and hash
	data := vs.ToData()
	bytes, _ := data.MarshalCramberry()
	return HashBytes(bytes)
}
