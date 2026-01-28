package engine

import "time"

// Config holds configuration for the consensus engine
type Config struct {
	// ChainID identifies the blockchain
	ChainID string

	// Timeouts
	Timeouts TimeoutConfig

	// WAL configuration
	WALPath string
	WALSync bool // Force sync on every write

	// Block configuration
	MaxBlockBytes int64
	MaxBlockGas   int64

	// Mempool integration
	CreateEmptyBlocks     bool
	CreateEmptyBlocksInterval time.Duration

	// Skip timeout commit (for testing)
	SkipTimeoutCommit bool
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		ChainID:                   "leaderberry-chain",
		Timeouts:                  DefaultTimeoutConfig(),
		WALPath:                   "data/cs.wal",
		WALSync:                   true,
		MaxBlockBytes:             22020096, // ~21MB
		MaxBlockGas:               -1,       // no limit
		CreateEmptyBlocks:         true,
		CreateEmptyBlocksInterval: 0,
		SkipTimeoutCommit:         false,
	}
}

// ValidateBasic performs basic validation of the config
func (cfg *Config) ValidateBasic() error {
	if cfg.ChainID == "" {
		return ErrInvalidBlock // reusing error for now
	}
	if cfg.WALPath == "" {
		return ErrWALWrite
	}
	return nil
}
