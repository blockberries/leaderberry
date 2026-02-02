# Contributing to Leaderberry

Thank you for your interest in contributing to Leaderberry! This document provides guidelines for contributing to the project.

## Code of Conduct

Be respectful, collaborative, and constructive in all interactions.

## Development Setup

### Prerequisites

- Go 1.25.6 or later
- [Cramberry](https://github.com/blockberries/cramberry) for code generation
- golangci-lint (optional, for linting)

### Building

```bash
# Generate code from Cramberry schemas
make generate

# Build all packages
make build

# Run tests
make test

# Run linter
make lint
```

## API Stability and Versioning

Leaderberry follows semantic versioning (SemVer):

- **MAJOR**: Incompatible API changes
- **MINOR**: Backwards-compatible functionality additions
- **PATCH**: Backwards-compatible bug fixes

### Public API Packages

All top-level packages are considered **public API** and follow strict compatibility guarantees:

| Package | Stability | Description |
|---------|-----------|-------------|
| `engine` | **Stable** | Consensus state machine interface |
| `types` | **Stable** | Core type definitions |
| `wal` | **Stable** | Write-ahead log interface |
| `privval` | **Stable** | Private validator interface |
| `evidence` | **Stable** | Byzantine evidence interface |

### Generated Code

The `types/generated/` package contains Cramberry-generated code from `schema/*.cram` files.

**Important:**
- Never edit generated files directly
- Schema changes must maintain binary compatibility
- Use Cramberry field deprecation for backwards compatibility

### Compatibility Rules

**Within a MAJOR version:**
- ✅ Add new exported types, functions, or methods
- ✅ Add new fields to structs (if using proper serialization)
- ✅ Add new interfaces
- ✅ Deprecate APIs (with clear migration path)
- ❌ Remove or rename exported identifiers
- ❌ Change function signatures
- ❌ Change serialization format without version bump
- ❌ Change consensus behavior

**Breaking changes require a MAJOR version bump** and should be carefully considered.

## Schema Changes

Cramberry schemas define network-serializable types. Schema changes affect binary compatibility:

### Adding Fields
```cramberry
struct Block {
    height: uint64
    data: BlockData
    new_field: optional<string>  // OK: Optional fields maintain compatibility
}
```

### Deprecating Fields
```cramberry
struct Block {
    old_field: deprecated<bytes>  // Mark as deprecated, but keep for compatibility
    new_field: bytes
}
```

### Schema Process
1. Modify `.cram` files in `schema/`
2. Run `make generate` to regenerate Go code
3. Update type extensions in `types/` if needed
4. Add migration code if deprecating fields
5. Update tests to cover new functionality

## Pull Request Process

### Before Submitting

1. **Read CLAUDE.md** - Understand project conventions and design patterns
2. **Run tests** - `make test` must pass
3. **Run linter** - `make lint` should have no errors
4. **Update docs** - Document new features and API changes
5. **Add tests** - New functionality requires test coverage

### PR Checklist

- [ ] Tests pass locally (`make test`)
- [ ] Linter passes (`make lint`)
- [ ] Documentation updated (README, ARCHITECTURE, package docs)
- [ ] CHANGELOG.md updated (if user-facing change)
- [ ] Breaking changes clearly marked and justified
- [ ] Commit messages follow conventions (see below)

### Commit Message Format

```
<type>: <summary>

<optional body>
```

**Types:**
- `feat:` New feature
- `fix:` Bug fix
- `refactor:` Code restructuring without behavior change
- `test:` Test additions or modifications
- `docs:` Documentation changes
- `perf:` Performance improvements
- `chore:` Build, CI, or tooling changes

**Examples:**
```
feat: add support for validator set updates

fix: prevent double-sign in edge case after crash recovery

refactor: extract vote validation into separate method

test: add integration test for multi-round consensus

docs: clarify locking rules in state machine

perf: optimize vote tracker quorum detection
```

## Code Style

### Go Conventions

Follow standard Go conventions:
- Use `gofmt` for formatting
- Use `go vet` for static analysis
- Follow [Effective Go](https://golang.org/doc/effective_go)
- Use [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)

### Project-Specific Patterns

See `CODE_REVIEW.md` for detailed documentation of established patterns:

1. **Locked Pattern**: Public methods acquire lock, internal `*Locked` methods assume lock held
2. **Deep Copy Pattern**: Return copies from getters, copy before storing
3. **Generation Counter Pattern**: Detect stale references after Reset()
4. **Deterministic Tie-Breaking**: Lexicographic ordering for equal priorities
5. **Atomic Persistence Pattern**: Persist state BEFORE returning signatures

### Package Organization

- One package per directory
- Package name matches directory name
- Keep packages focused and cohesive
- Avoid circular dependencies
- Tests alongside code with `_test.go` suffix

### Testing

```go
// Table-driven tests
func TestVoteValidation(t *testing.T) {
    tests := []struct {
        name    string
        vote    *types.Vote
        wantErr bool
    }{
        {"valid prevote", makeValidPrevote(), false},
        {"invalid signature", makeInvalidSigVote(), true},
        // ...
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.vote.Verify(chainID, pubKey)
            if (err != nil) != tt.wantErr {
                t.Errorf("got error %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

**Test Requirements:**
- Unit tests for all new functionality
- Race detector must pass (`go test -race`)
- Test edge cases and error conditions
- Integration tests for multi-component interactions
- Byzantine scenario tests for consensus logic

## Consensus Changes

Changes to consensus behavior require **extreme care**:

1. **Propose change** - Open issue discussing the change and rationale
2. **Spec review** - Ensure consistency with Tendermint specification
3. **Security review** - Consider Byzantine attack vectors
4. **Test thoroughly** - Add Byzantine and adversarial tests
5. **Document precisely** - Update ARCHITECTURE.md with new behavior

**Consensus-breaking changes require coordinated network upgrades.**

## Documentation

### Package Documentation

Every package should have a `doc.go` file with:
- High-level package purpose
- Key types and their relationships
- Usage examples
- Thread safety guarantees
- Important invariants

### Godoc

Write godoc-compatible comments:
```go
// NewEngine creates a new consensus engine with the provided configuration.
// The engine manages the full BFT consensus lifecycle including proposal,
// voting, commit, and recovery.
//
// Parameters:
//   - cfg: Engine configuration including timeouts and chain ID
//   - valSet: Initial validator set
//   - privVal: Private validator for signing
//   - wal: Write-ahead log for crash recovery
//   - app: Application for block execution (optional)
//
// Returns an initialized engine ready to start at any height.
func NewEngine(cfg *Config, valSet *types.ValidatorSet, privVal privval.PrivValidator, wal wal.WAL, app Application) *Engine {
    // ...
}
```

### Architecture Documentation

For significant features:
1. Update `ARCHITECTURE.md` with design details
2. Add diagrams if helpful (ASCII or mermaid)
3. Document invariants and correctness arguments
4. Explain trade-offs and design decisions

## Getting Help

- **Issues**: Open a GitHub issue for bugs or feature requests
- **Discussions**: Use GitHub Discussions for questions
- **Documentation**: Check README.md, ARCHITECTURE.md, and CODE_REVIEW.md

## License

By contributing to Leaderberry, you agree that your contributions will be licensed under the same license as the project.
