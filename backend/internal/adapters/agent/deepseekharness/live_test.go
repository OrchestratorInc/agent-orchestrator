package deepseekharness

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func liveBinary(t *testing.T) string {
	t.Helper()
	if os.Getenv("AO_LIVE_DEEPSEEK") != "1" {
		t.Skip("set AO_LIVE_DEEPSEEK=1 to test the installed DeepSeek Harness")
	}
	binary, err := exec.LookPath("dsh")
	if err != nil {
		t.Fatalf("resolve DeepSeek Harness: %v", err)
	}
	return binary
}

func runLive(t *testing.T, timeout time.Duration, args ...string) (out string, timedOut bool, err error) {
	t.Helper()
	sink, err := os.CreateTemp(t.TempDir(), "dsh-*.log")
	if err != nil {
		t.Fatalf("capture file: %v", err)
	}
	defer func() { _ = sink.Close() }()

	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // test-only
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = sink
	cmd.Stderr = sink
	if startErr := cmd.Start(); startErr != nil {
		t.Fatalf("start: %v", startErr)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	select {
	case err = <-waited:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		timedOut = true
	}
	body, readErr := os.ReadFile(sink.Name())
	if readErr != nil {
		t.Fatalf("read capture: %v", readErr)
	}
	return string(body), timedOut, err
}

func TestLiveHeadlessRejectsAnUnknownSessionID(t *testing.T) {
	binary := liveBinary(t)
	out, timedOut, err := runLive(t, time.Minute, binary, "--profile", headlessProfile,
		"--session-id", "ao-sess-unknown-"+t.Name(), "print ok")
	if timedOut {
		t.Fatalf("run did not finish; output: %s", out)
	}
	if err == nil {
		t.Fatalf("an unknown --session-id was accepted; output: %s", out)
	}
	if !strings.Contains(out, "does not exist") {
		t.Fatalf("unexpected failure for an unknown session id: %s", out)
	}
}

func TestLiveHeadlessBlocksWithoutATask(t *testing.T) {
	binary := liveBinary(t)

	stdin, writer := io.Pipe()
	cmd := exec.Command(binary, "--profile", headlessProfile) //nolint:gosec // test-only
	cmd.Stdin = stdin
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	select {
	case err := <-waited:
		_ = writer.Close()
		t.Fatalf("a task-less headless run exited (%v) instead of waiting on stdin", err)
	case <-time.After(15 * time.Second):
	}

	_ = writer.Close()
	select {
	case <-waited:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Error("the run did not exit after stdin closed")
	}
}

func TestLiveLaunchCommandIsAcceptedByTheBinary(t *testing.T) {
	binary := liveBinary(t)
	p := &Plugin{resolvedBinary: binary}
	argv, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID: "ao-sess-unknown-" + t.Name(),
		Prompt:    "print ok",
	})
	if err != nil {
		t.Fatalf("launch command: %v", err)
	}

	out, timedOut, runErr := runLive(t, time.Minute, argv...)
	if timedOut {
		t.Fatalf("the launch argv blocked instead of running the task: %s", out)
	}
	for _, trap := range []string{"does not exist", "a task is required"} {
		if strings.Contains(out, trap) {
			t.Fatalf("the launch argv hit %q: %s (err %v)", trap, out, runErr)
		}
	}
}

func TestLiveCredentialStoreLayoutIsAccepted(t *testing.T) {
	binary := liveBinary(t)
	home := t.TempDir()
	store := filepath.Join(home, ".credentials.yaml")
	if err := os.WriteFile(store, []byte("version: 1\nrefs:\n  DEEPSEEK_API_KEY: sk-not-a-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_HOME", home)
	t.Setenv(credentialsPathEnv, store)
	t.Setenv(deepseekCredentialKey, "")

	p := &Plugin{resolvedBinary: binary}
	status, err := p.AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized for the version-1 refs layout", status)
	}

	out, timedOut, _ := runLive(t, time.Minute, binary, "--profile", headlessProfile, "print ok")
	if timedOut {
		t.Fatalf("run did not finish; output: %s", out)
	}
	for _, rejection := range []string{"pre-release flat layout", "credentials-local:", "MISSING_CREDENTIAL"} {
		if strings.Contains(out, rejection) {
			t.Fatalf("Harness rejected a store AO reports as authorized (%q): %s", rejection, out)
		}
	}
}
