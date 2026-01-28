package engine

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	gen "github.com/blockberries/leaderberry/types/generated"
)

const (
	// timeoutChannelSize is the buffer size for timeout channels
	timeoutChannelSize = 100
)

// RoundStep type aliases
type RoundStep = gen.RoundStepType

const (
	RoundStepNewHeight     = gen.RoundStepTypeRoundStepNewHeight
	RoundStepNewRound      = gen.RoundStepTypeRoundStepNewRound
	RoundStepPropose       = gen.RoundStepTypeRoundStepPropose
	RoundStepPrevote       = gen.RoundStepTypeRoundStepPrevote
	RoundStepPrevoteWait   = gen.RoundStepTypeRoundStepPrevoteWait
	RoundStepPrecommit     = gen.RoundStepTypeRoundStepPrecommit
	RoundStepPrecommitWait = gen.RoundStepTypeRoundStepPrecommitWait
	RoundStepCommit        = gen.RoundStepTypeRoundStepCommit
)

// TimeoutInfo represents a timeout event
type TimeoutInfo struct {
	Duration time.Duration
	Height   int64
	Round    int32
	Step     RoundStep
}

// TimeoutConfig holds timeout configuration
type TimeoutConfig struct {
	Propose      time.Duration
	ProposeDelta time.Duration
	Prevote      time.Duration
	PrevoteDelta time.Duration
	Precommit      time.Duration
	PrecommitDelta time.Duration
	Commit       time.Duration
}

// DefaultTimeoutConfig returns default timeout configuration
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		Propose:        3000 * time.Millisecond,
		ProposeDelta:   500 * time.Millisecond,
		Prevote:        1000 * time.Millisecond,
		PrevoteDelta:   500 * time.Millisecond,
		Precommit:      1000 * time.Millisecond,
		PrecommitDelta: 500 * time.Millisecond,
		Commit:         1000 * time.Millisecond,
	}
}

// TimeoutTicker manages timeouts for the consensus state machine
type TimeoutTicker struct {
	mu     sync.Mutex
	config TimeoutConfig

	timer    *time.Timer
	tickCh   chan TimeoutInfo
	tockCh   chan TimeoutInfo
	stopCh   chan struct{}
	running  bool

	// Metrics
	droppedTimeouts uint64
}

// NewTimeoutTicker creates a new TimeoutTicker
func NewTimeoutTicker(config TimeoutConfig) *TimeoutTicker {
	return &TimeoutTicker{
		config: config,
		tickCh: make(chan TimeoutInfo, timeoutChannelSize),
		tockCh: make(chan TimeoutInfo, timeoutChannelSize),
		stopCh: make(chan struct{}),
	}
}

// Start starts the timeout ticker
func (tt *TimeoutTicker) Start() {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	if tt.running {
		return
	}
	tt.running = true

	go tt.run()
}

// Stop stops the timeout ticker
func (tt *TimeoutTicker) Stop() {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	if !tt.running {
		return
	}
	tt.running = false

	close(tt.stopCh)
	if tt.timer != nil {
		tt.timer.Stop()
	}
}

// Chan returns the channel that delivers timeout events
func (tt *TimeoutTicker) Chan() <-chan TimeoutInfo {
	return tt.tockCh
}

// ScheduleTimeout schedules a new timeout
func (tt *TimeoutTicker) ScheduleTimeout(ti TimeoutInfo) {
	tt.tickCh <- ti
}

func (tt *TimeoutTicker) run() {
	for {
		select {
		case <-tt.stopCh:
			return

		case ti := <-tt.tickCh:
			tt.mu.Lock()
			// Cancel any existing timer
			if tt.timer != nil {
				tt.timer.Stop()
			}

			// Calculate duration
			duration := tt.calculateDuration(ti)
			ti.Duration = duration
			tiCopy := ti

			// Start new timer
			tt.timer = time.AfterFunc(duration, func() {
				select {
				case tt.tockCh <- tiCopy:
				case <-tt.stopCh:
					// Ticker stopped, don't send
				default:
					// Channel full, drop timeout and log warning
					count := atomic.AddUint64(&tt.droppedTimeouts, 1)
					log.Printf("WARN: dropped timeout due to full channel: height=%d round=%d step=%d total_dropped=%d",
						tiCopy.Height, tiCopy.Round, tiCopy.Step, count)
				}
			})
			tt.mu.Unlock()
		}
	}
}

func (tt *TimeoutTicker) calculateDuration(ti TimeoutInfo) time.Duration {
	switch ti.Step {
	case RoundStepPropose:
		return tt.config.Propose + time.Duration(ti.Round)*tt.config.ProposeDelta
	case RoundStepPrevoteWait:
		return tt.config.Prevote + time.Duration(ti.Round)*tt.config.PrevoteDelta
	case RoundStepPrecommitWait:
		return tt.config.Precommit + time.Duration(ti.Round)*tt.config.PrecommitDelta
	case RoundStepCommit:
		return tt.config.Commit
	default:
		return time.Second
	}
}

// Propose returns the propose timeout for a round
func (tt *TimeoutTicker) Propose(round int32) time.Duration {
	return tt.config.Propose + time.Duration(round)*tt.config.ProposeDelta
}

// Prevote returns the prevote wait timeout for a round
func (tt *TimeoutTicker) Prevote(round int32) time.Duration {
	return tt.config.Prevote + time.Duration(round)*tt.config.PrevoteDelta
}

// Precommit returns the precommit wait timeout for a round
func (tt *TimeoutTicker) Precommit(round int32) time.Duration {
	return tt.config.Precommit + time.Duration(round)*tt.config.PrecommitDelta
}

// Commit returns the commit timeout
func (tt *TimeoutTicker) Commit() time.Duration {
	return tt.config.Commit
}

// DroppedTimeouts returns the number of timeouts dropped due to full channel
func (tt *TimeoutTicker) DroppedTimeouts() uint64 {
	return atomic.LoadUint64(&tt.droppedTimeouts)
}
