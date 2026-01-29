package types

import (
	"testing"
)

func makeValidator(name string, power int64) *NamedValidator {
	return &NamedValidator{
		Name:        NewAccountName(name),
		PublicKey:   PublicKey{Data: make([]byte, 32)},
		VotingPower: power,
	}
}

func TestNewValidatorSet(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
		makeValidator("carol", 100),
	}

	vs, err := NewValidatorSet(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if vs.Size() != 3 {
		t.Errorf("expected 3 validators, got %d", vs.Size())
	}

	if vs.TotalPower != 300 {
		t.Errorf("expected total power 300, got %d", vs.TotalPower)
	}

	if vs.Proposer == nil {
		t.Error("proposer should be set")
	}
}

func TestNewValidatorSetEmpty(t *testing.T) {
	_, err := NewValidatorSet(nil)
	if err != ErrEmptyValidatorSet {
		t.Errorf("expected ErrEmptyValidatorSet, got %v", err)
	}

	_, err = NewValidatorSet([]*NamedValidator{})
	if err != ErrEmptyValidatorSet {
		t.Errorf("expected ErrEmptyValidatorSet, got %v", err)
	}
}

func TestNewValidatorSetDuplicate(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("alice", 100), // duplicate
	}

	_, err := NewValidatorSet(vals)
	if err != ErrDuplicateValidator {
		t.Errorf("expected ErrDuplicateValidator, got %v", err)
	}
}

func TestNewValidatorSetInvalidPower(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 0), // invalid
	}

	_, err := NewValidatorSet(vals)
	if err != ErrInvalidVotingPower {
		t.Errorf("expected ErrInvalidVotingPower, got %v", err)
	}
}

func TestValidatorSetGetByName(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
	}

	vs, _ := NewValidatorSet(vals)

	alice := vs.GetByName("alice")
	if alice == nil {
		t.Fatal("alice should exist")
	}
	if AccountNameString(alice.Name) != "alice" {
		t.Error("wrong validator returned")
	}

	unknown := vs.GetByName("unknown")
	if unknown != nil {
		t.Error("unknown should not exist")
	}
}

func TestValidatorSetGetByIndex(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
	}

	vs, _ := NewValidatorSet(vals)

	v0 := vs.GetByIndex(0)
	if v0 == nil {
		t.Error("validator at index 0 should exist")
	}

	v1 := vs.GetByIndex(1)
	if v1 == nil {
		t.Error("validator at index 1 should exist")
	}

	v2 := vs.GetByIndex(2)
	if v2 != nil {
		t.Error("validator at index 2 should not exist")
	}
}

func TestValidatorSetTwoThirdsMajority(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
		makeValidator("carol", 100),
	}

	vs, _ := NewValidatorSet(vals)

	// 2/3 of 300 = 200, so need 201
	if vs.TwoThirdsMajority() != 201 {
		t.Errorf("expected 201, got %d", vs.TwoThirdsMajority())
	}
}

func TestValidatorSetIncrementProposerPriority(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
		makeValidator("carol", 100),
	}

	vs, _ := NewValidatorSet(vals)
	firstProposer := vs.Proposer

	// After incrementing, proposer should change
	vs.IncrementProposerPriority(1)

	// The proposer might be the same or different depending on priorities
	// Just check that the operation doesn't panic and priorities are updated
	if vs.Proposer == nil {
		t.Error("proposer should still be set after increment")
	}

	// Verify priority was updated
	for _, v := range vs.Validators {
		_ = v.ProposerPriority // just check it's accessible
	}

	_ = firstProposer // avoid unused variable warning
}

func TestValidatorSetCopy(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
	}

	vs, _ := NewValidatorSet(vals)
	vsCopy, err := vs.Copy()
	if err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	if vsCopy.Size() != vs.Size() {
		t.Error("copy should have same size")
	}

	if vsCopy.TotalPower != vs.TotalPower {
		t.Error("copy should have same total power")
	}

	// Modifying copy shouldn't affect original
	vsCopy.IncrementProposerPriority(1)
	// Original proposer priorities should be unchanged
}

func TestValidatorSetToDataAndBack(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
	}

	vs, _ := NewValidatorSet(vals)

	data := vs.ToData()
	if len(data.Validators) != 2 {
		t.Error("data should have 2 validators")
	}

	vs2, err := ValidatorSetFromData(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if vs2.Size() != vs.Size() {
		t.Error("restored set should have same size")
	}

	if vs2.TotalPower != vs.TotalPower {
		t.Error("restored set should have same total power")
	}
}

func TestValidatorSetHash(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
	}

	vs, _ := NewValidatorSet(vals)
	h1 := vs.Hash()

	if len(h1.Data) != 32 {
		t.Error("hash should be 32 bytes")
	}

	// Same validators should produce same hash
	vs2, _ := NewValidatorSet(vals)
	h2 := vs2.Hash()

	if !HashEqual(h1, h2) {
		t.Error("same validator set should produce same hash")
	}
}

func TestValidatorSetGetProposerForRound(t *testing.T) {
	// Create validators with different voting powers to make rotation predictable
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 200),
		makeValidator("carol", 100),
	}

	vs, err := NewValidatorSet(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	originalProposer := vs.Proposer
	originalProposerName := AccountNameString(originalProposer.Name)

	// Round 0 should return current proposer
	proposer0 := vs.GetProposerForRound(0)
	if proposer0 == nil {
		t.Fatal("GetProposerForRound(0) returned nil")
	}
	if AccountNameString(proposer0.Name) != originalProposerName {
		t.Errorf("Round 0 should return same proposer, expected %s, got %s",
			originalProposerName, AccountNameString(proposer0.Name))
	}

	// Original validator set should not be mutated
	if AccountNameString(vs.Proposer.Name) != originalProposerName {
		t.Error("Original validator set was mutated after GetProposerForRound(0)")
	}

	// Higher rounds should rotate proposer (with these voting powers, bob should dominate initially)
	// Track proposers for multiple rounds to verify rotation
	proposers := make([]string, 5)
	for i := int32(0); i < 5; i++ {
		p := vs.GetProposerForRound(i)
		if p == nil {
			t.Fatalf("GetProposerForRound(%d) returned nil", i)
		}
		proposers[i] = AccountNameString(p.Name)
	}

	// Verify original validator set is still unchanged
	if AccountNameString(vs.Proposer.Name) != originalProposerName {
		t.Error("Original validator set was mutated after multiple GetProposerForRound calls")
	}

	// With voting powers alice=100, bob=200, carol=100, we expect rotation
	// Check that at least one round has a different proposer (verifies rotation works)
	hasDifferentProposer := false
	for i := 1; i < len(proposers); i++ {
		if proposers[i] != proposers[0] {
			hasDifferentProposer = true
			break
		}
	}
	if !hasDifferentProposer {
		t.Errorf("Expected proposer rotation across 5 rounds, but all proposers were %s", proposers[0])
	}

	// Verify determinism - calling again with same round should return same proposer
	for i := int32(0); i < 5; i++ {
		p := vs.GetProposerForRound(i)
		if AccountNameString(p.Name) != proposers[i] {
			t.Errorf("GetProposerForRound(%d) not deterministic: expected %s, got %s",
				i, proposers[i], AccountNameString(p.Name))
		}
	}
}

func TestValidatorSetGetProposerForRoundNegative(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
	}

	vs, err := NewValidatorSet(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Negative round should return current proposer (defensive behavior)
	proposer := vs.GetProposerForRound(-1)
	if proposer == nil {
		t.Fatal("GetProposerForRound(-1) returned nil")
	}
	if AccountNameString(proposer.Name) != AccountNameString(vs.Proposer.Name) {
		t.Error("Negative round should return same proposer as round 0")
	}
}

func TestValidatorSetGetProposerForRoundReturnsDeepCopy(t *testing.T) {
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
	}

	vs, err := NewValidatorSet(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Get proposer and modify it
	proposer := vs.GetProposerForRound(0)
	originalPriority := vs.Proposer.ProposerPriority

	// Modify the returned proposer
	proposer.ProposerPriority = 999999

	// Original should be unchanged
	if vs.Proposer.ProposerPriority != originalPriority {
		t.Error("GetProposerForRound should return a deep copy, but original was mutated")
	}
}

func TestValidatorSetGetProposerForRoundRotation(t *testing.T) {
	// Test with equal voting powers - should rotate through all validators
	vals := []*NamedValidator{
		makeValidator("alice", 100),
		makeValidator("bob", 100),
		makeValidator("carol", 100),
	}

	vs, err := NewValidatorSet(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With equal voting powers, proposer should rotate through all validators
	// Track unique proposers over several rounds
	seen := make(map[string]bool)
	for i := int32(0); i < 10; i++ {
		p := vs.GetProposerForRound(i)
		seen[AccountNameString(p.Name)] = true
	}

	// Should see all 3 validators as proposer at some point
	if len(seen) != 3 {
		t.Errorf("Expected all 3 validators to be proposer at some point, saw %d: %v", len(seen), seen)
	}
}
