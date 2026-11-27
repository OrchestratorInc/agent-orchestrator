package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseTaskID(t *testing.T) {
	cases := []struct {
		raw     string
		want    taskID
		wantErr bool
	}{
		{raw: "repo-1", want: taskID{session: "repo-1"}},
		{raw: " repo-2:7a1f-turn ", want: taskID{session: "repo-2", turn: "7a1f-turn"}},
		{raw: "repo-2:a:b", want: taskID{session: "repo-2", turn: "a:b"}},
		{raw: "", wantErr: true},
		{raw: "bad id", wantErr: true},
		{raw: "repo-2:", wantErr: true},
		{raw: ":turn", wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseTaskID(tc.raw)
		if tc.wantErr {
			var ue usageError
			if !errors.As(err, &ue) {
				t.Errorf("parseTaskID(%q) err = %v, want usage error", tc.raw, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseTaskID(%q) = %+v, %v; want %+v", tc.raw, got, err, tc.want)
		}
		if got.String() != strings.TrimSpace(tc.raw) {
			t.Errorf("taskID(%q).String() = %q", tc.raw, got.String())
		}
	}
}

func decodeTaskConversation(t *testing.T, raw string) *taskConversation {
	t.Helper()
	var conv taskConversation
	if err := json.Unmarshal([]byte(raw), &conv); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	return &conv
}

func TestEvaluateTask(t *testing.T) {
	chat := taskSession{ID: "repo-1", Mode: "chat", ProvisionState: "ready"}
	tui := func(activity string) taskSession {
		s := taskSession{ID: "repo-1", Mode: "tui", ProvisionState: "ready"}
		s.Activity.State = activity
		return s
	}
	blocked := chat
	blocked.Activity.State = "blocked"

	cases := []struct {
		name        string
		sess        taskSession
		conv        string
		turn        string
		wantStatus  string
		wantReason  string
		wantCode    string
		wantSpawned []string
	}{
		{name: "archived", sess: taskSession{ID: "repo-1", IsTerminated: true}, wantStatus: taskStatusFailed, wantReason: "archived"},
		{name: "provision failed", sess: taskSession{ID: "repo-1", ProvisionState: "failed", ProvisionError: "default branch is unresolved"}, wantStatus: taskStatusFailed, wantReason: "default branch is unresolved"},
		{name: "provisioning", sess: taskSession{ID: "repo-1", ProvisionState: "provisioning"}, wantStatus: taskStatusStarting},
		{name: "tui active", sess: tui("active"), wantStatus: taskStatusRunning},
		{name: "tui blocked", sess: tui("blocked"), wantStatus: taskStatusNeedsInput},
		// At its prompt before and after the task looks the same, so never "done".
		{name: "tui at its prompt", sess: tui("waiting_input"), wantStatus: taskStatusStarting},
		{name: "tui exited", sess: tui("exited"), wantStatus: taskStatusFailed},
		{name: "tui idle", sess: tui("idle"), wantStatus: taskStatusStarting},
		{name: "no turns yet", sess: chat, conv: `{"turns":[]}`, wantStatus: taskStatusStarting},
		{name: "first turn queued", sess: chat, conv: `{"turns":[{"id":"t1","state":"queued"}]}`, wantStatus: taskStatusQueued},
		{name: "first turn running", sess: chat, conv: `{"turns":[{"id":"t1","state":"running"}]}`, wantStatus: taskStatusRunning},
		{name: "first turn completed", sess: chat, conv: `{"turns":[{"id":"t1","state":"completed"}]}`, wantStatus: taskStatusDone},
		{
			name: "signed out", sess: chat,
			conv:       `{"turns":[{"id":"t1","state":"failed","failureCode":"auth_required","errorMessage":"Not logged in · Please run /login"}]}`,
			wantStatus: taskStatusFailed, wantReason: "Not logged in · Please run /login (auth_required)", wantCode: "auth_required",
		},
		{name: "provider key missing", sess: chat, conv: `{"turns":[{"id":"t1","state":"failed","failureCode":"provider_key_missing"}]}`, wantStatus: taskStatusFailed, wantCode: "provider_key_missing"},
		{name: "interrupted", sess: chat, conv: `{"turns":[{"id":"t1","state":"interrupted"}]}`, wantStatus: taskStatusFailed, wantReason: "interrupted"},
		{name: "cancelled", sess: chat, conv: `{"turns":[{"id":"t1","state":"cancelled"}]}`, wantStatus: taskStatusFailed, wantReason: "cancelled"},
		{
			name: "re-auth event before any turn", sess: chat,
			conv:       `{"turns":[],"activities":[{"activityKind":"system","status":"completed","detail":{"event":"auth.reauth_required"}}]}`,
			wantStatus: taskStatusFailed, wantCode: "auth_required",
		},
		{
			name: "retried turn follows the retry", sess: chat,
			conv:       `{"turns":[{"id":"t1","state":"failed","failureCode":"auth_required"},{"id":"t2","state":"running","retryOfTurnId":"t1"}]}`,
			wantStatus: taskStatusRunning,
		},
		{
			name: "pending approval", sess: chat,
			conv:       `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`,
			wantStatus: taskStatusNeedsInput,
		},
		// The session state is read before the conversation and may describe an
		// earlier turn, so a chat task relies on its own turn's pending activity.
		{name: "session blocked without a pending activity on the turn", sess: blocked, conv: `{"turns":[{"id":"t1","state":"running"}]}`, wantStatus: taskStatusRunning},
		{
			name: "a failed attempt's stale approval does not block its retry", sess: chat, turn: "t1",
			conv:       `{"turns":[{"id":"t1","state":"failed"},{"id":"t2","state":"running","retryOfTurnId":"t1"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`,
			wantStatus: taskStatusRunning,
		},
		{
			name: "a later turn's approval does not reopen a finished task", sess: blocked, turn: "t1",
			conv:       `{"turns":[{"id":"t1","state":"completed"},{"id":"t2","state":"running"}],"activities":[{"turnId":"t2","activityKind":"approval","status":"pending"}]}`,
			wantStatus: taskStatusDone,
		},
		{
			name: "needs input keeps the workers the turn started", sess: chat, turn: "t1",
			conv: `{"turns":[{"id":"t1","state":"running"}],"activities":[
				{"turnId":"t1","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-4"}},
				{"turnId":"t1","activityKind":"user_input","status":"pending"}]}`,
			wantStatus: taskStatusNeedsInput, wantSpawned: []string{"repo-4"},
		},
		{
			name: "workers from every attempt of a retried task", sess: chat, turn: "t1",
			conv: `{"turns":[{"id":"t1","state":"failed"},{"id":"t0","state":"completed"},{"id":"t2","state":"running","retryOfTurnId":"t1"}],"activities":[
				{"turnId":"t1","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-4"}},
				{"turnId":"t0","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-3"}},
				{"turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-5"}}]}`,
			wantStatus: taskStatusRunning, wantSpawned: []string{"repo-4", "repo-5"},
		},
		{
			name: "orchestrator turn picks the named turn", sess: chat, turn: "t2",
			conv:       `{"turns":[{"id":"t1","state":"completed"},{"id":"t2","state":"running"}]}`,
			wantStatus: taskStatusRunning,
		},
		{
			name: "orchestrator turn not created yet", sess: chat, turn: "t9",
			conv:       `{"turns":[{"id":"t1","state":"running"}]}`,
			wantStatus: taskStatusStarting,
		},
		{
			name: "batched turn follows the turn that delivered it", sess: chat, turn: "t3",
			conv:       `{"batchedDeliveries":[{"turnId":"t3","deliveredWithTurnId":"t2"}],"turns":[{"id":"t2","state":"completed"}]}`,
			wantStatus: taskStatusDone,
		},
		{
			name: "orchestrator lists the workers it spawned", sess: chat, turn: "t2",
			conv: `{"turns":[{"id":"t2","state":"completed"}],"activities":[
				{"turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-4"}},
				{"turnId":"t1","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-3"}},
				{"turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.renamed","targetSessionId":"repo-5"}}]}`,
			wantStatus: taskStatusDone, wantSpawned: []string{"repo-4"},
		},
		{
			// Older history existing says nothing about the task's own turn.
			name: "first turn not loaded", sess: chat,
			conv:       `{"hasMoreBefore":true,"turns":[{"id":"t50","state":"running"}]}`,
			wantStatus: taskStatusStarting,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var conv *taskConversation
			if tc.conv != "" {
				conv = decodeTaskConversation(t, tc.conv)
			}
			got := evaluateTask(tc.sess, conv, tc.turn)
			if got.status != tc.wantStatus {
				t.Fatalf("status = %q (%s), want %q", got.status, got.reason, tc.wantStatus)
			}
			if tc.wantReason != "" && !strings.Contains(got.reason, tc.wantReason) {
				t.Fatalf("reason = %q, want it to contain %q", got.reason, tc.wantReason)
			}
			if got.failureCode != tc.wantCode {
				t.Fatalf("failureCode = %q, want %q", got.failureCode, tc.wantCode)
			}
			if !reflect.DeepEqual(got.spawned, tc.wantSpawned) {
				t.Fatalf("spawned = %v, want %v", got.spawned, tc.wantSpawned)
			}
		})
	}
}

// taskDaemon is a scripted daemon for `ao task` command tests.
type taskDaemon struct {
	mu        sync.Mutex
	lastQuery string
	requests  []string
	bodies    map[string]string
	// routes maps "METHOD /path" to a status and body.
	routes map[string]func() (int, string)
}

func newTaskDaemon(t *testing.T) *taskDaemon {
	t.Helper()
	d := &taskDaemon{bodies: map[string]string{}, routes: map[string]func() (int, string){
		"GET /api/v1/projects/repo": func() (int, string) {
			return 200, `{"project":{"id":"repo","path":"/tmp/repo","config":{"worker":{"agent":"claude-code"},"orchestrator":{"agent":"claude-code"}}}}`
		},
		"POST /api/v1/agents/claude-code/probe": func() (int, string) {
			return 200, `{"agent":{"id":"claude-code","authStatus":"configured"},"supported":true,"installed":true}`
		},
		"GET /api/v1/agents/auth-plans": func() (int, string) {
			return 200, `{"plans":[{"agentId":"codex","displayCommand":"codex login"},{"agentId":"claude-code","displayCommand":"claude auth login"}]}`
		},
	}}
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		if key != cliInvokedRequest {
			d.requests = append(d.requests, key)
			d.bodies[key] = string(body)
		}
		d.lastQuery = r.URL.RawQuery
		route := d.routes[key]
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if route == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found","code":"NOT_FOUND","message":`+jsonQuote("no route "+key)+`}`)
			return
		}
		status, out := route()
		if status < 0 {
			// Simulate a reply lost after the request reached the daemon.
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return d
}

func (d *taskDaemon) set(key string, status int, body string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.routes[key] = func() (int, string) { return status, body }
}

func (d *taskDaemon) setFunc(key string, fn func() (int, string)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.routes[key] = fn
}

func (d *taskDaemon) saw(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.requests {
		if r == key {
			return true
		}
	}
	return false
}

// fakeClockDeps makes Sleep advance Now, so wait loops run instantly.
func fakeClockDeps() Deps {
	now := time.Unix(1_800_000_000, 0)
	var mu sync.Mutex
	return Deps{
		ProcessAlive: func(int) bool { return true },
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		Sleep: func(d time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			now = now.Add(d)
		},
	}
}

func decodeTaskResult(t *testing.T, out string) map[string]any {
	t.Helper()
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	keys := make([]string, 0, len(res))
	for k := range res {
		keys = append(keys, k)
	}
	for _, want := range []string{"status", "taskId", "sessionIds", "reason", "fix"} {
		if _, ok := res[want]; !ok {
			t.Fatalf("JSON missing %q: %s", want, out)
		}
	}
	if len(res) != 5 {
		t.Fatalf("JSON has extra keys %v: %s", keys, out)
	}
	return res
}

func TestTaskStartWorkerSignedOutFailsWithLoginCommand(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"repo-1"}`)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"idle"}}}`)
	d.set("GET /api/v1/sessions/repo-1/conversation", 200, `{"turns":[{"id":"t1","state":"failed","failureCode":"auth_required","errorMessage":"Not logged in · Please run /login"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--worker", "--wait", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "failed" || res["taskId"] != "repo-1" || res["fix"] != "run `claude auth login`, then retry" {
		t.Fatalf("result = %v", res)
	}
	if !strings.Contains(res["reason"].(string), "auth_required") {
		t.Fatalf("reason = %v", res["reason"])
	}
	if !reflect.DeepEqual(res["sessionIds"], []any{"repo-1"}) {
		t.Fatalf("sessionIds = %v", res["sessionIds"])
	}
	var req map[string]any
	_ = json.Unmarshal([]byte(d.bodies["POST /api/v1/orchestrators/delegate"]), &req)
	if req["projectId"] != "repo" || req["brief"] != "fix calc" {
		t.Fatalf("delegate body = %v", req)
	}
	if d.saw("POST /api/v1/orchestrators") {
		t.Fatal("--worker must not start the orchestrator")
	}
}

func TestTaskStartPreflightUnauthorizedStopsBeforeSpawning(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--worker", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "failed" || res["fix"] != "run `claude auth login`, then retry" {
		t.Fatalf("result = %v", res)
	}
	if d.saw("POST /api/v1/orchestrators/delegate") {
		t.Fatal("a signed-out agent must not get a session")
	}
}

func TestTaskStartOrchestratorSignInConflictNamesLogin(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 409, `{"error":"conflict","code":"AGENT_ACCOUNT_DEFAULT_UNUSABLE","message":"The default account is signed out"}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["fix"] != "run `claude auth login`, then retry" || !strings.Contains(res["reason"].(string), "AGENT_ACCOUNT_DEFAULT_UNUSABLE") {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskStartOrchestratorWaitsForTheTurnToRun(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/conversation/messages", 202, `{"turnId":"t2","state":"queued","duplicate":false}`)
	// The worker the turn started is busy too.
	d.set("GET /api/v1/sessions/repo-5", 200, `{"session":{"id":"repo-5","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/repo-5/conversation", 200, `{"turns":[{"id":"w1","state":"running"}]}`)
	deps := fakeClockDeps()
	polls := 0
	var firstRunning time.Time
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		polls++
		if polls < 3 {
			return 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"queued"}]}`
		}
		if firstRunning.IsZero() {
			firstRunning = deps.Now()
		}
		return 200, `{"turns":[{"id":"t1","state":"completed"},{"id":"t2","state":"running"}],"activities":[
			{"turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-5"}}]}`
	})

	out, _, err := executeCLI(t, deps, "task", "start", "fix calc", "--project", "repo", "--wait", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v\n%s", ExitCode(err), err, out)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "running" || res["taskId"] != "repo-orchestrator:t2" {
		t.Fatalf("result = %v", res)
	}
	if !reflect.DeepEqual(res["sessionIds"], []any{"repo-orchestrator", "repo-5"}) {
		t.Fatalf("sessionIds = %v", res["sessionIds"])
	}
	// A running turn counts as started only after it has run for five seconds.
	if settled := deps.Now().Sub(firstRunning); settled < 5*time.Second {
		t.Fatalf("reported running %s after the turn was first seen running; want at least 5s", settled)
	}
	var msg map[string]string
	_ = json.Unmarshal([]byte(d.bodies["POST /api/v1/sessions/repo-orchestrator/conversation/messages"]), &msg)
	if msg["text"] != "fix calc" {
		t.Fatalf("message = %q; want the brief unchanged", msg["text"])
	}
	if !strings.HasPrefix(msg["clientMessageId"], "ao-task-") {
		t.Fatalf("clientMessageId = %q", msg["clientMessageId"])
	}
}

func TestTaskStartOrchestratorTerminalFallsBackToSend(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"tui","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/conversation/messages", 409, `{"error":"conflict","code":"SESSION_MODE_MISMATCH","message":"no chat conversation"}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/send", 200, `{"ok":true,"sessionId":"repo-orchestrator","message":"fix calc"}`)
	t.Setenv("AO_SESSION_ID", "") // outside any Orchestrator session

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if !strings.HasPrefix(out, "task repo-orchestrator: running") {
		t.Fatalf("stdout = %q", out)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(d.bodies["POST /api/v1/sessions/repo-orchestrator/send"]), &sent)
	if !reflect.DeepEqual(sent, map[string]any{"message": "fix calc", "userAuthored": true}) {
		t.Fatalf("send body = %v; want what `ao send` sends outside a session", sent)
	}

	// Inside a session, the calling agent names itself instead.
	t.Setenv("AO_SESSION_ID", "other-7")
	if _, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo"); err != nil {
		t.Fatalf("inside a session: exit = %d: %v", ExitCode(err), err)
	}
	sent = nil
	d.mu.Lock()
	_ = json.Unmarshal([]byte(d.bodies["POST /api/v1/sessions/repo-orchestrator/send"]), &sent)
	d.mu.Unlock()
	if !reflect.DeepEqual(sent, map[string]any{"message": "fix calc", "senderSessionId": "other-7"}) {
		t.Fatalf("send body inside a session = %v", sent)
	}
	if d.saw("GET /api/v1/sessions/repo-orchestrator/conversation") {
		t.Fatal("a terminal session has no conversation to read")
	}
}

func TestTaskWaitNeedsInputExitsThree(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-3", 200, `{"session":{"id":"repo-3","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	d.set("GET /api/v1/sessions/repo-3/conversation", 200, `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "wait", "repo-3", "--json")
	if got := ExitCode(err); got != 3 {
		t.Fatalf("exit = %d (%v), want 3", got, err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "needs_input" {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskWaitTimesOutWithSignInHint(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"idle"}}}`)
	d.set("GET /api/v1/sessions/repo-1/conversation", 200, `{"turns":[]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "wait", "repo-1", "--timeout", "30s", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "failed" || !strings.Contains(res["reason"].(string), "timed out after 30s") {
		t.Fatalf("result = %v", res)
	}
	if res["fix"] != "if claude-code is not signed in, run `claude auth login`, then retry; otherwise check `ao session get repo-1`" {
		t.Fatalf("fix = %v", res["fix"])
	}
}

func TestTaskStatusDoneHumanOutput(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	d.set("GET /api/v1/sessions/repo-1/conversation", 200, `{"turns":[{"id":"t1","state":"completed"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	want := "task repo-1: done (the agent finished its turn)\n  sessions: repo-1\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestTaskStatusUnknownSessionFails(t *testing.T) {
	newTaskDaemon(t)
	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-9", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	// The daemon's envelope (message and code) is kept after the summary.
	if res := decodeTaskResult(t, out); !strings.HasPrefix(res["reason"].(string), "session repo-9 not found: ") || !strings.Contains(res["reason"].(string), "(NOT_FOUND)") {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskStartResolvesProjectFromWorkingDirectory(t *testing.T) {
	d := newTaskDaemon(t)
	dir := t.TempDir()
	t.Chdir(dir)
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"repo"}]}`)
	d.set("GET /api/v1/projects/repo", 200, `{"project":{"id":"repo","path":`+jsonQuote(dir)+`,"config":{"worker":{"agent":"claude-code"}}}}`)
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"repo-1"}`)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"provisioning"}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--worker", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "starting" || res["taskId"] != "repo-1" {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskStartRejectsEmptyBrief(t *testing.T) {
	_, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "   ")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
}

func TestTaskExitErrorCarriesItsCode(t *testing.T) {
	if got := ExitCode(taskExitError{code: taskExitNeedsInput, msg: "waiting"}); got != 3 {
		t.Fatalf("ExitCode(needs input) = %d, want 3", got)
	}
	if got := ExitCode(taskExitError{code: 1, msg: "failed"}); got != 1 {
		t.Fatalf("ExitCode(failed) = %d", got)
	}
}

func TestTaskWaitRestartsSettleWhenARetryTakesOver(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	start := deps.Now()
	var retrySeen time.Time
	d.setFunc("GET /api/v1/sessions/repo-1/conversation", func() (int, string) {
		if deps.Now().Sub(start) < 4*time.Second {
			return 200, `{"turns":[{"id":"t1","state":"running"}]}`
		}
		if retrySeen.IsZero() {
			retrySeen = deps.Now()
		}
		return 200, `{"turns":[{"id":"t1","state":"failed"},{"id":"t2","state":"running","retryOfTurnId":"t1"}]}`
	})

	out, _, err := executeCLI(t, deps, "task", "wait", "repo-1", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "running" {
		t.Fatalf("result = %v", res)
	}
	if retrySeen.IsZero() {
		t.Fatal("wait returned before it ever saw the retry t2")
	}
	if settled := deps.Now().Sub(retrySeen); settled < 5*time.Second {
		t.Fatalf("retry reported started after %s; the settle time must restart for the new turn", settled)
	}
}

func TestTaskWaitDeadlineBeforeSettleIsATimeout(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/repo-1/conversation", 200, `{"turns":[{"id":"t1","state":"running"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "wait", "repo-1", "--timeout", "2s", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "failed" || !strings.Contains(res["reason"].(string), "only just started") {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskStatusPagesBackToTheFirstTurn(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.setFunc("GET /api/v1/sessions/repo-1/conversation", func() (int, string) {
		d.mu.Lock()
		query := d.lastQuery
		d.mu.Unlock()
		if query == "beforeSequence=300" {
			return 200, `{"hasMoreBefore":false,"oldestSequence":1,"turns":[{"id":"t1","state":"failed","failureCode":"auth_required","errorMessage":"Not logged in"}]}`
		}
		return 200, `{"hasMoreBefore":true,"oldestSequence":300,"turns":[{"id":"t9","state":"running"}]}`
	})

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-1", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "failed" || res["fix"] != "run `claude auth login`, then retry" {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskStartAmbiguousProjectIsAUsageError(t *testing.T) {
	d := newTaskDaemon(t)
	dir := t.TempDir()
	t.Chdir(dir)
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"a"},{"id":"b"}]}`)
	d.set("GET /api/v1/projects/a", 200, `{"project":{"id":"a","path":`+jsonQuote(dir)+`}}`)
	d.set("GET /api/v1/projects/b", 200, `{"project":{"id":"b","path":`+jsonQuote(dir)+`}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--json")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
	if out != "" {
		t.Fatalf("a usage error prints no JSON, got %q", out)
	}
}

func TestTaskStartRetriesALostReplyWithTheSameMessageID(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, `{"turns":[{"id":"t2","state":"running"}]}`)
	var ids []string
	d.setFunc("POST /api/v1/sessions/repo-orchestrator/conversation/messages", func() (int, string) {
		var msg map[string]string
		d.mu.Lock()
		_ = json.Unmarshal([]byte(d.bodies["POST /api/v1/sessions/repo-orchestrator/conversation/messages"]), &msg)
		d.mu.Unlock()
		ids = append(ids, msg["clientMessageId"])
		if len(ids) == 1 {
			return -1, "" // drop the connection
		}
		return 202, `{"turnId":"t2","state":"running","duplicate":true}`
	})

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["taskId"] != "repo-orchestrator:t2" {
		t.Fatalf("result = %v", res)
	}
	if len(ids) != 2 || ids[0] != ids[1] || ids[0] == "" {
		t.Fatalf("client message ids = %v; want the retry to reuse the first id", ids)
	}
}

func TestTaskStartUnconfirmedDeliveryHasNoTaskID(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/conversation/messages", -1, "")

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["taskId"] != "" || !strings.Contains(res["reason"].(string), "client message id ao-task-") ||
		res["fix"] != "check `ao session history repo-orchestrator` before starting the task again, so it is not sent twice" {
		t.Fatalf("result = %v", res)
	}
}

func TestExitCodeKeepsOtherExitErrorsAtOne(t *testing.T) {
	// Re-run this test binary as a helper that exits 2, so the check needs no
	// POSIX shell (the native E2E job also runs on Windows).
	if os.Getenv("AO_TASK_TEST_EXIT_HELPER") == "1" {
		os.Exit(2)
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestExitCodeKeepsOtherExitErrorsAtOne$")
	helper.Env = append(os.Environ(), "AO_TASK_TEST_EXIT_HELPER=1")
	err := helper.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("setup: %v", err)
	}
	if got := ExitCode(fmt.Errorf("codex login failed: %w", err)); got != 1 {
		t.Fatalf("ExitCode(wrapped exec.ExitError) = %d, want 1", got)
	}
}

func TestTaskStatusPagesBackWhileThePageSplitsTheTurn(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-7", 200, `{"session":{"id":"repo-7","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	d.set("GET /api/v1/sessions/repo-7/conversation", 200, `{"turns":[{"id":"w1","state":"completed"}]}`)
	var pages []string
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		d.mu.Lock()
		query := d.lastQuery
		d.mu.Unlock()
		pages = append(pages, query)
		switch query {
		case "beforeSequence=300":
			// Older rows of the same turn, including the worker it spawned.
			return 200, `{"hasMoreBefore":true,"oldestSequence":100,"turns":[{"id":"t2","state":"running"}],"activities":[
				{"id":"a1","turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-7"}}]}`
		case "beforeSequence=100":
			return 200, `{"hasMoreBefore":true,"oldestSequence":40,"turns":[{"id":"t1","state":"completed"}]}`
		case "":
			return 200, `{"hasMoreBefore":true,"oldestSequence":300,"turns":[{"id":"t2","state":"running"}]}`
		}
		t.Errorf("unexpected page %q", query)
		return 500, `{}`
	})

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-orchestrator:t2", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); !reflect.DeepEqual(res["sessionIds"], []any{"repo-orchestrator", "repo-7"}) {
		t.Fatalf("result = %v", res)
	}
	if want := []string{"", "beforeSequence=300", "beforeSequence=100"}; !reflect.DeepEqual(pages, want) {
		t.Fatalf("pages read = %q; want %q (stop once the earlier turn t1 is loaded)", pages, want)
	}
}

func TestTaskStartWithNoRegisteredProjectIsAUsageError(t *testing.T) {
	d := newTaskDaemon(t)
	t.Chdir(t.TempDir())
	d.set("GET /api/v1/projects", 200, `{"projects":[]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--json")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
	if out != "" || !strings.Contains(err.Error(), "ao project add --path <repo>") {
		t.Fatalf("stdout = %q, err = %v", out, err)
	}
}

func TestTaskWaitTimeoutWhileProvisioningDoesNotBlameSignIn(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"provisioning"}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "wait", "repo-1", "--timeout", "10s", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if fix := res["fix"].(string); strings.Contains(fix, "signed in") || !strings.Contains(fix, "workspace setup had not finished") {
		t.Fatalf("fix = %q", fix)
	}
}

// Work then the prompt is reported as started, never done: the work may predate
// the brief, and a terminal session cannot confirm completion.
func TestTaskWaitTerminalWorkThenPromptIsStartedNotDone(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	start := deps.Now()
	d.setFunc("GET /api/v1/sessions/repo-orchestrator", func() (int, string) {
		state := "waiting_input"
		if elapsed := deps.Now().Sub(start); elapsed >= time.Second && elapsed < 3*time.Second {
			state = "active"
		}
		return 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"tui","provisionState":"ready","activity":{"state":"` + state + `"}}}`
	})

	out, _, err := executeCLI(t, deps, "task", "wait", "repo-orchestrator", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "running" || !strings.Contains(res["reason"].(string), "does not report whether this task is done") {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskWaitTerminalPromptOnlySaysSoOnTimeout(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"tui","provisionState":"ready","activity":{"state":"waiting_input"}}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "wait", "repo-orchestrator", "--timeout", "10s", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if !strings.Contains(res["reason"].(string), "a terminal session does not report whether it took this task") || strings.Contains(res["fix"].(string), "signed in") {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskStartWorkerRetriesALostReplyWithTheSameRequestID(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"provisioning"}}`)
	var ids []string
	d.setFunc("POST /api/v1/orchestrators/delegate", func() (int, string) {
		var req map[string]string
		d.mu.Lock()
		_ = json.Unmarshal([]byte(d.bodies["POST /api/v1/orchestrators/delegate"]), &req)
		d.mu.Unlock()
		ids = append(ids, req["clientRequestId"])
		if len(ids) == 1 {
			return -1, ""
		}
		return 202, `{"ok":true,"workerId":"repo-1"}`
	})

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--worker", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["taskId"] != "repo-1" {
		t.Fatalf("result = %v", res)
	}
	if len(ids) != 2 || ids[0] != ids[1] || !strings.HasPrefix(ids[0], "ao-task-") {
		t.Fatalf("client request ids = %v; want the retry to reuse the first id", ids)
	}
}

func TestTaskStartTerminalDeliveryUnconfirmedPointsAtTheTerminal(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-1","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"tui","provisionState":"ready","activity":{"state":"idle"}}}`)
	d.set("POST /api/v1/sessions/repo-1/conversation/messages", 409, `{"error":"conflict","code":"SESSION_MODE_MISMATCH","message":"no chat conversation"}`)
	d.set("POST /api/v1/sessions/repo-1/send", 409, `{"error":"conflict","code":"SESSION_DELIVERY_UNCONFIRMED","message":"Message was written to the terminal but the agent did not start a turn"}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["fix"] != "check the agent's terminal with `ao session get repo-1`; if claude-code is not signed in, run `claude auth login`, then retry" {
		t.Fatalf("result = %v", res)
	}
}

func TestTaskStartWaitKeepsTheTerminalWorkSeenAtStart(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/conversation/messages", 409, `{"error":"conflict","code":"SESSION_MODE_MISMATCH","message":"no chat conversation"}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/send", 200, `{"ok":true,"sessionId":"repo-orchestrator","message":"fix calc"}`)
	reads := 0
	d.setFunc("GET /api/v1/sessions/repo-orchestrator", func() (int, string) {
		reads++
		// Reads 1 (preflight) and 2 (the start's status) see the agent working;
		// it is back at its prompt by the first wait poll.
		state := "waiting_input"
		if reads <= 2 {
			state = "active"
		}
		return 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"tui","provisionState":"ready","activity":{"state":"` + state + `"}}}`
	})

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--wait", "--timeout", "10s", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v\n%s", ExitCode(err), err, out)
	}
	if res := decodeTaskResult(t, out); res["status"] != "running" || !strings.Contains(res["reason"].(string), "does not report whether this task is done") {
		t.Fatalf("result = %v", res)
	}
}

// needs_input exits 3 through the real entrypoint and is not reported as a CLI
// usage error, while a real usage error still is.
func TestTaskNeedsInputIsNotAUsageErrorEvent(t *testing.T) {
	const usageEvent = "POST /internal/telemetry/cli-usage-error"
	d := newTaskDaemon(t)
	d.set(usageEvent, 204, ``)
	d.set("GET /api/v1/sessions/repo-3", 200, `{"session":{"id":"repo-3","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	d.set("GET /api/v1/sessions/repo-3/conversation", 200, `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`)

	out, _, err := executeCLIRoot(t, fakeClockDeps(), "task", "wait", "repo-3", "--json")
	if got := ExitCode(err); got != 3 {
		t.Fatalf("exit = %d (%v), want 3", got, err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "needs_input" {
		t.Fatalf("result = %v", res)
	}
	if d.saw(usageEvent) {
		t.Fatal("needs_input was reported as a CLI usage error")
	}

	// Control: the same harness does see the event for a real usage error.
	_, _, err = executeCLIRoot(t, fakeClockDeps(), "task", "status", "bad id")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("usage error exit = %d (%v), want 2", got, err)
	}
	if !d.saw(usageEvent) {
		t.Fatal("a usage error should still be reported as one")
	}
}

func TestTaskUnknownSubcommandIsAUsageError(t *testing.T) {
	_, _, err := executeCLI(t, fakeClockDeps(), "task", "statuz", "repo-1")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
	if !strings.Contains(err.Error(), `unknown command "statuz"`) {
		t.Fatalf("err = %v", err)
	}
	out, _, err := executeCLI(t, fakeClockDeps(), "task")
	if err != nil || !strings.Contains(out, "Exit codes: 0 started") {
		t.Fatalf("bare `ao task`: err = %v, out = %q", err, out)
	}
}

// A wait that runs out while the task is still queued must not look like a
// failure: the task is accepted and will run, and a retry would send it twice.
func TestTaskWaitTimeoutWhileQueuedExitsFour(t *testing.T) {
	const usageEvent = "POST /internal/telemetry/cli-usage-error"
	d := newTaskDaemon(t)
	d.set(usageEvent, 204, ``)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"queued"}]}`)

	out, _, err := executeCLIRoot(t, fakeClockDeps(), "task", "wait", "repo-orchestrator:t2", "--timeout", "30s", "--json")
	if got := ExitCode(err); got != 4 {
		t.Fatalf("exit = %d (%v), want 4", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "queued" || !strings.Contains(res["reason"].(string), "do not start it again") ||
		res["fix"] != "wait longer with `ao task wait repo-orchestrator:t2`, or check `ao session get repo-orchestrator`" {
		t.Fatalf("result = %v", res)
	}
	if d.saw(usageEvent) {
		t.Fatal("a queued timeout was reported as a CLI usage error")
	}
}

// A one-shot read of a queued task is not a failure either; like any other
// unfinished status it exits 0.
func TestTaskStatusQueuedExitsZero(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"queued"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-orchestrator:t2", "--json")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "queued" {
		t.Fatalf("result = %v", res)
	}
}

// An orchestrator task is not done while a worker its turn started is still
// working, and it needs input when a worker does.
func TestTaskStatusFoldsTheOrchestratorsWorkers(t *testing.T) {
	orch := `{"turns":[{"id":"t2","state":"completed"}],"activities":[
		{"id":"a1","turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-3"}},
		{"id":"a2","turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-4"}}]}`
	worker := func(state, activity string) string {
		return `{"turns":[{"id":"w1","state":"` + state + `"}],"activities":[` + activity + `]}`
	}
	cases := []struct {
		name         string
		w3, w4       string
		wantStatus   string
		wantExit     int
		wantInReason string
	}{
		{name: "a worker still running", w3: worker("completed", ""), w4: worker("running", ""), wantStatus: "running", wantExit: 0, wantInReason: "still working: repo-4 (running)"},
		{name: "a worker needs input", w3: worker("running", `{"turnId":"w1","activityKind":"approval","status":"pending"}`), w4: worker("completed", ""), wantStatus: "needs_input", wantExit: 3, wantInReason: "worker repo-3 is waiting"},
		{name: "a worker failed", w3: worker("completed", ""), w4: `{"turns":[{"id":"w1","state":"failed","errorMessage":"tests failed"}]}`, wantStatus: "failed", wantExit: 1, wantInReason: "worker repo-4 failed: tests failed"},
		{name: "all finished", w3: worker("completed", ""), w4: worker("completed", ""), wantStatus: "done", wantExit: 0, wantInReason: "its 2 worker(s) finished"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTaskDaemon(t)
			for _, id := range []string{"repo-orchestrator", "repo-3", "repo-4"} {
				d.set("GET /api/v1/sessions/"+id, 200, `{"session":{"id":"`+id+`","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
			}
			d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, orch)
			d.set("GET /api/v1/sessions/repo-3/conversation", 200, tc.w3)
			d.set("GET /api/v1/sessions/repo-4/conversation", 200, tc.w4)

			out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-orchestrator:t2", "--json")
			if got := ExitCode(err); got != tc.wantExit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tc.wantExit)
			}
			res := decodeTaskResult(t, out)
			if res["status"] != tc.wantStatus || !strings.Contains(res["reason"].(string), tc.wantInReason) {
				t.Fatalf("result = %v", res)
			}
			if !reflect.DeepEqual(res["sessionIds"], []any{"repo-orchestrator", "repo-3", "repo-4"}) {
				t.Fatalf("sessionIds = %v", res["sessionIds"])
			}
		})
	}
}

func TestTaskStartAgentOrBaseWithoutWorkerIsAUsageError(t *testing.T) {
	d := newTaskDaemon(t)
	for _, args := range [][]string{{"--base", "no-such-branch"}, {"--agent", "codex"}} {
		_, _, err := executeCLI(t, fakeClockDeps(), append([]string{"task", "start", "fix calc", "--project", "repo"}, args...)...)
		if got := ExitCode(err); got != 2 || !strings.Contains(err.Error(), "apply only with --worker") {
			t.Fatalf("%v: exit = %d (%v), want usage error 2", args, got, err)
		}
	}
	if d.saw("POST /api/v1/orchestrators") {
		t.Fatal("a usage error must not start the orchestrator")
	}
}

// Asking for a named chat turn on a terminal session is a lookup failure, not
// a report of whatever the terminal is doing.
func TestTaskStatusNamedTurnOnTerminalSessionFails(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"tui","provisionState":"ready","activity":{"state":"active"}}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-1:t1", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "failed" || !strings.Contains(res["reason"].(string), "has no chat turn t1") {
		t.Fatalf("result = %v", res)
	}
	// The bare session id still reports terminal activity.
	out, _, err = executeCLI(t, fakeClockDeps(), "task", "status", "repo-1", "--json")
	if err != nil || decodeTaskResult(t, out)["status"] != "running" {
		t.Fatalf("bare id: err = %v, out = %s", err, out)
	}
}

// A failed turn read just before the deadline is reported as failed even when
// the optional sign-in guidance lookup runs past the deadline.
func TestTaskWaitKeepsAFailureWhenGuidanceRunsPastTheDeadline(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	start := deps.Now()
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		if deps.Now().Sub(start) < time.Second {
			return 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"queued"}]}`
		}
		return 200, `{"turns":[{"id":"t1","state":"completed"},{"id":"t2","state":"failed","failureCode":"auth_required","errorMessage":"Not logged in"}]}`
	})
	d.setFunc("GET /api/v1/agents/auth-plans", func() (int, string) {
		time.Sleep(1200 * time.Millisecond) // longer than the 1s left
		return 200, `{"plans":[{"agentId":"claude-code","displayCommand":"claude auth login"}]}`
	})

	out, _, err := executeCLI(t, deps, "task", "wait", "repo-orchestrator:t2", "--timeout", "2s", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1 (the failure that was read)", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "failed" || !strings.Contains(res["reason"].(string), "auth_required") {
		t.Fatalf("result = %v", res)
	}
}

// Exit 4 must match a read made at the end of the wait: a queued turn that was
// cancelled during the last sleep is reported as failed, not "will run".
func TestTaskWaitRereadsAQueuedTaskBeforeExitFour(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	start := deps.Now()
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		if deps.Now().Sub(start) < 10*time.Second {
			return 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"queued"}]}`
		}
		return 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"cancelled"}]}`
	})

	out, _, err := executeCLI(t, deps, "task", "wait", "repo-orchestrator:t2", "--timeout", "10s", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "failed" || !strings.Contains(res["reason"].(string), "cancelled") {
		t.Fatalf("result = %v", res)
	}
}

// A worker is read at its latest turn: follow-up work the orchestrator sent it
// after its first turn keeps the task running, and an approval on that work
// makes the task need input.
func TestTaskStatusReadsAWorkersLatestTurn(t *testing.T) {
	orch := `{"turns":[{"id":"t2","state":"completed"}],"activities":[
		{"id":"a1","turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-3"}}]}`
	cases := []struct {
		name, worker string
		wantStatus   string
		wantExit     int
	}{
		{name: "follow-up turn running", worker: `{"turns":[{"id":"w1","state":"completed"},{"id":"w2","state":"running"}]}`, wantStatus: "running", wantExit: 0},
		{name: "follow-up turn waiting on an approval", worker: `{"turns":[{"id":"w1","state":"completed"},{"id":"w2","state":"running"}],"activities":[{"turnId":"w2","activityKind":"approval","status":"pending"}]}`, wantStatus: "needs_input", wantExit: 3},
		{name: "follow-up turn finished", worker: `{"turns":[{"id":"w1","state":"completed"},{"id":"w2","state":"completed"}]}`, wantStatus: "done", wantExit: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTaskDaemon(t)
			for _, id := range []string{"repo-orchestrator", "repo-3"} {
				d.set("GET /api/v1/sessions/"+id, 200, `{"session":{"id":"`+id+`","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
			}
			d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, orch)
			d.set("GET /api/v1/sessions/repo-3/conversation", 200, tc.worker)

			out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-orchestrator:t2", "--json")
			if got := ExitCode(err); got != tc.wantExit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tc.wantExit)
			}
			if res := decodeTaskResult(t, out); res["status"] != tc.wantStatus {
				t.Fatalf("result = %v", res)
			}
		})
	}
}

// A retried orchestrator turn that succeeded is not failed by the worker an
// earlier, superseded attempt started; the earlier worker stays in sessionIds.
func TestTaskStatusRetrySupersedesAnEarlierAttemptsFailedWorker(t *testing.T) {
	d := newTaskDaemon(t)
	for _, id := range []string{"repo-orchestrator", "repo-3", "repo-4"} {
		d.set("GET /api/v1/sessions/"+id, 200, `{"session":{"id":"`+id+`","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	}
	d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, `{"turns":[
		{"id":"t1","state":"failed","errorMessage":"rate limited"},
		{"id":"t2","state":"completed","retryOfTurnId":"t1"}],"activities":[
		{"id":"a1","turnId":"t1","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-3"}},
		{"id":"a2","turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-4"}}]}`)
	d.set("GET /api/v1/sessions/repo-3/conversation", 200, `{"turns":[{"id":"w1","state":"failed","errorMessage":"tests failed"}]}`)
	d.set("GET /api/v1/sessions/repo-4/conversation", 200, `{"turns":[{"id":"w1","state":"completed"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-orchestrator:t1", "--json")
	if err != nil {
		t.Fatalf("exit = %d (%v), want 0", ExitCode(err), err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "done" {
		t.Fatalf("result = %v", res)
	}
	if !reflect.DeepEqual(res["sessionIds"], []any{"repo-orchestrator", "repo-3", "repo-4"}) {
		t.Fatalf("sessionIds = %v", res["sessionIds"])
	}
}

// An earlier attempt's worker that cannot be read may still be working, so the
// task is not reported done even though the retry and its worker finished.
func TestTaskStatusUnreadEarlierWorkerKeepsTheTaskRunning(t *testing.T) {
	d := newTaskDaemon(t)
	for _, id := range []string{"repo-orchestrator", "repo-4"} {
		d.set("GET /api/v1/sessions/"+id, 200, `{"session":{"id":"`+id+`","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	}
	d.set("GET /api/v1/sessions/repo-3", 503, `{"error":"unavailable","code":"TEMPORARY","message":"try again"}`)
	d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, `{"turns":[
		{"id":"t1","state":"failed"},
		{"id":"t2","state":"completed","retryOfTurnId":"t1"}],"activities":[
		{"id":"a1","turnId":"t1","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-3"}},
		{"id":"a2","turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-4"}}]}`)
	d.set("GET /api/v1/sessions/repo-4/conversation", 200, `{"turns":[{"id":"w1","state":"completed"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-orchestrator:t1", "--json")
	if err != nil {
		t.Fatalf("exit = %d (%v), want 0", ExitCode(err), err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "running" || !strings.Contains(res["reason"].(string), "repo-3 (could not be read") {
		t.Fatalf("result = %v", res)
	}
}

// A worker's running turn with a pending approval is reported as needs_input
// even when a queued follow-up turn comes after it.
func TestTaskStatusWorkerApprovalBeforeAQueuedFollowUp(t *testing.T) {
	d := newTaskDaemon(t)
	for _, id := range []string{"repo-orchestrator", "repo-3"} {
		d.set("GET /api/v1/sessions/"+id, 200, `{"session":{"id":"`+id+`","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	}
	d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, `{"turns":[{"id":"t2","state":"completed"}],"activities":[
		{"id":"a1","turnId":"t2","activityKind":"ao_action","status":"completed","detail":{"action":"session.spawned","targetSessionId":"repo-3"}}]}`)
	d.set("GET /api/v1/sessions/repo-3/conversation", 200, `{"turns":[
		{"id":"w1","state":"running"},
		{"id":"w2","state":"queued"}],"activities":[
		{"id":"p1","turnId":"w1","activityKind":"approval","status":"pending"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-orchestrator:t2", "--json")
	if got := ExitCode(err); got != 3 {
		t.Fatalf("exit = %d (%v), want 3", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "needs_input" || !strings.Contains(res["reason"].(string), "repo-3") {
		t.Fatalf("result = %v", res)
	}
	// The fix names the worker to answer, not the orchestrator in sessionIds[0].
	// It then says how to keep following the original task.
	if fix := res["fix"].(string); !strings.HasPrefix(fix, "answer the pending request in session repo-3 ") || !strings.HasSuffix(fix, "`ao task wait repo-orchestrator:t2`") {
		t.Fatalf("fix = %q; want it to name worker repo-3, then `ao task wait repo-orchestrator:t2`", fix)
	}
}

// A task waiting on its own session's approval names that session in fix.
func TestTaskNeedsInputFixNamesTheSession(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-3", 200, `{"session":{"id":"repo-3","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	d.set("GET /api/v1/sessions/repo-3/conversation", 200, `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "status", "repo-3", "--json")
	if got := ExitCode(err); got != 3 {
		t.Fatalf("exit = %d (%v), want 3", got, err)
	}
	if res := decodeTaskResult(t, out); !strings.Contains(res["fix"].(string), "session repo-3") {
		t.Fatalf("result = %v", res)
	}
}

// An orchestrator that accepted the message without a turn id may still run
// the task: the result must not read as "nothing was created".
func TestTaskStartAcceptedWithoutTurnIDSaysCheckBeforeResending(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/conversation/messages", 202, `{"state":"queued","duplicate":false}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["taskId"] != "" || !strings.Contains(res["reason"].(string), "may still run") ||
		res["fix"] != "check `ao session history repo-orchestrator` before starting the task again, so it does not run twice" {
		t.Fatalf("result = %v", res)
	}
}

// Cancelling during the final queued re-read is an interrupted wait, not exit 4
// from the stale queued observation.
func TestTaskWaitCancelledDuringTheFinalReadIsInterrupted(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	start := deps.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		if deps.Now().Sub(start) >= 5*time.Second {
			cancel() // the caller gives up during the final read
		}
		return 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"queued"}]}`
	})

	c := &commandContext{deps: deps.withDefaults()}
	res := c.waitTask(ctx, taskResult{TaskID: "repo-orchestrator:t2"}, 5*time.Second)
	if res.Status != taskStatusFailed || !strings.Contains(res.Reason, "interrupted while waiting") || res.TaskID != "repo-orchestrator:t2" || res.waitTimedOut {
		t.Fatalf("result = %+v; want an interrupted wait for the known task id", res)
	}
}

// A transient status-read failure (503) right after a worker task is created
// keeps its task id and does not end `start --wait`; the next read succeeds.
func TestTaskStartWaitRetriesATransientReadFailure(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"repo-1"}`)
	reads := 0
	d.setFunc("GET /api/v1/sessions/repo-1", func() (int, string) {
		reads++
		if reads <= 2 {
			return 503, `{"error":"unavailable","code":"TEMPORARY","message":"try again"}`
		}
		return 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`
	})
	d.set("GET /api/v1/sessions/repo-1/conversation", 200, `{"turns":[{"id":"t1","state":"completed"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--worker", "--wait", "--json")
	if err != nil {
		t.Fatalf("exit = %d (%v), want 0", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "done" || res["taskId"] != "repo-1" {
		t.Fatalf("result = %v", res)
	}
}

// `ao task wait` keeps polling through a transient read failure, but a
// permanent lookup error (unknown session) still ends it at once.
func TestTaskWaitTransientVersusPermanentReadFailures(t *testing.T) {
	d := newTaskDaemon(t)
	reads := 0
	d.setFunc("GET /api/v1/sessions/repo-1", func() (int, string) {
		reads++
		if reads == 1 {
			return 503, `{"error":"unavailable","code":"TEMPORARY","message":"try again"}`
		}
		return 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`
	})
	d.set("GET /api/v1/sessions/repo-1/conversation", 200, `{"turns":[{"id":"t1","state":"completed"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "wait", "repo-1", "--json")
	if err != nil || decodeTaskResult(t, out)["status"] != "done" {
		t.Fatalf("transient: err = %v, out = %s", err, out)
	}

	out, _, err = executeCLI(t, fakeClockDeps(), "task", "wait", "repo-9", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("permanent: exit = %d (%v), want 1", got, err)
	}
	if res := decodeTaskResult(t, out); !strings.HasPrefix(res["reason"].(string), "session repo-9 not found: ") {
		t.Fatalf("permanent: result = %v", res)
	}
}

// Exit 4 needs a successful final read that still says queued; a failed final
// read is a status-read failure for the known task id.
func TestTaskWaitFailedFinalReadIsNotExitFour(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	start := deps.Now()
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		if deps.Now().Sub(start) >= 5*time.Second {
			return 503, `{"error":"unavailable","code":"TEMPORARY","message":"try again"}`
		}
		return 200, `{"turns":[{"id":"t1","state":"running"},{"id":"t2","state":"queued"}]}`
	})

	out, _, err := executeCLI(t, deps, "task", "wait", "repo-orchestrator:t2", "--timeout", "5s", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if res["status"] != "failed" || res["taskId"] != "repo-orchestrator:t2" || !strings.Contains(res["reason"].(string), "final status read failed") ||
		res["fix"] != "check `ao task status repo-orchestrator:t2` before starting the task again, so it does not run twice" {
		t.Fatalf("result = %v", res)
	}
}

// A cancel during the last sleep is an interruption, not a timeout.
func TestTaskWaitCancelledDuringSleepIsInterrupted(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := deps.Sleep
	deps.Sleep = func(dur time.Duration) {
		sleep(dur)
		cancel() // the caller gives up while the wait sleeps
	}
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"provisioning"}}`)

	c := &commandContext{deps: deps.withDefaults()}
	res := c.waitTask(ctx, taskResult{TaskID: "repo-1"}, time.Second)
	if res.Status != taskStatusFailed || !strings.Contains(res.Reason, "interrupted while waiting") {
		t.Fatalf("result = %+v; want an interrupted wait, not a timeout", res)
	}
}

// After a lost reply, an API error on the retry leaves the outcome unknown,
// so the result keeps the recovery handle instead of a plain failure.
func TestTaskStartLostReplyThenAPIErrorKeepsTheRecoveryHandle(t *testing.T) {
	d := newTaskDaemon(t)
	calls := 0
	d.setFunc("POST /api/v1/orchestrators/delegate", func() (int, string) {
		calls++
		if calls == 1 {
			return -1, "" // reply lost
		}
		return 409, `{"error":"conflict","code":"CLIENT_REQUEST_INCOMPLETE","message":"request still being recorded"}`
	})

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--worker", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1", got, err)
	}
	res := decodeTaskResult(t, out)
	if !strings.Contains(res["reason"].(string), "could not confirm that the worker started (client request id ao-task-") ||
		res["fix"] != "check `ao session ls` before starting the task again, so it does not run twice" {
		t.Fatalf("result = %v", res)
	}
}

// The project's saved orchestrator agent is not probed before the daemon names
// the running orchestrator: an existing orchestrator on a ready agent gets the
// brief even when the newly saved agent is signed out.
func TestTaskStartChecksTheRunningOrchestratorsAgentNotTheSavedOne(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/projects/repo", 200, `{"project":{"id":"repo","path":"/tmp/repo","config":{"worker":{"agent":"claude-code"},"orchestrator":{"agent":"codex"}}}}`)
	d.set("POST /api/v1/agents/codex/probe", 200, `{"agent":{"id":"codex","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/conversation/messages", 202, `{"turnId":"t2","state":"running"}`)
	d.set("GET /api/v1/sessions/repo-orchestrator/conversation", 200, `{"turns":[{"id":"t2","state":"running"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo", "--json")
	if err != nil {
		t.Fatalf("exit = %d (%v), want 0", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["taskId"] != "repo-orchestrator:t2" {
		t.Fatalf("result = %v", res)
	}
	if d.saw("POST /api/v1/agents/codex/probe") {
		t.Fatal("the saved orchestrator agent must not be probed before the running session is known")
	}
}

// Settle time is measured from when the reading was taken: a running reading
// that started before the deadline but had not yet run for 5 s does not become
// settled because the read itself finished after the deadline.
func TestTaskWaitSettleCannotPassAfterTheDeadline(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	start := deps.Now()
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	var mu sync.Mutex
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		mu.Lock()
		defer mu.Unlock()
		elapsed := deps.Now().Sub(start)
		if elapsed < time.Second {
			return 200, `{"turns":[{"id":"t0","state":"running"},{"id":"t2","state":"queued"}]}`
		}
		if elapsed >= 5*time.Second {
			deps.Sleep(time.Second) // this read itself runs past the 5.5 s deadline
		}
		return 200, `{"turns":[{"id":"t0","state":"completed"},{"id":"t2","state":"running"}]}`
	})

	out, _, err := executeCLI(t, deps, "task", "wait", "repo-orchestrator:t2", "--timeout", "5500ms", "--json")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit = %d (%v), want 1: the turn ran only 4 s before the deadline", got, err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "failed" || !strings.Contains(res["reason"].(string), "only just started") {
		t.Fatalf("result = %v", res)
	}
}

// The settle clock starts when the running turn is first seen, at the end of
// that read, not when the read began.
func TestTaskWaitSettleStartsWhenRunningIsFirstSeen(t *testing.T) {
	d := newTaskDaemon(t)
	deps := fakeClockDeps()
	d.set("GET /api/v1/sessions/repo-1", 200, `{"session":{"id":"repo-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	var mu sync.Mutex
	var seenAt time.Time
	d.setFunc("GET /api/v1/sessions/repo-1/conversation", func() (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if seenAt.IsZero() {
			deps.Sleep(2 * time.Second) // the first running read is slow
			seenAt = deps.Now()
		}
		return 200, `{"turns":[{"id":"t1","state":"running"}]}`
	})

	out, _, err := executeCLI(t, deps, "task", "wait", "repo-1", "--json")
	if err != nil {
		t.Fatalf("exit = %d (%v), want 0", ExitCode(err), err)
	}
	if res := decodeTaskResult(t, out); res["status"] != "running" {
		t.Fatalf("result = %v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if settled := deps.Now().Sub(seenAt); settled < 5*time.Second {
		t.Fatalf("confirmed %s after the turn was first seen running; want at least 5s", settled)
	}
}
