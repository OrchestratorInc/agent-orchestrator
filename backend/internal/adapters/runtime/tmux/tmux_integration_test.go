package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRuntimeIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	ctx := context.Background()
	id := strings.ReplaceAll(t.Name(), "/", "_")
	r := New(Options{Timeout: 5 * time.Second})

	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id})

	t.Cleanup(func() {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id})
	})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-c", "echo hello-from-tmux"},
		Env:           map[string]string{"AO_SESSION_ID": id},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	alive, err := r.IsAlive(ctx, h)
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if !alive {
		t.Fatal("alive = false, want true after create")
	}

	out := waitForOutput(t, r, h, "hello-from-tmux", 5*time.Second)
	if !strings.Contains(out, "hello-from-tmux") {
		t.Fatalf("output = %q, want hello-from-tmux", out)
	}

	if err := r.SendMessage(ctx, h, "echo hello-send"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	out = waitForOutput(t, r, h, "hello-send", 5*time.Second)
	if !strings.Contains(out, "hello-send") {
		t.Fatalf("output after SendMessage = %q, want hello-send", out)
	}

	if err := r.Destroy(ctx, h); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	alive, err = r.IsAlive(ctx, h)
	if err != nil && !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("IsAlive after destroy: %v", err)
	}
	if alive {
		t.Fatal("alive after destroy = true, want false")
	}
}

func TestRuntimeIntegrationDeliversEnvironmentThroughAFile(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	ctx := context.Background()
	id := strings.ReplaceAll(t.Name(), "/", "_")
	envDir := t.TempDir()
	r := New(Options{Timeout: 5 * time.Second, EnvDir: envDir})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id})
	t.Cleanup(func() { _ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id}) })

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-c", `printf 'key-length:%s\n' "${#AO_TEST_PROVIDER_KEY}"`},
		Env:           map[string]string{"AO_SESSION_ID": id, "AO_TEST_PROVIDER_KEY": "sk-test-0123456789"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out := waitForOutput(t, r, h, "key-length:18", 5*time.Second); !strings.Contains(out, "key-length:18") {
		t.Fatalf("output = %q, want key-length:18", out)
	}
	entries, err := os.ReadDir(envDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("launch environment left behind: %v err=%v", entries, err)
	}
}

func TestRuntimeIntegrationExactSessionParsing(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	ctx := context.Background()
	base := strings.ReplaceAll(t.Name(), "/", "_")
	longID := base + "_long"
	prefixID := base

	r := New(Options{Timeout: 5 * time.Second})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: longID})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: prefixID})

	t.Cleanup(func() {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: longID})
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: prefixID})
	})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(longID),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-c", "echo ready"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	prefixAlive, err := r.IsAlive(ctx, ports.RuntimeHandle{ID: prefixID})
	if err != nil {
		t.Logf("IsAlive prefix returned error (acceptable): %v", err)
	}
	if prefixAlive {
		_ = r.Destroy(ctx, h)
		t.Fatal("prefix handle reported alive; tmux session matching is not exact")
	}
}

func TestRuntimeIntegrationLegacyDefaultSocketIgnoresInheritedTMUX(t *testing.T) {
	systemTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}

	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ao-tmux-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	legacyID := strings.ReplaceAll(t.Name(), "/", "_") + "_legacy"
	spoofID := strings.ReplaceAll(t.Name(), "/", "_") + "_spoof"
	privateID := strings.ReplaceAll(t.Name(), "/", "_") + "_private"
	for _, socketName := range []string{"default", "spoof", "ao"} {
		t.Cleanup(func() {
			_ = exec.Command(systemTmux, "-L", socketName, "kill-server").Run()
		})
	}
	start := func(socketName, sessionID string) {
		t.Helper()
		if out, startErr := exec.Command(
			systemTmux,
			"-L", socketName,
			"new-session", "-d", "-s", sessionID,
			"sleep 30",
		).CombinedOutput(); startErr != nil {
			t.Fatalf("start tmux -L %s: %v: %s", socketName, startErr, out)
		}
	}
	start("default", legacyID)
	start("spoof", spoofID)
	start("ao", privateID)

	spoofIdentity, err := exec.Command(
		systemTmux,
		"-L", "spoof",
		"display-message", "-p", "#{socket_path},#{pid},0",
	).Output()
	if err != nil {
		t.Fatalf("read spoof socket identity: %v", err)
	}
	t.Setenv("TMUX", strings.TrimSpace(string(spoofIdentity)))
	if out, err := exec.Command(systemTmux, "has-session", "-t", spoofID).CombinedOutput(); err != nil {
		t.Fatalf("test setup did not redirect plain tmux through inherited TMUX: %v: %s", err, out)
	}

	r := New(Options{
		Binary:       systemTmux,
		LegacyBinary: systemTmux,
		SocketName:   "ao",
		Timeout:      5 * time.Second,
	})
	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: legacyID})
	if err != nil || !alive {
		t.Fatalf("legacy default-socket session = (%v, %v), want (true, nil)", alive, err)
	}
	alive, err = r.IsAlive(context.Background(), ports.RuntimeHandle{ID: spoofID})
	if err != nil {
		t.Fatalf("spoof-only session probe: %v", err)
	}
	if alive {
		t.Fatal("spoof-only session was misclassified as AO's legacy session")
	}
}

func TestRuntimeIntegrationAdoptsLegacyDefaultWhenNamedSocketDoesNotExist(t *testing.T) {
	systemTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}

	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ao-tmux-migration-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	legacyID := strings.ReplaceAll(t.Name(), "/", "_") + "_legacy"
	for _, socketName := range []string{"default", "ao"} {
		t.Cleanup(func() {
			_ = exec.Command(systemTmux, "-L", socketName, "kill-server").Run()
		})
	}
	if out, startErr := exec.Command(
		systemTmux,
		"-L", "default",
		"new-session", "-d", "-s", legacyID,
		"sh",
	).CombinedOutput(); startErr != nil {
		t.Fatalf("start legacy tmux session: %v: %s", startErr, out)
	}
	missingOut, missingErr := exec.Command(systemTmux, "-L", "ao", "has-session", "-t", legacyID).CombinedOutput()
	if missingErr == nil {
		t.Fatal("test setup unexpectedly found a named AO server")
	}
	if !serverSocketAbsentOutput(string(missingOut)) {
		t.Fatalf("named AO probe = %q, want missing-socket diagnostic", missingOut)
	}

	r := New(Options{
		Binary:       systemTmux,
		LegacyBinary: systemTmux,
		SocketName:   "ao",
		Timeout:      5 * time.Second,
	})
	r.enterDelay = 0
	handle := ports.RuntimeHandle{ID: legacyID}
	alive, err := r.IsAlive(context.Background(), handle)
	if err != nil || !alive {
		t.Fatalf("legacy default-socket session = (%v, %v), want (true, nil)", alive, err)
	}
	if err := r.SendMessage(context.Background(), handle, "echo legacy-send-ok"); err != nil {
		t.Fatalf("SendMessage to adopted legacy session: %v", err)
	}
	out := waitForOutput(t, r, handle, "legacy-send-ok", 5*time.Second)
	if !strings.Contains(out, "legacy-send-ok") {
		t.Fatalf("legacy output = %q, want legacy-send-ok", out)
	}
	if out, probeErr := exec.Command(systemTmux, "-L", "ao", "list-sessions").CombinedOutput(); probeErr == nil {
		t.Fatalf("legacy discovery unexpectedly created named AO server: %s", out)
	}
}

func TestRuntimeIntegrationLegacyDefaultSocketDestroyEnforcesDetachOnDestroy(t *testing.T) {
	systemTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TERM", "xterm-256color")

	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ao-tmux-legacy-dod-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)

	legacyID := strings.ReplaceAll(t.Name(), "/", "_") + "_legacy"
	decoyID := strings.ReplaceAll(t.Name(), "/", "_") + "_decoy"
	for _, socketName := range []string{"default", "ao"} {
		t.Cleanup(func() {
			_ = exec.Command(systemTmux, "-L", socketName, "kill-server").Run()
		})
	}

	if out, startErr := exec.Command(systemTmux, "-L", "default", "new-session", "-d", "-s", decoyID, "sh", "-c", "sleep 300").CombinedOutput(); startErr != nil {
		t.Fatalf("create decoy session: %v: %s", startErr, out)
	}
	if out, startErr := exec.Command(systemTmux, "-L", "default", "set-option", "-g", "detach-on-destroy", "off").CombinedOutput(); startErr != nil {
		t.Fatalf("set global detach-on-destroy off: %v: %s", startErr, out)
	}
	if out, startErr := exec.Command(
		systemTmux, "-L", "default",
		"new-session", "-d", "-s", legacyID,
		"sh", "-lc", "printf AO_READY\\n; exec sh -i",
	).CombinedOutput(); startErr != nil {
		t.Fatalf("create legacy session: %v: %s", startErr, out)
	}

	r := New(Options{
		Binary:       systemTmux,
		LegacyBinary: systemTmux,
		SocketName:   "ao",
		Timeout:      5 * time.Second,
	})
	r.enterDelay = 0
	handle := ports.RuntimeHandle{ID: legacyID}

	alive, err := r.IsAlive(context.Background(), handle)
	if err != nil || !alive {
		t.Fatalf("legacy session = (%v, %v), want (true, nil)", alive, err)
	}

	stream, err := r.Attach(context.Background(), handle, 24, 80)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer stream.Close()

	var mu sync.Mutex
	var got strings.Builder
	readErrCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stream.Read(buf)
			if n > 0 {
				mu.Lock()
				got.Write(buf[:n])
				mu.Unlock()
			}
			if readErr != nil {
				readErrCh <- readErr
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return got.String()
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(snapshot(), "AO_READY") {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(snapshot(), "AO_READY") {
		t.Fatalf("attach stream never showed AO_READY, got %q", snapshot())
	}

	if err := r.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	select {
	case <-readErrCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("attach stream still open after Destroy, want it closed; got %q (want it to not contain the decoy's shell)", snapshot())
	}
}

func TestRuntimeIntegrationLegacyDefaultSocketExternalKillEnforcesDetachOnDestroy(t *testing.T) {
	systemTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TERM", "xterm-256color")

	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ao-tmux-legacy-extkill-dod-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)

	legacyID := strings.ReplaceAll(t.Name(), "/", "_") + "_legacy"
	decoyID := strings.ReplaceAll(t.Name(), "/", "_") + "_decoy"
	for _, socketName := range []string{"default", "ao"} {
		t.Cleanup(func() {
			_ = exec.Command(systemTmux, "-L", socketName, "kill-server").Run()
		})
	}

	if out, startErr := exec.Command(systemTmux, "-L", "default", "new-session", "-d", "-s", decoyID, "sh", "-c", "sleep 300").CombinedOutput(); startErr != nil {
		t.Fatalf("create decoy session: %v: %s", startErr, out)
	}
	if out, startErr := exec.Command(systemTmux, "-L", "default", "set-option", "-g", "detach-on-destroy", "off").CombinedOutput(); startErr != nil {
		t.Fatalf("set global detach-on-destroy off: %v: %s", startErr, out)
	}
	if out, startErr := exec.Command(
		systemTmux, "-L", "default",
		"new-session", "-d", "-s", legacyID,
		"sh", "-lc", "printf AO_READY\\n; exec sh -i",
	).CombinedOutput(); startErr != nil {
		t.Fatalf("create legacy session: %v: %s", startErr, out)
	}

	r := New(Options{
		Binary:       systemTmux,
		LegacyBinary: systemTmux,
		SocketName:   "ao",
		Timeout:      5 * time.Second,
	})
	r.enterDelay = 0
	handle := ports.RuntimeHandle{ID: legacyID}

	stream, err := r.Attach(context.Background(), handle, 24, 80)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer stream.Close()

	verifyOut, verifyErr := exec.Command(systemTmux, "-L", "default", "show-options", "-t", "="+legacyID+":", "-v", "detach-on-destroy").CombinedOutput()
	if verifyErr != nil {
		t.Fatalf("show-options detach-on-destroy after Attach: %v: %s", verifyErr, verifyOut)
	}
	if got := strings.TrimSpace(string(verifyOut)); got != "on" {
		t.Fatalf("detach-on-destroy after Attach = %q, want \"on\" (Attach must enforce it at adoption time)", got)
	}

	var mu sync.Mutex
	var got strings.Builder
	readErrCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stream.Read(buf)
			if n > 0 {
				mu.Lock()
				got.Write(buf[:n])
				mu.Unlock()
			}
			if readErr != nil {
				readErrCh <- readErr
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return got.String()
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(snapshot(), "AO_READY") {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(snapshot(), "AO_READY") {
		t.Fatalf("attach stream never showed AO_READY, got %q", snapshot())
	}

	if out, killErr := exec.Command(systemTmux, "-L", "default", "kill-session", "-t", "="+legacyID).CombinedOutput(); killErr != nil {
		t.Fatalf("external kill-session: %v: %s", killErr, out)
	}

	select {
	case <-readErrCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("attach stream still open after an external kill-session, want it closed; got %q (want it to not contain the decoy's shell)", snapshot())
	}
}

func TestRuntimeIntegrationSupervisedExitKeepsInteractiveShell(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	ctx := context.Background()
	id := strings.ReplaceAll(t.Name(), "/", "_")
	const launchID = "launch-1"
	r := New(Options{Timeout: 5 * time.Second})
	tmuxID := SessionName(id)
	workspace := t.TempDir()
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: tmuxID})
	t.Cleanup(func() { _ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: tmuxID}) })

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: workspace,
		Argv:          []string{os.Args[0], "-test.run=TestSupervisorProcessHelper", "--", "agent-process", "supervise", "--session", id, "--launch", launchID, "--"},
		Env:           map[string]string{"AO_TMUX_SUPERVISOR_HELPER": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := ports.SupervisedProcessRef{SessionID: domain.SessionID(id), LaunchID: launchID}
	deadline := time.Now().Add(5 * time.Second)
	for {
		alive, probeErr := r.IsSupervisedProcessAlive(ctx, h, ref)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		if alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervised workload did not appear in the tmux process tree")
		}
		time.Sleep(100 * time.Millisecond)
	}

	deadline = time.Now().Add(5 * time.Second)
	for {
		alive, probeErr := r.IsSupervisedProcessAlive(ctx, h, ref)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervised workload remained alive after normal exit")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if alive, err := r.IsAlive(ctx, h); err != nil || !alive {
		t.Fatalf("tmux after workload exit = (%v, %v), want (true, nil)", alive, err)
	}
	if err := r.SendMessage(ctx, h, "echo shell-after-agent-exit"); err != nil {
		t.Fatal(err)
	}
	out := waitForOutput(t, r, h, "shell-after-agent-exit", 5*time.Second)
	if !strings.Contains(out, "shell-after-agent-exit") {
		t.Fatalf("post-exit shell output = %q", out)
	}

	restarted, err := r.Restart(ctx, h, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: workspace,
		Argv:          []string{"sh", "-c", "echo managed-agent-resumed"},
	})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted != h {
		t.Fatalf("restart handle = %+v, want existing handle %+v", restarted, h)
	}
	out = waitForOutput(t, r, restarted, "managed-agent-resumed", 5*time.Second)
	if !strings.Contains(out, "managed-agent-resumed") {
		t.Fatalf("restart output = %q, want managed-agent-resumed", out)
	}
	if err := r.SendMessage(ctx, restarted, "echo shell-after-managed-resume"); err != nil {
		t.Fatal(err)
	}
	out = waitForOutput(t, r, restarted, "shell-after-managed-resume", 5*time.Second)
	if !strings.Contains(out, "shell-after-managed-resume") {
		t.Fatalf("post-resume shell output = %q", out)
	}
}

func TestSupervisorProcessHelper(t *testing.T) {
	if os.Getenv("AO_TMUX_SUPERVISOR_HELPER") != "1" {
		return
	}
	time.Sleep(2 * time.Second)
}

func waitForOutput(t *testing.T, r *Runtime, h ports.RuntimeHandle, want string, deadline time.Duration) string {
	t.Helper()
	end := time.Now().Add(deadline)
	var out string
	for time.Now().Before(end) {
		var err error
		out, err = r.GetOutput(context.Background(), h, 50)
		if err != nil {
			t.Fatalf("GetOutput: %v", err)
		}
		if strings.Contains(out, want) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	return out
}
