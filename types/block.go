package types

import (
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
	data, _ := b.Header.MarshalCramberry()
	return HashBytes(data)
}

// BlockHeaderHash computes the hash of a block header
func BlockHeaderHash(h *BlockHeader) Hash {
	if h == nil {
		return HashEmpty()
	}
	data, _ := h.MarshalCramberry()
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
	data, _ := c.MarshalCramberry()
	return HashBytes(data)
}
