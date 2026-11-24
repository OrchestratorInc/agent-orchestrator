package daemon

import (
	"log/slog"
	"testing"
	"time"
)

func TestShutdownBudgetSharesOneLimitAcrossWaits(t *testing.T) {
	b := newShutdownBudget(50*time.Millisecond, slog.New(slog.DiscardHandler))
	closed := make(chan struct{})
	close(closed)
	if !b.wait("finished", closed) || !b.wait("nothing to wait for", nil) {
		t.Fatal("a finished stage was reported as timed out")
	}
	start := time.Now()
	if b.wait("hung", make(chan struct{})) {
		t.Fatal("a hung stage was reported as finished")
	}
	if b.run("hung too", func() { select {} }) {
		t.Fatal("a stage after the budget ran out was reported as finished")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("two hung stages took %s, want them to share the 50ms budget", elapsed)
	}
	if !b.wait("finished after exhaustion", closed) {
		t.Fatal("an already finished stage must still count as finished")
	}
}

func TestShutdownBudgetRunWaitsForWork(t *testing.T) {
	b := newShutdownBudget(time.Second, slog.New(slog.DiscardHandler))
	ran := false
	if !b.run("work", func() { time.Sleep(10 * time.Millisecond); ran = true }) || !ran {
		t.Fatal("run returned before its work finished")
	}
	if b.remaining >= time.Second || b.remaining <= 0 {
		t.Fatalf("remaining = %s, want the work's time taken from the budget", b.remaining)
	}
}
