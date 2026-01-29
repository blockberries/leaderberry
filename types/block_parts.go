package types

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

// Block parts configuration
const (
	// BlockPartSize is the maximum size of a single block part
	BlockPartSize = 65536 // 64KB

	// MaxBlockParts is the maximum number of parts a block can have
	MaxBlockParts = 1024 // Allows blocks up to 64MB
)

// Errors
var (
	ErrPartSetInvalidIndex = errors.New("invalid part index")
	ErrPartSetInvalidProof = errors.New("invalid part proof")
	ErrPartSetAlreadyHas   = errors.New("part set already has part")
	ErrPartSetFull         = errors.New("part set is full")
	ErrPartSetNotComplete  = errors.New("part set not complete")
	ErrPartSizeTooLarge    = errors.New("part size too large")
	ErrTooManyParts        = errors.New("too many parts")
)

// BlockPart represents a single part of a block with Merkle proof
type BlockPart struct {
	Index     uint16 // Part index
	Bytes     []byte // Part data
	ProofPath []Hash // CR3: Sibling hashes for Merkle proof verification
	ProofRoot Hash   // CR3: The expected Merkle root
}

// BlockPartSetHeader describes a set of block parts
type BlockPartSetHeader struct {
	Total uint16 // Total number of parts
	Hash  Hash   // Merkle root of part hashes
}

// IsZero returns true if the header is unset
func (h BlockPartSetHeader) IsZero() bool {
	return h.Total == 0
}

// Equals checks equality with another header
func (h BlockPartSetHeader) Equals(other BlockPartSetHeader) bool {
	return h.Total == other.Total && HashEqual(h.Hash, other.Hash)
}

// PartSet is a set of block parts
type PartSet struct {
	mu sync.RWMutex

	total      uint16
	hash       Hash
	parts      []*BlockPart
	partsBits  []uint64 // Bitmap of which parts we have
	partsCount uint16   // Number of parts we have
	data       []byte   // Assembled data (nil until complete)
}

// NewPartSetFromData creates a PartSet by splitting data into parts
func NewPartSetFromData(data []byte) (*PartSet, error) {
	if len(data) == 0 {
		return nil, errors.New("cannot create part set from empty data")
	}

	// Calculate number of parts needed
	total := (len(data) + BlockPartSize - 1) / BlockPartSize
	if total > MaxBlockParts {
		return nil, fmt.Errorf("%w: need %d parts, max is %d", ErrTooManyParts, total, MaxBlockParts)
	}

	parts := make([]*BlockPart, total)
	leafHashes := make([]Hash, total)

	// Split into parts and compute leaf hashes
	for i := 0; i < total; i++ {
		start := i * BlockPartSize
		end := start + BlockPartSize
		if end > len(data) {
			end = len(data)
		}

		partData := make([]byte, end-start)
		copy(partData, data[start:end])

		// Hash this part
		h := sha256.Sum256(partData)
		leafHashes[i] = MustNewHash(h[:])

		parts[i] = &BlockPart{
			Index: uint16(i),
			Bytes: partData,
		}
	}

	// CR3: Build Merkle tree and extract proofs for each part
	rootHash, proofPaths := buildMerkleTreeWithProofs(leafHashes)

	// Attach proofs to parts
	for i, part := range parts {
		part.ProofPath = proofPaths[i]
		part.ProofRoot = rootHash
	}

	// Create bitmap (all parts present)
	numWords := (total + 63) / 64
	bits := make([]uint64, numWords)
	for i := 0; i < total; i++ {
		wordIdx := i / 64
		bitIdx := i % 64
		bits[wordIdx] |= uint64(1) << bitIdx
	}

	return &PartSet{
		total:      uint16(total),
		hash:       rootHash,
		parts:      parts,
		partsBits:  bits,
		partsCount: uint16(total),
		data:       data,
	}, nil
}

// NewPartSetFromHeader creates an empty PartSet from a header
func NewPartSetFromHeader(header BlockPartSetHeader) (*PartSet, error) {
	if header.Total > MaxBlockParts {
		return nil, fmt.Errorf("%w: %d parts", ErrTooManyParts, header.Total)
	}

	numWords := (int(header.Total) + 63) / 64
	return &PartSet{
		total:      header.Total,
		hash:       header.Hash,
		parts:      make([]*BlockPart, header.Total),
		partsBits:  make([]uint64, numWords),
		partsCount: 0,
	}, nil
}

// Header returns the part set header
func (ps *PartSet) Header() BlockPartSetHeader {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return BlockPartSetHeader{
		Total: ps.total,
		Hash:  ps.hash,
	}
}

// Total returns the total number of parts
func (ps *PartSet) Total() uint16 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.total
}

// Count returns the number of parts we have
func (ps *PartSet) Count() uint16 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.partsCount
}

// IsComplete returns true if we have all parts
func (ps *PartSet) IsComplete() bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.partsCount == ps.total
}

// HasPart returns true if we have the part at index
func (ps *PartSet) HasPart(index uint16) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.hasPartUnlocked(index)
}

func (ps *PartSet) hasPartUnlocked(index uint16) bool {
	if index >= ps.total {
		return false
	}
	wordIdx := index / 64
	bitIdx := index % 64
	return (ps.partsBits[wordIdx] & (uint64(1) << bitIdx)) != 0
}

// GetPart returns the part at index, or nil if not present.
// TWENTIETH_REFACTOR: Returns a deep copy to prevent caller corruption of internal state.
func (ps *PartSet) GetPart(index uint16) *BlockPart {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if index >= ps.total {
		return nil
	}
	return CopyBlockPart(ps.parts[index])
}

// AddPart adds a part to the set. Returns error if invalid.
func (ps *PartSet) AddPart(part *BlockPart) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if part.Index >= ps.total {
		return fmt.Errorf("%w: %d >= %d", ErrPartSetInvalidIndex, part.Index, ps.total)
	}

	if len(part.Bytes) > BlockPartSize {
		return fmt.Errorf("%w: %d > %d", ErrPartSizeTooLarge, len(part.Bytes), BlockPartSize)
	}

	if ps.hasPartUnlocked(part.Index) {
		return ErrPartSetAlreadyHas
	}

	// CR3: Verify Merkle proof - compute part hash and verify proof path
	partHash := sha256.Sum256(part.Bytes)
	if !verifyMerkleProof(MustNewHash(partHash[:]), part.Index, part.ProofPath, ps.hash) {
		return ErrPartSetInvalidProof
	}

	// Add the part
	ps.parts[part.Index] = part
	wordIdx := part.Index / 64
	bitIdx := part.Index % 64
	ps.partsBits[wordIdx] |= uint64(1) << bitIdx
	ps.partsCount++

	// If complete, assemble data
	if ps.partsCount == ps.total {
		// L5: Return error if assembly fails (e.g., hash mismatch)
		if err := ps.assembleData(); err != nil {
			return err
		}
	}

	return nil
}

// assembleData combines all parts into the full data (caller must hold lock).
// L5: Returns error if hash verification fails.
func (ps *PartSet) assembleData() error {
	// Calculate total size
	totalSize := 0
	for _, part := range ps.parts {
		totalSize += len(part.Bytes)
	}

	// Assemble
	ps.data = make([]byte, 0, totalSize)
	for _, part := range ps.parts {
		ps.data = append(ps.data, part.Bytes...)
	}

	// Verify hash
	partHashes := make([][]byte, len(ps.parts))
	for i, part := range ps.parts {
		h := sha256.Sum256(part.Bytes)
		partHashes[i] = h[:]
	}
	computedHash := computeMerkleRoot(partHashes)

	if !HashEqual(computedHash, ps.hash) {
		// L5: Data doesn't match - clear it and return error
		ps.data = nil
		return fmt.Errorf("assembled data hash mismatch: expected %x, got %x", ps.hash.Data, computedHash.Data)
	}

	return nil
}

// GetData returns the assembled data, or error if not complete
func (ps *PartSet) GetData() ([]byte, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if ps.partsCount != ps.total {
		return nil, ErrPartSetNotComplete
	}

	if ps.data == nil {
		return nil, errors.New("data assembly failed")
	}

	// Return a copy
	data := make([]byte, len(ps.data))
	copy(data, ps.data)
	return data, nil
}

// MissingParts returns indices of parts we don't have
func (ps *PartSet) MissingParts() []uint16 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	missing := make([]uint16, 0, int(ps.total-ps.partsCount))
	for i := uint16(0); i < ps.total; i++ {
		if !ps.hasPartUnlocked(i) {
			missing = append(missing, i)
		}
	}
	return missing
}

// computeMerkleRoot computes a simple merkle root from a list of hashes
func computeMerkleRoot(hashes [][]byte) Hash {
	if len(hashes) == 0 {
		return HashEmpty()
	}
	if len(hashes) == 1 {
		return MustNewHash(hashes[0])
	}

	// Simple merkle: hash pairs until we have one
	for len(hashes) > 1 {
		next := make([][]byte, 0, (len(hashes)+1)/2)
		for i := 0; i < len(hashes); i += 2 {
			if i+1 < len(hashes) {
				// Combine pair
				combined := append(hashes[i], hashes[i+1]...)
				h := sha256.Sum256(combined)
				next = append(next, h[:])
			} else {
				// Odd one out - hash with itself
				combined := append(hashes[i], hashes[i]...)
				h := sha256.Sum256(combined)
				next = append(next, h[:])
			}
		}
		hashes = next
	}

	return MustNewHash(hashes[0])
}

// CR3: buildMerkleTreeWithProofs builds a Merkle tree and returns the root along
// with proof paths for each leaf. A proof path contains the sibling hashes needed
// to reconstruct the root from a given leaf.
func buildMerkleTreeWithProofs(leafHashes []Hash) (Hash, [][]Hash) {
	n := len(leafHashes)
	if n == 0 {
		return HashEmpty(), nil
	}
	if n == 1 {
		return leafHashes[0], [][]Hash{{}}
	}

	// Initialize proofs for each leaf
	proofs := make([][]Hash, n)
	for i := range proofs {
		proofs[i] = make([]Hash, 0)
	}

	// Convert to [][]byte for processing
	currentLevel := make([][]byte, n)
	for i, h := range leafHashes {
		currentLevel[i] = make([]byte, len(h.Data))
		copy(currentLevel[i], h.Data)
	}

	// Track which original indices correspond to current level positions
	indices := make([][]int, n)
	for i := range indices {
		indices[i] = []int{i}
	}

	// Build tree level by level
	for len(currentLevel) > 1 {
		nextLevel := make([][]byte, 0, (len(currentLevel)+1)/2)
		nextIndices := make([][]int, 0, (len(currentLevel)+1)/2)

		for i := 0; i < len(currentLevel); i += 2 {
			var left, right []byte
			var leftIndices, rightIndices []int

			left = currentLevel[i]
			leftIndices = indices[i]

			if i+1 < len(currentLevel) {
				right = currentLevel[i+1]
				rightIndices = indices[i+1]
			} else {
				// Odd one out - pair with itself
				right = currentLevel[i]
				rightIndices = nil // No additional indices
			}

			// Add sibling to proofs for all leaves in this subtree
			rightHash := MustNewHash(right)
			leftHash := MustNewHash(left)

			for _, idx := range leftIndices {
				proofs[idx] = append(proofs[idx], rightHash)
			}
			for _, idx := range rightIndices {
				proofs[idx] = append(proofs[idx], leftHash)
			}

			// Compute parent hash
			combined := append(left, right...)
			h := sha256.Sum256(combined)
			nextLevel = append(nextLevel, h[:])

			// Merge indices
			merged := make([]int, 0, len(leftIndices)+len(rightIndices))
			merged = append(merged, leftIndices...)
			if rightIndices != nil {
				merged = append(merged, rightIndices...)
			}
			nextIndices = append(nextIndices, merged)
		}

		currentLevel = nextLevel
		indices = nextIndices
	}

	return MustNewHash(currentLevel[0]), proofs
}

// CR3: verifyMerkleProof verifies that a part hash is included in the Merkle root
// via the provided proof path.
func verifyMerkleProof(partHash Hash, index uint16, proofPath []Hash, root Hash) bool {
	if len(proofPath) == 0 {
		// Single element tree - hash should equal root
		return HashEqual(partHash, root)
	}

	current := partHash
	idx := index

	for _, sibling := range proofPath {
		var combined []byte
		if idx%2 == 0 {
			// Current is left child
			combined = append(current.Data, sibling.Data...)
		} else {
			// Current is right child
			combined = append(sibling.Data, current.Data...)
		}
		h := sha256.Sum256(combined)
		current = MustNewHash(h[:])
		idx = idx / 2
	}

	return HashEqual(current, root)
}

// BlockPartsFromBlock creates a PartSet from a block
func BlockPartsFromBlock(block *Block) (*PartSet, error) {
	if block == nil {
		return nil, errors.New("cannot create parts from nil block")
	}

	data, err := block.MarshalCramberry()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal block: %w", err)
	}

	return NewPartSetFromData(data)
}

// BlockFromParts reassembles a block from a complete part set
func BlockFromParts(ps *PartSet) (*Block, error) {
	data, err := ps.GetData()
	if err != nil {
		return nil, err
	}

	block := &Block{}
	if err := block.UnmarshalCramberry(data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block: %w", err)
	}

	return block, nil
}

// PartSetBitmap tracks which parts a peer has (for gossip)
type PartSetBitmap struct {
	mu    sync.RWMutex
	bits  []uint64
	total uint16
}

// NewPartSetBitmap creates a bitmap for tracking parts
func NewPartSetBitmap(total uint16) *PartSetBitmap {
	numWords := (int(total) + 63) / 64
	return &PartSetBitmap{
		bits:  make([]uint64, numWords),
		total: total,
	}
}

// Set marks a part as present
func (pb *PartSetBitmap) Set(index uint16) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if index >= pb.total {
		return
	}
	wordIdx := index / 64
	bitIdx := index % 64
	pb.bits[wordIdx] |= uint64(1) << bitIdx
}

// Has returns true if part is marked present
func (pb *PartSetBitmap) Has(index uint16) bool {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	if index >= pb.total {
		return false
	}
	wordIdx := index / 64
	bitIdx := index % 64
	return (pb.bits[wordIdx] & (uint64(1) << bitIdx)) != 0
}

// Missing returns indices of parts not present
func (pb *PartSetBitmap) Missing() []uint16 {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	missing := make([]uint16, 0)
	for i := uint16(0); i < pb.total; i++ {
		wordIdx := i / 64
		bitIdx := i % 64
		if (pb.bits[wordIdx] & (uint64(1) << bitIdx)) == 0 {
			missing = append(missing, i)
		}
	}
	return missing
}

// ToBytes serializes the bitmap to bytes
func (pb *PartSetBitmap) ToBytes() []byte {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	// 2 bytes for total, then the bit words
	data := make([]byte, 2+len(pb.bits)*8)
	data[0] = byte(pb.total)
	data[1] = byte(pb.total >> 8)

	for i, word := range pb.bits {
		for j := 0; j < 8; j++ {
			data[2+i*8+j] = byte(word >> (j * 8))
		}
	}
	return data
}

// PartSetBitmapFromBytes deserializes a bitmap from bytes
func PartSetBitmapFromBytes(data []byte) (*PartSetBitmap, error) {
	if len(data) < 2 {
		return nil, errors.New("bitmap data too short")
	}

	total := uint16(data[0]) | uint16(data[1])<<8

	// M2: Validate total against MaxBlockParts
	if total > MaxBlockParts {
		return nil, fmt.Errorf("bitmap total %d exceeds MaxBlockParts %d", total, MaxBlockParts)
	}

	numWords := (int(total) + 63) / 64

	expectedLen := 2 + numWords*8
	if len(data) < expectedLen {
		return nil, errors.New("bitmap data incomplete")
	}

	bits := make([]uint64, numWords)
	for i := 0; i < numWords; i++ {
		var word uint64
		for j := 0; j < 8; j++ {
			word |= uint64(data[2+i*8+j]) << (j * 8)
		}
		bits[i] = word
	}

	return &PartSetBitmap{
		bits:  bits,
		total: total,
	}, nil
}

// Diff returns parts that src has but dst doesn't
func (pb *PartSetBitmap) Diff(other *PartSetBitmap) []uint16 {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	diff := make([]uint16, 0)

	for i := uint16(0); i < pb.total; i++ {
		wordIdx := i / 64
		bitIdx := i % 64
		hasSelf := (pb.bits[wordIdx] & (uint64(1) << bitIdx)) != 0
		hasOther := false
		if int(wordIdx) < len(other.bits) {
			hasOther = (other.bits[wordIdx] & (uint64(1) << bitIdx)) != 0
		}
		if hasSelf && !hasOther {
			diff = append(diff, i)
		}
	}
	return diff
}

// String returns a string representation of the bitmap
func (pb *PartSetBitmap) String() string {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("PartSetBitmap{total=%d, ", pb.total))

	count := 0
	for i := uint16(0); i < pb.total; i++ {
		wordIdx := i / 64
		bitIdx := i % 64
		if (pb.bits[wordIdx] & (uint64(1) << bitIdx)) != 0 {
			count++
		}
	}

	buf.WriteString(fmt.Sprintf("have=%d/%d}", count, pb.total))
	return buf.String()
}

// CopyBlockPart creates a deep copy of a BlockPart.
// TWENTIETH_REFACTOR: Added to prevent caller corruption when GetPart returns internal state.
func CopyBlockPart(part *BlockPart) *BlockPart {
	if part == nil {
		return nil
	}

	// Deep copy Bytes
	bytesCopy := make([]byte, len(part.Bytes))
	copy(bytesCopy, part.Bytes)

	// Deep copy ProofPath
	proofPathCopy := make([]Hash, len(part.ProofPath))
	for i, h := range part.ProofPath {
		proofPathCopy[i] = *CopyHash(&h)
	}

	return &BlockPart{
		Index:     part.Index,
		Bytes:     bytesCopy,
		ProofPath: proofPathCopy,
		ProofRoot: *CopyHash(&part.ProofRoot),
	}
}
