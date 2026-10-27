package workertransport

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
	"github.com/creack/pty"
)

// requireDtach skips unless the real dependencies exist: dtach, and /proc for
// verifying the recorded agent PID (Freestyle VMs are Linux).
func requireDtach(t *testing.T) *persistentAgent {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("persistent agent verification reads /proc")
	}
	persist := newPersistentAgent(t.TempDir())
	if persist == nil {
		t.Skip("dtach is not installed")
	}
	return persist
}

// startClient runs one dtach client under a PTY, as the supervisor does.
func startClient(t *testing.T, persist *persistentAgent, script string) (*exec.Cmd, *os.File) {
	t.Helper()
	command := persist.command(context.Background(), "/bin/sh", []string{"-c", script})
	command.Env = terminalEnvironment(persist.environment())
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatalf("start dtach client: %v", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	return command, terminal
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPersistentAgentOutlivesItsClientAndIsReattached(t *testing.T) {
	persist := requireDtach(t)
	t.Cleanup(func() { _ = persist.stop(time.Second) })
	first, _ := startClient(t, persist, "sleep 300")
	var pid int
	waitFor(t, "the agent to start", func() bool {
		var ok bool
		pid, ok = persist.running()
		return ok
	})

	// The worker exiting kills only its client.
	_ = first.Process.Kill()
	_ = first.Wait()
	if again, ok := persist.running(); !ok || again != pid {
		t.Fatalf("agent did not survive its client: running=%v pid=%d want %d", ok, again, pid)
	}

	// The next worker's launch attaches to the same agent instead of starting
	// another one.
	second, _ := startClient(t, persist, "echo must-not-run; sleep 300")
	time.Sleep(300 * time.Millisecond)
	if again, ok := persist.running(); !ok || again != pid {
		t.Fatalf("second launch did not attach: running=%v pid=%d want %d", ok, again, pid)
	}

	// A deliberate stop ends the agent, and the attached client follows.
	if err := persist.stop(5 * time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, ok := persist.running(); ok {
		t.Fatal("agent still running after stop")
	}
	exited := make(chan error, 1)
	go func() { exited <- second.Wait() }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("dtach client did not exit after its agent stopped")
	}
}

func TestPersistentAgentStopEndsTheWholeProcessGroup(t *testing.T) {
	persist := requireDtach(t)
	childFile := filepath.Join(t.TempDir(), "child.pid")
	// The agent starts a background job, as a dev server or test run would.
	startClient(t, persist, "sleep 300 & echo $! > "+childFile+"; wait")
	var child int
	waitFor(t, "the background job", func() bool {
		data, err := os.ReadFile(childFile)
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && child > 0
	})
	if err := persist.stop(5 * time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, "the background job to end", func() bool {
		return syscall.Kill(child, 0) != nil
	})
}

func TestPersistentAgentReportsTheAgentsOwnExitCode(t *testing.T) {
	persist := requireDtach(t)
	client, _ := startClient(t, persist, "exit 7")
	_ = client.Wait()
	waitFor(t, "the exit status", func() bool {
		code, ok := persist.exitCode()
		return ok && code == 7
	})
	if _, ok := persist.running(); ok {
		t.Fatal("an exited agent is reported as running")
	}
}

// A stale PID file must never get an unrelated process signalled: PIDs are
// reused, so only a process carrying the launcher marker counts.
func TestPersistentAgentIgnoresAReusedPID(t *testing.T) {
	persist := requireDtach(t)
	unrelated := exec.Command("sleep", "300")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() })
	if err := os.WriteFile(persist.pidFile(), []byte(strconv.Itoa(unrelated.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := persist.running(); ok {
		t.Fatal("an unrelated process was taken for the agent")
	}
	if err := persist.stop(time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := syscall.Kill(unrelated.Process.Pid, 0); err != nil {
		t.Fatalf("stop signalled an unrelated process: %v", err)
	}
}

// Without the switch, or without dtach, agents launch exactly as before.
func TestAgentPersistenceIsOptIn(t *testing.T) {
	off := &Supervisor{DataDir: t.TempDir()}
	if off.persistsAgent(worker.TerminalCommand{Kind: "agent"}) {
		t.Fatal("persistence on without PersistAgent")
	}
	on := &Supervisor{DataDir: t.TempDir(), PersistAgent: true}
	if _, err := exec.LookPath("dtach"); err != nil {
		if on.persistsAgent(worker.TerminalCommand{Kind: "agent"}) {
			t.Fatal("persistence on without dtach installed")
		}
		return
	}
	if !on.persistsAgent(worker.TerminalCommand{Kind: "agent"}) {
		t.Fatal("persistence off for the interactive agent")
	}
	for _, input := range []worker.TerminalCommand{
		{Kind: "agent", Review: true},
		{Kind: "reviewer"},
		{Kind: "workspace"},
	} {
		if on.persistsAgent(input) {
			t.Fatalf("persistence applied to %+v", input)
		}
	}
}

// exitRecordingControl records published agent terminal exits.
type exitRecordingControl struct {
	supervisorControlStub
	exits chan int
}

func (c *exitRecordingControl) PublishTerminalExit(_ context.Context, _ string, code int, _ bool) error {
	c.exits <- code
	return nil
}

func persistentSupervisor(t *testing.T, control Control) *Supervisor {
	t.Helper()
	requireDtach(t)
	return &Supervisor{
		Control: control, Workspace: t.TempDir(), DataDir: t.TempDir(), PersistAgent: true,
		AgentCommand: workerexec.Command{Path: "/bin/sh", Args: []string{"-c", "sleep 300"}},
		terminals:    make(map[string]*terminalProcess),
	}
}

// A handoff to Chat must end the agent itself, not only this worker's dtach
// client; otherwise the TUI agent keeps owning the conversation the Chat
// runner is about to resume.
func TestInterfaceHandoffEndsThePersistedAgent(t *testing.T) {
	control := &exitRecordingControl{exits: make(chan int, 4)}
	supervisor := persistentSupervisor(t, control)
	terminalID := "00000000-0000-0000-0000-000000000051"
	if err := supervisor.openTerminal(context.Background(), worker.TerminalCommand{TerminalID: terminalID, Kind: "agent"}); err != nil {
		t.Fatal(err)
	}
	persist := supervisor.persistentAgent()
	waitFor(t, "the agent to start", func() bool { _, ok := persist.running(); return ok })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := supervisor.closeTerminalForInterfaceHandoff(ctx, terminalID); err != nil {
		t.Fatalf("handoff close: %v", err)
	}
	if _, ok := persist.running(); ok {
		t.Fatal("agent still running after the handoff closed its terminal")
	}
}

// A worker shutting down (wake, repair, self-update) must leave the agent
// running and must not report the session's agent as exited.
func TestWorkerShutdownLeavesThePersistedAgentRunning(t *testing.T) {
	control := &exitRecordingControl{exits: make(chan int, 4)}
	supervisor := persistentSupervisor(t, control)
	terminalID := "00000000-0000-0000-0000-000000000052"
	if err := supervisor.openTerminal(context.Background(), worker.TerminalCommand{TerminalID: terminalID, Kind: "agent"}); err != nil {
		t.Fatal(err)
	}
	persist := supervisor.persistentAgent()
	t.Cleanup(func() { _ = persist.stop(time.Second) })
	var pid int
	waitFor(t, "the agent to start", func() bool { var ok bool; pid, ok = persist.running(); return ok })
	supervisor.closeAllTerminals()
	select {
	case code := <-control.exits:
		t.Fatalf("worker shutdown reported the agent exited (code %d)", code)
	case <-time.After(time.Second):
	}
	if again, ok := persist.running(); !ok || again != pid {
		t.Fatalf("agent did not survive worker shutdown: running=%v pid=%d want %d", ok, again, pid)
	}
}

// The agent exiting on its own is still reported, with its own exit code.
func TestPersistedAgentExitIsReported(t *testing.T) {
	control := &exitRecordingControl{exits: make(chan int, 4)}
	supervisor := persistentSupervisor(t, control)
	supervisor.AgentCommand = workerexec.Command{Path: "/bin/sh", Args: []string{"-c", "exit 3"}}
	if err := supervisor.openTerminal(context.Background(), worker.TerminalCommand{TerminalID: "00000000-0000-0000-0000-000000000053", Kind: "agent"}); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-control.exits:
		if code != 3 {
			t.Fatalf("published exit code %d, want the agent's own 3", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent exit was not published")
	}
}
