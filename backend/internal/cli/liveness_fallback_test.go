package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// These tests cover a CLI that cannot see the daemon's PID, as inside Codex's
// Linux sandbox, which gives every shell command its own PID namespace (#6820).
// ProcessAlive always reports the PID as dead; the daemon must still be found
// through its loopback /healthz, and only when that probe proves ownership.

const hiddenDaemonPID = 4242

// pidHidden is the ProcessAlive a sandboxed CLI effectively has.
func pidHidden(int) bool { return false }

type fakeDaemon struct {
	srv        *httptest.Server
	apiCalls   atomic.Int32
	shutdownOK atomic.Bool
}

// newFakeDaemon serves /healthz and /readyz as the daemon would, reporting the
// given service and PID, plus a /api/v1/ping route that counts calls.
// healthStatus 0 means /healthz answers 500.
func newFakeDaemon(t *testing.T, cfg testConfig, service string, pid int, healthStatus int) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			if healthStatus == 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, service, pid)
		case "/readyz":
			_, _ = fmt.Fprintf(w, `{"status":"ready","service":%q,"pid":%d}`, service, pid)
		case "/api/v1/ping":
			d.apiCalls.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/shutdown":
			d.shutdownOK.Store(true)
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
	return &commandContext{deps: Deps{HTTPClient: &http.Client{}, ProcessAlive: pidHidden}}
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

	out, _, err := executeCLI(t, Deps{HTTPClient: &http.Client{}, ProcessAlive: pidHidden}, "status", "--json")
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

			out, _, err := executeCLI(t, Deps{HTTPClient: &http.Client{}, ProcessAlive: pidHidden}, "status", "--json")
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

	out, _, err := executeCLI(t, Deps{HTTPClient: &http.Client{}, ProcessAlive: pidHidden}, "stop", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !d.shutdownOK.Load() {
		t.Fatal("stop did not request shutdown from a verified daemon whose PID is hidden")
	}
	if !strings.Contains(out, `"state": "stopped"`) {
		t.Fatalf("stop did not report stopped:\n%s", out)
	}
}
