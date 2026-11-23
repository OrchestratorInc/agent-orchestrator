package cli

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/access"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func accessDaemon(t *testing.T) {
	t.Helper()
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	t.Setenv(agentTokenEnv, "")
	registry, err := access.LoadRegistry(access.TokensPath(cfg.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Access: registry}, httpd.ControlDeps{}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
}

func runAccess(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
	return out, err
}

func TestTokenLifecycleAndGrantThroughTheRealDaemon(t *testing.T) {
	accessDaemon(t)

	out, err := runAccess(t, "whoami")
	if err != nil || !strings.Contains(out, "user") || !strings.Contains(out, "no limit") {
		t.Fatalf("whoami as the user: %q %v", out, err)
	}

	out, err = runAccess(t, "token", "create", "api-bot", "--preset", "operate", "--project", "api", "--expires", "30d", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Secret string
		Token  struct {
			ID    string
			Grant struct {
				Preset    string
				Projects  []string
				ExpiresAt *time.Time
			}
		}
	}
	if json.Unmarshal([]byte(out), &created) != nil || !strings.HasPrefix(created.Secret, access.TokenPrefix) ||
		created.Token.Grant.Preset != "operate" || len(created.Token.Grant.Projects) != 1 || created.Token.Grant.ExpiresAt == nil {
		t.Fatalf("token create: %s", out)
	}
	if out, err = runAccess(t, "token", "ls"); err != nil || !strings.Contains(out, "api-bot") || strings.Contains(out, created.Secret) {
		t.Fatalf("token ls: %q %v", out, err)
	}
	if _, err = runAccess(t, "token", "create", "bad", "--preset", "read", "--group", "sessions.read"); ExitCode(err) != 2 {
		t.Errorf("--preset with --group: exit %d, want 2", ExitCode(err))
	}
	if _, err = runAccess(t, "token", "create", "bad", "--expires", "soon"); ExitCode(err) != 2 {
		t.Errorf("a bad --expires: exit %d, want 2", ExitCode(err))
	}

	t.Setenv(agentTokenEnv, created.Secret)
	if out, err = runAccess(t, "whoami"); err != nil || !strings.Contains(out, "token api-bot") || !strings.Contains(out, "projects: api") {
		t.Fatalf("whoami with the token: %q %v", out, err)
	}
	if _, err = runAccess(t, "token", "ls"); ExitCode(err) != exitRefused {
		t.Errorf("a token listing tokens: exit %d (%v), want %d", ExitCode(err), err, exitRefused)
	}
	if _, err = runAccess(t, "project", "ls"); ExitCode(err) != exitRefused || !strings.Contains(err.Error(), "limited to: api") {
		t.Errorf("a limited token listing every project: exit %d (%v)", ExitCode(err), err)
	}
	if _, err = runAccess(t, "call", "project.remove", "--input", `{"id":"api"}`); ExitCode(err) != exitRefused || !strings.Contains(err.Error(), "projects.manage") {
		t.Errorf("call above the grant: exit %d (%v)", ExitCode(err), err)
	}
	if _, err = runAccess(t, "call", "deleteProject"); ExitCode(err) != exitNotFound {
		t.Errorf("call of an unknown action: exit %d (%v), want %d", ExitCode(err), err, exitNotFound)
	}
	if _, err = runAccess(t, "call", "project.get"); ExitCode(err) != 2 {
		t.Errorf("call with a missing path field: exit %d (%v), want 2", ExitCode(err), err)
	}
	for _, tc := range [][2]string{
		{"automation.list", `null`}, {"project.get", `[]`}, {"project.get", `{"id":"api"`},
		{"project.get", `{"id":"api"} {"id":"web"}`}, {"project.get", `{"id":"api"} }`},
	} {
		if _, err = runAccess(t, "call", tc[0], "--input", tc[1]); ExitCode(err) != 2 {
			t.Errorf("call %s --input %s: exit %d (%v), want 2", tc[0], tc[1], ExitCode(err), err)
		}
	}
	var apiErr apiResponseError
	if _, err = runAccess(t, "call", "project.get", "--input", `{"id":"api"}`); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotImplemented {
		t.Errorf("an allowed call must reach its handler (501 with no project service), got %v", err)
	}
	if out, err = runAccess(t, "actions", "ls", "--group", "projects.manage"); err != nil || !strings.Contains(out, "project.remove") || !strings.Contains(out, "no") || strings.Contains(out, "session.list") {
		t.Errorf("actions ls --group: %q %v", out, err)
	}
	if out, err = runAccess(t, "actions", "show", "session.spawn"); err != nil || !strings.Contains(out, `"projectId"`) {
		t.Errorf("actions show: %q %v", out, err)
	}

	t.Setenv(agentTokenEnv, "")
	if _, err = runAccess(t, "token", "revoke", "api-bot"); err != nil {
		t.Fatal(err)
	}
	if _, err = runAccess(t, "token", "revoke", "api-bot"); err == nil {
		t.Error("revoking twice must fail")
	}
	t.Setenv(agentTokenEnv, created.Secret)
	if _, err = runAccess(t, "whoami"); ExitCode(err) != exitRefused || !strings.Contains(err.Error(), "AGENT_TOKEN_INVALID") {
		t.Errorf("a revoked token: exit %d (%v)", ExitCode(err), err)
	}
}

func TestActionRequestSplitsInput(t *testing.T) {
	spawn := accessActionDTO{ID: "session.send", Method: http.MethodPost, Path: "/api/v1/sessions/{sessionId}/send",
		Parameters: []map[string]any{{"name": "sessionId", "in": "path"}}, Body: map[string]any{"type": "object"}}
	path, body, err := actionRequest(spawn, map[string]any{"sessionId": "api 1", "message": "hi"})
	if err != nil || path != "/api/v1/sessions/api%201/send" {
		t.Fatalf("path %q err %v", path, err)
	}
	if fields, _ := body.(map[string]any); len(fields) != 1 || fields["message"] != "hi" {
		t.Errorf("body %v", body)
	}

	list := accessActionDTO{ID: "session.list", Method: http.MethodGet, Path: "/api/v1/sessions",
		Parameters: []map[string]any{{"name": "project", "in": "query"}, {"name": "active", "in": "query"}}}
	path, body, err = actionRequest(list, map[string]any{"project": "api", "active": true})
	if err != nil || path != "/api/v1/sessions?active=true&project=api" || body != nil {
		t.Errorf("list: %q %v %v", path, body, err)
	}
	if _, _, err = actionRequest(list, map[string]any{"projct": "api"}); ExitCode(err) != 2 {
		t.Errorf("an unknown field on a GET: %v", err)
	}

	interrupt := accessActionDTO{ID: "session.interrupt", Method: http.MethodPost, Path: "/api/v1/sessions/{sessionId}/stop-turn",
		Parameters: []map[string]any{{"name": "sessionId", "in": "path"}}}
	if _, body, err = actionRequest(interrupt, map[string]any{"sessionId": "api-1"}); err != nil || body == nil {
		t.Errorf("a POST with no body schema must still send an empty object: %v %v", body, err)
	}
}

func TestSessionWaitExitCodes(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	states := map[string][]string{"api-1": {"active", "active", "waiting_input"}, "api-2": {"active", "idle"}, "api-3": {"active"}, "fresh": {"idle"}, "broken": {"idle"}}
	polls := map[string]int{}
	created := time.Date(2026, 11, 23, 11, 59, 50, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
		if session, isHistory := strings.CutSuffix(id, "/history"); isHistory {
			total := 1
			if session == "fresh" {
				total = 0
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"turns": []any{}, "total": total})
			return
		}
		sequence, ok := states[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		state := sequence[min(polls[id], len(sequence)-1)]
		polls[id]++
		session := map[string]any{"id": id, "status": "working", "createdAt": created, "activity": map[string]any{"state": state}}
		if id == "broken" {
			session["provisionState"] = "failed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session": session})
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	clock := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	deps := func() Deps {
		return Deps{
			ProcessAlive: func(int) bool { return true },
			Now:          func() time.Time { return clock },
			Sleep:        func(d time.Duration) { clock = clock.Add(d) },
		}
	}
	out, _, err := executeCLI(t, deps(), "session", "wait", "api-1", "--json")
	var result sessionWaitResult
	if ExitCode(err) != exitNeedsInput || json.Unmarshal([]byte(out), &result) != nil || result.State != "needs_input" || result.WaitedSeconds != 4 {
		t.Errorf("a session that asks: exit %d, out %s", ExitCode(err), out)
	}
	if out, _, err = executeCLI(t, deps(), "session", "wait", "api-3", "api-2"); err != nil || !strings.Contains(out, "api-2: idle") {
		t.Errorf("the first settled session must win: %q %v", out, err)
	}
	if out, _, err = executeCLI(t, deps(), "session", "wait", "api-3", "--timeout", "6s"); ExitCode(err) != exitTimedOut || !strings.Contains(out, "still working") {
		t.Errorf("a timeout: exit %d, out %q", ExitCode(err), out)
	}
	clock = created.Add(10 * time.Second)
	if out, _, err = executeCLI(t, deps(), "session", "wait", "fresh", "--json"); err != nil || json.Unmarshal([]byte(out), &result) != nil || result.State != "idle" || result.WaitedSeconds != 50 {
		t.Errorf("a session that never started must be given the startup window: %s %v", out, err)
	}
	clock = created.Add(time.Hour)
	if out, _, err = executeCLI(t, deps(), "session", "wait", "api-3", "--timeout", "3s", "--interval", "1h", "--json"); ExitCode(err) != exitTimedOut ||
		json.Unmarshal([]byte(out), &result) != nil || !result.TimedOut || result.WaitedSeconds != 3 {
		t.Errorf("an interval above the timeout must not outlast it: exit %d, out %s", ExitCode(err), out)
	}
	if out, _, err = executeCLI(t, deps(), "session", "wait", "broken"); ExitCode(err) != 1 || !strings.Contains(out, "broken: failed") {
		t.Errorf("a session that failed to start: exit %d, out %q, want exit 1", ExitCode(err), out)
	}
	if _, _, err = executeCLI(t, deps(), "session", "wait"); ExitCode(err) != 2 {
		t.Errorf("no session id: exit %d, want 2", ExitCode(err))
	}
}

func TestSendWaitIgnoresTheStateFromBeforeTheMessage(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	created := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	idleSince, picksUp, finishes := created.Add(time.Hour), created.Add(3*time.Hour+3*time.Second), created.Add(3*time.Hour+5*time.Second)
	type observed struct {
		state string
		at    time.Time
	}
	afterSend := []observed{{"idle", idleSince}, {"idle", idleSince}, {"active", picksUp}, {"idle", finishes}}
	sent, polls, daemonFails := false, 0, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/sessions/api-1/send":
			sent = true
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/sessions/api-1":
			if sent && daemonFails {
				http.Error(w, `{"message":"storage is busy","code":"STORAGE_BUSY"}`, http.StatusInternalServerError)
				return
			}
			now := observed{"idle", idleSince}
			if sent {
				now = afterSend[min(polls, len(afterSend)-1)]
				polls++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"session": map[string]any{
				"id": "api-1", "status": "idle", "createdAt": created,
				"activity": map[string]any{"state": now.state, "lastActivityAt": now.at},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	clock := created.Add(3 * time.Hour)
	deps := Deps{
		ProcessAlive: func(int) bool { return true },
		Now:          func() time.Time { return clock },
		Sleep:        func(d time.Duration) { clock = clock.Add(d) },
	}

	if out, _, err := executeCLI(t, deps, "session", "wait", "api-1"); err != nil || !strings.Contains(out, "api-1: idle after 0s") {
		t.Fatalf("a plain wait settles on the current state: %q %v", out, err)
	}
	for _, timeout := range []string{"0s", "-5s"} {
		if _, _, err := executeCLI(t, deps, "send", "--session", "api-1", "--message", "run the tests", "--wait", "--timeout", timeout); ExitCode(err) != 2 || sent {
			t.Fatalf("--wait --timeout %s must be refused before the message is sent: exit %d, sent %v", timeout, ExitCode(err), sent)
		}
	}
	out, _, err := executeCLI(t, deps, "send", "--session", "api-1", "--message", "run the tests", "--wait")
	if err != nil || !sent || !strings.Contains(out, "api-1: idle after 6s") {
		t.Errorf("send --wait must wait for the turn the message starts: sent %v, out %q, err %v", sent, out, err)
	}
	if polls != len(afterSend) {
		t.Errorf("polls after the send = %d, want %d: the idle state from before the message must not count", polls, len(afterSend))
	}
	sent, daemonFails = false, true
	if _, _, err := executeCLI(t, deps, "send", "--session", "api-1", "--message", "run the tests", "--wait"); ExitCode(err) != 1 || !sent ||
		!strings.Contains(err.Error(), "the message was sent") || !strings.Contains(err.Error(), "STORAGE_BUSY") {
		t.Errorf("a failure after delivery must say the message was sent: sent %v, exit %d, err %v", sent, ExitCode(err), err)
	}
	if _, _, err := executeCLI(t, deps, "send", "--session", "api-1", "--steer", "--recover-only", "--client-message-id", "m-1", "--wait"); ExitCode(err) != 2 {
		t.Errorf("--wait with --recover-only: exit %d, want 2", ExitCode(err))
	}
}
