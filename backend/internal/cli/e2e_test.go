//go:build e2e

// Package cli_test holds the end-to-end suite for the `ao` CLI. It builds the
// real binary and drives it (start/status/doctor/stop + the daemon-control HTTP
// surface) against fully isolated state — a per-test temp run-file, data dir,
// and an OS-assigned free loopback port — so it never touches a developer's real
// AO install. Unlike the Linux-only container smoke test, this runs natively on
// every OS in CI (ubuntu/macos/windows), which is the only way to exercise the
// unix setsid vs Windows CREATE_NEW_PROCESS_GROUP detach paths and the per-OS
// os.UserConfigDir resolution.
//
// It is gated behind the `e2e` build tag so it never runs in the normal
// `go test ./...` lane (it spawns processes and binds ports):
//
//	go test -tags e2e ./internal/cli/...           # run it
//	go test -tags e2e -v -run TestE2E ./internal/cli/...   # verbose, see every command
package cli_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// aoBin is the path to the binary built once for the whole suite.
var aoBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ao-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mktemp:", err)
		os.Exit(1)
	}
	aoBin = filepath.Join(dir, "ao")
	if runtime.GOOS == "windows" {
		aoBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", aoBin, "github.com/aoagents/agent-orchestrator/backend/cmd/ao")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build ao:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// env is an isolated CLI environment: its own state files and free port.
type env struct {
	runFile string
	dataDir string
	port    int
}

func newEnv(t *testing.T) env {
	t.Helper()
	dir := t.TempDir()
	return env{
		runFile: filepath.Join(dir, "running.json"),
		dataDir: filepath.Join(dir, "data"),
		port:    freePort(t),
	}
}

// environ builds the child env: the ambient environment with every inherited
// AO_* var stripped (so a real daemon's AO_PORT can't leak in) plus our isolated
// settings. portOverride, when non-empty, replaces the numeric AO_PORT — used to
// inject an invalid value.
func (e env) environ(portOverride string) []string {
	out := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "AO_") {
			continue
		}
		if strings.HasPrefix(kv, "GITHUB_TOKEN=") || strings.HasPrefix(kv, "GH_TOKEN=") || strings.HasPrefix(kv, "GH_CONFIG_DIR=") {
			continue
		}
		out = append(out, kv)
	}
	port := fmt.Sprintf("%d", e.port)
	if portOverride != "" {
		port = portOverride
	}
	return append(out, "AO_RUN_FILE="+e.runFile, "AO_DATA_DIR="+e.dataDir, "AO_PORT="+port, "GH_CONFIG_DIR="+filepath.Join(e.dataDir, "gh-config"), "AO_TELEMETRY_REMOTE=off")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("alloc free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// run executes `ao args...` in env e and returns combined output + exit code.
func (e env) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return e.runEnv(t, e.environ(""), args...)
}

func (e env) runEnv(t *testing.T, environ []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(aoBin, args...)
	cmd.Env = environ
	b, err := cmd.CombinedOutput()
	out := string(b)
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v\n%s", args, err, out)
		}
	}
	t.Logf("$ ao %s\n%s(exit %d)", strings.Join(args, " "), out, code)
	return out, code
}

func asExit(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// startDaemon brings the daemon up and registers a stop on cleanup. `ao start`
// no longer spawns the daemon (the desktop app owns it now), so the e2e suite
// drives the hidden `ao daemon` command directly and polls for readiness.
func (e env) startDaemon(t *testing.T) {
	t.Helper()
	cmd := exec.Command(aoBin, "daemon")
	cmd.Env = e.environ("")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn ao daemon: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	t.Cleanup(func() {
		e.run(t, "stop")
		select {
		case <-waitDone:
			return
		case <-time.After(5 * time.Second):
		}
		// The daemon did not exit on `ao stop` within the timeout: a shutdown
		// regression is hiding behind a green test. Fail, force-kill, and wait
		// for the child to be reaped so it cannot survive the test.
		t.Errorf("daemon process did not exit within 5s of `ao stop`; forcing kill")
		_ = cmd.Process.Kill()
		<-waitDone
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := e.run(t, "status", "--json")
		if strings.Contains(out, `"state": "ready"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not become ready within 10s; last status:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func mustContain(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Fatalf("expected output to contain %q; got:\n%s", want, out)
	}
}

func mustNotContain(t *testing.T, out, notWant string) {
	t.Helper()
	if strings.Contains(out, notWant) {
		t.Fatalf("expected output NOT to contain %q; got:\n%s", notWant, out)
	}
}

// ---------------------------------------------------------------------------

func TestE2E_VersionAndHelp(t *testing.T) {
	e := newEnv(t)

	if out, code := e.run(t, "version"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("version: exit %d, out %q", code, out)
	}
	if _, code := e.run(t, "--version"); code != 0 {
		t.Fatalf("--version exit %d", code)
	}

	out, code := e.run(t, "--help")
	if code != 0 {
		t.Fatalf("--help exit %d", code)
	}
	for _, want := range []string{"start", "stop", "status", "doctor", "completion", "version"} {
		mustContain(t, out, want)
	}
	// the internal daemon command is hidden from help (rendered as "\n  daemon")
	mustNotContain(t, out, "\n  daemon")
}

func TestE2E_DoctorDoesNotTouchTheStore(t *testing.T) {
	e := newEnv(t)

	out, code := e.run(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor (fresh) exit %d: %s", code, out)
	}
	mustContain(t, out, "git")
	mustContain(t, out, "database not created yet") // sqlite WARN, never migrated

	// doctor must NOT create/migrate the DB — the daemon is the sole writer.
	if _, err := os.Stat(filepath.Join(e.dataDir, "ao.db")); err == nil {
		t.Fatal("doctor created ao.db; the CLI must not open/migrate the store")
	}

	if out, code := e.run(t, "doctor", "--json"); code != 0 || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("doctor --json: exit %d, out %s", code, out)
	}
}

func TestE2E_StatusStopped(t *testing.T) {
	e := newEnv(t)
	out, code := e.run(t, "status", "--json")
	if code != 0 { // status always exits 0
		t.Fatalf("status exit %d", code)
	}
	mustContain(t, out, `"state": "stopped"`)
	mustNotContain(t, out, "startedAt")

	if out, code := e.run(t, "stop"); code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop-when-stopped: exit %d, out %s", code, out) // idempotent
	}
}

func TestE2E_Lifecycle(t *testing.T) {
	e := newEnv(t)
	e.startDaemon(t)

	out, _ := e.run(t, "status", "--json")
	mustContain(t, out, `"state": "ready"`)
	mustContain(t, out, fmt.Sprintf(`"port": %d`, e.port))

	// the daemon (not the CLI) has created + migrated the store
	if _, err := os.Stat(filepath.Join(e.dataDir, "ao.db")); err != nil {
		t.Fatalf("daemon should have created ao.db: %v", err)
	}
	out, _ = e.run(t, "doctor")
	mustContain(t, out, "migrations are applied by the daemon")

	// /healthz identity
	body := httpGet(t, e.port, "/healthz")
	mustContain(t, body, "agent-orchestrator-daemon")

	if out, code := e.run(t, "stop"); code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: exit %d, out %s", code, out)
	}
	if _, err := os.Stat(e.runFile); !os.IsNotExist(err) {
		t.Fatal("run-file should be removed after stop")
	}
}

func TestE2E_ShutdownGuard(t *testing.T) {
	e := newEnv(t)
	e.startDaemon(t)

	// A cross-site Origin header must be rejected without stopping the daemon.
	if code := postShutdown(t, e.port, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }); code != http.StatusForbidden {
		t.Fatalf("cross-origin /shutdown = %d, want 403", code)
	}
	// A non-loopback Host (DNS-rebinding) must be rejected too.
	if code := postShutdown(t, e.port, func(r *http.Request) { r.Host = "evil.example" }); code != http.StatusForbidden {
		t.Fatalf("rebinding-host /shutdown = %d, want 403", code)
	}
	// The daemon survived both.
	out, _ := e.run(t, "status", "--json")
	mustContain(t, out, `"state": "ready"`)
}

func TestE2E_StaleRunFile(t *testing.T) {
	e := newEnv(t)
	// PID 2147483647 is never alive -> the CLI must classify this as stale.
	content := fmt.Sprintf(`{"pid":2147483647,"port":%d,"startedAt":"2020-01-01T00:00:00Z"}`, e.port)
	if err := os.MkdirAll(filepath.Dir(e.runFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.runFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _ := e.run(t, "status", "--json")
	mustContain(t, out, `"state": "stale"`)

	if out, code := e.run(t, "stop"); code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop stale: exit %d, out %s", code, out)
	}
	if _, err := os.Stat(e.runFile); !os.IsNotExist(err) {
		t.Fatal("stale run-file should be removed")
	}
}

func TestE2E_ExitCodes(t *testing.T) {
	e := newEnv(t)

	if _, code := e.run(t, "status", "--definitely-not-a-flag"); code != 2 {
		t.Fatalf("bad flag exit %d, want 2", code)
	}
	if _, code := e.run(t, "completion"); code != 2 { // missing required arg
		t.Fatalf("missing-arg exit %d, want 2", code)
	}
	if _, code := e.run(t, "completion", "notashell"); code == 0 { // runtime error
		t.Fatal("unsupported shell should be non-zero")
	}
	// invalid config is a runtime error (1), not a usage error (2).
	if _, code := e.runEnv(t, e.environ("notaport"), "status"); code != 1 {
		t.Fatalf("invalid AO_PORT exit %d, want 1", code)
	}
}

func TestE2E_Completion(t *testing.T) {
	e := newEnv(t)
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		out, code := e.run(t, "completion", sh)
		if code != 0 || strings.TrimSpace(out) == "" {
			t.Fatalf("completion %s: exit %d, empty=%v", sh, code, strings.TrimSpace(out) == "")
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers (loopback)

func httpClient() *http.Client { return &http.Client{Timeout: 3 * time.Second} }

func httpGet(t *testing.T, port int, path string) string {
	t.Helper()
	resp, err := httpClient().Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b := make([]byte, 4096)
	n, _ := resp.Body.Read(b)
	return string(b[:n])
}

// postShutdown issues POST /shutdown with mutator applied, returns the status code.
func postShutdown(t *testing.T, port int, mutate func(*http.Request)) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/shutdown", port), nil)
	if err != nil {
		t.Fatal(err)
	}
	mutate(req)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("POST /shutdown: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
