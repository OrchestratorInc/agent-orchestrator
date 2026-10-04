package agentauth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

type fakeLoginWatcher struct {
	mu      sync.Mutex
	alive   []bool
	calls   int
	forever bool
}

func (w *fakeLoginWatcher) IsShellTerminalChildAlive(_ context.Context, _ string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.forever {
		return true, nil
	}
	idx := w.calls - 1
	if idx >= len(w.alive) {
		idx = len(w.alive) - 1
	}
	return w.alive[idx], nil
}

type fakeRefresher struct {
	mu          sync.Mutex
	invalidated []string
	rechecked   chan string
}

func (r *fakeRefresher) InvalidateAgentAuthentication(agentID string) {
	r.mu.Lock()
	r.invalidated = append(r.invalidated, agentID)
	r.mu.Unlock()
}

func (r *fakeRefresher) RecheckAgent(agentID string) { r.rechecked <- agentID }

func (r *fakeRefresher) invalidatedSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.invalidated...)
}

func TestStartRefreshesReadinessWhenLoginCompletes(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{terminal: shellterm.ShellTerminal{HandleID: "handle-login"}}
	svc := New(foundExecutable("pi"), opener)
	watcher := &fakeLoginWatcher{alive: []bool{true, true, false}}
	refresher := &fakeRefresher{rechecked: make(chan string, 1)}
	svc.EnableLoginCompletionRefresh(context.Background(), watcher, refresher)
	svc.pollInterval = time.Millisecond
	svc.initialGrace = time.Millisecond

	if _, err := svc.Start(context.Background(), "pi"); err != nil {
		t.Fatalf("Start(pi): %v", err)
	}

	select {
	case id := <-refresher.rechecked:
		if id != "pi" {
			t.Fatalf("RecheckAgent(%q), want pi", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login completion did not refresh readiness")
	}
	if inv := refresher.invalidatedSnapshot(); len(inv) != 1 || inv[0] != "pi" {
		t.Fatalf("InvalidateAgentAuthentication calls = %v, want [pi]", inv)
	}
}

func TestLoginCompletionWatchStopsWhenContextCancelled(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{terminal: shellterm.ShellTerminal{HandleID: "handle-running"}}
	svc := New(foundExecutable("pi"), opener)
	watcher := &fakeLoginWatcher{forever: true}
	refresher := &fakeRefresher{rechecked: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	svc.EnableLoginCompletionRefresh(ctx, watcher, refresher)
	svc.pollInterval = time.Millisecond

	if _, err := svc.Start(context.Background(), "pi"); err != nil {
		t.Fatalf("Start(pi): %v", err)
	}

	// The login command never exits, so readiness must not be refreshed; once the
	// daemon context is cancelled the monitor must stop instead of leaking.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case id := <-refresher.rechecked:
		t.Fatalf("refreshed readiness for %q while login was still running", id)
	case <-time.After(50 * time.Millisecond):
	}
	if inv := refresher.invalidatedSnapshot(); len(inv) != 0 {
		t.Fatalf("InvalidateAgentAuthentication calls = %v, want none", inv)
	}
}

func TestStartWithoutLoginCompletionWiringDoesNotPanic(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{terminal: shellterm.ShellTerminal{HandleID: "handle-plain"}}
	svc := New(foundExecutable("pi"), opener)

	if _, err := svc.Start(context.Background(), "pi"); err != nil {
		t.Fatalf("Start(pi) without login-completion wiring: %v", err)
	}
}
