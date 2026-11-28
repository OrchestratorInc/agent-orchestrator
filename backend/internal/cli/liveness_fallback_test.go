package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/runfile"
)

// These tests cover a CLI that cannot see the daemon's PID, as inside Codex's
// Linux sandbox, which gives every shell command its own PID namespace (#6820).
// ProcessAlive always reports the PID as dead; the daemon must still be found
// through its loopback /healthz, and only when that probe proves ownership.
// PIDMayBeHidden is forced on so these run on every OS; the default (Linux
// only) is covered by TestDeadPIDIsNotOverriddenWherePIDsCannotBeHidden.

const hiddenDaemonPID = 4242

// pidHidden is the ProcessAlive a sandboxed CLI effectively has.
func pidHidden(int) bool { return false }

func pidsCanHide() bool    { return true }
func pidsCannotHide() bool { return false }

// hiddenPIDDeps are the deps of a CLI inside a PID-isolated sandbox.
func hiddenPIDDeps() Deps {
	return Deps{HTTPClient: &http.Client{}, ProcessAlive: pidHidden, PIDMayBeHidden: pidsCanHide}
}

type fakeDaemon struct {
	srv        *httptest.Server
	apiCalls   atomic.Int32
	readyCalls atomic.Int32
	shutdownOK atomic.Bool
	// down is set as soon as the daemon starts draining after /shutdown;
	// /healthz then answers 500, as the real server closes its listeners first.
	down atomic.Bool
	// runFileAtExit records whether the run-file still existed when the daemon
	// finished draining, i.e. whether nobody removed it from under the daemon.
	runFileAtExit atomic.Bool
	stopped       chan struct{}
}

// newFakeDaemon serves /healthz and /readyz as the daemon would, reporting the
// given service and PID, plus a /api/v1/ping route that counts calls.
// healthStatus 0 means /healthz answers 500. Like the real daemon, after
// /shutdown it stops answering at once, drains, then removes its own run-file.
func newFakeDaemon(t *testing.T, cfg testConfig, service string, pid int, healthStatus int) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{stopped: make(chan struct{})}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			if healthStatus == 0 || d.down.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, service, pid)
		case "/readyz":
			d.readyCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{"status":"ready","service":%q,"pid":%d}`, service, pid)
		case "/api/v1/ping":
			d.apiCalls.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/shutdown":
			if d.shutdownOK.Swap(true) {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			go func() {
				defer close(d.stopped)
				time.Sleep(50 * time.Millisecond)
				d.down.Store(true) // listeners closed; still draining
				time.Sleep(400 * time.Millisecond)
				_, err := os.Stat(cfg.runFile)
				d.runFileAtExit.Store(err == nil)
				_ = runfile.Remove(cfg.runFile)
			}()
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.srv.Close)
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: hiddenDaemonPID, Port: serverPort(t, d.srv.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("write run-file: %v", err)
	}
	return d
}

func hiddenPIDContext() *commandContext {
	return &commandContext{deps: hiddenPIDDeps()}
}

func TestClientReachesDaemonWhosePIDIsHidden(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("healthz proves ownership: request is sent", func(t *testing.T) {
		cfg := setConfigEnv(t)
		d := newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)
		var out map[string]bool
		if err := hiddenPIDContext().getJSON(ctx, "ping", &out); err != nil {
			t.Fatalf("getJSON with hidden PID: %v", err)
		}
		if got := d.apiCalls.Load(); got != 1 {
			t.Fatalf("API calls = %d, want 1", got)
		}
	})

	cases := []struct {
		name         string
		service      string
		pid          int
		healthStatus int
	}{
		{"healthz reports a different PID", daemonmeta.ServiceName, hiddenDaemonPID + 1, http.StatusOK},
		{"healthz is from another service on the port", "something-else", hiddenDaemonPID, http.StatusOK},
		{"healthz fails", daemonmeta.ServiceName, hiddenDaemonPID, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name+": daemon reported not running", func(t *testing.T) {
			cfg := setConfigEnv(t)
			d := newFakeDaemon(t, cfg, tc.service, tc.pid, tc.healthStatus)
			err := hiddenPIDContext().getJSON(ctx, "ping", nil)
			if !errors.Is(err, errDaemonNotRunning) {
				t.Fatalf("err = %v, want errDaemonNotRunning", err)
			}
			if !strings.Contains(err.Error(), "stale run-file") {
				t.Fatalf("err = %q, want stale run-file message", err)
			}
			if got := d.apiCalls.Load(); got != 0 {
				t.Fatalf("API calls = %d, want 0: request must not reach an unverified process", got)
			}
		})
	}

	t.Run("nothing listens on the port", func(t *testing.T) {
		cfg := setConfigEnv(t)
		d := newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)
		d.srv.Close()
		if err := hiddenPIDContext().getJSON(ctx, "ping", nil); !errors.Is(err, errDaemonNotRunning) {
			t.Fatalf("err = %v, want errDaemonNotRunning", err)
		}
	})
}

func TestStatusReadyWhenDaemonPIDIsHidden(t *testing.T) {
	cfg := setConfigEnv(t)
	newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)

	out, _, err := executeCLI(t, hiddenPIDDeps(), "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"state": "ready"`) {
		t.Fatalf("status with hidden PID did not report ready:\n%s", out)
	}
}

func TestStatusStaleWhenHiddenPIDIsNotProven(t *testing.T) {
	cases := []struct {
		name         string
		service      string
		pid          int
		healthStatus int
	}{
		{"different PID", daemonmeta.ServiceName, hiddenDaemonPID + 1, http.StatusOK},
		{"different service", "something-else", hiddenDaemonPID, http.StatusOK},
		{"healthz fails", daemonmeta.ServiceName, hiddenDaemonPID, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			newFakeDaemon(t, cfg, tc.service, tc.pid, tc.healthStatus)

			out, _, err := executeCLI(t, hiddenPIDDeps(), "status", "--json")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"state": "stale"`) || !strings.Contains(out, "run-file points to a dead process") {
				t.Fatalf("status did not report stale:\n%s", out)
			}
		})
	}
}

// Before the fallback, `ao stop` inside a PID-isolated sandbox classified the
// live daemon as stale and deleted its run-file without stopping it. It must
// now ask the daemon to shut down.
func TestStopShutsDownDaemonWhosePIDIsHidden(t *testing.T) {
	cfg := setConfigEnv(t)
	d := newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)

	out, _, err := executeCLI(t, hiddenPIDDeps(), "stop", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !d.shutdownOK.Load() {
		t.Fatal("stop did not request shutdown from a verified daemon whose PID is hidden")
	}
	if !strings.Contains(out, `"state": "stopped"`) {
		t.Fatalf("stop did not report stopped:\n%s", out)
	}
	// The CLI never removes the run-file of a daemon it cannot see, so the
	// file being gone when stop returns means stop waited for the daemon.
	if _, err := os.Stat(cfg.runFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stop returned before the daemon removed its run-file (stat err = %v)", err)
	}
	<-d.stopped
	if !d.runFileAtExit.Load() {
		t.Fatal("stop removed the run-file while the daemon was still shutting down")
	}
}

// In Codex's read-only reviewer sandbox the CLI cannot write ~/.ao. The daemon
// removes its own run-file, so stop must succeed without being able to.
func TestStopSucceedsWhenHiddenPIDRunFileIsReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not make files undeletable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	cfg := setConfigEnv(t)
	d := newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)
	dir := filepath.Dir(cfg.runFile)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	// The daemon cannot remove its run-file either, so stop waits out its
	// timeout, finds the daemon no longer answering, and reports stopped.
	out, _, err := executeCLI(t, hiddenPIDDeps(), "stop", "--json", "--timeout", "1s")
	if err != nil {
		t.Fatalf("stop with a read-only run-file folder: %v", err)
	}
	if !d.shutdownOK.Load() || !strings.Contains(out, `"state": "stopped"`) {
		t.Fatalf("stop did not shut the daemon down and report stopped:\n%s", out)
	}
}

// On macOS and Windows a PID that fails the liveness check is dead. Whatever
// answers on the stale run-file's port, even claiming the right service and
// PID, must not be treated as the daemon or receive requests.
func TestDeadPIDIsNotOverriddenWherePIDsCannotBeHidden(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deps := Deps{HTTPClient: &http.Client{}, ProcessAlive: pidHidden, PIDMayBeHidden: pidsCannotHide}

	t.Run("API request is refused", func(t *testing.T) {
		cfg := setConfigEnv(t)
		impostor := newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)
		err := (&commandContext{deps: deps}).getJSON(ctx, "ping", nil)
		if !errors.Is(err, errDaemonNotRunning) {
			t.Fatalf("err = %v, want errDaemonNotRunning", err)
		}
		if got := impostor.apiCalls.Load(); got != 0 {
			t.Fatalf("API calls = %d, want 0", got)
		}
	})

	t.Run("status reports stale", func(t *testing.T) {
		cfg := setConfigEnv(t)
		newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)
		out, _, err := executeCLI(t, deps, "status", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"state": "stale"`) {
			t.Fatalf("status did not report stale:\n%s", out)
		}
	})

	t.Run("default is Linux only", func(t *testing.T) {
		if got, want := DefaultDeps().PIDMayBeHidden(), runtime.GOOS == "linux"; got != want {
			t.Fatalf("default PIDMayBeHidden() = %v, want %v on %s", got, want, runtime.GOOS)
		}
	})
}

// A /healthz body is a few hundred bytes. An oversized one must be rejected
// rather than buffered, and must not vouch for the daemon.
func TestOversizedHealthzBodyIsRejected(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("unexpected request to %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d,"pad":"%s"}`,
			daemonmeta.ServiceName, hiddenDaemonPID, strings.Repeat("x", maxProbeBody))
	}))
	t.Cleanup(srv.Close)
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: hiddenDaemonPID, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	err := hiddenPIDContext().getJSON(context.Background(), "ping", nil)
	if !errors.Is(err, errDaemonNotRunning) {
		t.Fatalf("err = %v, want errDaemonNotRunning for an oversized /healthz", err)
	}
}

// A /healthz that times out proves nothing about a daemon whose PID is hidden.
// It must not be reported stale, and stop must keep its run-file.
func TestHiddenPIDProbeTimeoutKeepsRunFile(t *testing.T) {
	cfg := setConfigEnv(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: hiddenDaemonPID, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	deps := hiddenPIDDeps()
	deps.HTTPClient = &http.Client{Timeout: 200 * time.Millisecond}

	out, _, err := executeCLI(t, deps, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"state": "unhealthy"`) {
		t.Fatalf("status after a probe timeout did not report unhealthy:\n%s", out)
	}
	if _, _, err := executeCLI(t, deps, "stop", "--json"); err == nil {
		t.Fatal("stop succeeded against a daemon it could not verify")
	}
	if _, err := os.Stat(cfg.runFile); err != nil {
		t.Fatalf("stop removed the run-file after an inconclusive probe: %v", err)
	}
}

// The probe must be answered by the run-file's port itself. A redirect from
// whatever listens there must not be followed to another host.
func TestProbeDoesNotFollowRedirects(t *testing.T) {
	cfg := setConfigEnv(t)
	var elsewhere atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, hiddenDaemonPID)
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.RedirectHandler(target.URL+"/healthz", http.StatusFound))
	t.Cleanup(redirector.Close)
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: hiddenDaemonPID, Port: serverPort(t, redirector.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	err := hiddenPIDContext().getJSON(context.Background(), "ping", nil)
	if !errors.Is(err, errDaemonNotRunning) {
		t.Fatalf("err = %v, want errDaemonNotRunning", err)
	}
	if got := elsewhere.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests, want 0", got)
	}
}

// Once inspection has verified a hidden-PID daemon over loopback, stop must
// keep treating it as hidden. A later PID check that matches an unrelated
// process with the same number must not switch it to waiting on that PID.
func TestStopKeepsHiddenWaitWhenPIDNumberIsReused(t *testing.T) {
	cfg := setConfigEnv(t)
	d := newFakeDaemon(t, cfg, daemonmeta.ServiceName, hiddenDaemonPID, http.StatusOK)
	deps := hiddenPIDDeps()
	// Hidden until inspection has verified the daemon (its last step is
	// /readyz); after that an unrelated process holds the same PID number.
	var afterInspection atomic.Int32
	deps.ProcessAlive = func(int) bool {
		if d.readyCalls.Load() == 0 {
			return false
		}
		afterInspection.Add(1)
		return true
	}

	out, _, err := executeCLI(t, deps, "stop", "--json", "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"state": "stopped"`) || !d.shutdownOK.Load() {
		t.Fatalf("stop did not stop the hidden daemon:\n%s", out)
	}
	// Any PID check after inspection would be acting on the reused number.
	if got := afterInspection.Load(); got != 0 {
		t.Fatalf("stop checked the PID %d time(s) after inspection, want 0", got)
	}
}
