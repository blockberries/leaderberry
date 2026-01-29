package evidence

import (
	"testing"
	"time"

	"github.com/blockberries/leaderberry/types"
	gen "github.com/blockberries/leaderberry/types/generated"
)

func makeTestValidator(name string, index uint16, power int64) *types.NamedValidator {
	pubKey := make([]byte, 32)
	pubKey[0] = byte(index)
	return &types.NamedValidator{
		Name:        types.NewAccountName(name),
		Index:       index,
		PublicKey:   types.MustNewPublicKey(pubKey),
		VotingPower: power,
	}
}

func makeTestValidatorSet() *types.ValidatorSet {
	vals := []*types.NamedValidator{
		makeTestValidator("alice", 0, 100),
		makeTestValidator("bob", 1, 100),
		makeTestValidator("carol", 2, 100),
	}
	vs, _ := types.NewValidatorSet(vals)
	return vs
}

func TestPoolNew(t *testing.T) {
	pool := NewPool(DefaultConfig())
	if pool == nil {
		t.Fatal("NewPool should not return nil")
	}
	if pool.Size() != 0 {
		t.Errorf("new pool should have size 0, got %d", pool.Size())
	}
}

func TestPoolCheckVoteEquivocation(t *testing.T) {
	pool := NewPool(DefaultConfig())
	valSet := makeTestValidatorSet()

	blockHash1 := types.HashBytes([]byte("block1"))
	blockHash2 := types.HashBytes([]byte("block2"))

	// First vote
	vote1 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash1,
		Timestamp:      1000,
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}

	// Check first vote - should not be equivocation
	ev, err := pool.CheckVote(vote1, valSet, "")
	if err != nil {
		t.Fatalf("CheckVote failed: %v", err)
	}
	if ev != nil {
		t.Error("first vote should not be equivocation")
	}

	// Same vote again - not equivocation
	ev, err = pool.CheckVote(vote1, valSet, "")
	if err != nil {
		t.Fatalf("CheckVote failed: %v", err)
	}
	if ev != nil {
		t.Error("same vote should not be equivocation")
	}

	// Different vote at same H/R/S - equivocation!
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash2,
		Timestamp:      1001,
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}

	ev, err = pool.CheckVote(vote2, valSet, "")
	if err != nil {
		t.Fatalf("CheckVote failed: %v", err)
	}
	if ev == nil {
		t.Fatal("should detect equivocation")
	}

	// Check evidence fields
	if ev.VoteA.Height != 1 || ev.VoteB.Height != 1 {
		t.Error("evidence should have correct height")
	}
	if ev.TotalVotingPower != valSet.TotalPower {
		t.Errorf("expected total power %d, got %d", valSet.TotalPower, ev.TotalVotingPower)
	}
	if ev.ValidatorPower != 100 {
		t.Errorf("expected validator power 100, got %d", ev.ValidatorPower)
	}
}

func TestPoolCheckVoteSameBlock(t *testing.T) {
	pool := NewPool(DefaultConfig())
	valSet := makeTestValidatorSet()

	blockHash := types.HashBytes([]byte("block"))

	vote1 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1000,
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}

	_, _ = pool.CheckVote(vote1, valSet, "")

	// Same block hash - not equivocation
	vote2 := &gen.Vote{
		Type:           types.VoteTypePrevote,
		Height:         1,
		Round:          0,
		BlockHash:      &blockHash,
		Timestamp:      1001,
		Validator:      types.NewAccountName("alice"),
		ValidatorIndex: 0,
	}

	ev, err := pool.CheckVote(vote2, valSet, "")
	if err != nil {
		t.Fatalf("CheckVote failed: %v", err)
	}
	if ev != nil {
		t.Error("votes for same block should not be equivocation")
	}
}

func TestPoolAddEvidence(t *testing.T) {
	pool := NewPool(DefaultConfig())
	pool.Update(1, time.Now())

	ev := &gen.Evidence{
		Type:   EvidenceTypeDuplicateVote,
		Height: 1,
		Time:   time.Now().UnixNano(),
		Data:   []byte("test evidence data"),
	}

	err := pool.AddEvidence(ev)
	if err != nil {
		t.Fatalf("AddEvidence failed: %v", err)
	}

	if pool.Size() != 1 {
		t.Errorf("pool should have 1 evidence, got %d", pool.Size())
	}

	// Duplicate should fail
	err = pool.AddEvidence(ev)
	if err != ErrDuplicateEvidence {
		t.Errorf("expected ErrDuplicateEvidence, got %v", err)
	}
}

func TestPoolPendingEvidence(t *testing.T) {
	pool := NewPool(DefaultConfig())
	pool.Update(1, time.Now())

	// Add some evidence
	for i := 0; i < 5; i++ {
		ev := &gen.Evidence{
			Type:   EvidenceTypeDuplicateVote,
			Height: int64(i + 1),
			Time:   time.Now().Add(time.Duration(i) * time.Second).UnixNano(),
			Data:   []byte("test evidence"),
		}
		_ = pool.AddEvidence(ev)
	}

	if pool.Size() != 5 {
		t.Errorf("pool should have 5 evidence, got %d", pool.Size())
	}

	// Get pending with limit
	pending := pool.PendingEvidence(100) // Small limit
	if len(pending) == 0 {
		t.Error("should return some pending evidence")
	}
	if len(pending) > 2 {
		t.Error("should respect byte limit")
	}

	// Get all pending
	pending = pool.PendingEvidence(0) // Use default
	if len(pending) != 5 {
		t.Errorf("expected 5 pending, got %d", len(pending))
	}
}

func TestPoolMarkCommitted(t *testing.T) {
	pool := NewPool(DefaultConfig())
	pool.Update(1, time.Now())

	ev := &gen.Evidence{
		Type:   EvidenceTypeDuplicateVote,
		Height: 1,
		Time:   time.Now().UnixNano(),
		Data:   []byte("test"),
	}
	_ = pool.AddEvidence(ev)

	if pool.Size() != 1 {
		t.Fatal("should have 1 pending")
	}

	// Mark as committed
	pool.MarkCommitted([]gen.Evidence{*ev})

	if pool.Size() != 0 {
		t.Errorf("should have 0 pending after commit, got %d", pool.Size())
	}

	// Adding same evidence again should fail (already committed)
	err := pool.AddEvidence(ev)
	if err != ErrDuplicateEvidence {
		t.Errorf("expected ErrDuplicateEvidence for committed evidence, got %v", err)
	}
}

func TestPoolExpiredEvidence(t *testing.T) {
	config := DefaultConfig()
	config.MaxAgeBlocks = 10

	pool := NewPool(config)
	pool.Update(100, time.Now())

	// Old evidence (height 50, current is 100, max age is 10)
	ev := &gen.Evidence{
		Type:   EvidenceTypeDuplicateVote,
		Height: 50,
		Time:   time.Now().UnixNano(),
		Data:   []byte("test"),
	}

	err := pool.AddEvidence(ev)
	if err != ErrEvidenceExpired {
		t.Errorf("expected ErrEvidenceExpired, got %v", err)
	}
}

func TestPoolUpdate(t *testing.T) {
	config := DefaultConfig()
	config.MaxAgeBlocks = 5

	pool := NewPool(config)
	pool.Update(1, time.Now())

	// Add evidence at height 1
	ev := &gen.Evidence{
		Type:   EvidenceTypeDuplicateVote,
		Height: 1,
		Time:   time.Now().UnixNano(),
		Data:   []byte("test"),
	}
	_ = pool.AddEvidence(ev)

	if pool.Size() != 1 {
		t.Error("should have 1 pending")
	}

	// Update to height 10 - evidence should be pruned
	pool.Update(10, time.Now())

	if pool.Size() != 0 {
		t.Errorf("evidence should be pruned, got %d", pool.Size())
	}
}

func TestVoteKey(t *testing.T) {
	vote := &gen.Vote{
		Type:      types.VoteTypePrevote,
		Height:    1,
		Round:     0,
		Validator: types.NewAccountName("alice"),
	}

	key := voteKey(vote)
	expected := "alice/1/0/1" // name/height/round/type(prevote=1)
	if key != expected {
		t.Errorf("expected key %q, got %q", expected, key)
	}
}

func TestEvidenceKey(t *testing.T) {
	ev := &gen.Evidence{
		Type:   EvidenceTypeDuplicateVote,
		Height: 1,
		Time:   1000,
		Data:   []byte("test data"),
	}

	key := evidenceKey(ev)
	// Key format: type/height/time/hash_prefix (first 8 bytes of SHA256)
	// SHA256("test data") first 8 bytes in hex
	if len(key) < 10 { // Should be at least "1/1/1000/..."
		t.Errorf("key too short: %q", key)
	}

	// Verify it starts with type/height/time
	if key[:8] != "1/1/1000" {
		t.Errorf("key should start with '1/1/1000', got %q", key[:8])
	}

	// Same evidence should have same key
	key2 := evidenceKey(ev)
	if key != key2 {
		t.Error("same evidence should produce same key")
	}

	// Different data should produce different key
	ev2 := &gen.Evidence{
		Type:   EvidenceTypeDuplicateVote,
		Height: 1,
		Time:   1000,
		Data:   []byte("different data"),
	}
	key3 := evidenceKey(ev2)
	if key == key3 {
		t.Error("different data should produce different keys")
	}
}

func TestVotesForSameBlock(t *testing.T) {
	hash1 := types.HashBytes([]byte("block1"))
	hash2 := types.HashBytes([]byte("block2"))

	tests := []struct {
		name string
		a    *gen.Vote
		b    *gen.Vote
		same bool
	}{
		{
			name: "both nil",
			a:    &gen.Vote{},
			b:    &gen.Vote{},
			same: true,
		},
		{
			name: "a nil b not",
			a:    &gen.Vote{},
			b:    &gen.Vote{BlockHash: &hash1},
			same: false,
		},
		{
			name: "same hash",
			a:    &gen.Vote{BlockHash: &hash1},
			b:    &gen.Vote{BlockHash: &hash1},
			same: true,
		},
		{
			name: "different hash",
			a:    &gen.Vote{BlockHash: &hash1},
			b:    &gen.Vote{BlockHash: &hash2},
			same: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := votesForSameBlock(tt.a, tt.b); got != tt.same {
				t.Errorf("votesForSameBlock() = %v, want %v", got, tt.same)
			}
		})
	}
}
