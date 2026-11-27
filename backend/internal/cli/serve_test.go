package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// fakeServeDaemon answers /healthz and /readyz as the Orchestrator daemon
// with the given PID, serving the configured AO_DATA_DIR.
func fakeServeDaemon(t *testing.T, pid int) *httptest.Server {
	t.Helper()
	return fakeServeDaemonIn(t, pid, os.Getenv("AO_DATA_DIR"))
}

func decodeServeResult(t *testing.T, out string) serveResult {
	t.Helper()
	var res serveResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode serve --json output: %v\n%s", err, out)
	}
	return res
}

func TestServeReusesRunningDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := fakeServeDaemon(t, 4242)
	port := serverPort(t, srv.URL)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: port, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	started := false
	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == 4242 },
		StartDetached: func(processStartConfig) (startedProcess, error) {
			started = true
			return startedProcess{}, nil
		},
	}, "serve", "--json", "--port", "4000")
	if err != nil {
		t.Fatal(err)
	}
	if started {
		t.Fatal("serve started a second daemon although one is serving this data dir")
	}
	res := decodeServeResult(t, out)
	want := serveResult{Status: "ready", Port: port, PID: 4242, URL: fmt.Sprintf("http://127.0.0.1:%d", port), Reused: true, RunFile: cfg.runFile, DataDir: cfg.dataDir}
	if res != want {
		t.Fatalf("serve result = %#v, want %#v", res, want)
	}
	if !strings.Contains(errOut, "--port 4000 was ignored") {
		t.Fatalf("serve did not warn that --port was ignored; stderr:\n%s", errOut)
	}
}

func TestServeStartsDetachedDaemonAndWaitsForReady(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_TELEMETRY_REMOTE", "off")
	srv := fakeServeDaemon(t, 5151)
	port := serverPort(t, srv.URL)
	var got processStartConfig
	out, _, err := executeCLI(t, Deps{
		Executable:   func() (string, error) { return "/opt/ao/bin/ao", nil },
		ProcessAlive: func(pid int) bool { return pid == 5151 },
		StartDetached: func(c processStartConfig) (startedProcess, error) {
			got = c
			// The daemon publishes its run file once it is listening.
			if err := runfile.Write(cfg.runFile, runfile.Info{PID: 5151, Port: port, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
				t.Fatal(err)
			}
			return startedProcess{PID: 5151, Exited: make(chan error)}, nil
		},
	}, "serve", "--json", "--port", "4123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/opt/ao/bin/ao" || strings.Join(got.Args, " ") != "daemon" {
		t.Fatalf("started %q %v, want the hidden `ao daemon`", got.Path, got.Args)
	}
	env := envMap(got.Env)
	if _, ok := env["AO_OWNER"]; ok {
		t.Fatalf("AO_OWNER = %q, want unset (headless daemon)", env["AO_OWNER"])
	}
	if env["AO_PORT"] != "4123" {
		t.Fatalf("AO_PORT = %q, want the --port value", env["AO_PORT"])
	}
	if env["AO_TELEMETRY_REMOTE"] != "off" {
		t.Fatalf("explicit AO_TELEMETRY_REMOTE=off was overridden: %q", env["AO_TELEMETRY_REMOTE"])
	}
	res := decodeServeResult(t, out)
	if res.Status != "ready" || res.PID != 5151 || res.Port != port || res.Reused {
		t.Fatalf("serve result = %#v", res)
	}
}

func TestServeReportsDaemonThatExitsEarly(t *testing.T) {
	setConfigEnv(t)
	exited := make(chan error, 1)
	exited <- errors.New("exit status 1")
	_, _, err := executeCLI(t, Deps{
		Executable:   func() (string, error) { return "/opt/ao/bin/ao", nil },
		ProcessAlive: func(int) bool { return false },
		StartDetached: func(processStartConfig) (startedProcess, error) {
			return startedProcess{PID: 9, Exited: exited}, nil
		},
	}, "serve")
	if err == nil || !strings.Contains(err.Error(), "daemon exited before it became ready") || !strings.Contains(err.Error(), "daemon.log") {
		t.Fatalf("err = %v, want an early-exit error pointing at daemon.log", err)
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", ExitCode(err))
	}
}

func TestServeTimesOutWaitingForReady(t *testing.T) {
	setConfigEnv(t)
	now := time.Unix(0, 0)
	_, _, err := executeCLI(t, Deps{
		Executable:   func() (string, error) { return "/opt/ao/bin/ao", nil },
		ProcessAlive: func(int) bool { return false },
		Now:          func() time.Time { return now },
		Sleep:        func(d time.Duration) { now = now.Add(d) },
		StartDetached: func(processStartConfig) (startedProcess, error) {
			return startedProcess{PID: 9, Exited: make(chan error)}, nil
		},
	}, "serve", "--timeout", "1s")
	if err == nil || !strings.Contains(err.Error(), "did not become ready within 1s") {
		t.Fatalf("err = %v, want a readiness timeout", err)
	}
}

func TestServeRejectsBadFlags(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{{"serve", "--port", "70000"}, {"serve", "--timeout", "0s"}, {"serve", "extra"}, {"serve", "--background", "--foreground"}} {
		_, _, err := executeCLI(t, Deps{}, args...)
		if ExitCode(err) != 2 {
			t.Fatalf("%v: exit code = %d (err %v), want usage error 2", args, ExitCode(err), err)
		}
	}
}

func TestDaemonExitedErrorNamesDataDirLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	exitErr := exec.Command("sh", "-c", "exit 75").Run()
	err := daemonExitedError(exitErr, "/d", "/d/daemon.log")
	if !strings.Contains(err.Error(), "another Orchestrator process holds the data dir /d") {
		t.Fatalf("err = %v", err)
	}
}

func TestServeEnvTelemetry(t *testing.T) {
	cases := []struct {
		name       string
		base       []string
		optedOut   bool
		version    string
		wantRemote string
	}{
		{name: "release build exports like the app", version: "1.2.3", wantRemote: "posthog"},
		{name: "dev build never exports", version: "dev", wantRemote: "off"},
		{name: "app opt-out", version: "1.2.3", optedOut: true, wantRemote: "off"},
		{name: "app opt-out beats inherited env", base: []string{"AO_TELEMETRY_REMOTE=posthog"}, version: "1.2.3", optedOut: true, wantRemote: "off"},
		{name: "DO_NOT_TRACK", base: []string{"DO_NOT_TRACK=1"}, version: "1.2.3", wantRemote: "off"},
		{name: "DO_NOT_TRACK beats inherited env", base: []string{"DO_NOT_TRACK=1", "AO_TELEMETRY_REMOTE=posthog"}, version: "1.2.3", wantRemote: "off"},
		{name: "DO_NOT_TRACK=0 is not an opt-out", base: []string{"DO_NOT_TRACK=0"}, version: "1.2.3", wantRemote: "posthog"},
		{name: "explicit env wins", base: []string{"AO_TELEMETRY_REMOTE=off"}, version: "1.2.3", wantRemote: "off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := serveEnv(append([]string{"PATH=/bin", "AO_OWNER=app"}, tc.base...), 0, tc.optedOut, tc.version)
			m := envMap(env)
			if m["AO_TELEMETRY_REMOTE"] != tc.wantRemote {
				t.Fatalf("AO_TELEMETRY_REMOTE = %q, want %q", m["AO_TELEMETRY_REMOTE"], tc.wantRemote)
			}
			if _, ok := m["AO_OWNER"]; ok || m["PATH"] != "/bin" {
				t.Fatalf("env kept an inherited AO_OWNER or lost caller values: %v", m)
			}
			if _, ok := m["AO_PORT"]; ok {
				t.Fatalf("AO_PORT set without --port: %v", m)
			}
			if m["AO_TELEMETRY_POSTHOG_KEY"] != servePostHogKey || m["AO_TELEMETRY_EVENTS"] != "on" {
				t.Fatalf("telemetry defaults missing: %v", m)
			}
		})
	}
}

func TestTelemetryOptedOutReadsAppMarker(t *testing.T) {
	dir := t.TempDir()
	if telemetryOptedOut(dir) {
		t.Fatal("opted out without a marker")
	}
	if err := os.WriteFile(filepath.Join(dir, "telemetry_opt_out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !telemetryOptedOut(dir) {
		t.Fatal("the app's opt-out marker was not honoured")
	}
}

// The desktop app and `ao serve` must export to the same PostHog project.
func TestServePostHogKeyMatchesDesktopApp(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "frontend", "src", "shared", "posthog-config.ts"))
	if err != nil {
		t.Skipf("frontend source not available: %v", err)
	}
	if !strings.Contains(string(src), fmt.Sprintf("DEFAULT_POSTHOG_PROJECT_KEY = %q", servePostHogKey)) {
		t.Fatalf("servePostHogKey differs from DEFAULT_POSTHOG_PROJECT_KEY in posthog-config.ts")
	}
}

// A login-time service start is bootstrapping, not usage, like `ao start`.
func TestServeDoesNotEmitCLIInvocation(t *testing.T) {
	for _, cmd := range NewRootCommand(Deps{}).Commands() {
		if cmd.Name() == "serve" {
			if shouldEmitCLIInvocation(cmd) {
				t.Fatal("ao serve emits cli-invoked")
			}
			return
		}
	}
	t.Fatal("serve not registered")
}

// fakeServeDaemonIn is fakeServeDaemon reporting the data dir it serves, as
// the real daemon does through its working directory.
func fakeServeDaemonIn(t *testing.T, pid int, dataDir string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := map[string]string{"/healthz": "ok", "/readyz": "ready"}[r.URL.Path]
		if status == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":%q,"service":%q,"pid":%d,"workingDirectory":%q}`, status, daemonmeta.ServiceName, pid, dataDir)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestServeRefusesDaemonOfAnotherDataDir(t *testing.T) {
	cfg := setConfigEnv(t)
	other := t.TempDir()
	srv := fakeServeDaemonIn(t, 4242, other)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(pid int) bool { return pid == 4242 }}, "serve", "--json")
	if err == nil || !strings.Contains(err.Error(), "serves data dir") || !strings.Contains(err.Error(), "AO_RUN_FILE") {
		t.Fatalf("err = %v, want a data-dir conflict", err)
	}
}

func TestServeReusesDaemonOfSameDataDir(t *testing.T) {
	cfg := setConfigEnv(t)
	if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	srv := fakeServeDaemonIn(t, 4242, cfg.dataDir)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(pid int) bool { return pid == 4242 }}, "serve", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if res := decodeServeResult(t, out); !res.Reused {
		t.Fatalf("result = %#v, want reused", res)
	}
}

// foregroundHarness drives `ao serve --foreground` against a fake daemon whose
// fate is decided after a few watch ticks.
type foregroundHarness struct {
	t       *testing.T
	cfg     testConfig
	alive   map[int]bool
	now     time.Time
	ticks   int
	atTick  int
	onTick  func()
	started int
}

func newForegroundHarness(t *testing.T) *foregroundHarness {
	return &foregroundHarness{t: t, cfg: setConfigEnv(t), alive: map[int]bool{}, now: time.Unix(1000, 0), atTick: 3}
}

func (h *foregroundHarness) daemon(pid int, owner string) {
	h.t.Helper()
	srv := fakeServeDaemon(h.t, pid)
	if err := runfile.Write(h.cfg.runFile, runfile.Info{PID: pid, Port: serverPort(h.t, srv.URL), StartedAt: time.Unix(100, 0).UTC(), Owner: owner}); err != nil {
		h.t.Fatal(err)
	}
	h.alive[pid] = true
}

func (h *foregroundHarness) run(args ...string) (string, error) {
	h.t.Helper()
	old := serveForegroundDaemon
	h.t.Cleanup(func() { serveForegroundDaemon = old })
	serveForegroundDaemon = func([]string) error {
		h.started++
		return nil
	}
	out, _, err := executeCLI(h.t, Deps{
		ProcessAlive:   func(pid int) bool { return h.alive[pid] },
		PIDMayBeHidden: func() bool { return false },
		Now:            func() time.Time { return h.now },
		Sleep: func(d time.Duration) {
			h.now = h.now.Add(d)
			h.ticks++
			if h.ticks == h.atTick && h.onTick != nil {
				h.onTick()
			}
		},
	}, append([]string{"serve", "--foreground"}, args...)...)
	return out, err
}

func TestServeForegroundWatchesReusedDaemonUntilCleanStop(t *testing.T) {
	h := newForegroundHarness(t)
	h.daemon(4242, "persistent")
	h.onTick = func() {
		h.alive[4242] = false
		_ = runfile.Remove(h.cfg.runFile)
	}
	out, err := h.run("--json")
	if err != nil {
		t.Fatalf("clean stop should exit 0: %v", err)
	}
	if h.started != 0 {
		t.Fatal("a second daemon was started beside the running one")
	}
	if res := decodeServeResult(t, out); !res.Reused || res.PID != 4242 {
		t.Fatalf("result = %#v", res)
	}
}

func TestServeForegroundExitsNonZeroWhenWatchedDaemonCrashes(t *testing.T) {
	h := newForegroundHarness(t)
	h.daemon(4242, "persistent")
	h.onTick = func() { h.alive[4242] = false } // run file left behind
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), "exited without shutting down") {
		t.Fatalf("err = %v, want a crash error so the service restarts", err)
	}
}

func TestServeForegroundExitsNonZeroWhenAppDaemonQuits(t *testing.T) {
	h := newForegroundHarness(t)
	h.daemon(4242, "app")
	h.onTick = func() {
		h.alive[4242] = false
		_ = runfile.Remove(h.cfg.runFile)
	}
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), "stopped with the app") {
		t.Fatalf("err = %v, want a restart error when the app's daemon goes away", err)
	}
}

func TestServeForegroundFollowsAppReplacement(t *testing.T) {
	h := newForegroundHarness(t)
	h.atTick = 2
	old := serveForegroundDaemon
	t.Cleanup(func() { serveForegroundDaemon = old })
	// The desktop app shuts the in-process daemon down and starts its own
	// persistent daemon on the same data dir, which is later stopped cleanly.
	h.onTick = func() {
		h.alive[6161] = false
		_ = runfile.Remove(h.cfg.runFile)
	}
	out, err := h.runWithDaemon(func() { h.daemon(6161, "persistent") })
	if err != nil {
		t.Fatalf("clean stop of the replacement should exit 0: %v", err)
	}
	if !strings.Contains(out, "pid 6161") {
		t.Fatalf("serve did not follow the replacement daemon:\n%s", out)
	}
}

// runWithDaemon is run with an in-process daemon that, when it returns,
// leaves replace() running in its place.
func (h *foregroundHarness) runWithDaemon(replace func()) (string, error) {
	h.t.Helper()
	serveForegroundDaemon = func([]string) error {
		h.started++
		replace()
		return nil
	}
	out, _, err := executeCLI(h.t, Deps{
		ProcessAlive:   func(pid int) bool { return h.alive[pid] },
		PIDMayBeHidden: func() bool { return false },
		Now:            func() time.Time { return h.now },
		Sleep: func(d time.Duration) {
			h.now = h.now.Add(d)
			h.ticks++
			if h.ticks == h.atTick && h.onTick != nil {
				h.onTick()
			}
		},
	}, "serve", "--foreground")
	return out, err
}

func TestServeRefusesDaemonThatDoesNotReportItsDataDir(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := fakeServeDaemonIn(t, 4242, "")
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	started := false
	_, _, err := executeCLI(t, Deps{
		ProcessAlive:  func(pid int) bool { return pid == 4242 },
		StartDetached: func(processStartConfig) (startedProcess, error) { started = true; return startedProcess{}, nil },
	}, "serve")
	if err == nil || !strings.Contains(err.Error(), "does not report its data dir") {
		t.Fatalf("err = %v, want an unverifiable-identity error", err)
	}
	if started {
		t.Fatal("started a second daemon beside one it could not verify")
	}
}

func TestSameDirComparesFilesystemIdentity(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !sameDir(a, a+string(filepath.Separator)) {
		t.Fatal("the same directory did not match itself")
	}
	if sameDir(a, b) {
		t.Fatal("two distinct directories matched")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(a, link); err == nil && !sameDir(link, a) {
		t.Fatal("a symlink did not match its target")
	}
}

func TestServeForegroundRefusesReplacementForAnotherDataDir(t *testing.T) {
	h := newForegroundHarness(t)
	h.daemon(4242, "persistent")
	h.onTick = func() {
		// The run file now names a ready daemon that serves another data dir.
		srv := fakeServeDaemonIn(h.t, 7070, h.t.TempDir())
		if err := runfile.Write(h.cfg.runFile, runfile.Info{PID: 7070, Port: serverPort(h.t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
			h.t.Fatal(err)
		}
		h.alive[4242], h.alive[7070] = false, true
	}
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), "serves data dir") {
		t.Fatalf("err = %v, want a data-dir conflict instead of following pid 7070", err)
	}
}

func TestServeForegroundRestartsWhenReplacementCrashes(t *testing.T) {
	h := newForegroundHarness(t)
	h.atTick = 2
	old := serveForegroundDaemon
	t.Cleanup(func() { serveForegroundDaemon = old })
	// The in-process daemon is shut down; the app's replacement writes its run
	// file and dies before it is ready.
	_, err := h.runWithDaemon(func() {
		if err := runfile.Write(h.cfg.runFile, runfile.Info{PID: 8080, Port: 1, StartedAt: time.Unix(100, 0).UTC(), Owner: "persistent"}); err != nil {
			h.t.Fatal(err)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "replacement daemon (pid 8080) exited without shutting down") {
		t.Fatalf("err = %v, want a restart error for the crashed replacement", err)
	}
}

// `ao setup` and the installer call `ao serve --background`; it is the default.
func TestServeAcceptsBackgroundFlag(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := fakeServeDaemon(t, 4242)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(pid int) bool { return pid == 4242 }}, "serve", "--background", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if res := decodeServeResult(t, out); res.Status != "ready" {
		t.Fatalf("result = %#v", res)
	}
}

// The foreground daemon reads this process's environment, so an inherited
// AO_OWNER must be removed there too, not only from the detached child's env.
func TestApplyProcessEnvRemovesInheritedOwner(t *testing.T) {
	t.Setenv("AO_OWNER", "app")
	t.Setenv("AO_SERVE_TEST_KEEP", "1")
	before := os.Environ()
	t.Cleanup(func() {
		if err := applyProcessEnv(before); err != nil {
			t.Error(err)
		}
	})
	if err := applyProcessEnv(serveEnv(os.Environ(), 4100, false, "dev")); err != nil {
		t.Fatal(err)
	}
	if v, ok := os.LookupEnv("AO_OWNER"); ok {
		t.Fatalf("AO_OWNER = %q still set for the foreground daemon", v)
	}
	if os.Getenv("AO_PORT") != "4100" || os.Getenv("AO_SERVE_TEST_KEEP") != "1" {
		t.Fatalf("env not applied: AO_PORT=%q keep=%q", os.Getenv("AO_PORT"), os.Getenv("AO_SERVE_TEST_KEEP"))
	}
}

// An `ao stop` that is still shutting the daemon down while the service's
// serve starts must stick: serve exits 0 instead of starting its own daemon.
func TestServeForegroundHonoursStopDuringStartup(t *testing.T) {
	h := newForegroundHarness(t)
	h.daemon(4242, "")
	h.atTick = 2
	// Shutting down: the PID is alive but its listener is already closed.
	if err := runfile.Write(h.cfg.runFile, runfile.Info{PID: 4242, Port: 1, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	h.onTick = func() {
		h.alive[4242] = false
		_ = runfile.Remove(h.cfg.runFile)
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("a stop during startup should exit 0: %v", err)
	}
	if h.started != 0 {
		t.Fatal("serve started its own daemon in place of one that was being stopped")
	}
}

func TestServeForegroundWritesAttachMarkerWhileWatching(t *testing.T) {
	h := newForegroundHarness(t)
	h.daemon(4242, "")
	marker := serveAttachMarker(h.cfg.runFile, os.Getpid())
	seen := false
	h.onTick = func() {
		_, err := os.Stat(marker)
		seen = err == nil
		h.alive[4242] = false
		_ = runfile.Remove(h.cfg.runFile)
	}
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no attach marker while watching")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("attach marker left behind after exit: %v", err)
	}
}

// On a fresh home the run file's directory does not exist yet.
func TestServeForegroundCreatesMarkerDirOnFreshHome(t *testing.T) {
	h := newForegroundHarness(t)
	fresh := filepath.Join(t.TempDir(), "fresh", ".ao")
	h.cfg.runFile = filepath.Join(fresh, "running.json")
	t.Setenv("AO_RUN_FILE", h.cfg.runFile)
	marker := serveAttachMarker(h.cfg.runFile, os.Getpid())
	seen := false
	h.atTick = 1
	old := serveForegroundDaemon
	t.Cleanup(func() { serveForegroundDaemon = old })
	_, err := h.runWithDaemon(func() {
		_, statErr := os.Stat(marker)
		seen = statErr == nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no attach marker while the daemon ran on a fresh home")
	}
}

// An agent's `ao serve` must not hand its own session and agent variables to
// the daemon, which passes its environment to every session it spawns.
func TestServeEnvScrubsCallerSession(t *testing.T) {
	base := []string{
		"PATH=/bin", "HOME=/h",
		"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_SESSION_ID=s1",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret", "CLAUDE_CODE_USE_BEDROCK=1",
		"CODEX_THREAD_ID=t", "GEMINI_CLI=1", "CURSOR_AGENT=1", "OPENCODE=1", "AI_AGENT=claude",
		"AO_SESSION_ID=ao-1", "AO_SESSION=ao-1", "AO_PROJECT_ID=p", "AO_OWNER=app", "AO_APP_RUN_ID=r",
		"AO_DATA_DIR=/d", "AO_RUN_FILE=/r.json", "AO_PORT=3101",
	}
	env := envMap(serveEnv(base, 0, false, "dev"))
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_TOKEN", "CODEX_THREAD_ID", "GEMINI_CLI", "CURSOR_AGENT", "OPENCODE", "AI_AGENT", "AO_SESSION_ID", "AO_SESSION", "AO_PROJECT_ID", "AO_OWNER", "AO_APP_RUN_ID"} {
		if v, ok := env[k]; ok {
			t.Errorf("%s=%q reached the daemon", k, v)
		}
	}
	for k, want := range map[string]string{"PATH": "/bin", "HOME": "/h", "CLAUDE_CODE_USE_BEDROCK": "1", "AO_DATA_DIR": "/d", "AO_RUN_FILE": "/r.json", "AO_PORT": "3101"} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q kept", k, env[k], want)
		}
	}
}

func TestServeAnalyticsNoticeShownOnce(t *testing.T) {
	cfg := setConfigEnv(t)
	c := config.Config{RunFilePath: cfg.runFile}
	var first, second, off bytes.Buffer
	noteAnalyticsOnce(&off, c, []string{"AO_TELEMETRY_REMOTE=off"})
	noteAnalyticsOnce(&first, c, []string{"AO_TELEMETRY_REMOTE=posthog"})
	noteAnalyticsOnce(&second, c, []string{"AO_TELEMETRY_REMOTE=posthog"})
	if off.Len() != 0 {
		t.Fatalf("notice printed with analytics off: %q", off.String())
	}
	if !strings.Contains(first.String(), "DO_NOT_TRACK=1") {
		t.Fatalf("first notice = %q, want the opt-out", first.String())
	}
	if second.Len() != 0 {
		t.Fatalf("notice printed twice: %q", second.String())
	}
}

// An explicit --port wins over an invalid inherited AO_PORT.
func TestServePortFlagOverridesInvalidEnvPort(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := fakeServeDaemon(t, 4242)
	port := serverPort(t, srv.URL)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: port, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_PORT", "not-a-port")
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(pid int) bool { return pid == 4242 }}, "serve", "--json", "--port", fmt.Sprint(port))
	if err != nil {
		t.Fatalf("--port did not override the invalid AO_PORT: %v", err)
	}
	if res := decodeServeResult(t, out); res.PID != 4242 {
		t.Fatalf("result = %#v", res)
	}
	t.Setenv("AO_PORT", "not-a-port")
	if _, _, err := executeCLI(t, Deps{}, "serve"); err == nil || !strings.Contains(err.Error(), "invalid AO_PORT") {
		t.Fatalf("err = %v, want the invalid AO_PORT rejected without --port", err)
	}
}

// A daemon seen ready at startup that then stops cleanly is an `ao stop`:
// serve must not start one in its place.
func TestServeForegroundReadyThenStoppedDuringStartup(t *testing.T) {
	h := newForegroundHarness(t)
	h.daemon(4242, "")
	checks := 0
	old := serveForegroundDaemon
	t.Cleanup(func() { serveForegroundDaemon = old })
	serveForegroundDaemon = func([]string) error { h.started++; return nil }
	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool {
			checks++
			if checks == 1 {
				return true // settleStartup: ready
			}
			_ = runfile.Remove(h.cfg.runFile) // `ao stop` lands before the next look
			return false
		},
		PIDMayBeHidden: func() bool { return false },
		Now:            func() time.Time { return h.now },
		Sleep:          func(d time.Duration) { h.now = h.now.Add(d) },
	}, "serve", "--foreground")
	if err != nil {
		t.Fatalf("a stop during startup should exit 0: %v", err)
	}
	if h.started != 0 {
		t.Fatal("serve started a daemon in place of one that was just stopped")
	}
}

// A child that lost the data-dir lock to a concurrent start is not a failure
// when that winner serves this data dir.
func TestServeAcceptsConcurrentWinnerAfterChildExit(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := fakeServeDaemon(t, 5151)
	exited := make(chan error, 1)
	exited <- errors.New("exit status 75")
	out, _, err := executeCLI(t, Deps{
		Executable:   func() (string, error) { return "/opt/ao/bin/ao", nil },
		ProcessAlive: func(pid int) bool { return pid == 5151 },
		StartDetached: func(processStartConfig) (startedProcess, error) {
			// The winner is ready by the time the child's exit is seen.
			if err := runfile.Write(cfg.runFile, runfile.Info{PID: 5151, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
				t.Fatal(err)
			}
			return startedProcess{PID: 9, Exited: exited}, nil
		},
	}, "serve", "--json")
	if err != nil {
		t.Fatalf("serve failed although a same-dir daemon is ready: %v", err)
	}
	if res := decodeServeResult(t, out); res.PID != 5151 || !res.Reused {
		t.Fatalf("result = %#v, want the winner, reused", res)
	}
}
