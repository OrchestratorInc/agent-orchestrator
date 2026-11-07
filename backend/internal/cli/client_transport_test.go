package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
)

// transportServer returns an httptest server that always answers with the
// given status and body for any CLI daemon call.
func transportServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func transportContext(t *testing.T, cfg testConfig, srv *httptest.Server) *commandContext {
	t.Helper()
	writeRunFileFor(t, cfg, srv)
	return &commandContext{deps: Deps{
		HTTPClient:   &http.Client{},
		ProcessAlive: func(int) bool { return true },
	}}
}

// TestTransportDistinguishesEmptyResponses is the API-014 regression: an empty
// 200/201 with a required decoded result must fail, while legitimate
// bodyless calls (nil out) and 204 responses keep succeeding.
func TestTransportDistinguishesEmptyResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("empty 200 with required body fails", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := transportServer(t, http.StatusOK, ``)
		c := transportContext(t, cfg, srv)
		var out spawnResult
		if err := c.postJSON(ctx, "sessions", struct{}{}, &out); err == nil {
			t.Fatal("expected error for empty 200 with required body, got nil")
		} else if !strings.Contains(err.Error(), "missing required response body") {
			t.Fatalf("error = %q, want missing required response body", err.Error())
		}
	})

	t.Run("empty 201 with required body fails", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := transportServer(t, http.StatusCreated, ``)
		c := transportContext(t, cfg, srv)
		var out spawnResult
		if err := c.postJSON(ctx, "sessions", struct{}{}, &out); err == nil {
			t.Fatal("expected error for empty 201 with required body, got nil")
		}
	})

	t.Run("204 empty with decoded result succeeds", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := transportServer(t, http.StatusNoContent, ``)
		c := transportContext(t, cfg, srv)
		var out projectRemoveResult
		if err := c.deleteJSON(ctx, "projects/demo", &out); err != nil {
			t.Fatalf("204 with decoded result should succeed, got: %v", err)
		}
	})

	t.Run("nil output tolerates empty 200", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := transportServer(t, http.StatusOK, ``)
		c := transportContext(t, cfg, srv)
		if err := c.postJSON(ctx, "sessions/ao-1/activity", struct{}{}, nil); err != nil {
			t.Fatalf("nil output should tolerate empty body, got: %v", err)
		}
	})

	t.Run("valid JSON still decodes", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := transportServer(t, http.StatusOK, `{"session":{"id":"demo-11","status":"idle"}}`)
		c := transportContext(t, cfg, srv)
		var out spawnResult
		if err := c.postJSON(ctx, "sessions", struct{}{}, &out); err != nil {
			t.Fatalf("valid JSON should decode, got: %v", err)
		}
		if out.Session.ID != "demo-11" {
			t.Fatalf("Session.ID = %q, want demo-11", out.Session.ID)
		}
	})

	t.Run("malformed JSON fails", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := transportServer(t, http.StatusOK, `{not-json`)
		c := transportContext(t, cfg, srv)
		var out spawnResult
		if err := c.postJSON(ctx, "sessions", struct{}{}, &out); err == nil {
			t.Fatal("expected error for malformed JSON, got nil")
		}
	})

	t.Run("literal null with required body fails", func(t *testing.T) {
		for _, body := range []string{`null`, "  null  \n"} {
			cfg := setConfigEnv(t)
			srv := transportServer(t, http.StatusOK, body)
			c := transportContext(t, cfg, srv)
			var out spawnResult
			err := c.postJSON(ctx, "sessions", struct{}{}, &out)
			if err == nil {
				t.Fatalf("body %q: expected error for null body, got nil", body)
			}
			if !strings.Contains(err.Error(), "missing required response body") {
				t.Fatalf("body %q: error = %q, want missing required response body", body, err.Error())
			}
		}
	})

	t.Run("daemon error envelope keeps code and request id", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := transportServer(t, http.StatusNotFound, `{"message":"nope","code":"NOT_FOUND","requestId":"req-1"}`)
		c := transportContext(t, cfg, srv)
		var out spawnResult
		err := c.postJSON(ctx, "sessions", struct{}{}, &out)
		var apiErr apiResponseError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error type = %T, want apiResponseError", err)
		}
		if apiErr.ErrorBody.Code != "NOT_FOUND" || apiErr.ErrorBody.RequestID != "req-1" {
			t.Fatalf("envelope = %+v, want code NOT_FOUND request req-1", apiErr.ErrorBody)
		}
	})
}

// TestSpawnRejectsEmptySessionID ensures a daemon that answers spawn with an
// empty (but valid) body can never print a success line.
func TestSpawnRejectsEmptySessionID(t *testing.T) {
	for _, body := range []string{``, `{}`} {
		cfg := setConfigEnv(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
				_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","config":{"worker":{"agent":"codex"}}}}`)
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
				_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
				_, _ = io.WriteString(w, body)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		writeRunFileFor(t, cfg, srv)

		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
			"spawn", "--project", "demo", "--agent", "codex", "--name", "worker")
		if err == nil {
			t.Fatalf("body %q: expected spawn error, got success %q", body, out)
		}
		if strings.Contains(out, "spawned session") {
			t.Fatalf("body %q: must not print success, got %q", body, out)
		}
	}
}

// TestReviewSubmitBatchRejectsEmptyEntries ensures batch review submit fails
// instead of printing success when any returned entry is missing its run ID
// or verdict — including empty objects and JSON nulls.
func TestReviewSubmitBatchRejectsEmptyEntries(t *testing.T) {
	batchInput := `[{"runId":"run-1","verdict":"approved"}]`
	tests := []struct {
		name string
		body string
	}{
		{"empty entry", `{"reviews":[{}]}`},
		{"null entry", `{"reviews":[null]}`},
		{"id-only entry", `{"reviews":[{"id":"run-1"}]}`},
		{"verdict-only entry", `{"reviews":[{"verdict":"approved"}]}`},
		{"one bad entry among good", `{"reviews":[{"id":"run-1","verdict":"approved"},{}]}`},
		{"empty array and empty single", `{"reviews":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)

			deps := Deps{ProcessAlive: func(int) bool { return true }}
			deps.In = strings.NewReader(batchInput)
			out, _, err := executeCLI(t, deps,
				"review", "submit", "sess-1", "--reviews", "-")
			if err == nil {
				t.Fatalf("body %q: expected batch submit error, got nil", tt.body)
			}
			if !strings.Contains(err.Error(), "empty review result") {
				t.Fatalf("body %q: err = %q, want empty review result", tt.body, err.Error())
			}
			if strings.Contains(out, "recorded") {
				t.Fatalf("body %q: must not print success, got %q", tt.body, out)
			}
		})
	}
}

// TestReviewSubmitRejectsEmptyResult ensures review submit fails instead of
// reporting a recorded review when the daemon returns nothing useful: an
// empty body fails at the transport layer, while a valid-but-empty document
// fails at the caller guard.
func TestReviewSubmitRejectsEmptyResult(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"empty body fails at transport", ``, "missing required response body"},
		{"empty document fails at guard", `{}`, "empty review result"},
		{"half-populated document fails at guard", `{"review":{"id":"run-1"}}`, "empty review result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
				"review", "submit", "sess-1", "--run", "run-1", "--verdict", "approved")
			if err == nil {
				t.Fatalf("body %q: expected review submit error, got nil", tt.body)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("body %q: err = %q, want %q", tt.body, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestTransportReachesDaemonWhosePIDIsHidden covers a CLI call from inside a
// sandbox with its own PID namespace (Codex's Linux sandbox): the daemon's PID
// is invisible there, so only a healthz probe naming the run-file PID may stand
// in for the PID check. Anything else on the port is still a stale run-file.
func TestTransportReachesDaemonWhosePIDIsHidden(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []struct {
		name      string
		health    func() string
		wantStale bool
	}{
		{
			name: "healthz names the run-file daemon",
			health: func() string {
				return fmt.Sprintf(`{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
			},
		},
		{
			name: "healthz names another pid",
			health: func() string {
				return fmt.Sprintf(`{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid()+1)
			},
			wantStale: true,
		},
		{
			name:      "port answers but is not the AO daemon",
			health:    func() string { return `{"status":"ok"}` },
			wantStale: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			apiCalled := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/healthz" {
					_, _ = io.WriteString(w, tc.health())
					return
				}
				apiCalled = true
				_, _ = io.WriteString(w, `{}`)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			c := &commandContext{deps: Deps{
				HTTPClient:   &http.Client{},
				ProcessAlive: func(int) bool { return false },
			}}

			err := c.postJSON(ctx, "sessions/ao-1/activity", struct{}{}, nil)
			if tc.wantStale {
				if !errors.Is(err, errDaemonNotRunning) || !strings.Contains(err.Error(), "stale run-file") {
					t.Fatalf("err = %v, want stale run-file", err)
				}
				if apiCalled {
					t.Fatal("API route was called on a port the CLI could not verify")
				}
				return
			}
			if err != nil {
				t.Fatalf("postJSON: %v", err)
			}
			if !apiCalled {
				t.Fatal("API route was not called")
			}
		})
	}
}
