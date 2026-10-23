//go:build darwin || linux

package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// runtimePaneMessenger writes through the real runtime the way the daemon's
// runtime messenger does; the agent's own input log is the receipt evidence.
type runtimePaneMessenger struct{ rt *conpty.Runtime }

func (m runtimePaneMessenger) Send(ctx context.Context, id domain.SessionID, msg string) error {
	return m.rt.SendMessage(ctx, ports.RuntimeHandle{ID: string(id)}, msg)
}

type nativeSendTarget struct {
	id        domain.SessionID
	dir       string
	runFile   string
	input     string
	reporting string
	release   string
	rt        *conpty.Runtime
	handle    ports.RuntimeHandle
}

const nativeLoggingAgent = `printf ready > "$AO_TEST_AGENT_READY"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$AO_TEST_AGENT_INPUT"
  if [ "$line" = "__exit__" ]; then exit 0; fi
done`

func startNativeSupervisedTarget(t *testing.T, id domain.SessionID, launch string, agentArgs ...string) *nativeSendTarget {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	n := &nativeSendTarget{
		id: id, dir: dir, runFile: filepath.Join(dir, "running.json"),
		input: filepath.Join(dir, "agent.input"), reporting: filepath.Join(dir, "supervisor.reporting"),
		release: filepath.Join(dir, "supervisor.release"),
	}
	n.rt = conpty.New(conpty.Options{RunFilePath: n.runFile})
	argv := append([]string{exe, "agent-process", "supervise", "--session", string(id), "--launch", launch, "--",
		"/bin/sh", "-c", nativeLoggingAgent}, agentArgs...)
	ready := filepath.Join(dir, "agent.ready")
	n.handle, err = n.rt.Create(context.Background(), ports.RuntimeConfig{
		SessionID: id, WorkspacePath: dir, Argv: argv,
		Env: map[string]string{
			"AO_RUNTIME_LAUNCH_ID":         launch,
			"AO_TEST_AGENT_READY":          ready,
			"AO_TEST_AGENT_INPUT":          n.input,
			"AO_TEST_SUPERVISOR_REPORTING": n.reporting,
			"AO_TEST_SUPERVISOR_RELEASE":   n.release,
		},
	})
	if err != nil {
		t.Fatalf("create native target: %v", err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(n.release, nil, 0o600)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = n.rt.Destroy(ctx, n.handle)
	})
	waitForNativeTestFile(t, ready, "ready")
	return n
}

func newNativeSendManager(t *testing.T, rt *conpty.Runtime, rec domain.SessionRecord) *Manager {
	t.Helper()
	st := newFakeStore()
	st.sessions[rec.ID] = rec
	return New(Deps{
		Runtime: rt, Agents: singleAgent{fakeAgent{}}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: runtimePaneMessenger{rt: rt}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
}

// A healthy supervised agent whose launch prompt contains "<defunct>" must
// still receive guarded sends.
func TestNativeSendReachesHealthyAgentWithDefunctInPrompt(t *testing.T) {
	n := startNativeSupervisedTarget(t, "defunct-send", "launch-1", "explain <defunct> processes")
	m := newNativeSendManager(t, n.rt, launchedTUISession(n.id, "launch-1"))
	if err := m.Send(context.Background(), n.id, "message for a healthy agent", nil); err != nil {
		t.Fatalf("Send to a healthy agent = %v, want delivery", err)
	}
	waitForNativeTestFile(t, n.input, "message for a healthy agent")
}

// A pre-upgrade registry entry has no supervised key. After the agent exits
// while its supervisor reports, a daemon that adopts the host must not
// acknowledge a send the agent can never read.
func TestNativeSendToAdoptedPreUpgradeExitedAgentIsNotAcknowledged(t *testing.T) {
	n := startNativeSupervisedTarget(t, "legacy-send", "launch-1")
	path := filepath.Join(n.dir, "windows-pty-hosts.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		delete(entry, "supervised")
	}
	rewritten, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := n.rt.SendInput(context.Background(), n.handle, "__exit__\r"); err != nil {
		t.Fatal(err)
	}
	waitForNativeTestFile(t, n.reporting, "reporting")

	adopted := conpty.New(conpty.Options{RunFilePath: n.runFile})
	m := newNativeSendManager(t, adopted, launchedTUISession(n.id, "launch-1"))
	err = m.Send(context.Background(), n.id, "message after agent exit", nil)
	if log, _ := os.ReadFile(n.input); strings.Contains(string(log), "message after agent exit") {
		t.Fatalf("an exited agent cannot read input; log=%q", log)
	}
	if err == nil {
		t.Fatal("Send to an adopted pre-upgrade target whose agent exited reported delivery")
	}
	if !errors.Is(err, ErrAgentExited) && !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("Send = %v, want an exited or inconclusive refusal", err)
	}
}
