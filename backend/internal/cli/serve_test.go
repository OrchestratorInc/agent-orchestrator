package cli

import (
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

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// fakeServeDaemon answers /healthz and /readyz as the Orchestrator daemon
// with the given PID.
func fakeServeDaemon(t *testing.T, pid int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, pid)
		case "/readyz":
			_, _ = fmt.Fprintf(w, `{"status":"ready","service":%q,"pid":%d}`, daemonmeta.ServiceName, pid)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
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
	if env["AO_OWNER"] != "persistent" {
		t.Fatalf("AO_OWNER = %q, want persistent", env["AO_OWNER"])
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
	for _, args := range [][]string{{"serve", "--port", "70000"}, {"serve", "--timeout", "0s"}, {"serve", "extra"}} {
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
		{name: "DO_NOT_TRACK=0 is not an opt-out", base: []string{"DO_NOT_TRACK=0"}, version: "1.2.3", wantRemote: "posthog"},
		{name: "explicit env wins", base: []string{"AO_TELEMETRY_REMOTE=off"}, version: "1.2.3", wantRemote: "off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := serveEnv(append([]string{"PATH=/bin"}, tc.base...), 0, tc.optedOut, tc.version)
			m := envMap(env)
			if m["AO_TELEMETRY_REMOTE"] != tc.wantRemote {
				t.Fatalf("AO_TELEMETRY_REMOTE = %q, want %q", m["AO_TELEMETRY_REMOTE"], tc.wantRemote)
			}
			if m["AO_OWNER"] != "persistent" || m["PATH"] != "/bin" {
				t.Fatalf("env lost owner or caller values: %v", m)
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
