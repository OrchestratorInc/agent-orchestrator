package shellagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestMain shortens the retry delays once: watch goroutines from an earlier
// test can still read them while a later test starts, so tests never write them.
func TestMain(m *testing.M) {
	initialWatchRetryDelay, watchRetryMax = time.Millisecond, time.Millisecond
	os.Exit(m.Run())
}

// fakeRuntime replays readings pushed on readings, the way a terminal host
// pushes changes, and serves a fixed screen.
type fakeRuntime struct {
	readings chan ports.TerminalForeground
	screen   string
	started  chan struct{}
	stopped  chan struct{}
	reads    atomic.Int32
}

func (f *fakeRuntime) WatchTerminalForeground(ctx context.Context, _ ports.RuntimeHandle, fn func(ports.TerminalForeground)) error {
	if f.started != nil {
		f.started <- struct{}{}
		defer func() { f.stopped <- struct{}{} }()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case fg := <-f.readings:
			fn(fg)
		}
	}
}

func TestManagerWatchesOnlyWhileSubscribed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground), started: make(chan struct{}, 1), stopped: make(chan struct{}, 1)}
	m := NewManager(ctx, rt, nil, nil)
	m.TrackShellTerminal("shell-1")
	select {
	case <-rt.started:
		t.Fatal("started watching before a client subscribed")
	default:
	}
	unsubscribe := m.SubscribeShellAgents(func(json.RawMessage) {})
	select {
	case <-rt.started:
	case <-time.After(time.Second):
		t.Fatal("did not start watching after subscribe")
	}
	second := m.SubscribeShellAgents(func(json.RawMessage) {})
	unsubscribe()
	select {
	case <-rt.stopped:
		t.Fatal("stopped watching while another client is subscribed")
	default:
	}
	second()
	select {
	case <-rt.stopped:
	case <-time.After(time.Second):
		t.Fatal("watch did not stop after unsubscribe")
	}
	unsubscribe = m.SubscribeShellAgents(func(json.RawMessage) {})
	select {
	case <-rt.started:
	case <-time.After(time.Second):
		t.Fatal("watch did not restart after resubscribe")
	}
	unsubscribe()
}

func TestManagerUntracksWithoutSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground), started: make(chan struct{}, 1), stopped: make(chan struct{}, 1)}
	m := NewManager(ctx, rt, nil, nil)
	m.TrackShellTerminal("shell-1")
	m.UntrackShellTerminal("shell-1")
	if len(m.tracked) != 0 {
		t.Fatalf("closed shell remained tracked: %+v", m.tracked)
	}
	unsubscribe := m.SubscribeShellAgents(func(json.RawMessage) {})
	defer unsubscribe()
	select {
	case <-rt.started:
		t.Fatal("started watching a shell closed before subscription")
	default:
	}
}

func (f *fakeRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	f.reads.Add(1)
	return f.screen, nil
}

func newTestManager(t *testing.T) (*Manager, *fakeRuntime) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := NewManager(ctx, rt, nil, nil)
	t.Cleanup(m.SubscribeShellAgents(func(json.RawMessage) {}))
	return m, rt
}

func waitForState(t *testing.T, m *Manager, want State) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		m.mu.Lock()
		got := m.current["shell-1"].State
		m.mu.Unlock()
		if got == want {
			return
		}
	}
	t.Fatalf("state never became %q", want)
}

func TestManagerFollowsAnAgentThroughATurn(t *testing.T) {
	m, rt := newTestManager(t)
	m.TrackShellTerminal("shell-1")
	claude := []string{"claude"}

	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: claude, Busy: true}
	waitForState(t, m, StateWorking)
	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: claude}
	waitForState(t, m, StateIdle)

	rt.screen = "Do you want to proceed?\n❯ 1. Yes\n  2. No"
	// A fast prompt can arrive as a settled screen without an intervening busy state.
	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: claude}
	waitForState(t, m, StateWaiting)

	rt.readings <- ports.TerminalForeground{PGID: 10, Argv: []string{"-zsh"}}
	waitForState(t, m, "")
}

// A screen read can finish after the terminal closed; its reading is dropped.
func TestManagerDropsAReadingForAClosedTerminal(t *testing.T) {
	m, _ := newTestManager(t)
	closed, cancel := context.WithCancel(context.Background())
	cancel()
	m.observe(closed, "shell-1", ports.TerminalForeground{PGID: 20, Argv: []string{"claude"}, Busy: true})
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.current) != 0 {
		t.Fatalf("closed terminal's agent came back: %+v", m.current)
	}
}

func TestManagerSendsSnapshotsToSubscribers(t *testing.T) {
	m, rt := newTestManager(t)
	got := make(chan []Agent, 4)
	unsubscribe := m.SubscribeShellAgents(func(snapshot json.RawMessage) {
		var agents []Agent
		if err := json.Unmarshal(snapshot, &agents); err != nil {
			t.Errorf("snapshot %s: %v", snapshot, err)
		}
		got <- agents
	})
	defer unsubscribe()
	if agents := <-got; len(agents) != 0 {
		t.Fatalf("first snapshot = %+v, want empty", agents)
	}

	m.TrackShellTerminal("shell-1")
	rt.readings <- ports.TerminalForeground{PGID: 20, Argv: []string{"codex"}, Busy: true}
	want := Agent{HandleID: "shell-1", Harness: domain.HarnessCodex, State: StateWorking}
	if agents := <-got; len(agents) != 1 || agents[0] != want {
		t.Fatalf("snapshot = %+v, want [%+v]", agents, want)
	}
}

func TestManagerKeepsAnIdleReadingWhileTheUserTypes(t *testing.T) {
	m, rt := newTestManager(t)
	m.TrackShellTerminal("shell-1")
	claude := []string{"claude"}
	rt.readings <- ports.TerminalForeground{PGID: 9, Argv: claude}
	waitForState(t, m, StateIdle)
	before := rt.reads.Load()

	for range 3 {
		rt.readings <- ports.TerminalForeground{PGID: 9, Argv: claude, EchoOnly: true}
	}
	// A reading the watch handles after the echoes proves they were processed.
	rt.readings <- ports.TerminalForeground{PGID: 9, Argv: claude, Busy: true}
	waitForState(t, m, StateWorking)
	if got := rt.reads.Load(); got != before {
		t.Fatalf("typing at an idle prompt read the screen %d times, want none", got-before)
	}
}

func TestManagerRereadsAWaitingAgentAfterTyping(t *testing.T) {
	m, rt := newTestManager(t)
	rt.screen = "Do you want to proceed? (y/n)"
	m.TrackShellTerminal("shell-1")
	rt.readings <- ports.TerminalForeground{PGID: 9, Argv: []string{"claude"}}
	waitForState(t, m, StateWaiting)

	// The answer cleared the prompt; the echo-only update must notice.
	rt.screen = "> "
	rt.readings <- ports.TerminalForeground{PGID: 9, Argv: []string{"claude"}, EchoOnly: true}
	waitForState(t, m, StateIdle)
}

// unreachableRuntime fails every connection the way a dead host's pipe does.
type unreachableRuntime struct{ dials atomic.Int32 }

func (u *unreachableRuntime) WatchTerminalForeground(context.Context, ports.RuntimeHandle, func(ports.TerminalForeground)) error {
	u.dials.Add(1)
	return errors.New("dial: connection refused")
}

func (u *unreachableRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return "", nil
}

func TestManagerStopsRetryingAnUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &unreachableRuntime{}
	m := NewManager(ctx, rt, nil, nil)
	t.Cleanup(m.SubscribeShellAgents(func(json.RawMessage) {}))
	m.TrackShellTerminal("shell-1")

	deadline := time.Now().Add(2 * time.Second)
	for int(rt.dials.Load()) < maxWatchFailures && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := int(rt.dials.Load()); got != maxWatchFailures {
		t.Fatalf("dialed an unreachable host %d times, want %d then stop", got, maxWatchFailures)
	}
}

func TestManagerRestartsAGivenUpWatchForANewSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &unreachableRuntime{}
	m := NewManager(ctx, rt, nil, nil)
	t.Cleanup(m.SubscribeShellAgents(func(json.RawMessage) {}))
	m.TrackShellTerminal("shell-1")
	gaveUp := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.tracked["shell-1"] == nil
	}
	deadline := time.Now().Add(2 * time.Second)
	for !gaveUp() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !gaveUp() {
		t.Fatal("the watch never gave up on the unreachable host")
	}

	// Another client subscribes while the first is still connected.
	t.Cleanup(m.SubscribeShellAgents(func(json.RawMessage) {}))
	deadline = time.Now().Add(2 * time.Second)
	for int(rt.dials.Load()) <= maxWatchFailures && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := int(rt.dials.Load()); got <= maxWatchFailures {
		t.Fatalf("dials = %d after a new subscriber, want the watch restarted", got)
	}
}

func TestManagerRecordsHarnessOncePerChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := NewManager(ctx, rt, nil, nil)
	recorded := make(chan domain.AgentHarness, 4)
	m.SetHarnessRecorder(func(_ context.Context, handleID string, harness domain.AgentHarness) {
		if handleID == "shell-1" {
			recorded <- harness
		}
	})
	m.TrackShellTerminal("shell-1")
	defer m.SubscribeShellAgents(func(json.RawMessage) {})()
	for _, fg := range []ports.TerminalForeground{
		{Argv: []string{"claude"}, Busy: true},
		{Argv: []string{"claude"}, Busy: true},
		{Argv: []string{"zsh"}},
		{Argv: []string{"codex"}, Busy: true},
	} {
		rt.readings <- fg
	}
	want := []domain.AgentHarness{domain.HarnessClaudeCode, "", domain.HarnessCodex}
	for _, harness := range want {
		select {
		case got := <-recorded:
			if got != harness {
				t.Fatalf("recorded %q, want %q", got, harness)
			}
		case <-time.After(time.Second):
			t.Fatalf("harness %q not recorded", harness)
		}
	}
	select {
	case extra := <-recorded:
		t.Fatalf("recorded %q again", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

type fakeAgents struct{ agent ports.Agent }

func (f fakeAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return f.agent, f.agent != nil }

type conversationAgent struct {
	ports.Agent
	id      string
	history bool
	pid     int
}

func (c *conversationAgent) ProcessConversationID(_ context.Context, pid int) (string, bool, error) {
	c.pid = pid
	return c.id, c.id != "", nil
}

func (c *conversationAgent) NativeConversationExists(context.Context, ports.SessionRef, string, map[string]string) (bool, error) {
	return c.history, nil
}

func TestReadShellForegroundNamesFolderAgentAndConversation(t *testing.T) {
	ctx := context.Background()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const id = "0f8c2b9e-3c1a-4d5e-9f00-1a2b3c4d5e6f"
	for _, tc := range []struct {
		name     string
		argv     []string
		history  bool
		wantH    domain.AgentHarness
		wantConv string
	}{
		{"agent with history", []string{"claude"}, true, domain.HarnessClaudeCode, id},
		{"agent never prompted", []string{"claude"}, false, domain.HarnessClaudeCode, ""},
		{"shell prompt", []string{"-zsh"}, true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &fakeRuntime{readings: make(chan ports.TerminalForeground, 1)}
			agent := &conversationAgent{id: id, history: tc.history}
			m := NewManager(ctx, rt, fakeAgents{agent}, nil)
			rt.readings <- ports.TerminalForeground{PGID: os.Getpid(), Argv: tc.argv}
			dir, harness, conversation, ok := m.ReadShellForeground(ctx, "shell-1")
			if !ok || harness != tc.wantH || conversation != tc.wantConv {
				t.Fatalf("read = %q %q %q %v", dir, harness, conversation, ok)
			}
			if goruntime.GOOS == "darwin" || goruntime.GOOS == "linux" {
				gotDir, _ := filepath.EvalSymlinks(dir)
				wantDir, _ := filepath.EvalSymlinks(wd)
				if gotDir != wantDir {
					t.Fatalf("dir = %q, want %q", dir, wd)
				}
			}
			if tc.wantH != "" && agent.pid != os.Getpid() {
				t.Fatalf("conversation asked for pid %d", agent.pid)
			}
		})
	}
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := NewManager(ctx, rt, nil, nil)
	foregroundReadTimeout = 20 * time.Millisecond
	t.Cleanup(func() { foregroundReadTimeout = 3 * time.Second })
	if _, _, _, ok := m.ReadShellForeground(ctx, "shell-1"); ok {
		t.Fatal("a terminal that never answered was read")
	}
}

func TestManagerRecordsFirstReadingWithoutAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &fakeRuntime{readings: make(chan ports.TerminalForeground)}
	m := NewManager(ctx, rt, nil, nil)
	recorded := make(chan domain.AgentHarness, 4)
	m.SetHarnessRecorder(func(_ context.Context, _ string, harness domain.AgentHarness) { recorded <- harness })
	m.TrackShellTerminal("shell-1")
	defer m.SubscribeShellAgents(func(json.RawMessage) {})()
	rt.readings <- ports.TerminalForeground{Argv: []string{"zsh"}}
	rt.readings <- ports.TerminalForeground{Argv: []string{"zsh"}}
	select {
	case got := <-recorded:
		if got != "" {
			t.Fatalf("recorded %q, want \"\"", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first reading not recorded")
	}
	select {
	case extra := <-recorded:
		t.Fatalf("recorded %q again", extra)
	case <-time.After(50 * time.Millisecond):
	}
}
