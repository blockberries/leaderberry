package engine

import (
	"errors"
	"time"
)

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
		CreateEmptyBlocks:         true,
		CreateEmptyBlocksInterval: 0,
		SkipTimeoutCommit:         false,
	}
}

// Config validation errors
var (
	ErrEmptyChainID       = errors.New("chain_id is required")
	ErrEmptyWALPath       = errors.New("wal_path is required")
	ErrInvalidTimeout     = errors.New("timeout must be positive")
	ErrTimeoutTooLarge    = errors.New("timeout exceeds maximum (5 minutes)")
	ErrInvalidMaxBlockBytes = errors.New("max_block_bytes must be positive")
)

// ValidateBasic performs basic validation of the config
func (cfg *Config) ValidateBasic() error {
	if cfg.ChainID == "" {
		return ErrEmptyChainID
	}
	if cfg.WALPath == "" {
		return ErrEmptyWALPath
	}

	// Validate timeouts
	if err := cfg.Timeouts.Validate(); err != nil {
		return err
	}

	// Validate block configuration
	if cfg.MaxBlockBytes <= 0 {
		return ErrInvalidMaxBlockBytes
	}

	return nil
}

// Validate validates timeout configuration
func (tc TimeoutConfig) Validate() error {
	maxTimeout := 5 * time.Minute

	if tc.Propose <= 0 {
		return errors.New("propose timeout must be positive")
	}
	if tc.Propose > maxTimeout {
		return errors.New("propose timeout exceeds 5 minutes")
	}

	if tc.ProposeDelta < 0 {
		return errors.New("propose_delta must be non-negative")
	}

	if tc.Prevote <= 0 {
		return errors.New("prevote timeout must be positive")
	}
	if tc.Prevote > maxTimeout {
		return errors.New("prevote timeout exceeds 5 minutes")
	}

	if tc.PrevoteDelta < 0 {
		return errors.New("prevote_delta must be non-negative")
	}

	if tc.Precommit <= 0 {
		return errors.New("precommit timeout must be positive")
	}
	if tc.Precommit > maxTimeout {
		return errors.New("precommit timeout exceeds 5 minutes")
	}

	if tc.PrecommitDelta < 0 {
		return errors.New("precommit_delta must be non-negative")
	}

	if tc.Commit <= 0 {
		return errors.New("commit timeout must be positive")
	}
	if tc.Commit > maxTimeout {
		return errors.New("commit timeout exceeds 5 minutes")
	}

	return nil
}
