package shellagent

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeRuntime replays foreground readings pushed on readings, the way a
// terminal host pushes a change, and serves a fixed screen.
type fakeRuntime struct {
	readings chan ports.TerminalForeground
	screen   string
}

func (f *fakeRuntime) WatchTerminalForeground(ctx context.Context, _ ports.RuntimeHandle, fn func(ports.TerminalForeground)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case fg := <-f.readings:
			fn(fg)
		}
	}
}

func (f *fakeRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return f.screen, nil
}

func newTestManager(t *testing.T, rt *fakeRuntime) *Manager {
	t.Helper()
	previous := startupGrace
	startupGrace = 0
	t.Cleanup(func() { startupGrace = previous })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewManager(ctx, rt, nil, nil)
}

func waitForAgent(t *testing.T, m *Manager, handleID string, want *Agent) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got *Agent
		for _, a := range m.Snapshot() {
			if a.HandleID == handleID {
				got = &a
			}
		}
		if (want == nil && got == nil) || (want != nil && got != nil && got.Harness == want.Harness && got.State == want.State) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent for %s = %+v, want %+v", handleID, got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestManagerFollowsAnAgentThroughATurn(t *testing.T) {
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := newTestManager(t, rt)
	m.TrackShellTerminal("shell-1")

	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: []string{"claude"}, Busy: true}
	waitForAgent(t, m, "shell-1", &Agent{Harness: domain.HarnessClaudeCode, State: StateWorking})

	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: []string{"claude"}}
	waitForAgent(t, m, "shell-1", &Agent{Harness: domain.HarnessClaudeCode, State: StateIdle})

	rt.screen = "Do you want to proceed?\n❯ 1. Yes\n  2. No"
	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: []string{"claude"}, Busy: true}
	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: []string{"claude"}}
	waitForAgent(t, m, "shell-1", &Agent{Harness: domain.HarnessClaudeCode, State: StateWaiting})

	rt.readings <- ports.TerminalForeground{PGID: 10, Argv: []string{"-zsh"}}
	waitForAgent(t, m, "shell-1", nil)
}

func TestManagerUntrackClearsTheAgent(t *testing.T) {
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := newTestManager(t, rt)
	m.TrackShellTerminal("shell-1")
	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: []string{"codex"}, Busy: true}
	waitForAgent(t, m, "shell-1", &Agent{Harness: domain.HarnessCodex, State: StateWorking})

	m.UntrackShellTerminal("shell-1")
	waitForAgent(t, m, "shell-1", nil)
}

// A recheck runs beside the watch loop. One that finishes after its terminal
// closed must not bring the agent back.
func TestManagerDropsAResultForAClosedTerminal(t *testing.T) {
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := newTestManager(t, rt)
	m.TrackShellTerminal("shell-1")
	m.mu.Lock()
	w := m.tracked["shell-1"]
	m.mu.Unlock()

	m.UntrackShellTerminal("shell-1")
	m.observe(context.Background(), "shell-1", w, ports.TerminalForeground{PGID: 20, Argv: []string{"claude"}, Busy: true})
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() = %+v after the terminal closed, want none", got)
	}
}

// A result whose reading was superseded while it read the screen is dropped,
// so it cannot overwrite the newer state.
func TestManagerDropsASupersededResult(t *testing.T) {
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := newTestManager(t, rt)
	m.TrackShellTerminal("shell-1")
	m.mu.Lock()
	w := m.tracked["shell-1"]
	gen := w.gen
	w.gen++ // a newer reading started
	m.mu.Unlock()

	if m.setIfCurrent("shell-1", w, gen, &Agent{HandleID: "shell-1", Harness: domain.HarnessClaudeCode, State: StateIdle}) {
		t.Fatal("setIfCurrent applied a superseded result")
	}
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() = %+v, want none", got)
	}
}
