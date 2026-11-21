package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
		{name: "first turn queued", sess: chat, conv: `{"turns":[{"id":"t1","state":"queued"}]}`, wantStatus: taskStatusStarting},
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
	d.set("POST /api/v1/agents/codex/probe", 200, `{"agent":{"id":"codex","authStatus":"authorized"},"supported":true,"installed":true}`)
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

	out, _, err := executeCLI(t, deps, "task", "start", "fix calc", "--project", "repo", "--agent", "codex", "--base", "feat/x", "--wait", "--json")
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
	if !strings.HasPrefix(msg["text"], "fix calc\n\n") || !strings.Contains(msg["text"], "`--agent codex`") || !strings.Contains(msg["text"], "`--base feat/x`") {
		t.Fatalf("message = %q", msg["text"])
	}
	if !strings.HasPrefix(msg["clientMessageId"], "ao-task-") {
		t.Fatalf("clientMessageId = %q", msg["clientMessageId"])
	}
	if !d.saw("POST /api/v1/agents/codex/probe") {
		t.Fatal("the worker agent named by --agent should be checked too")
	}
}

func TestTaskStartOrchestratorTerminalFallsBackToSend(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("POST /api/v1/orchestrators", 201, `{"orchestrator":{"id":"repo-orchestrator","projectId":"repo"}}`)
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"tui","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/conversation/messages", 409, `{"error":"conflict","code":"SESSION_MODE_MISMATCH","message":"no chat conversation"}`)
	d.set("POST /api/v1/sessions/repo-orchestrator/send", 200, `{"ok":true,"sessionId":"repo-orchestrator","message":"fix calc"}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "start", "fix calc", "--project", "repo")
	if err != nil {
		t.Fatalf("exit = %d: %v", ExitCode(err), err)
	}
	if !strings.HasPrefix(out, "task repo-orchestrator: running") {
		t.Fatalf("stdout = %q", out)
	}
	if d.saw("GET /api/v1/sessions/repo-orchestrator/conversation") {
		t.Fatal("a terminal session has no conversation to read")
	}
}

func TestTaskWaitNeedsInputExitsTwo(t *testing.T) {
	d := newTaskDaemon(t)
	d.set("GET /api/v1/sessions/repo-3", 200, `{"session":{"id":"repo-3","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"waiting_input"}}}`)
	d.set("GET /api/v1/sessions/repo-3/conversation", 200, `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "task", "wait", "repo-3", "--json")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
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
	if res := decodeTaskResult(t, out); res["reason"] != "session repo-9 not found" {
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
	if got := ExitCode(taskExitError{code: 2, msg: "waiting"}); got != 2 {
		t.Fatalf("ExitCode(needs input) = %d", got)
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
	err := exec.Command("sh", "-c", "exit 2").Run()
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
	d.set("GET /api/v1/sessions/repo-orchestrator", 200, `{"session":{"id":"repo-orchestrator","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.setFunc("GET /api/v1/sessions/repo-orchestrator/conversation", func() (int, string) {
		d.mu.Lock()
		query := d.lastQuery
		d.mu.Unlock()
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
	if d.saw("GET /api/v1/sessions/repo-orchestrator/conversation") && d.lastQuery != "beforeSequence=100" {
		t.Fatalf("stopped paging at %q; want it to stop once the earlier turn t1 is loaded", d.lastQuery)
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

func TestTaskWaitTerminalWorkThenPromptIsDone(t *testing.T) {
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
	if res := decodeTaskResult(t, out); res["status"] != "done" {
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
