package types

import (
	"fmt"

	gen "github.com/blockberries/leaderberry/types/generated"
)

// Type aliases for generated types
type Block = gen.Block
type BlockHeader = gen.BlockHeader
type BlockData = gen.BlockData
type BatchCertRef = gen.BatchCertRef

// BlockHash computes the hash of a block
func BlockHash(b *Block) Hash {
	if b == nil {
		return HashEmpty()
	}
	data, err := b.Header.MarshalCramberry()
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal block header for hash: %v", err))
	}
	return HashBytes(data)
}

// BlockHeaderHash computes the hash of a block header
func BlockHeaderHash(h *BlockHeader) Hash {
	if h == nil {
		return HashEmpty()
	}
	data, err := h.MarshalCramberry()
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal block header: %v", err))
	}
	return HashBytes(data)
}

// NewBlock creates a new block
func NewBlock(header *BlockHeader, data *BlockData, lastCommit *Commit) *Block {
	block := &Block{
		Header:     *header,
		LastCommit: lastCommit,
	}
	if data != nil {
		block.Data = *data
	}
	return block
}

// NewBlockHeader creates a new block header
func NewBlockHeader(
	chainID string,
	height int64,
	timestamp int64,
	lastBlockHash *Hash,
	lastCommitHash *Hash,
	validatorsHash *Hash,
	appHash *Hash,
	proposer AccountName,
) *BlockHeader {
	return &BlockHeader{
		ChainId:        chainID,
		Height:         height,
		Time:           timestamp,
		LastBlockHash:  lastBlockHash,
		LastCommitHash: lastCommitHash,
		ValidatorsHash: validatorsHash,
		AppHash:        appHash,
		Proposer:       proposer,
	}
}

// CommitHash computes the hash of a commit
func CommitHash(c *Commit) Hash {
	if c == nil {
		return HashEmpty()
	}
	data, err := c.MarshalCramberry()
	if err != nil {
		panic(fmt.Sprintf("CONSENSUS CRITICAL: failed to marshal commit for hash: %v", err))
	}
	return HashBytes(data)
}

// CopyHash creates a deep copy of a Hash.
func CopyHash(h *Hash) *Hash {
	if h == nil {
		return nil
	}
	hashCopy := &Hash{}
	if len(h.Data) > 0 {
		hashCopy.Data = make([]byte, len(h.Data))
		copy(hashCopy.Data, h.Data)
	}
	return hashCopy
}

// CopyCommitSig creates a deep copy of a CommitSig.
// TWELFTH_REFACTOR: Added nil check for defensive programming.
func CopyCommitSig(sig *CommitSig) CommitSig {
	if sig == nil {
		return CommitSig{}
	}
	sigCopy := CommitSig{
		ValidatorIndex: sig.ValidatorIndex,
		Timestamp:      sig.Timestamp,
	}

	// Deep copy Signature
	if len(sig.Signature.Data) > 0 {
		sigCopy.Signature.Data = make([]byte, len(sig.Signature.Data))
		copy(sigCopy.Signature.Data, sig.Signature.Data)
	}

	// Deep copy BlockHash
	sigCopy.BlockHash = CopyHash(sig.BlockHash)

	return sigCopy
}

// CopyCommit creates a deep copy of a Commit.
// SEVENTH_REFACTOR: Ensures all slice fields (Signatures, BlockHash.Data) are copied
// to prevent the original from being modified through the copy.
func CopyCommit(c *Commit) *Commit {
	if c == nil {
		return nil
	}

	commitCopy := &Commit{
		Height: c.Height,
		Round:  c.Round,
	}

	// Deep copy BlockHash
	if len(c.BlockHash.Data) > 0 {
		commitCopy.BlockHash.Data = make([]byte, len(c.BlockHash.Data))
		copy(commitCopy.BlockHash.Data, c.BlockHash.Data)
	}

	// Deep copy Signatures slice
	if len(c.Signatures) > 0 {
		commitCopy.Signatures = make([]CommitSig, len(c.Signatures))
		for i, sig := range c.Signatures {
			commitCopy.Signatures[i] = CopyCommitSig(&sig)
		}
	}

	return commitCopy
}

// CopyBatchCertRef creates a deep copy of a BatchCertRef.
func CopyBatchCertRef(ref *BatchCertRef) BatchCertRef {
	refCopy := BatchCertRef{
		Round: ref.Round,
	}

	// Deep copy CertificateDigest (Hash)
	if len(ref.CertificateDigest.Data) > 0 {
		refCopy.CertificateDigest.Data = make([]byte, len(ref.CertificateDigest.Data))
		copy(refCopy.CertificateDigest.Data, ref.CertificateDigest.Data)
	}

	// Deep copy Validator (AccountName)
	refCopy.Validator = CopyAccountName(ref.Validator)

	return refCopy
}

// CopyBlockHeader creates a deep copy of a BlockHeader.
func CopyBlockHeader(h *BlockHeader) BlockHeader {
	headerCopy := BlockHeader{
		ChainId:  h.ChainId,
		Height:   h.Height,
		Time:     h.Time,
		DagRound: h.DagRound,
	}

	// Deep copy optional Hash pointers
	headerCopy.LastBlockHash = CopyHash(h.LastBlockHash)
	headerCopy.LastCommitHash = CopyHash(h.LastCommitHash)
	headerCopy.ValidatorsHash = CopyHash(h.ValidatorsHash)
	headerCopy.AppHash = CopyHash(h.AppHash)
	headerCopy.ConsensusHash = CopyHash(h.ConsensusHash)

	// Deep copy BatchCertRefs slice
	if len(h.BatchCertRefs) > 0 {
		headerCopy.BatchCertRefs = make([]BatchCertRef, len(h.BatchCertRefs))
		for i, ref := range h.BatchCertRefs {
			headerCopy.BatchCertRefs[i] = CopyBatchCertRef(&ref)
		}
	}

	// Deep copy Proposer (AccountName)
	headerCopy.Proposer = CopyAccountName(h.Proposer)

	return headerCopy
}

// CopyBlockData creates a deep copy of BlockData.
func CopyBlockData(d *BlockData) BlockData {
	dataCopy := BlockData{}

	// Deep copy BatchDigests slice
	if len(d.BatchDigests) > 0 {
		dataCopy.BatchDigests = make([]Hash, len(d.BatchDigests))
		for i, digest := range d.BatchDigests {
			if len(digest.Data) > 0 {
				dataCopy.BatchDigests[i].Data = make([]byte, len(digest.Data))
				copy(dataCopy.BatchDigests[i].Data, digest.Data)
			}
		}
	}

	return dataCopy
}

// CopyBlock creates a deep copy of a Block.
// SEVENTH_REFACTOR: Ensures all pointer/slice fields are copied to prevent
// the original from being modified through the copy. This is critical for
// async callbacks in BlockSync which may execute after the caller modifies
// the original block.
func CopyBlock(b *Block) *Block {
	if b == nil {
		return nil
	}

	blockCopy := &Block{
		Header: CopyBlockHeader(&b.Header),
		Data:   CopyBlockData(&b.Data),
	}

	// Deep copy Evidence slice ([][]byte)
	if len(b.Evidence) > 0 {
		blockCopy.Evidence = make([][]byte, len(b.Evidence))
		for i, ev := range b.Evidence {
			if len(ev) > 0 {
				blockCopy.Evidence[i] = make([]byte, len(ev))
				copy(blockCopy.Evidence[i], ev)
			}
		}
	}

	// Deep copy LastCommit
	blockCopy.LastCommit = CopyCommit(b.LastCommit)

	return blockCopy
}
