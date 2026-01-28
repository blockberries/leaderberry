package engine

import (
	"testing"
	"time"
)

func TestDefaultTimeoutConfig(t *testing.T) {
	cfg := DefaultTimeoutConfig()

	if cfg.Propose <= 0 {
		t.Error("propose timeout should be positive")
	}
	if cfg.Prevote <= 0 {
		t.Error("prevote timeout should be positive")
	}
	if cfg.Precommit <= 0 {
		t.Error("precommit timeout should be positive")
	}
}

func TestTimeoutTickerBasic(t *testing.T) {
	cfg := TimeoutConfig{
		Propose:        50 * time.Millisecond,
		ProposeDelta:   10 * time.Millisecond,
		Prevote:        50 * time.Millisecond,
		PrevoteDelta:   10 * time.Millisecond,
		Precommit:      50 * time.Millisecond,
		PrecommitDelta: 10 * time.Millisecond,
		Commit:         50 * time.Millisecond,
	}

	tt := NewTimeoutTicker(cfg)
	tt.Start()
	defer tt.Stop()

	// Schedule a timeout
	tt.ScheduleTimeout(TimeoutInfo{
		Height: 1,
		Round:  0,
		Step:   RoundStepPropose,
	})

	// Wait for timeout
	select {
	case ti := <-tt.Chan():
		if ti.Height != 1 || ti.Round != 0 || ti.Step != RoundStepPropose {
			t.Errorf("unexpected timeout info: %+v", ti)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout not received")
	}
}

func TestTimeoutTickerCancel(t *testing.T) {
	cfg := TimeoutConfig{
		Propose:      100 * time.Millisecond,
		ProposeDelta: 10 * time.Millisecond,
	}

	tt := NewTimeoutTicker(cfg)
	tt.Start()
	defer tt.Stop()

	// Schedule a timeout
	tt.ScheduleTimeout(TimeoutInfo{
		Height: 1,
		Round:  0,
		Step:   RoundStepPropose,
	})

	// Schedule a new timeout that should cancel the first
	tt.ScheduleTimeout(TimeoutInfo{
		Height: 1,
		Round:  1,
		Step:   RoundStepPropose,
	})

	// Wait for timeout - should be for round 1, not round 0
	select {
	case ti := <-tt.Chan():
		if ti.Round != 1 {
			t.Errorf("expected round 1 timeout, got round %d", ti.Round)
		}
	case <-time.After(300 * time.Millisecond):
		t.Error("timeout not received")
	}
}

func TestTimeoutCalculation(t *testing.T) {
	cfg := TimeoutConfig{
		Propose:        100 * time.Millisecond,
		ProposeDelta:   10 * time.Millisecond,
		Prevote:        100 * time.Millisecond,
		PrevoteDelta:   10 * time.Millisecond,
		Precommit:      100 * time.Millisecond,
		PrecommitDelta: 10 * time.Millisecond,
		Commit:         100 * time.Millisecond,
	}

	tt := NewTimeoutTicker(cfg)

	// Round 0
	if tt.Propose(0) != 100*time.Millisecond {
		t.Errorf("unexpected propose timeout for round 0: %v", tt.Propose(0))
	}

	// Round 1
	if tt.Propose(1) != 110*time.Millisecond {
		t.Errorf("unexpected propose timeout for round 1: %v", tt.Propose(1))
	}

	// Round 5
	if tt.Propose(5) != 150*time.Millisecond {
		t.Errorf("unexpected propose timeout for round 5: %v", tt.Propose(5))
	}
}
