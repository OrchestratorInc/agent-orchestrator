package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func forkCommandServer(t *testing.T, refuse bool) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/fork":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			bodies = append(bodies, in["clientRequestId"])
			if refuse {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"conflict","code":"FORK_SOURCE_BUSY","message":"Wait for the agent to go idle to fork this session."}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"sessionId":"demo-9","displayName":"Fork of Demo"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1/fork-options":
			_, _ = w.Write([]byte(`{"allowed":false,"refusal":{"code":"FORK_SOURCE_BUSY","message":"Wait for the agent to go idle to fork this session."}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestSessionForkCreatesFork(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, bodies := forkCommandServer(t, false)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "fork", "demo-1")
	if err != nil {
		t.Fatalf("session fork failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `forked demo-1 into demo-9 "Fork of Demo"`) {
		t.Fatalf("output = %q", out)
	}
	if len(*bodies) != 1 || (*bodies)[0] == "" {
		t.Fatalf("client request ids = %v", *bodies)
	}
}

func TestSessionForkRefusalIsRuntimeError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := forkCommandServer(t, true)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "fork", "demo-1")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "FORK_SOURCE_BUSY") {
		t.Fatalf("err = %v exit = %d", err, ExitCode(err))
	}
}

func TestSessionForkCheckOnly(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, bodies := forkCommandServer(t, false)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "fork", "demo-1", "--check")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "FORK_SOURCE_BUSY") || len(*bodies) != 0 {
		t.Fatalf("output = %q, forks = %v", out, *bodies)
	}
}

func TestSessionForkNeedsAnID(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "session", "fork")
	if ExitCode(err) != 2 {
		t.Fatalf("exit = %d, want usage error", ExitCode(err))
	}
}
