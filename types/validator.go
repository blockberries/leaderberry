package types

import (
	"errors"
	"fmt"
	"sort"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type NamedValidator = gen.NamedValidator
type ValidatorSetData = gen.ValidatorSetData

// Constants
const (
	// MaxValidators is the maximum number of validators in a set
	// Limited by uint16 index and practical performance considerations
	MaxValidators = 65535

	// MaxTotalVotingPower prevents overflow in priority calculations
	MaxTotalVotingPower = int64(1) << 60

	// PriorityWindowSize for clamping priorities
	PriorityWindowSize = MaxTotalVotingPower * 2
)

// Errors
var (
	ErrValidatorNotFound  = errors.New("validator not found")
	ErrDuplicateValidator = errors.New("duplicate validator")
	ErrEmptyValidatorSet  = errors.New("empty validator set")
	ErrInvalidVotingPower = errors.New("invalid voting power")
	ErrTooManyValidators  = errors.New("too many validators")
	ErrTotalPowerOverflow = errors.New("total voting power overflow")
	ErrEmptyValidatorName = errors.New("validator has empty name")
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
	if len(validators) > MaxValidators {
		return nil, fmt.Errorf("%w: %d (max %d)", ErrTooManyValidators, len(validators), MaxValidators)
	}

	vs := &ValidatorSet{
		Validators: make([]*NamedValidator, len(validators)),
		byName:     make(map[string]*NamedValidator),
		byIndex:    make(map[uint16]*NamedValidator),
	}

	// Copy and validate
	for i, v := range validators {
		// L5: Check for empty validator name to prevent panics in Hash()
		if IsAccountNameEmpty(v.Name) {
			return nil, fmt.Errorf("%w: validator %d", ErrEmptyValidatorName, i)
		}
		if v.VotingPower <= 0 {
			return nil, ErrInvalidVotingPower
		}
		name := AccountNameString(v.Name)
		if _, exists := vs.byName[name]; exists {
			return nil, ErrDuplicateValidator
		}

		// Check for total power overflow
		if vs.TotalPower > MaxTotalVotingPower-v.VotingPower {
			return nil, fmt.Errorf("%w: exceeds %d", ErrTotalPowerOverflow, MaxTotalVotingPower)
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

// centerPriorities centers the priorities around zero.
// L6: Integer division means centering is approximate. This is acceptable
// as the important property is that priorities remain bounded, not exact centering.
// The lost fractional part (sum % len) is at most (len-1), which is negligible
// compared to typical priority values.
func (vs *ValidatorSet) centerPriorities() {
	if len(vs.Validators) == 0 {
		return
	}

	// Calculate average (integer division - some precision loss is acceptable)
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

// TwoThirdsMajority returns the voting power needed for 2/3+ majority.
// The calculation avoids multiplying TotalPower by 2 (which could overflow
// if TotalPower > MaxInt64/2) by computing 2/3 as (1/3 + 1/3 + adjustment).
// L2: Note that third + third can still overflow if third > MaxInt64/2, but
// this is prevented by the MaxTotalVotingPower limit.
func (vs *ValidatorSet) TwoThirdsMajority() int64 {
	// Avoid overflow by dividing first, then adjusting
	// 2/3 majority means > 2/3, so we need (2*total/3) + 1
	// Rewrite as: total/3 + total/3 + adjustment for remainder

	third := vs.TotalPower / 3
	remainder := vs.TotalPower % 3

	// 2/3 = third + third
	twoThirds := third + third

	// If there's a remainder of 2, we need to add 1 more to get true 2/3
	if remainder == 2 {
		twoThirds++
	}

	// +1 to require strictly greater than 2/3
	return twoThirds + 1
}

// IncrementProposerPriority updates priorities and selects next proposer
// DEPRECATED: Use WithIncrementedPriority for thread-safe operations
func (vs *ValidatorSet) IncrementProposerPriority(times int32) {
	if len(vs.Validators) == 0 {
		return
	}

	for i := int32(0); i < times; i++ {
		// Increment all priorities by voting power (with overflow protection)
		for _, v := range vs.Validators {
			newPriority := v.ProposerPriority + v.VotingPower
			// Clamp to prevent overflow
			if newPriority > PriorityWindowSize/2 {
				newPriority = PriorityWindowSize / 2
			}
			v.ProposerPriority = newPriority
		}

		// Decrease proposer's priority by total power
		proposer := vs.getProposer()
		if proposer != nil {
			newPriority := proposer.ProposerPriority - vs.TotalPower
			// Clamp to prevent underflow
			if newPriority < -PriorityWindowSize/2 {
				newPriority = -PriorityWindowSize / 2
			}
			proposer.ProposerPriority = newPriority
		}
	}

	vs.centerPriorities()
	vs.Proposer = vs.getProposer()
}

// WithIncrementedPriority returns a new ValidatorSet with priorities incremented.
// This is the immutable pattern for thread-safe proposer rotation (CR4).
// The original ValidatorSet is not modified.
func (vs *ValidatorSet) WithIncrementedPriority(times int32) (*ValidatorSet, error) {
	// Create a deep copy first
	newVS, err := vs.Copy()
	if err != nil {
		return nil, err
	}

	// Now increment priorities on the copy
	newVS.IncrementProposerPriority(times)

	return newVS, nil
}

// Copy creates a deep copy of the validator set.
// M1: Deep copies AccountName and PublicKey.Data to avoid shared references.
// M2: Preserves priorities exactly by building set manually (avoids NewValidatorSet
// which reinitializes priorities if all are zero).
func (vs *ValidatorSet) Copy() (*ValidatorSet, error) {
	validators := make([]*NamedValidator, len(vs.Validators))
	for i, v := range vs.Validators {
		// M1: Deep copy Name (has *string that could be shared)
		nameCopy := CopyAccountName(v.Name)

		// M1: Deep copy PublicKey.Data
		var pubKeyCopy PublicKey
		if len(v.PublicKey.Data) > 0 {
			pubKeyCopy.Data = make([]byte, len(v.PublicKey.Data))
			copy(pubKeyCopy.Data, v.PublicKey.Data)
		}

		validators[i] = &NamedValidator{
			Name:             nameCopy,
			Index:            v.Index,
			PublicKey:        pubKeyCopy,
			VotingPower:      v.VotingPower,
			ProposerPriority: v.ProposerPriority,
		}
	}

	// M2: Build the set manually to preserve priorities exactly
	// (NewValidatorSet would reinitialize priorities if all are zero)
	newVS := &ValidatorSet{
		Validators: validators,
		TotalPower: vs.TotalPower,
		byName:     make(map[string]*NamedValidator),
		byIndex:    make(map[uint16]*NamedValidator),
	}

	for _, v := range validators {
		name := AccountNameString(v.Name)
		newVS.byName[name] = v
		newVS.byIndex[v.Index] = v
	}

	// Copy proposer reference (point to the new validator in the copy)
	if vs.Proposer != nil {
		newVS.Proposer = newVS.byIndex[vs.Proposer.Index]
	}

	return newVS, nil
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

// Hash computes a deterministic hash of the validator set.
// SIXTH_REFACTOR: ProposerPriority is explicitly excluded from the hash because it's
// mutable state that changes every round. Including it would cause two validator sets
// with identical composition to produce different hashes, breaking light client
// verification and state sync.
func (vs *ValidatorSet) Hash() Hash {
	// Sort validators by name for deterministic ordering
	sorted := make([]*NamedValidator, len(vs.Validators))
	copy(sorted, vs.Validators)
	sort.Slice(sorted, func(i, j int) bool {
		return AccountNameString(sorted[i].Name) < AccountNameString(sorted[j].Name)
	})

	// Create data from sorted validators, explicitly zeroing ProposerPriority
	// to ensure hash is deterministic regardless of current priority state
	validators := make([]NamedValidator, len(sorted))
	for i, v := range sorted {
		validators[i] = NamedValidator{
			Name:             v.Name,
			Index:            v.Index,
			PublicKey:        v.PublicKey,
			VotingPower:      v.VotingPower,
			ProposerPriority: 0, // SIXTH_REFACTOR: Exclude from hash
		}
	}

	// Find proposer index in sorted list
	var proposerIndex uint16
	if vs.Proposer != nil {
		proposerName := AccountNameString(vs.Proposer.Name)
		for i, v := range sorted {
			if AccountNameString(v.Name) == proposerName {
				proposerIndex = uint16(i)
				break
			}
		}
	}

	// Create data struct from sorted validators
	data := &ValidatorSetData{
		Validators:    validators,
		ProposerIndex: proposerIndex,
		TotalPower:    vs.TotalPower,
	}

	bytes, err := data.MarshalCramberry()
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal validator set for hash: %v", err))
	}
	return HashBytes(bytes)
}
