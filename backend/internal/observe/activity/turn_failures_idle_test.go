package activity

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// countingTurnFailureSessions is fakeTurnFailureSessions, safe to read while the
// observer's goroutine polls it, recording each call and its context.
type countingTurnFailureSessions struct {
	mu          sync.Mutex
	rows        []domain.TurnFailureCandidate
	calls       int
	cancellable bool
}

func (c *countingTurnFailureSessions) ListTurnFailureCandidates(ctx context.Context, harness domain.AgentHarness) ([]domain.TurnFailureCandidate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.cancellable = c.cancellable || ctx.Done() != nil
	return fakeTurnFailureSessions{rows: c.rows}.ListTurnFailureCandidates(ctx, harness)
}

func (c *countingTurnFailureSessions) snapshot() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.cancellable
}

func startTurnFailureObserver(t *testing.T, sessions *countingTurnFailureSessions, wake chan struct{}) (stop func()) {
	t.Helper()
	observer := NewTurnFailureObserver(sessions, &fakeSink{}, fakeAgents{domain.HarnessCodex: codex.New()}, TurnFailureConfig{
		Tick: 5 * time.Millisecond, IdleTick: time.Hour, Wake: wake,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := observer.Start(ctx)
	return func() { cancel(); <-done }
}

// With no turn to watch, the observer waits for its idle tick or a session change.
func TestTurnFailureObserverRestsWithNoTurnAndWakesOnChange(t *testing.T) {
	sessions := &countingTurnFailureSessions{}
	wake := make(chan struct{}, 1)
	stop := startTurnFailureObserver(t, sessions, wake)
	defer stop()
	waitForCalls(t, sessions, 1)
	time.Sleep(100 * time.Millisecond) // 20 ticks: only extra polls could change the count
	if calls, _ := sessions.snapshot(); calls != 1 {
		t.Fatalf("queried %d times with no turn running, want only the first poll", calls)
	}
	wake <- struct{}{}
	waitForCalls(t, sessions, 2)
	if _, cancellable := sessions.snapshot(); cancellable {
		t.Fatal("candidates were read with a cancellable context")
	}
}

// While a turn is watched the observer keeps its short tick.
func TestTurnFailureObserverStaysFastWhileATurnRuns(t *testing.T) {
	sessions := &countingTurnFailureSessions{rows: []domain.TurnFailureCandidate{turnFailureCandidate(t, time.Now().UTC())}}
	stop := startTurnFailureObserver(t, sessions, make(chan struct{}))
	defer stop()
	// Five polls at a 5 ms tick; the idle tick is an hour.
	waitForCalls(t, sessions, 5)
}

// A turn whose transcript cannot be read yet opens no watch, but it is still a
// turn in progress: the observer keeps its short tick, so a failure written to
// the transcript later is caught within one tick, not one idle tick.
func TestTurnFailureObserverStaysFastBeforeTheTranscriptIsReadable(t *testing.T) {
	candidate := turnFailureCandidate(t, time.Now().UTC())
	candidate.NativeTranscriptPath = filepath.Join(t.TempDir(), "not-written-yet.jsonl")
	sessions := &countingTurnFailureSessions{rows: []domain.TurnFailureCandidate{candidate}}
	stop := startTurnFailureObserver(t, sessions, make(chan struct{}))
	defer stop()
	// Five polls at a 5 ms tick; the idle tick is an hour.
	waitForCalls(t, sessions, 5)
}

// A turn starting changes its session, so a change while no turn is watched
// polls at once rather than one tick later: a starting turn is seen no later
// than the 2 s tick would have seen it.
func TestTurnFailureObserverPollsAtOnceOnChangeWhileIdle(t *testing.T) {
	sessions := &countingTurnFailureSessions{}
	wake := make(chan struct{}, 1)
	observer := NewTurnFailureObserver(sessions, &fakeSink{}, fakeAgents{domain.HarnessCodex: codex.New()}, TurnFailureConfig{
		Tick: 300 * time.Millisecond, IdleTick: time.Hour, Wake: wake,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := observer.Start(ctx)
	defer func() { cancel(); <-done }()
	waitForCalls(t, sessions, 1)
	time.Sleep(400 * time.Millisecond) // past one tick since the first poll

	woke := time.Now()
	wake <- struct{}{}
	for {
		if calls, _ := sessions.snapshot(); calls >= 2 {
			return
		}
		if time.Since(woke) > 200*time.Millisecond {
			t.Fatal("the poll after a session change took over 200 ms; want at once (tick 300 ms)")
		}
		time.Sleep(time.Millisecond)
	}
}

// waitForCalls waits, up to 2 s, until the observer has queried at least n
// times.
func waitForCalls(t *testing.T, sessions *countingTurnFailureSessions, n int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(time.Millisecond) {
		calls, _ := sessions.snapshot()
		if calls >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queried %d times, want at least %d", calls, n)
		}
	}
}
