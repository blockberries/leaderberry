package types

import (
	"bytes"
	"testing"
)

func TestPartSetFromData(t *testing.T) {
	// Create data that spans multiple parts
	data := make([]byte, BlockPartSize*2+1000) // 2.something parts
	for i := range data {
		data[i] = byte(i % 256)
	}

	ps, err := NewPartSetFromData(data)
	if err != nil {
		t.Fatalf("failed to create part set: %v", err)
	}

	// Should have 3 parts
	if ps.Total() != 3 {
		t.Errorf("expected 3 parts, got %d", ps.Total())
	}

	// Should be complete
	if !ps.IsComplete() {
		t.Error("expected part set to be complete")
	}

	// Get data back
	got, err := ps.GetData()
	if err != nil {
		t.Fatalf("failed to get data: %v", err)
	}

	if !bytes.Equal(data, got) {
		t.Error("data mismatch")
	}
}

func TestPartSetFromDataSmall(t *testing.T) {
	// Small data that fits in one part
	data := []byte("hello world")

	ps, err := NewPartSetFromData(data)
	if err != nil {
		t.Fatalf("failed to create part set: %v", err)
	}

	if ps.Total() != 1 {
		t.Errorf("expected 1 part, got %d", ps.Total())
	}

	got, err := ps.GetData()
	if err != nil {
		t.Fatalf("failed to get data: %v", err)
	}

	if !bytes.Equal(data, got) {
		t.Error("data mismatch")
	}
}

func TestPartSetReassembly(t *testing.T) {
	// Create source data
	data := make([]byte, BlockPartSize*3+5000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// Create part set from data
	srcPS, err := NewPartSetFromData(data)
	if err != nil {
		t.Fatalf("failed to create source part set: %v", err)
	}

	// Create empty part set from header
	header := srcPS.Header()
	dstPS, err := NewPartSetFromHeader(header)
	if err != nil {
		t.Fatalf("failed to create dest part set: %v", err)
	}

	// Should start empty
	if dstPS.Count() != 0 {
		t.Errorf("expected 0 parts, got %d", dstPS.Count())
	}
	if dstPS.IsComplete() {
		t.Error("should not be complete yet")
	}

	// Add parts one by one
	for i := uint16(0); i < srcPS.Total(); i++ {
		part := srcPS.GetPart(i)
		if part == nil {
			t.Fatalf("source part %d is nil", i)
		}

		if err := dstPS.AddPart(part); err != nil {
			t.Fatalf("failed to add part %d: %v", i, err)
		}

		if dstPS.Count() != i+1 {
			t.Errorf("expected %d parts, got %d", i+1, dstPS.Count())
		}
	}

	// Should be complete now
	if !dstPS.IsComplete() {
		t.Error("expected part set to be complete")
	}

	// Verify data
	got, err := dstPS.GetData()
	if err != nil {
		t.Fatalf("failed to get data: %v", err)
	}

	if !bytes.Equal(data, got) {
		t.Error("reassembled data mismatch")
	}
}

func TestPartSetDuplicatePart(t *testing.T) {
	data := make([]byte, BlockPartSize*2)
	for i := range data {
		data[i] = byte(i)
	}

	srcPS, _ := NewPartSetFromData(data)
	dstPS, _ := NewPartSetFromHeader(srcPS.Header())

	part := srcPS.GetPart(0)

	// First add should succeed
	if err := dstPS.AddPart(part); err != nil {
		t.Fatalf("first add failed: %v", err)
	}

	// Second add should fail
	err := dstPS.AddPart(part)
	if err != ErrPartSetAlreadyHas {
		t.Errorf("expected ErrPartSetAlreadyHas, got %v", err)
	}
}

func TestPartSetInvalidIndex(t *testing.T) {
	data := make([]byte, BlockPartSize)
	srcPS, _ := NewPartSetFromData(data)
	dstPS, _ := NewPartSetFromHeader(srcPS.Header())

	// Create part with invalid index
	invalidPart := &BlockPart{
		Index:     999,
		Bytes:     []byte("test"),
		ProofPath: []Hash{},
		ProofRoot: srcPS.Header().Hash,
	}

	err := dstPS.AddPart(invalidPart)
	if err == nil {
		t.Error("expected error for invalid index")
	}
}

func TestPartSetInvalidProof(t *testing.T) {
	data := make([]byte, BlockPartSize)
	srcPS, _ := NewPartSetFromData(data)
	dstPS, _ := NewPartSetFromHeader(srcPS.Header())

	// Create part with invalid proof (wrong proof path)
	invalidPart := &BlockPart{
		Index:     0,
		Bytes:     srcPS.GetPart(0).Bytes,
		ProofPath: []Hash{HashBytes([]byte("wrong"))}, // Wrong sibling
		ProofRoot: srcPS.Header().Hash,
	}

	err := dstPS.AddPart(invalidPart)
	if err != ErrPartSetInvalidProof {
		t.Errorf("expected ErrPartSetInvalidProof, got %v", err)
	}
}

func TestPartSetMissing(t *testing.T) {
	data := make([]byte, BlockPartSize*5)
	srcPS, _ := NewPartSetFromData(data)
	dstPS, _ := NewPartSetFromHeader(srcPS.Header())

	// All should be missing initially
	missing := dstPS.MissingParts()
	if len(missing) != 5 {
		t.Errorf("expected 5 missing, got %d", len(missing))
	}

	// Add parts 1 and 3
	_ = dstPS.AddPart(srcPS.GetPart(1))
	_ = dstPS.AddPart(srcPS.GetPart(3))

	// Now 0, 2, 4 should be missing
	missing = dstPS.MissingParts()
	if len(missing) != 3 {
		t.Errorf("expected 3 missing, got %d", len(missing))
	}

	expectedMissing := []uint16{0, 2, 4}
	for i, idx := range missing {
		if idx != expectedMissing[i] {
			t.Errorf("missing[%d] = %d, expected %d", i, idx, expectedMissing[i])
		}
	}
}

func TestPartSetHeader(t *testing.T) {
	data := []byte("test data for header")
	ps, _ := NewPartSetFromData(data)

	header := ps.Header()
	if header.Total != 1 {
		t.Errorf("expected total 1, got %d", header.Total)
	}
	if header.IsZero() {
		t.Error("header should not be zero")
	}

	zeroHeader := BlockPartSetHeader{}
	if !zeroHeader.IsZero() {
		t.Error("zero header should be zero")
	}

	// Compare headers
	header2 := ps.Header()
	if !header.Equals(header2) {
		t.Error("same headers should be equal")
	}

	differentHeader := BlockPartSetHeader{Total: 2, Hash: header.Hash}
	if header.Equals(differentHeader) {
		t.Error("different headers should not be equal")
	}
}

func TestPartSetBitmap(t *testing.T) {
	pb := NewPartSetBitmap(10)

	// All should be missing initially
	if len(pb.Missing()) != 10 {
		t.Errorf("expected 10 missing, got %d", len(pb.Missing()))
	}

	// Set some bits
	pb.Set(0)
	pb.Set(5)
	pb.Set(9)

	if !pb.Has(0) {
		t.Error("expected Has(0) to be true")
	}
	if !pb.Has(5) {
		t.Error("expected Has(5) to be true")
	}
	if pb.Has(3) {
		t.Error("expected Has(3) to be false")
	}

	missing := pb.Missing()
	if len(missing) != 7 {
		t.Errorf("expected 7 missing, got %d", len(missing))
	}
}

func TestPartSetBitmapSerialization(t *testing.T) {
	pb := NewPartSetBitmap(100)
	pb.Set(0)
	pb.Set(50)
	pb.Set(99)

	// Serialize
	data := pb.ToBytes()

	// Deserialize
	pb2, err := PartSetBitmapFromBytes(data)
	if err != nil {
		t.Fatalf("failed to deserialize: %v", err)
	}

	if pb2.total != pb.total {
		t.Errorf("total mismatch: %d vs %d", pb.total, pb2.total)
	}

	// Check same bits are set
	for i := uint16(0); i < 100; i++ {
		if pb.Has(i) != pb2.Has(i) {
			t.Errorf("bit %d mismatch", i)
		}
	}
}

func TestPartSetBitmapDiff(t *testing.T) {
	pb1 := NewPartSetBitmap(10)
	pb2 := NewPartSetBitmap(10)

	pb1.Set(0)
	pb1.Set(1)
	pb1.Set(5)

	pb2.Set(0)
	pb2.Set(3)

	// pb1 has 1, 5 that pb2 doesn't
	diff := pb1.Diff(pb2)
	if len(diff) != 2 {
		t.Errorf("expected 2 in diff, got %d", len(diff))
	}
}

func TestMerkleRoot(t *testing.T) {
	// Single hash
	hash1 := HashBytes([]byte("test1"))
	root := computeMerkleRoot([][]byte{hash1.Data})
	if !HashEqual(root, hash1) {
		t.Error("single hash should be the root")
	}

	// Empty should return empty hash
	emptyRoot := computeMerkleRoot(nil)
	if !IsHashEmpty(&emptyRoot) {
		t.Error("empty input should give empty hash")
	}

	// Multiple hashes should produce consistent root
	hashes := [][]byte{
		HashBytes([]byte("a")).Data,
		HashBytes([]byte("b")).Data,
		HashBytes([]byte("c")).Data,
	}
	root1 := computeMerkleRoot(hashes)
	root2 := computeMerkleRoot(hashes)
	if !HashEqual(root1, root2) {
		t.Error("same input should produce same root")
	}
}

func TestPartSetOutOfOrder(t *testing.T) {
	// Create data
	data := make([]byte, BlockPartSize*4)
	for i := range data {
		data[i] = byte(i % 256)
	}

	srcPS, _ := NewPartSetFromData(data)
	dstPS, _ := NewPartSetFromHeader(srcPS.Header())

	// Add parts out of order: 3, 1, 0, 2
	order := []uint16{3, 1, 0, 2}
	for _, idx := range order {
		if err := dstPS.AddPart(srcPS.GetPart(idx)); err != nil {
			t.Fatalf("failed to add part %d: %v", idx, err)
		}
	}

	// Should be complete
	if !dstPS.IsComplete() {
		t.Error("should be complete")
	}

	// Data should match
	got, err := dstPS.GetData()
	if err != nil {
		t.Fatalf("failed to get data: %v", err)
	}

	if !bytes.Equal(data, got) {
		t.Error("data mismatch after out-of-order assembly")
	}
}
