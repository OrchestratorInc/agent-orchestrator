package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type switchCommandCalls struct {
	switches     []map[string]any
	sends        []string
	busySends    int
	refusedSends int
}

func switchCommandServer(t *testing.T, status int, code string) (*httptest.Server, *switchCommandCalls) {
	t.Helper()
	calls := &switchCommandCalls{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/provider-switch":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			calls.switches = append(calls.switches, in)
			if code != "" {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"conflict","code":"` + code + `","message":"refused"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/send":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if calls.refusedSends < calls.busySends {
				calls.refusedSends++
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"conflict","code":"SESSION_COMPOSER_BUSY","message":"Session composer has an unsent draft"}`))
				return
			}
			message, _ := in["message"].(string)
			calls.sends = append(calls.sends, message)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func TestSessionSwitchForcedWithMessage(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "switch", "demo-1", "--agent", "codex", "--model", "gpt-6.1-sol", "--force", "-m", "Claude is down. Take over.")
	if err != nil {
		t.Fatalf("session switch failed: %v\nstderr=%s", err, errOut)
	}
	if len(calls.switches) != 1 {
		t.Fatalf("switch calls = %v", calls.switches)
	}
	body := calls.switches[0]
	if body["targetHarness"] != "codex" || body["model"] != "gpt-6.1-sol" || body["force"] != true || body["idempotencyKey"] == "" || body["message"] != nil {
		t.Fatalf("switch body = %v", body)
	}
	if len(calls.sends) != 1 || !strings.HasSuffix(calls.sends[0], "Claude is down. Take over.") {
		t.Fatalf("sent messages = %q", calls.sends)
	}
	if !strings.Contains(out, "switched demo-1 to codex") || !strings.Contains(out, "sent message to demo-1") {
		t.Fatalf("output = %q", out)
	}
}

func TestSessionSwitchWaitsForTheNewAgentToTakeTheMessage(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, 0, "")
	calls.busySends = 2
	writeRunFileFor(t, cfg, srv)
	sleeps := 0
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, Sleep: func(time.Duration) { sleeps++ }},
		"session", "switch", "demo-1", "--agent", "codex", "-m", "take over")
	if err != nil || len(calls.sends) != 1 || sleeps != 2 {
		t.Fatalf("err = %v sends = %q sleeps = %d", err, calls.sends, sleeps)
	}
}

func TestSessionSwitchToTheRunningAgentSucceeds(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := switchCommandServer(t, http.StatusConflict, "PROVIDER_SWITCH_already_active")
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "--agent", "codex")
	if err != nil || !strings.Contains(out, "demo-1 is already on codex") {
		t.Fatalf("out = %q err = %v", out, err)
	}
}

func TestSessionSwitchBusyPointsAtForce(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, calls := switchCommandServer(t, http.StatusConflict, "PROVIDER_SWITCH_busy")
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "switch", "demo-1", "--agent", "codex", "-m", "hi")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v exit = %d", err, ExitCode(err))
	}
	if len(calls.sends) != 0 {
		t.Fatalf("a refused switch sent %q", calls.sends)
	}
}

func TestSessionSwitchNeedsAnAgent(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "session", "switch", "demo-1")
	if ExitCode(err) != 2 {
		t.Fatalf("err = %v exit = %d", err, ExitCode(err))
	}
}

func TestSessionListFiltersByAgentQuietly(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/sessions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"sessions":[
			{"id":"demo-2","projectId":"demo","kind":"worker","harness":"claude-code","activity":{"state":"active"}},
			{"id":"demo-1","projectId":"demo","kind":"worker","harness":"claude-code","activity":{"state":"idle"}},
			{"id":"demo-3","projectId":"demo","kind":"worker","harness":"codex","activity":{"state":"idle"}}]}`))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "ls", "--agent", "claude-code", "-q")
	if err != nil || out != "demo-1\ndemo-2\n" {
		t.Fatalf("out = %q err = %v", out, err)
	}
}
