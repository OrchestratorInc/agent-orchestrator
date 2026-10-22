package workertransport

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

// fakeAgentTUI is a harness TUI stand-in. It boots for boot seconds, losing
// every key pressed meanwhile; then it takes input, enabling bracketed paste
// when signal is set. Every line it reads is logged. A submitted prompt is
// recorded in its Claude transcript when record is set.
const fakeAgentTUI = `sleep "$FAKE_BOOT"
stty -icanon min 0 time 0
cat >/dev/null
stty icanon
if [ "$FAKE_SIGNAL" = 1 ]; then printf '\033[?2004h'; fi
while IFS= read -r line; do
	printf '%s\n' "$line" >> "$FAKE_LOG"
	if [ "$FAKE_RECORD" = 1 ]; then printf '%s\n' "$FAKE_RECORD_LINE" >> "$FAKE_TRANSCRIPT"; fi
done
`

type fakeAgent struct {
	harness string
	boot    string
	signal  bool
	record  bool
	// noTranscript leaves the worker without the agent's transcript.
	noTranscript bool
}

type forwardSpy struct {
	supervisorControlStub
	mu        sync.Mutex
	turn      *worker.Turn
	claims    int
	completed int
	failed    int
}

func (c *forwardSpy) ClaimTurn(context.Context) (*worker.Turn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claims++
	turn := c.turn
	c.turn = nil
	return turn, nil
}

func (c *forwardSpy) CompleteTurn(context.Context, string, int, bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.completed++
	return nil
}

func (c *forwardSpy) FailTurn(context.Context, string, int, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed++
	return nil
}

func (c *forwardSpy) counts() (claims, completed, failed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claims, c.completed, c.failed
}

const fakeAgentTerminalID = "00000000-0000-0000-0000-000000000091"

// startFakeAgent opens the agent terminal running fake, with prompt queued.
// It returns the supervisor, its control, the file the agent logs the lines
// it reads to, and the agent's transcript.
func startFakeAgent(t *testing.T, ctx context.Context, fake fakeAgent, prompt string) (*Supervisor, *forwardSpy, string, string) {
	t.Helper()
	previousPoll := terminalTurnPollInterval
	terminalTurnPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { terminalTurnPollInterval = previousPoll })
	configDir, logDir := t.TempDir(), t.TempDir()
	transcript := filepath.Join(configDir, "projects", "-home-ao-work", "4f2c8a51-1f0e-4b7e-9a39-3d5f0c1b2a77.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "input.log")
	flag := func(on bool) string {
		if on {
			return "1"
		}
		return "0"
	}
	env := map[string]string{
		"FAKE_BOOT": fake.boot, "FAKE_SIGNAL": flag(fake.signal),
		"FAKE_RECORD": flag(fake.record), "FAKE_RECORD_LINE": strings.TrimSpace(claudePrompt),
		"FAKE_LOG": logPath, "FAKE_TRANSCRIPT": transcript,
	}
	if !fake.noTranscript {
		env["CLAUDE_CONFIG_DIR"] = configDir
	}
	control := &forwardSpy{turn: &worker.Turn{ID: "turn-1", Attempt: 1, Prompt: prompt}}
	harness := fake.harness
	if harness == "" {
		harness = "claude-code"
	}
	supervisor := &Supervisor{
		Control: control, Workspace: t.TempDir(), Shell: "/bin/sh", Harness: harness,
		AgentTerminalID: fakeAgentTerminalID, agentStarted: true, workspaceReady: true,
		AgentCommand: workerexec.Command{Path: "/bin/sh", Args: []string{"-c", fakeAgentTUI}, Env: env},
		terminals:    make(map[string]*terminalProcess),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	t.Cleanup(supervisor.closeAllTerminals)
	if err := supervisor.openTerminal(ctx, worker.TerminalCommand{TerminalID: fakeAgentTerminalID, Kind: "agent"}); err != nil {
		t.Fatal(err)
	}
	return supervisor, control, logPath, transcript
}

// forwardUntilHandled drives forwardTurn as Run's loop does until it handles
// a turn, and returns when it was handled.
func forwardUntilHandled(t *testing.T, ctx context.Context, supervisor *Supervisor) time.Time {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		handled, err := supervisor.forwardTurn(ctx)
		if err != nil {
			t.Fatalf("forward turn: %v", err)
		}
		if handled {
			return time.Now()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the queued turn was never forwarded")
	return time.Time{}
}

// agentInputLines waits for the fake agent to have read want lines (or a
// second more), and returns the lines it read.
func agentInputLines(t *testing.T, logPath string, want int) []string {
	t.Helper()
	var lines []string
	deadline := time.Now().Add(time.Second)
	for {
		data, _ := os.ReadFile(logPath)
		lines = nil
		if text := strings.TrimSuffix(string(data), "\n"); len(data) > 0 {
			lines = strings.Split(text, "\n")
		}
		if len(lines) >= want || time.Now().After(deadline) {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForAgentInputReady(t *testing.T, supervisor *Supervisor) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !supervisor.agentInputReady(fakeAgentTerminalID) {
		if time.Now().After(deadline) {
			t.Fatal("the agent terminal never took input")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func transcriptRecords(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// Staging sessions a280a5ce and 1669efc6: a Chat session switched to the
// terminal during Setup kept its first prompt queued. The forwarder typed it
// the moment the agent's PTY existed, before Claude's TUI took input, and
// completed the turn; the task never ran. The forwarder must wait for the
// agent to take input.
func TestForwardTurnWaitsForTheAgentToTakeInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := time.Now()
	supervisor, control, logPath, transcript := startFakeAgent(t, ctx,
		fakeAgent{boot: "0.8", signal: true, record: true}, "fix the flaky test")

	forwarded := forwardUntilHandled(t, ctx, supervisor)

	lines := agentInputLines(t, logPath, 1)
	if len(lines) != 1 || lines[0] != "fix the flaky test" {
		t.Fatalf("agent read %q, want the prompt submitted once", lines)
	}
	if forwarded.Sub(opened) < 800*time.Millisecond {
		t.Fatalf("turn forwarded %v after the agent started, before it took input", forwarded.Sub(opened))
	}
	if records := transcriptRecords(t, transcript); records != 1 {
		t.Fatalf("agent started %d turns, want 1", records)
	}
	if claims, completed, failed := control.counts(); claims != 1 || completed != 1 || failed != 0 {
		t.Fatalf("claims/completed/failed = %d/%d/%d, want 1/1/0", claims, completed, failed)
	}
}

// A message to an agent already taking input is typed at once.
func TestForwardTurnTypesAtOnceIntoAReadyAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor, control, logPath, transcript := startFakeAgent(t, ctx,
		fakeAgent{boot: "0", signal: true, record: true}, "next step")
	waitForAgentInputReady(t, supervisor)

	started := time.Now()
	handled, err := supervisor.forwardTurn(ctx)
	if err != nil || !handled {
		t.Fatalf("forward turn into a ready agent = (%v, %v), want (true, nil)", handled, err)
	}
	if elapsed := time.Since(started); elapsed >= promptEnterDelay+500*time.Millisecond {
		t.Fatalf("forwarding into a ready agent took %v", elapsed)
	}
	if lines := agentInputLines(t, logPath, 1); len(lines) != 1 || lines[0] != "next step" {
		t.Fatalf("agent read %q, want the prompt once", lines)
	}
	if records := transcriptRecords(t, transcript); records != 1 {
		t.Fatalf("agent started %d turns, want 1", records)
	}
	if claims, completed, _ := control.counts(); claims != 1 || completed != 1 {
		t.Fatalf("claims/completed = %d/%d, want 1/1", claims, completed)
	}
}

// An agent that never enables bracketed paste is forwarded to once the wait's
// bound passes, as before the wait, and the loop is woken for it.
func TestForwardTurnProceedsWhenTheAgentNeverSignals(t *testing.T) {
	previous := agentInputReadyTimeout
	agentInputReadyTimeout = 600 * time.Millisecond
	defer func() { agentInputReadyTimeout = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := time.Now()
	supervisor, control, logPath, _ := startFakeAgent(t, ctx,
		fakeAgent{boot: "0", noTranscript: true}, "run the tests")

	handled, err := supervisor.forwardTurn(ctx)
	if err != nil || handled {
		t.Fatalf("forward turn before the bound = (%v, %v), want (false, nil)", handled, err)
	}
	if claims, _, _ := control.counts(); claims != 0 {
		t.Fatalf("claimed %d turns before the agent took input", claims)
	}
	select {
	case <-supervisor.loopWakeChannel():
	case <-time.After(5 * time.Second):
		t.Fatal("the loop was not woken at the wait's bound")
	}
	forwarded := forwardUntilHandled(t, ctx, supervisor)
	if forwarded.Sub(opened) < agentInputReadyTimeout {
		t.Fatalf("turn forwarded %v after the agent started, before the bound", forwarded.Sub(opened))
	}
	if lines := agentInputLines(t, logPath, 1); len(lines) != 1 || lines[0] != "run the tests" {
		t.Fatalf("agent read %q, want the prompt once", lines)
	}
	if claims, completed, failed := control.counts(); claims != 1 || completed != 1 || failed != 0 {
		t.Fatalf("claims/completed/failed = %d/%d/%d, want 1/1/0", claims, completed, failed)
	}
}

// Once the agent takes input, forwardTurn holds Run's serial loop only for
// the paste-then-Enter beat: it completes the turn right after the write and
// never waits for the agent to start it, nor presses Enter again.
func TestForwardTurnDoesNotWaitForTheTurnToStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The worker reads this agent's transcript, which never shows the turn.
	supervisor, control, logPath, _ := startFakeAgent(t, ctx,
		fakeAgent{boot: "0", signal: true}, "deploy")
	waitForAgentInputReady(t, supervisor)

	started := time.Now()
	handled, err := supervisor.forwardTurn(ctx)
	if err != nil || !handled {
		t.Fatalf("forward turn = (%v, %v), want (true, nil)", handled, err)
	}
	if elapsed := time.Since(started); elapsed >= promptEnterDelay+500*time.Millisecond {
		t.Fatalf("forward turn held the loop for %v", elapsed)
	}
	if claims, completed, failed := control.counts(); claims != 1 || completed != 1 || failed != 0 {
		t.Fatalf("claims/completed/failed on return = %d/%d/%d, want 1/1/0", claims, completed, failed)
	}
	if lines := agentInputLines(t, logPath, 2); len(lines) != 1 || lines[0] != "deploy" {
		t.Fatalf("agent read %q, want the prompt once", lines)
	}
}

// Only harnesses known to enable bracketed paste wait for it; the rest, and a
// worker re-attaching to a running agent, forward as before.
func TestAgentInputReadyGatesOnlySignallingHarnesses(t *testing.T) {
	for _, test := range []struct {
		harness    string
		reattached bool
		ready      bool
	}{
		{"claude-code", false, false},
		{"codex", false, false},
		{"claude-code", true, true},
		{"cursor", false, true},
		{"opencode", false, true},
		{"", false, true},
	} {
		terminal := &terminalProcess{}
		terminal.input.start(test.harness, test.reattached)
		supervisor := &Supervisor{
			terminals: map[string]*terminalProcess{"agent": terminal},
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		if got := supervisor.agentInputReady("agent"); got != test.ready {
			t.Fatalf("%s (reattached %v) ready = %v, want %v", test.harness, test.reattached, got, test.ready)
		}
	}
	var scan bracketedPasteScan
	if scan.enabled([]byte("\x1b[?20")) || !scan.enabled([]byte("04h")) {
		t.Fatal("bracketed paste split across reads was not seen")
	}
}
