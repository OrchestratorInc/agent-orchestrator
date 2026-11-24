package daemon

import (
	"log/slog"
	"time"
)

// shutdownBudget caps the shutdown waits that have no timeout of their own,
// such as goroutines that should stop when the daemon context is cancelled.
// They share one budget: time spent in one wait is taken from the rest, so all
// of them together add at most one ShutdownTimeout to shutdown. A wait that
// runs out is logged and abandoned; the process exits soon after, and the
// next boot's reconciliation repairs what the abandoned work left unfinished.
type shutdownBudget struct {
	remaining time.Duration
	log       *slog.Logger
}

func newShutdownBudget(total time.Duration, log *slog.Logger) *shutdownBudget {
	return &shutdownBudget{remaining: total, log: log}
}

// wait blocks until done closes or the budget runs out, and reports whether
// done closed. A nil done has nothing to wait for.
func (b *shutdownBudget) wait(stage string, done <-chan struct{}) bool {
	if done == nil {
		return true
	}
	if b.remaining <= 0 {
		select {
		case <-done:
			return true
		default:
			b.log.Error("shutdown: skipped wait; shutdown budget exhausted", "stage", stage)
			return false
		}
	}
	start := time.Now()
	timer := time.NewTimer(b.remaining)
	defer timer.Stop()
	select {
	case <-done:
		b.remaining -= time.Since(start)
		return true
	case <-timer.C:
		b.remaining = 0
		b.log.Error("shutdown: stage did not finish within the shutdown budget", "stage", stage)
		return false
	}
}

// run calls fn and waits for it like wait. fn keeps running in the background
// when the budget runs out.
func (b *shutdownBudget) run(stage string, fn func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return b.wait(stage, done)
}
