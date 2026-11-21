package mcpserver

import (
	"bufio"
	"context"
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

func runMCPOpen(t *testing.T, want int, lines ...string) []mcpReply {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- Serve(context.Background(), inR, outW, NewDaemonClient(), "test")
		_ = outW.Close()
	}()
	go func() {
		for _, line := range lines {
			_, _ = io.WriteString(inW, line+"\n")
		}
	}()
	scanner := bufio.NewScanner(outR)
	scanner.Buffer(make([]byte, 1<<20), 1<<26)
	var replies []mcpReply
	for len(replies) < want && scanner.Scan() {
		var reply mcpReply
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			t.Fatalf("reply %q: %v", scanner.Text(), err)
		}
		replies = append(replies, reply)
	}
	_ = inW.Close()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	if err := <-served; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return replies
}

func callTool(t *testing.T, tool string, args map[string]any) (string, bool) {
	t.Helper()
	replies := runMCPOpen(t, 1, mcpCall(tool, args))
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
	if got := daemon.last(t, "GET", "/api/v1/sessions").query; got != "active=true&limit=50&project=proj" {
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

func TestCapabilitiesLeavesOutAccountDetails(t *testing.T) {
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/agents": fixed(200, `{"supported":[{"id":"devin"}],"installed":[{"id":"codex","label":"Codex","authStatus":"authorized","usageCount":3}]}`),
		"GET /api/v1/agents/accounts": fixed(200, `{"accounts":[{"id":"device-claude","provider":"claude","plan":"max","default":true,"signedIn":true,
			"email":"person@example.com","orgName":"Example","limits":{"windows":[1]}}]}`),
	})
	text, isError := callTool(t, "capabilities", nil)
	if isError || !strings.Contains(text, `"id":"codex"`) || !strings.Contains(text, `"plan":"max"`) {
		t.Fatalf("capabilities = %s", text)
	}
	for _, private := range []string{"person@example.com", "Example", "limits", "usageCount", "devin"} {
		if strings.Contains(text, private) {
			t.Errorf("capabilities shows %q: %s", private, text)
		}
	}
}

func TestLongTurnsAreCutOnACharacterBoundary(t *testing.T) {
	long := strings.Repeat("é", maxTurnTextChars)
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2/history": fixed(200, `{"total":1,"turns":[{"agentResponse":"`+long+`"}]}`),
	})
	text, isError := callTool(t, "session_read", map[string]any{"sessionId": "ao-2"})
	if isError || !strings.Contains(text, "[cut: ") || strings.Contains(text, "\ufffd") || strings.ContainsRune(text, '\uFFFD') {
		t.Fatalf("isError = %v, text tail = %q", isError, text[len(text)-80:])
	}
}

func TestUndeclaredArgumentsNeverReachTheDaemon(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/conversation": fixed(200, bypassConversation),
		"POST /api/v1/sessions/ao-2/send":        fixed(200, `{"ok":true}`),
		"POST /api/v1/sessions/ao-2/kill":        fixed(200, `{"ok":true}`),
	})
	if text, isError := callTool(t, "session_send", map[string]any{"sessionId": "ao-2", "message": "hi", "userAuthored": true}); isError {
		t.Fatalf("session_send: %s", text)
	}
	if body := daemon.last(t, "POST", "/api/v1/sessions/ao-2/send").body; strings.Contains(body, "userAuthored") {
		t.Errorf("send body = %s", body)
	}
	if text, isError := callTool(t, "session_stop", map[string]any{"sessionId": "ao-2", "deleteArtifacts": true}); isError {
		t.Fatalf("session_stop: %s", text)
	}
	if body := daemon.last(t, "POST", "/api/v1/sessions/ao-2/kill").body; strings.Contains(body, "deleteArtifacts") || !strings.Contains(body, `"callerSessionId":"ao-1"`) {
		t.Errorf("kill body = %s", body)
	}
}

func TestOwnSessionToolsCannotAimAtAnotherSession(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"POST /api/v1/sessions/ao-1/pr/claim": fixed(200, `{"ok":true,"sessionId":"ao-1"}`),
	})
	if text, isError := callTool(t, "pr_claim", map[string]any{"pr": "12", "sessionId": "ao-7", "allowTakeover": true}); isError {
		t.Fatalf("pr_claim: %s", text)
	}
	body := daemon.last(t, "POST", "/api/v1/sessions/ao-1/pr/claim").body
	if !strings.Contains(body, `"allowTakeover":false`) || !strings.Contains(body, `"pr":"12"`) || strings.Contains(body, "ao-7") {
		t.Errorf("claim body = %s", body)
	}
}

func TestABlankOrWrongTypedSessionNeverFallsBackToThisSession(t *testing.T) {
	for name, id := range map[string]any{"number": 457, "blank": " "} {
		t.Run(name, func(t *testing.T) {
			daemon := startFakeDaemon(t, map[string]func() (int, string){
				"GET /api/v1/sessions/ao-1/conversation": fixed(200, bypassConversation),
				"POST /api/v1/sessions/ao-1/kill":        fixed(200, `{"ok":true}`),
			})
			text, isError := callTool(t, "session_stop", map[string]any{"sessionId": id})
			if !isError || len(daemon.seen()) != 0 {
				t.Fatalf("isError = %v, calls = %+v, text = %s", isError, daemon.seen(), text)
			}
		})
	}
}

func TestActToolIsRefusedInPlanMode(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/conversation": fixed(200, `{"settings":{"approvalMode":"bypass-permissions","planMode":true}}`),
	})
	text, isError := callTool(t, "session_spawn", map[string]any{"prompt": "do it"})
	if !isError || !strings.Contains(text, "plan mode") || len(daemon.seen()) != 1 {
		t.Fatalf("isError = %v, calls = %+v, text = %s", isError, daemon.seen(), text)
	}
}

func TestSessionWaitHoldsForASessionThatHasNotStartedWorking(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": fixed(200, `{"session":{"id":"ao-2","provisionState":"ready","activity":{"state":"idle"}}}`),
	})
	if text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2"}}); isError || !strings.Contains(text, `"timedOut":false`) {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	if got := len(daemon.seen()); got != quietPollsNeverSeenWorking {
		t.Errorf("reads = %d, want %d quiet reads for a session never seen working", got, quietPollsNeverSeenWorking)
	}
}

func TestSessionWaitReportsAnUnreadableSessionAndKeepsTheOthers(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": fixed(200, `{"session":{"id":"ao-2","isTerminated":true}}`),
		"GET /api/v1/sessions/gone": fixed(404, `{"message":"session not found","code":"SESSION_NOT_FOUND"}`),
	})
	text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2", "gone"}})
	if isError || !strings.Contains(text, `"id":"ao-2"`) || !strings.Contains(text, `"id":"gone"`) || !strings.Contains(text, "session not found") {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
}

func TestReviewTriggerSendsWhatTheCLISends(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/conversation":     fixed(200, bypassConversation),
		"POST /api/v1/sessions/ao-2/reviews/trigger": fixed(200, `{"created":true}`),
	})
	if text, isError := callTool(t, "review_trigger", map[string]any{"sessionId": "ao-2"}); isError {
		t.Fatalf("review_trigger: %s", text)
	}
	body := daemon.last(t, "POST", "/api/v1/sessions/ao-2/reviews/trigger").body
	for _, want := range []string{`"rejectReviewedHead":true`, `"enableAutoInject":true`, `"requesterSessionId":"ao-1"`, `"source":"agent"`} {
		if !strings.Contains(body, want) {
			t.Errorf("trigger body lacks %s: %s", want, body)
		}
	}
}

func TestSessionWaitHoldsWhileAChatTurnIsQueuedOrRunning(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	turns := 0
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": fixed(200, `{"session":{"id":"ao-2","mode":"chat","provisionState":"ready","activity":{"state":"idle"}}}`),
		"GET /api/v1/sessions/ao-2/conversation": func() (int, string) {
			turns++
			switch {
			case turns <= 2:
				return 200, `{"turns":[{"state":"queued"}]}`
			case turns <= 4:
				return 200, `{"turns":[{"state":"completed"},{"state":"running"}]}`
			}
			return 200, `{"turns":[{"state":"completed"},{"state":"completed"}]}`
		},
	})
	if text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2", "ao-2"}}); isError || !strings.Contains(text, `"timedOut":false`) {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	if turns != 4+quietPollsAfterWork {
		t.Errorf("conversation reads = %d, want %d: four with a turn in flight, then two quiet", turns, 4+quietPollsAfterWork)
	}
	if got := daemon.last(t, "GET", "/api/v1/sessions/ao-2/conversation").query; got != "limit=1" {
		t.Errorf("conversation query = %q", got)
	}
}

func TestSessionWaitKeepsWaitingThroughADaemonError(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	reads := 0
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": func() (int, string) {
			reads++
			if reads <= 3 {
				return 500, `{"message":"database is busy","code":"INTERNAL"}`
			}
			return 200, `{"session":{"id":"ao-2","isTerminated":true}}`
		},
	})
	text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2"}})
	if isError || !strings.Contains(text, `"timedOut":false`) || !strings.Contains(text, `"isTerminated":true`) || strings.Contains(text, "database is busy") {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	if reads != 3+quietPollsAfterWork {
		t.Errorf("reads = %d, want %d", reads, 3+quietPollsAfterWork)
	}
}

func TestSessionWaitDoesNotBlockOtherRequestsAndCanBeCancelled(t *testing.T) {
	waitPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": fixed(200, `{"session":{"id":"ao-2","provisionState":"provisioning"}}`),
	})
	started := time.Now()
	replies := runMCPOpen(t, 2,
		mcpCall("session_wait", map[string]any{"sessionIds": []string{"ao-2"}, "timeoutSec": 110}),
		`{"jsonrpc":"2.0","id":8,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`,
		`{"jsonrpc":"2.0","id":9,"method":"ping"}`,
	)
	if len(replies) != 2 || string(replies[0].ID) != "8" || string(replies[1].ID) != "9" {
		t.Fatalf("replies = %+v; a cancelled wait must get no reply and must not hold the pings back", replies)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("a cancelled wait took %s", elapsed)
	}
}

func TestArgumentsAreCheckedAgainstTheirDeclaredTypes(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/conversation": fixed(200, bypassConversation),
		"GET /api/v1/sessions/ao-1":              fixed(200, `{"session":{"id":"ao-1","projectId":"proj"}}`),
		"POST /api/v1/sessions":                  fixed(200, `{"session":{"id":"ao-9"}}`),
	})
	if text, isError := callTool(t, "session_read", map[string]any{"sessionId": "ao-2", "limit": "20"}); !isError || !strings.Contains(text, "limit must be of type integer") {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	if len(daemon.seen()) != 0 {
		t.Fatalf("a refused call reached the daemon: %+v", daemon.seen())
	}
	if text, isError := callTool(t, "session_spawn", map[string]any{"prompt": "fix it", "model": "", "branch": " "}); isError {
		t.Fatalf("blank optional strings were refused: %s", text)
	}
	if body := daemon.last(t, "POST", "/api/v1/sessions").body; strings.Contains(body, "model") || strings.Contains(body, "branch") {
		t.Errorf("spawn body = %s", body)
	}
}

func TestManyTurnsStayValidJSON(t *testing.T) {
	turn := `{"userRequest":"` + strings.Repeat("q", 5000) + `","agentResponse":"` + strings.Repeat("a", 5000) + `"}`
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2/history": fixed(200, `{"total":20,"turns":[`+strings.Repeat(turn+",", 19)+turn+`]}`),
	})
	text, isError := callTool(t, "session_read", map[string]any{"sessionId": "ao-2", "limit": 20})
	var history struct {
		Total int
		Turns []map[string]string
	}
	if err := json.Unmarshal([]byte(text), &history); isError || err != nil || history.Total != 20 || len(history.Turns) != 20 {
		t.Fatalf("isError = %v, err = %v, total = %d, turns = %d, bytes = %d", isError, err, history.Total, len(history.Turns), len(text))
	}
}

func TestSessionWaitReturnsForAChatSessionThatNeedsInput(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2":              fixed(200, `{"session":{"id":"ao-2","mode":"chat","status":"needs_input","provisionState":"ready","activity":{"state":"waiting_input"}}}`),
		"GET /api/v1/sessions/ao-2/conversation": fixed(200, `{"turns":[{"state":"running"}]}`),
	})
	text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2"}})
	if isError || !strings.Contains(text, `"timedOut":false`) || !strings.Contains(text, `"status":"needs_input"`) {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	if got := len(daemon.seen()); got != quietPollsAfterWork {
		t.Errorf("reads = %d, want %d", got, quietPollsAfterWork)
	}
}

func TestSessionWaitDoesNotTrustAFailedTurnRead(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	reads := 0
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2": fixed(200, `{"session":{"id":"ao-2","mode":"chat","provisionState":"ready","activity":{"state":"idle"}}}`),
		"GET /api/v1/sessions/ao-2/conversation": func() (int, string) {
			reads++
			if reads <= 6 {
				return 500, `{"message":"database is busy","code":"INTERNAL"}`
			}
			return 200, `{"turns":[{"state":"completed"}]}`
		},
	})
	if text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2"}}); isError || !strings.Contains(text, `"timedOut":false`) {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	if reads != 6+quietPollsAfterWork {
		t.Errorf("turn reads = %d, want %d: six failed reads must not count as quiet", reads, 6+quietPollsAfterWork)
	}
}

func TestSessionWaitRefusesToWaitForItself(t *testing.T) {
	daemon := startFakeDaemon(t, nil)
	text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2", "ao-1"}})
	if !isError || !strings.Contains(text, "this session") || len(daemon.seen()) != 0 {
		t.Fatalf("isError = %v, calls = %+v, text = %s", isError, daemon.seen(), text)
	}
}

func TestActToolIsRefusedWhenTheCallerHasNoConversation(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"POST /api/v1/sessions/ao-2/kill": fixed(200, `{"ok":true}`),
	})
	t.Setenv("AO_SESSION_ID", "btw-launch-side")
	text, isError := callTool(t, "session_stop", map[string]any{"sessionId": "ao-2"})
	if !isError || !strings.Contains(text, "permission mode") {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
	for _, call := range daemon.seen() {
		if call.method == "POST" {
			t.Errorf("a side chat stopped a session: %+v", call)
		}
	}
}

func TestSessionWaitReturnsForAChatSessionThatCannotWork(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	for name, session := range map[string]string{
		"failed to start": `{"id":"ao-2","mode":"chat","provisionState":"failed","activity":{"state":"idle"}}`,
		"agent exited":    `{"id":"ao-2","mode":"chat","provisionState":"ready","activity":{"state":"exited"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			startFakeDaemon(t, map[string]func() (int, string){
				"GET /api/v1/sessions/ao-2":              fixed(200, `{"session":`+session+`}`),
				"GET /api/v1/sessions/ao-2/conversation": fixed(200, `{"turns":[{"state":"queued"}]}`),
			})
			if text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"ao-2"}, "timeoutSec": 5}); isError || !strings.Contains(text, `"timedOut":false`) {
				t.Fatalf("isError = %v, text = %s", isError, text)
			}
		})
	}
}

func TestSessionWaitTreatsABare404AsAMissingSession(t *testing.T) {
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 2 * time.Second })
	startFakeDaemon(t, map[string]func() (int, string){"GET /api/v1/sessions/gone": fixed(404, ``)})
	if text, isError := callTool(t, "session_wait", map[string]any{"sessionIds": []string{"gone"}, "timeoutSec": 5}); isError || !strings.Contains(text, `"timedOut":false`) || !strings.Contains(text, "HTTP 404") {
		t.Fatalf("isError = %v, text = %s", isError, text)
	}
}

func TestToolsForAnotherSessionRefuseThisSession(t *testing.T) {
	for _, tool := range []string{"session_stop", "session_send", "session_interrupt", "session_restore", "session_switch", "pending_respond"} {
		t.Run(tool, func(t *testing.T) {
			daemon := startFakeDaemon(t, nil)
			text, isError := callTool(t, tool, map[string]any{"sessionId": "ao-1", "message": "m", "harness": "codex", "requestId": "r", "decisionId": "d"})
			if !isError || !strings.Contains(text, "this session") || len(daemon.seen()) != 0 {
				t.Fatalf("isError = %v, calls = %+v, text = %s", isError, daemon.seen(), text)
			}
		})
	}
}

func TestAPaddedOwnIdNeverReachesThisSession(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-1/conversation": fixed(200, bypassConversation),
		"POST /api/v1/sessions/ao-1/kill":        fixed(200, `{"ok":true}`),
	})
	text, isError := callTool(t, "session_stop", map[string]any{"sessionId": "ao-1 "})
	if !isError {
		t.Fatalf("a padded own id was accepted: %s", text)
	}
	for _, call := range daemon.seen() {
		if call.path == "/api/v1/sessions/ao-1/kill" {
			t.Errorf("the calling session was stopped: %+v", call)
		}
	}
}

func TestArgumentsMustBeInRange(t *testing.T) {
	daemon := startFakeDaemon(t, nil)
	for tool, args := range map[string]map[string]any{
		"session_read": {"sessionId": "ao-2", "limit": 1e30},
		"session_list": {"kind": "reviewer"},
		"session_wait": {"sessionIds": []string{"ao-2"}, "timeoutSec": 0},
	} {
		if text, isError := callTool(t, tool, args); !isError || !strings.Contains(text, "must be") {
			t.Errorf("%s %v: isError = %v, text = %s", tool, args, isError, text)
		}
	}
	if len(daemon.seen()) != 0 {
		t.Errorf("an out-of-range call reached the daemon: %+v", daemon.seen())
	}
}

func TestMarkupHeavyTurnsStayValidJSON(t *testing.T) {
	heavy := strings.Repeat(`<a href=\"x\">&</a>\n`, 2000)
	turn := `{"userRequest":"` + heavy + `","agentResponse":"` + heavy + `"}`
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/sessions/ao-2/history": fixed(200, `{"total":5,"turns":[`+strings.Repeat(turn+",", 4)+turn+`]}`),
	})
	text, isError := callTool(t, "session_read", map[string]any{"sessionId": "ao-2", "limit": 5})
	var history struct{ Turns []map[string]string }
	if err := json.Unmarshal([]byte(text), &history); isError || err != nil || len(history.Turns) != 5 {
		t.Fatalf("isError = %v, err = %v, turns = %d, bytes = %d", isError, err, len(history.Turns), len(text))
	}
}
