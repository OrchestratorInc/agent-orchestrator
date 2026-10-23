//go:build e2e && (darwin || linux)

package cli_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// TestE2ENativeSupervisorExitReportWindow (T02) runs the real `ao agent-process
// supervise` binary as the child of a real native PTY host. After the managed
// agent exits, the supervisor stays alive while it reports the exit; the fake
// daemon below holds that report open. The supervisor's liveness in that window
// must not be reported as proof that the agent can receive input.
func TestE2ENativeSupervisorExitReportWindow(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")
	const sessionID, launchID = "t02-native", "launch-t02"

	reported := make(chan struct{})
	release := make(chan struct{})
	var reportOnce, releaseOnce sync.Once
	releaseReport := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseReport)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/"+sessionID+"/activity" {
			http.NotFound(w, r)
			return
		}
		// Observing the request proves the supervisor's child.Wait returned.
		// Hold only the response; nothing has updated durable activity yet.
		reportOnce.Do(func() { close(reported) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(daemon.Close)
	daemonURL, err := url.Parse(daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(daemonURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	if err := runfile.Write(runFile, runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	ready := filepath.Join(dir, "agent.ready")
	input := filepath.Join(dir, "agent.input")
	rt := conpty.New(conpty.Options{RunFilePath: runFile})
	ctx := context.Background()
	handle, err := rt.Create(ctx, ports.RuntimeConfig{
		SessionID:     sessionID,
		WorkspacePath: dir,
		Argv: []string{aoBin, "agent-process", "supervise", "--session", sessionID, "--launch", launchID, "--",
			"/bin/sh", "-c", `printf ready > "$AGENT_READY"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$AGENT_INPUT"
  if [ "$line" = "__exit__" ]; then exit 0; fi
done`},
		Env: map[string]string{
			"AO_RUNTIME_LAUNCH_ID": launchID,
			"AO_RUN_FILE":          runFile,
			"AO_DATA_DIR":          filepath.Join(dir, "data"),
			"AGENT_READY":          ready,
			"AGENT_INPUT":          input,
		},
	})
	if err != nil {
		t.Fatalf("create native runtime: %v", err)
	}
	t.Cleanup(func() {
		releaseReport()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rt.Destroy(cleanupCtx, handle)
	})
	waitForContent(t, ready, "ready")

	ref := ports.FencedRuntimeRef{Handle: handle, SessionID: sessionID, Generation: launchID}
	exact := ports.SupervisedProcessRef{SessionID: sessionID, LaunchID: launchID}
	if probe := rt.ProbeFencedRuntime(ctx, ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("live supervised agent probe = %+v, want alive", probe)
	}

	if err := rt.SendInput(ctx, handle, "__exit__\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reported:
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor never reported the managed agent's exit")
	}
	waitForContent(t, input, "__exit__")

	// The exit report is held: the supervisor, the host's direct child, is
	// still alive although the agent it manages has exited.
	supervisorAlive, childErr := rt.IsChildAlive(ctx, handle)
	if childErr != nil || !supervisorAlive {
		t.Fatalf("precondition: supervisor must still be alive in the report window: alive=%v err=%v", supervisorAlive, childErr)
	}
	// The ordinary reaper probe keeps its existing semantics in this window.
	if alive, err := rt.IsSupervisedProcessAlive(ctx, handle, exact); err != nil || !alive {
		t.Fatalf("ordinary reaper probe changed during the report window: alive=%v err=%v", alive, err)
	}
	probe := rt.ProbeFencedRuntime(ctx, ref)
	exactAlive, exactErr := rt.IsExactSupervisedProcessAlive(ctx, handle, exact)
	writeErr := rt.SendMessage(ctx, handle, "T02 message in report window")
	t.Logf("report window: fenced=%+v exactAlive=%v exactErr=%v hostWriteErr=%v", probe, exactAlive, exactErr, writeErr)
	if probe.Liveness == ports.FencedAlive {
		t.Errorf("T02: fenced probe confirms the agent alive while only its supervisor remains: %+v", probe)
	}
	if exactAlive {
		t.Errorf("T02: exact supervised probe confirms the agent alive while only its supervisor remains")
	}

	releaseReport()
	deadline := time.Now().Add(10 * time.Second)
	for {
		alive, err := rt.IsChildAlive(ctx, handle)
		if err == nil && !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("supervisor did not exit after its report completed: alive=%v err=%v", alive, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(readContent(input), "T02 message in report window") {
		t.Fatalf("the exited agent cannot have read input written in the report window")
	}
	if probe := rt.ProbeFencedRuntime(ctx, ref); probe.Liveness != ports.FencedDead {
		t.Fatalf("after supervisor exit probe = %+v, want dead", probe)
	}
}

func waitForContent(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(readContent(path), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in %s; got %q", want, path, readContent(path))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readContent(path string) string {
	raw, _ := os.ReadFile(path)
	return string(raw)
}
