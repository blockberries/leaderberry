# Tendermint Consensus Protocol

This document provides a detailed specification of the Tendermint Byzantine Fault Tolerant (BFT) consensus protocol as implemented in Leaderberry.

---

## Table of Contents

1. [Overview](#overview)
2. [Safety and Liveness](#safety-and-liveness)
3. [State Machine](#state-machine)
4. [Voting Rounds](#voting-rounds)
5. [Locking Mechanism](#locking-mechanism)
6. [Proof-of-Lock (POL)](#proof-of-lock-pol)
7. [Proposer Selection](#proposer-selection)
8. [Timeout Management](#timeout-management)
9. [Byzantine Assumptions](#byzantine-assumptions)

---

## Overview

Tendermint is a Byzantine Fault Tolerant (BFT) consensus algorithm that provides:

- **Safety:** Never finalize conflicting blocks (even under asynchrony)
- **Liveness:** Progress guaranteed under partial synchrony with 2/3+ honest validators
- **Finality:** Committed blocks never revert
- **Accountability:** Byzantine behavior is cryptographically proven

### Key Properties

| Property | Guarantee |
|----------|-----------|
| **Byzantine Tolerance** | Tolerates up to 1/3 Byzantine validators |
| **Finality Time** | Instant (no confirmation depth) |
| **Fork Resistance** | Never forks (safety always preserved) |
| **Round Time** | 1-3 seconds typical, increases exponentially on timeout |

---

## Safety and Liveness

### Safety Guarantee

**Theorem:** If less than 1/3 of validators are Byzantine, then two honest validators will never commit conflicting blocks at the same height.

**Proof Sketch:**
1. To commit, need 2/3+ precommits for block B at height H
2. To commit different block B' at H, need 2/3+ precommits for B'
3. Both sets require 2/3+ → at least 1/3+ overlap
4. If < 1/3 Byzantine → at least one honest validator in overlap
5. Honest validator can't precommit for both B and B' (locking rules)
6. Contradiction → cannot happen

### Liveness Guarantee

**Theorem:** Under partial synchrony (message delays eventually bounded), if 2/3+ validators are honest, consensus makes progress.

**Conditions:**
- Network eventually delivers messages within timeout
- 2/3+ validators are online and honest
- Clocks are loosely synchronized (within timeout bounds)

---

## State Machine

### States

```
NewHeight → NewRound → Propose → Prevote → PrevoteWait → Precommit → PrecommitWait → Commit
```

### State Transitions

#### NewHeight

**Entry Conditions:**
- Previous height committed
- Or genesis (height 1)

**Actions:**
1. Increment height
2. Reset round to 0
3. Clear proposal and locking state
4. Reset vote tracking

**Exit:** Immediate transition to NewRound

---

#### NewRound

**Entry Conditions:**
- From NewHeight
- Or from previous round (timeout or completion)

**Actions:**
1. Increment round (if from previous round)
2. Clear round-specific state
3. Calculate new proposer for this round

**Exit:** Immediate transition to Propose

---

#### Propose

**Entry Conditions:**
- From NewRound

**Actions:**

**If we are the proposer:**
1. Create proposal block:
   - If locked on a block: propose locked block with POL
   - Else if have valid block: propose valid block with POL
   - Else: create new block from mempool
2. Sign proposal
3. Broadcast proposal
4. Transition to Prevote immediately

**If we are not the proposer:**
1. Wait for proposal from network
2. When proposal received:
   - Validate proposal (signature, structure, POL if present)
   - Store proposal
   - Transition to Prevote
3. On timeout:
   - Transition to Prevote with no proposal

**Exit:** To Prevote (when proposal received or timeout)

**Timeout:** `TimeoutPropose + TimeoutProposeDelta × round`

---

#### Prevote

**Entry Conditions:**
- From Propose (proposal received or timeout)

**Actions:**

**Prevote Decision Logic:**

```
if LockedBlock != nil {
    // We're locked on a block
    if ProposalBlock == LockedBlock {
        // Case 1: Proposal matches our lock
        prevote(ProposalBlock)
    } else if canUnlock(Proposal) {
        // Case 2: Valid POL allows unlocking
        prevote(ProposalBlock)
    } else {
        // Case 3: Different block, can't unlock
        prevote(nil)
    }
} else {
    // We're not locked
    if ProposalBlock != nil && valid(ProposalBlock) {
        // Prevote for valid proposal
        prevote(ProposalBlock)
    } else {
        // No proposal or invalid
        prevote(nil)
    }
}
```

**Exit:** Immediate transition to PrevoteWait

---

#### PrevoteWait

**Entry Conditions:**
- From Prevote

**Actions:**
1. Collect prevotes from network
2. Check for 2/3+ prevotes:

**If 2/3+ prevotes for block B:**
- If round > LockedRound → update lock to (B, round)
- Set validBlock = B, validRound = round
- Transition to Precommit

**If 2/3+ prevotes for nil:**
- Transition to Precommit

**If 2/3+ prevotes for any value (B or nil):**
- Advance to Precommit (safety: someone saw 2/3+ prevotes)

**Round Skip (Liveness Optimization):**
- If receive 2/3+ prevotes from future round R > current round
- Skip directly to round R
- Helps catch up when lagging

**On timeout:**
- Transition to Precommit

**Exit:** To Precommit (when 2/3+ prevotes collected or timeout)

**Timeout:** `TimeoutPrevote + TimeoutPrevoteDelta × round`

---

#### Precommit

**Entry Conditions:**
- From PrevoteWait

**Actions:**

**Precommit Decision Logic:**

```
if saw 2/3+ prevotes for block B {
    if B == ProposalBlock && valid(B) {
        // Safe to precommit
        precommit(B)
    } else {
        // Didn't receive or validate B
        precommit(nil)
    }
} else if saw 2/3+ prevotes for nil {
    // Network prevoted nil
    precommit(nil)
} else {
    // Shouldn't happen (timeout path)
    precommit(nil)
}
```

**Exit:** Immediate transition to PrecommitWait

---

#### PrecommitWait

**Entry Conditions:**
- From Precommit

**Actions:**
1. Collect precommits from network
2. Check for 2/3+ precommits:

**If 2/3+ precommits for block B:**
- Update round to precommit round (if future)
- Transition to Commit

**If 2/3+ precommits for nil:**
- Increment round
- Transition to NewRound

**If 2/3+ precommits for any value:**
- Advance to next step (safety: someone saw 2/3+ precommits)

**Round Skip (Liveness Optimization):**
- If receive 2/3+ precommits from future round R > current round
- Skip directly to round R

**On timeout:**
- Increment round
- Transition to NewRound

**Exit:** To Commit (if 2/3+ precommits for block) or NewRound

**Timeout:** `TimeoutPrecommit + TimeoutPrecommitDelta × round`

---

#### Commit

**Entry Conditions:**
- From PrecommitWait (when 2/3+ precommits for block B)

**Actions:**
1. Finalize block B
2. Create commit certificate (aggregate of 2/3+ precommits)
3. Apply block to application state
4. Execute validator set updates (if any)
5. Notify application and network
6. Transition to NewHeight

**Exit:** To NewHeight (after CommitTimeout)

**Timeout:** `TimeoutCommit` (typically 1 second)

---

## Voting Rounds

### Three-Phase Commit

Each round has three voting phases:

1. **Proposal:** Proposer broadcasts block
2. **Prevote:** First voting round (soft agreement)
3. **Precommit:** Second voting round (hard commitment)

### Why Two Voting Rounds?

**One round is insufficient:**
- Byzantine proposer could send different blocks to different validators
- Some validators see valid block → vote yes
- Others see invalid block → vote no
- Network splits, no progress

**Two rounds provide safety:**
- Prevote: Validators signal "I saw a valid proposal"
- Precommit: Validators signal "2/3+ validators saw the same proposal"
- Ensures everyone precommitting saw the same block

### Vote Structure

```go
type Vote struct {
    Type           VoteType      // PREVOTE or PRECOMMIT
    Height         int64         // Consensus height
    Round          int32         // Round number
    BlockHash      *Hash         // Block being voted for (nil = no block)
    Timestamp      int64         // Vote timestamp
    Validator      AccountName   // Validator name
    ValidatorIndex uint16        // Validator index in set
    Signature      Signature     // Ed25519 signature
}
```

---

## Locking Mechanism

### Purpose

Locking prevents validators from precommitting for different blocks at the same height, which ensures safety.

### Locking Rules

**When to Lock:**
- When we see 2/3+ prevotes for block B at round R
- Set LockedBlock = B, LockedRound = R

**When Locked:**
- MUST prevote for LockedBlock (unless can unlock via POL)
- MUST NOT precommit for any other block

**When to Unlock:**
- When we see valid POL for different block from round R' > LockedRound
- Clear lock, prevote for new block

**Why It Works:**
- If 2/3+ prevoted for B at round R, at least one honest validator saw 2/3+ prevotes
- That validator is now locked on B
- For different block B' to get 2/3+ precommits, need that validator
- Validator won't precommit for B' (locked on B) unless unlocked by newer POL

### Lock State

```go
type LockState struct {
    LockedBlock *Block   // Block we're locked on (nil if not locked)
    LockedRound int32    // Round where we locked (-1 if not locked)
    ValidBlock  *Block   // Most recent valid block we've seen
    ValidRound  int32    // Round of valid block
}
```

---

## Proof-of-Lock (POL)

### Purpose

POL allows a proposer to convince other validators to prevote for a block they're locked on, enabling progress when the network has moved on.

### POL Structure

A POL is a set of 2/3+ prevotes for a specific block from a specific round.

```go
type Proposal struct {
    Block    Block    // Proposed block
    POLRound int32    // Round of POL (-1 if no POL)
    POLVotes []Vote   // Prevotes proving the POL
}
```

### When Proposer Includes POL

**If locked on block B from round R:**
- Propose B again in current round
- Include POL: 2/3+ prevotes for B from round R
- Proves to other validators why proposing old block

**If have valid block B from round R:**
- Propose B in current round
- Include POL from round R

### How Validators Use POL

When receiving proposal with POL:

```go
func canUnlock(proposal *Proposal) bool {
    if proposal.POLRound < 0 {
        return false  // No POL
    }

    if proposal.POLRound <= cs.LockedRound {
        return false  // POL not newer than our lock
    }

    // Verify POL has 2/3+ valid prevotes for this block
    if !verifyPOL(proposal.POLVotes, proposal.Block) {
        return false
    }

    return true  // Can unlock and prevote for proposal
}
```

**Unlocking Process:**
1. Verify POL has 2/3+ prevotes for proposal block
2. Verify POL round is greater than our locked round
3. Verify all POL signatures are valid
4. If all checks pass: prevote for proposal block (unlock)

---

## Proposer Selection

### Weighted Round-Robin Algorithm

Proposer rotates based on voting power using proposer priority.

**Priority Rules:**
1. Each validator has priority (starts at 0 for new validators, -1.125×P for existing)
2. Validator with highest priority is proposer
3. After each round:
   - All validators: `priority += votingPower`
   - Proposer: `priority -= totalVotingPower`

**Example (3 validators with power 100, 100, 50):**

```
Round 0:
  Alice:   priority =   0, power = 100 → proposer
  Bob:     priority =   0, power = 100
  Charlie: priority =   0, power =  50

After round 0:
  Alice:   0 + 100 - 250 = -150
  Bob:     0 + 100       =  100
  Charlie: 0 +  50       =   50

Round 1:
  Bob: priority = 100 → proposer
  Alice:   -150 + 100 = -50
  Bob:      100 + 100 - 250 = -50
  Charlie:   50 +  50 = 100

Round 2:
  Charlie: priority = 100 → proposer
  ...
```

### Tie-Breaking

If multiple validators have equal highest priority:
- Use lexicographic ordering of validator names
- Example: "alice" < "bob" → alice is proposer

### Round-Specific Proposer

```go
// Get proposer for specific round (not just current)
proposer := valSet.GetProposerForRound(round)
```

This is critical for:
- Validating proposals from correct proposer
- Skipping rounds without recomputing full state

---

## Timeout Management

### Timeout Formula

```
timeout = baseTimeout + delta × round
```

**Base Timeouts (Default):**
- Propose: 3 seconds
- Prevote: 1 second
- Precommit: 1 second
- Commit: 1 second

**Deltas (Exponential Backoff):**
- ProposeDelta: 500ms
- PrevoteDelta: 500ms
- PrecommitDelta: 500ms

**Example Timeouts by Round:**

| Round | Propose | Prevote | Precommit |
|-------|---------|---------|-----------|
| 0 | 3.0s | 1.0s | 1.0s |
| 1 | 3.5s | 1.5s | 1.5s |
| 2 | 4.0s | 2.0s | 2.0s |
| 5 | 5.5s | 3.5s | 3.5s |
| 10 | 8.0s | 6.0s | 6.0s |

### Why Exponential Backoff?

**Problem:** Network delays or slow proposer cause timeouts

**Without backoff:** Keep timing out at same rate, never make progress

**With backoff:** Give more time for messages to arrive in later rounds

### Maximum Round Limit

```go
const MaxRoundForTimeout = 10000
```

Prevents integer overflow in timeout calculations. After 10,000 rounds, timeouts remain constant.

---

## Byzantine Assumptions

### Threat Model

**Byzantine validators can:**
- Send conflicting messages to different validators
- Remain silent (don't send messages)
- Send messages at any time
- Collude with other Byzantine validators

**Byzantine validators cannot:**
- Break cryptography (forge signatures)
- Control more than 1/3 of voting power (assumption)
- Control network arbitrarily long (partial synchrony assumption)

### Byzantine Behaviors Detected

1. **Double-Signing:** Sign two different votes at same H/R/S
2. **Equivocation:** Send different messages to different validators
3. **Amnesia:** Forget previous locks (detected via conflicting prevotes)

### Safety Under Byzantine Faults

**Guaranteed if < 1/3 Byzantine:**
- No conflicting blocks finalized
- No forks
- No safety violations

**NOT guaranteed if ≥ 1/3 Byzantine:**
- Safety could be violated (consensus halts to preserve safety)
- Liveness not guaranteed (can't make progress)

### Liveness Under Byzantine Faults

**Guaranteed if:**
- < 1/3 Byzantine
- 2/3+ honest validators online
- Network eventually synchronous
- Clocks loosely synchronized

**Progress may stall if:**
- Network partitioned
- ≥ 1/3 validators offline
- Persistent network delays

---

## Implementation Notes

### Message Ordering

Consensus messages are processed sequentially by the state machine to maintain determinism.

### Replay Protection

WAL records all state transitions for crash recovery. After restart:
1. Replay all messages from WAL
2. Restore locking state, vote tracking
3. Resume from last recorded state

### Clock Synchronization

Validators must have clocks synchronized within timeout bounds (typically ±1 second). Use NTP for clock synchronization.

---

## References

- **Tendermint Paper:** Buchman, E. (2016). "Tendermint: Byzantine Fault Tolerance in the Age of Blockchains"
- **PBFT Paper:** Castro, M., & Liskov, B. (1999). "Practical Byzantine Fault Tolerance"
- **Tendermint Spec:** https://github.com/tendermint/spec

---

## Related Documentation

- [Design Patterns](design-patterns.md) - Implementation patterns
- [Performance](performance.md) - Performance characteristics
- [Troubleshooting](troubleshooting.md) - Common issues
