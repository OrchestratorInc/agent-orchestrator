package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type daemonCall struct{ method, path, query, body string }

type fakeDaemon struct {
	mu     sync.Mutex
	calls  []daemonCall
	routes map[string]func() (int, string)
}

func (d *fakeDaemon) seen() []daemonCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]daemonCall(nil), d.calls...)
}

func (d *fakeDaemon) last(t *testing.T, method, path string) daemonCall {
	t.Helper()
	calls := d.seen()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].method == method && calls[i].path == path {
			return calls[i]
		}
	}
	t.Fatalf("no %s %s among %+v", method, path, calls)
	return daemonCall{}
}

const bypassConversation = `{"settings":{"approvalMode":"bypass-permissions"}}`

func startFakeDaemon(t *testing.T, routes map[string]func() (int, string)) *fakeDaemon {
	t.Helper()
	daemon := &fakeDaemon{routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		daemon.mu.Lock()
		daemon.calls = append(daemon.calls, daemonCall{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
		daemon.mu.Unlock()
		respond, ok := daemon.routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"no route","code":"NOT_FOUND"}`)
			return
		}
		status, payload := respond()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("AO_SESSION_ID", "ao-1")
	return daemon
}

func fixed(status int, payload string) func() (int, string) {
	return func() (int, string) { return status, payload }
}

func callTool(t *testing.T, tool string, args map[string]any) (string, bool) {
	t.Helper()
	replies := runMCP(t, mcpCall(tool, args))
	if len(replies) != 1 || replies[0].Error != nil || len(replies[0].Result.Content) != 1 {
		t.Fatalf("reply = %+v", replies)
	}
	return replies[0].Result.Content[0].Text, replies[0].Result.IsError
}

func TestCatalogToolsAreListedWithValidSchemas(t *testing.T) {
	setConfigEnv(t)
	replies := runMCP(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	listed := map[string]bool{}
	for _, tool := range replies[0].Result.Tools {
		if listed[tool.Name] {
			t.Errorf("tool %q is listed twice", tool.Name)
		}
		listed[tool.Name] = true
	}
	for _, tool := range catalog {
		if !listed[tool.name] {
			t.Errorf("tool %q is not listed", tool.name)
		}
		for _, name := range tool.required {
			if _, ok := tool.props[name]; !ok {
				t.Errorf("tool %q requires %q, which its schema does not define", tool.name, name)
			}
		}
		if readOnly := tool.definition()["annotations"].(map[string]any)["readOnlyHint"]; readOnly != !tool.act {
			t.Errorf("tool %q readOnlyHint = %v, act = %v", tool.name, readOnly, tool.act)
		}
	}
	if !listed["html_render"] {
		t.Error("html_render is no longer listed")
	}
}

func TestReadToolCallsItsRouteAndTrimsTheAnswer(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions": fixed(200, `{"total":1,"sessions":[{"id":"ao-2","status":"working","activity":{"state":"active"},"artifactFiles":["big"],"provisionSteps":[1,2]}]}`),
	})
	text, isError := callTool(t, "session_list", map[string]any{"projectId": "proj", "active": true})
	if isError {
		t.Fatalf("session_list failed: %s", text)
	}
	if got := daemon.last(t, "GET", "/api/v1/sessions").query; got != "active=true&project=proj" {
		t.Errorf("query = %q", got)
	}
	if strings.Contains(text, "artifactFiles") || !strings.Contains(text, `"activity":"active"`) || !strings.Contains(text, `"id":"ao-2"`) {
		t.Errorf("answer = %s", text)
	}
	if len(daemon.seen()) != 1 {
		t.Errorf("a read tool checked the permission mode: %+v", daemon.seen())
	}
}

func TestActToolNeedsFullAccess(t *testing.T) {
	for _, tc := range []struct {
		name, conversation string
		wantSend           bool
	}{
		{"default mode is refused", `{"settings":{"approvalMode":"default"}}`, false},
		{"unset mode is refused", `{"settings":{}}`, false},
		{"full access runs", bypassConversation, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemon := startFakeDaemon(t, map[string]func() (int, string){
				"GET /api/v1/sessions/ao-1/conversation": fixed(200, tc.conversation),
				"POST /api/v1/sessions/ao-2/send":        fixed(200, `{"ok":true}`),
			})
			text, isError := callTool(t, "session_send", map[string]any{"sessionId": "ao-2", "message": "status?"})
			sent := false
			for _, call := range daemon.seen() {
				sent = sent || call.method == "POST"
			}
			if sent != tc.wantSend || isError == tc.wantSend {
				t.Fatalf("sent = %v, isError = %v, text = %s", sent, isError, text)
			}
			if !tc.wantSend && !strings.Contains(text, "full access") {
				t.Errorf("refusal does not say what to do: %s", text)
			}
			if tc.wantSend {
				if body := daemon.last(t, "POST", "/api/v1/sessions/ao-2/send").body; !strings.Contains(body, `"senderSessionId":"ao-1"`) || !strings.Contains(body, `"message":"status?"`) {
					t.Errorf("send body = %s", body)
				}
			}
		})
	}
}

func TestActToolIsRefusedOutsideASession(t *testing.T) {
	daemon := startFakeDaemon(t, nil)
	t.Setenv("AO_SESSION_ID", "")
	text, isError := callTool(t, "session_stop", map[string]any{"sessionId": "ao-2"})
	if !isError || len(daemon.seen()) != 0 {
		t.Fatalf("isError = %v, calls = %+v, text = %s", isError, daemon.seen(), text)
	}
}

func TestOwnSessionToolsNeedNoFullAccess(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"POST /api/v1/reports": fixed(200, `{"id":"rep-1"}`),
	})
	text, isError := callTool(t, "report", map[string]any{"state": "checkpoint", "note": "tests pass"})
	if isError {
		t.Fatalf("report failed: %s", text)
	}
	body := daemon.last(t, "POST", "/api/v1/reports").body
	if !strings.Contains(body, `"sessionId":"ao-1"`) || !strings.Contains(body, `"state":"checkpoint"`) || !strings.Contains(body, `"note":"tests pass"`) {
		t.Errorf("report body = %s", body)
	}
}

func TestSessionSpawnMirrorsTheCLIRequest(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/conversation": fixed(200, bypassConversation),
		"GET /api/v1/sessions/ao-1":              fixed(200, `{"session":{"id":"ao-1","projectId":"proj"}}`),
		"POST /api/v1/sessions":                  fixed(200, `{"session":{"id":"ao-9","status":"working","provisionState":"provisioning","prs":[]}}`),
	})
	text, isError := callTool(t, "session_spawn", map[string]any{"prompt": "fix the flaky test", "displayName": "Flaky test", "requestId": "req-1"})
	if isError {
		t.Fatalf("session_spawn failed: %s", text)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(daemon.last(t, "POST", "/api/v1/sessions").body), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"prompt": "fix the flaky test", "projectId": "proj", "parentSessionId": "ao-1", "async": true,
		"displayName": "Flaky test", "displayNameExplicit": true, "clientRequestId": "req-1",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("body[%q] = %v, want %v", key, body[key], value)
		}
	}
	if _, ok := body["approvalMode"]; ok {
		t.Errorf("spawn sent an approval mode: %v", body)
	}
	if !strings.Contains(text, `"id":"ao-9"`) || strings.Contains(text, "prs") {
		t.Errorf("answer = %s", text)
	}
}

func TestSessionWaitReturnsWhenEverySessionStops(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	reads := 0
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": func() (int, string) {
			reads++
			if reads < 3 {
				return 200, `{"session":{"id":"ao-2","provisionState":"ready","activity":{"state":"active"}}}`
			}
			return 200, `{"session":{"id":"ao-2","provisionState":"ready","status":"needs_input","activity":{"state":"waiting_input"}}}`
		},
	})
	text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2"}})
	if isError || !strings.Contains(text, `"timedOut":false`) || !strings.Contains(text, `"status":"needs_input"`) {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	if got := len(daemon.seen()); got != 4 {
		t.Errorf("reads = %d, want 4: two while it works, then two settled reads in a row", got)
	}
}

func TestSessionWaitTimesOutWithoutStoppingTheSession(t *testing.T) {
	waitPollInterval = 400 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": fixed(200, `{"session":{"id":"ao-2","provisionState":"provisioning"}}`),
	})
	text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2"}, "timeoutSec": 1})
	if isError || !strings.Contains(text, `"timedOut":true`) {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	for _, call := range daemon.seen() {
		if call.method != "GET" {
			t.Errorf("a timeout changed something: %+v", call)
		}
	}
}

func TestPendingToolsReadAndAnswerRequests(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/conversation": fixed(200, bypassConversation),
		"GET /api/v1/sessions/ao-2/conversation": fixed(200, `{"activities":[
			{"activityKind":"command","status":"completed","requestId":"r0"},
			{"activityKind":"approval","status":"pending","requestId":"r1","summary":"npm test","detail":{"decisions":[{"id":"allow_once"}]}},
			{"activityKind":"user_input","status":"resolved","requestId":"r2"}]}`),
		"POST /api/v1/sessions/ao-2/conversation/approvals/r1/resolve": fixed(204, ``),
		"POST /api/v1/sessions/ao-2/conversation/inputs/r3/resolve":    fixed(204, ``),
	})
	text, isError := callTool(t, "pending_list", map[string]any{"sessionId": "ao-2"})
	if isError || !strings.Contains(text, `"requestId":"r1"`) || !strings.Contains(text, "allow_once") || strings.Contains(text, "r0") || strings.Contains(text, "r2") {
		t.Fatalf("pending_list = %s", text)
	}
	if text, isError := callTool(t, "pending_respond", map[string]any{"sessionId": "ao-2", "requestId": "r1", "decisionId": "allow_once"}); isError {
		t.Fatalf("approval: %s", text)
	}
	if body := daemon.last(t, "POST", "/api/v1/sessions/ao-2/conversation/approvals/r1/resolve").body; body != `{"decisionId":"allow_once"}` {
		t.Errorf("approval body = %s", body)
	}
	if text, isError := callTool(t, "pending_respond", map[string]any{"sessionId": "ao-2", "requestId": "r3", "action": "accept", "content": map[string]any{"team": "platform"}}); isError {
		t.Fatalf("input: %s", text)
	}
	if body := daemon.last(t, "POST", "/api/v1/sessions/ao-2/conversation/inputs/r3/resolve").body; !strings.Contains(body, `"action":"accept"`) || !strings.Contains(body, `"team":"platform"`) {
		t.Errorf("input body = %s", body)
	}
}

func TestSessionPathDefaultsToThisSession(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/pr": fixed(200, `{"prs":[{"number":7,"title":"Fix","description":"long text","ci":{"state":"passing"}}]}`),
	})
	text, isError := callTool(t, "pr_status", nil)
	if isError || !strings.Contains(text, `"number":7`) || strings.Contains(text, "long text") {
		t.Fatalf("pr_status = %s", text)
	}
	daemon.last(t, "GET", "/api/v1/sessions/ao-1/pr")
}

func TestDaemonErrorsReachTheAgentAsToolErrors(t *testing.T) {
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/missing": fixed(404, `{"message":"session not found","code":"SESSION_NOT_FOUND","requestId":"req-9"}`),
	})
	text, isError := callTool(t, "session_get", map[string]any{"sessionId": "missing"})
	if !isError || !strings.Contains(text, "session not found") || !strings.Contains(text, "SESSION_NOT_FOUND") {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
}
