package workerexec

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// prewarmFixture runs Claude Chat turns against the fake ACP provider
// (TestCloudPromptProviderHelper), every agent appending to one capture.
type prewarmFixture struct {
	capture    string
	control    *promptControl
	supervisor *Supervisor
}

func newPrewarmFixture(t *testing.T, warm worker.Turn, gate <-chan struct{}) *prewarmFixture {
	t.Helper()
	dataDir, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dataDir, "claude"))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "claude-agent-acp")
	wrapper := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestCloudPromptProviderHelper$' -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "requests.jsonl")
	builder := HarnessBuilder{
		DataDir: dataDir, Binaries: map[string]string{"claude-code": binary},
		Launch: worker.LaunchContext{SessionID: "session-1", Kind: "worker", Harness: "claude-code", Mode: "trusted"},
		Env: map[string]string{
			"AO_PROMPT_TEST": "1", "AO_PROMPT_CAPTURE": capture, "AO_PROMPT_CAPTURE_APPEND": "1",
			"AO_PROMPT_TEST_HARNESS": "claude-code",
		},
	}
	credential := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "test-secret"}
	control := &promptControl{controlStub: controlStub{credential: credential}}
	supervisor := &Supervisor{
		Control: control, Builder: builder, UseProviderProtocol: true, Workspace: workspace,
		CancelInterval: time.Millisecond, CompletionRetry: time.Millisecond,
		WarmTurn: &warm, SessionGate: gate,
	}
	// As Run does when it returns.
	t.Cleanup(supervisor.discardWarmACP)
	return &prewarmFixture{capture: capture, control: control, supervisor: supervisor}
}

type capturedFrame struct {
	PID    int            `json:"pid"`
	Args   []string       `json:"args"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func (f *prewarmFixture) frames(t *testing.T) []capturedFrame {
	t.Helper()
	file, err := os.Open(f.capture)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var frames []capturedFrame
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var frame capturedFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			continue // a line still being written
		}
		frames = append(frames, frame)
	}
	return frames
}

// waitFor polls the capture until match holds.
func (f *prewarmFixture) waitFor(t *testing.T, what string, match func([]capturedFrame) bool) []capturedFrame {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		frames := f.frames(t)
		if match(frames) {
			return frames
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; frames: %+v", what, frames)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func agentPIDs(frames []capturedFrame) []int {
	var pids []int
	for _, frame := range frames {
		if len(frame.Args) > 0 {
			pids = append(pids, frame.PID)
		}
	}
	return pids
}

// promptPID is the agent that received the prompt text.
func promptPID(frames []capturedFrame, text string) int {
	for _, frame := range frames {
		if frame.Method != "session/prompt" {
			continue
		}
		raw, _ := json.Marshal(frame.Params["prompt"])
		if strings.Contains(string(raw), text) {
			return frame.PID
		}
	}
	return 0
}

func hasMethod(frames []capturedFrame, pid int, method string) bool {
	for _, frame := range frames {
		if frame.PID == pid && frame.Method == method {
			return true
		}
	}
	return false
}

func TestPrewarmedAgentServesFirstTurnAndOpensSessionAfterGate(t *testing.T) {
	gate := make(chan struct{})
	fixture := newPrewarmFixture(t, worker.Turn{Harness: "claude-code", Mode: "trusted"}, gate)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	fixture.supervisor.Prewarm(ctx)
	frames := fixture.waitFor(t, "the prewarmed agent's handshake", func(frames []capturedFrame) bool {
		pids := agentPIDs(frames)
		return len(pids) == 1 && hasMethod(frames, pids[0], "initialize")
	})
	warmPID := agentPIDs(frames)[0]
	// The checkout is not ready: the session must wait for it.
	time.Sleep(100 * time.Millisecond)
	if hasMethod(fixture.frames(t), warmPID, "session/new") {
		t.Fatal("prewarmed agent opened its session before the checkout was ready")
	}
	close(gate)
	fixture.waitFor(t, "the prewarmed session", func(frames []capturedFrame) bool {
		return hasMethod(frames, warmPID, "session/new")
	})

	turn := worker.Turn{ID: "turn-1", Attempt: 1, Harness: "claude-code", Mode: "trusted", Prompt: "FIRST TURN"}
	if err := fixture.supervisor.execute(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if fixture.control.failed != "" || !fixture.control.completed {
		t.Fatalf("turn failed: %q", fixture.control.failed)
	}
	if got := promptPID(fixture.frames(t), "FIRST TURN"); got != warmPID {
		t.Fatalf("first turn ran in agent %d, want the prewarmed agent %d", got, warmPID)
	}
	// The created session's identity is still published by the turn.
	published := false
	for _, activity := range fixture.control.activity {
		published = published || (activity.Event == "session-start" && activity.AgentSessionID == "native-1")
	}
	if !published {
		t.Fatalf("prewarmed session identity not published: %+v", fixture.control.activity)
	}
}

func TestPrewarmedAgentForAnotherLaunchIsReplaced(t *testing.T) {
	// Prewarmed to restore a conversation the turn does not continue.
	fixture := newPrewarmFixture(t, worker.Turn{Harness: "claude-code", Mode: "trusted", AgentSessionID: "native-1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	fixture.supervisor.Prewarm(ctx)
	frames := fixture.waitFor(t, "the prewarmed agent", func(frames []capturedFrame) bool {
		pids := agentPIDs(frames)
		return len(pids) == 1 && hasMethod(frames, pids[0], "session/load")
	})
	warmPID := agentPIDs(frames)[0]

	turn := worker.Turn{ID: "turn-1", Attempt: 1, Harness: "claude-code", Mode: "trusted", Prompt: "NEW CONVERSATION"}
	if err := fixture.supervisor.execute(ctx, turn); err != nil {
		t.Fatal(err)
	}
	frames = fixture.frames(t)
	got := promptPID(frames, "NEW CONVERSATION")
	if got == 0 || got == warmPID {
		t.Fatalf("turn ran in agent %d; want a new agent, not the mismatched prewarm %d", got, warmPID)
	}
	if !hasMethod(frames, got, "session/new") {
		t.Fatal("the turn's own agent did not create its session")
	}
}

func TestEachTurnPrewarmsTheNext(t *testing.T) {
	fixture := newPrewarmFixture(t, worker.Turn{Harness: "claude-code", Mode: "trusted"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	first := worker.Turn{ID: "turn-1", Attempt: 1, Harness: "claude-code", Mode: "trusted", Prompt: "TURN ONE"}
	if err := fixture.supervisor.execute(ctx, first); err != nil {
		t.Fatal(err)
	}
	firstPID := promptPID(fixture.frames(t), "TURN ONE")
	// The first turn created native-1; the next turn's agent restores it
	// before that turn arrives.
	frames := fixture.waitFor(t, "the next turn's prewarmed agent", func(frames []capturedFrame) bool {
		for _, pid := range agentPIDs(frames) {
			if pid != firstPID && hasMethod(frames, pid, "session/load") {
				return true
			}
		}
		return false
	})
	var warmPID int
	for _, pid := range agentPIDs(frames) {
		if pid != firstPID {
			warmPID = pid
		}
	}

	second := worker.Turn{ID: "turn-2", Attempt: 1, Harness: "claude-code", Mode: "trusted", Prompt: "TURN TWO", AgentSessionID: "native-1"}
	if err := fixture.supervisor.execute(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got := promptPID(fixture.frames(t), "TURN TWO"); got != warmPID {
		t.Fatalf("second turn ran in agent %d, want the agent prewarmed after the first turn %d", got, warmPID)
	}
}

func TestDiscardStopsPrewarmedAgent(t *testing.T) {
	fixture := newPrewarmFixture(t, worker.Turn{Harness: "claude-code", Mode: "trusted"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	fixture.supervisor.Prewarm(ctx)
	frames := fixture.waitFor(t, "the prewarmed session", func(frames []capturedFrame) bool {
		pids := agentPIDs(frames)
		return len(pids) == 1 && hasMethod(frames, pids[0], "session/new")
	})
	pid := agentPIDs(frames)[0]
	fixture.supervisor.discardWarmACP()
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatalf("prewarmed agent %d still running after discard", pid)
	}
	// Prewarm runs once per worker: a later Chat activation follows a
	// terminal handoff and must not prewarm.
	fixture.supervisor.Prewarm(ctx)
	fixture.supervisor.warmMu.Lock()
	warm := fixture.supervisor.warm
	fixture.supervisor.warmMu.Unlock()
	if warm != nil {
		t.Fatal("a second Chat activation prewarmed an agent")
	}
}
