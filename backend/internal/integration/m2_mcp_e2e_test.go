package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cli"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// R-03 end to end: the REAL CLI MCP server, driven in-process, over the real wire,
// service, and SQLite stack.
//
// The chain under test is
//
//	ao mcp (real)  →  HTTP  →  httpd controllers  →  collaboration/workflow service
//	               →  SQLite (workflow_messages, workflow_task_requests)
//	               →  change_log CDC triggers
//
// Nothing here starts a provider: an MCP tool is an HTTP call, and the identity it
// acts under comes only from the subprocess environment the daemon would have set.

// m2MCPEnv is one workflow attempt's MCP environment, exactly as the session manager
// builds it for a workflow session.
type m2MCPEnv struct {
	sessionID  string
	workflowID string
	attemptID  string
	lease      string
	capability string
}

// mcpToolReply is one JSON-RPC reply from the real server.
type mcpToolReply struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]any `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r mcpToolReply) text() string {
	var parts []string
	for _, block := range r.Result.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// mcpEnvVars installs one identity into the process environment for the duration of
// a test. The real CLI reads exactly these variables.
func mcpEnvVars(t *testing.T, env m2MCPEnv) {
	t.Helper()
	t.Setenv("AO_WORKFLOW_ID", env.workflowID)
	t.Setenv("AO_ATTEMPT_ID", env.attemptID)
	t.Setenv("AO_WORKFLOW_LEASE", "")
	t.Setenv("AO_BROWSER_CAPABILITY", env.capability)
	t.Setenv("AO_SESSION_ID", env.sessionID)
}

// mcpDeps points the CLI's daemon client at the fixture's httptest server by writing
// the same run-file a live daemon writes.
func mcpDeps(t *testing.T, env *m2Env) cli.Deps {
	t.Helper()
	port := m2ServerPort(t, env.srv.URL)
	runFile := filepath.Join(t.TempDir(), "running.json")
	info := runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Unix(100, 0).UTC()}
	if err := runfile.Write(runFile, info); err != nil {
		t.Fatalf("write run-file: %v", err)
	}
	t.Setenv("AO_RUN_FILE", runFile)
	t.Setenv("AO_DATA_DIR", t.TempDir())
	t.Setenv("AO_PORT", "3001")
	t.Setenv("AO_PROJECT_ID", "")
	t.Setenv("AO_REQUEST_TIMEOUT", "")
	t.Setenv("AO_SHUTDOWN_TIMEOUT", "")
	return cli.Deps{ProcessAlive: func(int) bool { return true }}
}

// m2ServerPort extracts the port from an httptest URL.
func m2ServerPort(t *testing.T, raw string) int {
	t.Helper()
	port, err := strconv.Atoi(raw[strings.LastIndex(raw, ":")+1:])
	if err != nil || port == 0 {
		t.Fatalf("parse server port from %q: %v", raw, err)
	}
	return port
}

// runRealMCP drives the shipped stdio server and returns its replies.
func runRealMCP(t *testing.T, deps cli.Deps, lines ...string) []mcpToolReply {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := cli.ServeMCP(context.Background(), deps, in, &out); err != nil {
		t.Fatalf("ServeMCP: %v", err)
	}
	var replies []mcpToolReply
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var reply mcpToolReply
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatalf("reply %q: %v", line, err)
		}
		replies = append(replies, reply)
	}
	return replies
}

// mcpCallLine builds one tools/call request.
func mcpCallLine(t *testing.T, id int, tool string, args map[string]any) string {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": json.RawMessage(params),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

// m2MCPFixture binds two attempts in two projects and returns their identities.
//
// It reuses the shared M2 fixture wholesale: the same store, capability resolver,
// gateway, coordinator, workflow service, and HTTP router the other M2 scenarios use.
func m2MCPFixture(t *testing.T, key string) (*m2Env, m2MCPEnv, m2MCPEnv) {
	t.Helper()
	env := newM2Env(t, openM2Store(t))
	workflowID, tasks := env.createWorkflow(t, key)

	// Attempt A owns the steerable task, attempt B the queue-only task. Each gets a
	// real lease from the real claim path, a real bound session, and a real
	// capability minted against that session's persisted verifier.
	attemptA, leaseA := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], key+"-a")
	boundA := env.bindAttempt(t, workflowID, attemptA, m2SteerHarness, key+"-a", "work")
	if boundA.Status != http.StatusOK {
		t.Fatalf("bind A: status=%d body=%s", boundA.Status, boundA.Raw)
	}
	sessionA := domain.SessionID(str(dig(boundA.Body, "binding", "sessionId")))
	capabilityA := m2IssueCapabilityToken(t, env, sessionA)

	attemptB, leaseB := env.claimAndStart(t, workflowID, tasks[m2QueueHarness], key+"-b")
	boundB := env.bindAttempt(t, workflowID, attemptB, m2QueueHarness, key+"-b", "work")
	if boundB.Status != http.StatusOK {
		t.Fatalf("bind B: status=%d body=%s", boundB.Status, boundB.Raw)
	}
	sessionB := domain.SessionID(str(dig(boundB.Body, "binding", "sessionId")))
	capabilityB := m2IssueCapabilityToken(t, env, sessionB)

	return env,
		m2MCPEnv{sessionID: string(sessionA), workflowID: string(workflowID), attemptID: string(attemptA), lease: leaseA, capability: capabilityA},
		m2MCPEnv{sessionID: string(sessionB), workflowID: string(workflowID), attemptID: string(attemptB), lease: leaseB, capability: capabilityB}
}

// m2IssueCapabilityToken is m2IssueCapability without the verifier return value.
func m2IssueCapabilityToken(t *testing.T, env *m2Env, sessionID domain.SessionID) string {
	t.Helper()
	token, _ := m2IssueCapability(t, env, sessionID)
	return token
}

// m2MCPTaskID reads the task id attempt A is scoped to, which is B's task for the
// addressed-message assertion.
func m2CountWorkflowMessageRows(t *testing.T, st *store.Store, workflowID domain.WorkflowID) int {
	t.Helper()
	rows, err := st.ListWorkflowMessages(context.Background(), workflowID, 100, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	return len(rows)
}

// m2ChangeLogForMessage reads change_log rows for one message id directly from the
// store's CDC surface, so the assertion is about the trigger rather than a manual emit.
type m2CDCEvent struct {
	Type    string
	Payload map[string]any
}

func m2ChangeLogMessageEvents(t *testing.T, st *store.Store, messageID string) []m2CDCEvent {
	t.Helper()
	events, err := st.EventsAfter(context.Background(), 0, 10000)
	if err != nil {
		t.Fatalf("read change_log: %v", err)
	}
	var out []m2CDCEvent
	for _, event := range events {
		if !strings.HasPrefix(string(event.Type), "workflow_message_") {
			continue
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(event.Payload, &decoded); err != nil {
			t.Fatalf("decode %s payload: %v", event.Type, err)
		}
		if str(decoded["messageId"]) != messageID {
			continue
		}
		out = append(out, m2CDCEvent{Type: string(event.Type), Payload: decoded})
	}
	return out
}

// TestM2MCPToolsOverTheRealServer is the R-03 acceptance scenario.
func TestM2MCPToolsOverTheRealServer(t *testing.T) {
	env, identityA, identityB := m2MCPFixture(t, "mcp-e2e")
	deps := mcpDeps(t, env)
	workflowID := domain.WorkflowID(identityA.workflowID)

	// --- tools/list: the five tools exist for a workflow attempt -----------------
	clearEnv := func() {
		for _, key := range []string{"AO_WORKFLOW_ID", "AO_ATTEMPT_ID", "AO_WORKFLOW_LEASE", "AO_BROWSER_CAPABILITY"} {
			t.Setenv(key, "")
		}
	}
	clearEnv()
	plain := runRealMCP(t, deps, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)[0]
	for _, tool := range plain.Result.Tools {
		if strings.HasPrefix(tool.Name, "workflow_") {
			t.Fatalf("a non-workflow session was offered %q", tool.Name)
		}
	}

	mcpEnvVars(t, identityA)
	listed := runRealMCP(t, deps, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)[0]
	offered := map[string]bool{}
	for _, tool := range listed.Result.Tools {
		offered[tool.Name] = true
	}
	wantTools := []string{
		"workflow_message_send", "workflow_messages_list", "workflow_task_request_create",
		"workflow_task_request_get", "workflow_task_requests_list",
	}
	for _, want := range wantTools {
		if !offered[want] {
			t.Fatalf("tools/list is missing %q; got %v", want, offered)
		}
	}
	// The schema is the forgeability boundary: no identity property exists anywhere.
	for _, tool := range listed.Result.Tools {
		if !strings.HasPrefix(tool.Name, "workflow_") {
			continue
		}
		for property := range tool.InputSchema.Properties {
			switch strings.ToLower(property) {
			case "workflowid", "attemptid", "lease", "leasetoken", "capability", "sender", "sendertaskid":
				t.Fatalf("%s exposes the identity field %q", tool.Name, property)
			}
		}
	}

	// --- workflow_message_send: one row, one CDC event, derived sender -----------
	messagesBefore := m2CountWorkflowMessageRows(t, env.store, workflowID)
	sendArgs := map[string]any{
		"kind": "progress", "body": "half done", "toTaskId": identityB.attemptID,
		"clientMessageId": "mcp-e2e-msg-1",
	}
	// toTaskId must be B's TASK, not its attempt; the daemon rejects an unknown task.
	sendArgs["toTaskId"] = m2TaskIDForAttempt(t, env, domain.WorkflowAttemptID(identityB.attemptID))
	sent := runRealMCP(t, deps, mcpCallLine(t, 2, "workflow_message_send", sendArgs))[0]
	if sent.Result.IsError {
		t.Fatalf("workflow_message_send failed: %s", sent.text())
	}
	var sentPayload map[string]any
	if err := json.Unmarshal([]byte(sent.text()), &sentPayload); err != nil {
		t.Fatalf("decode send result %q: %v", sent.text(), err)
	}
	messageID := str(sentPayload["id"])
	if messageID == "" {
		t.Fatalf("send result names no message: %s", sent.text())
	}
	if boolean(sentPayload["created"]) != true {
		t.Errorf("created = %v on a first message", sentPayload["created"])
	}
	if got := str(sentPayload["senderKind"]); got != "agent" {
		t.Errorf("senderKind = %q, want agent (derived, never supplied)", got)
	}
	if got := str(sentPayload["fromTaskId"]); got == "" {
		t.Error("the daemon recorded no sender task")
	}

	messagesAfter := m2CountWorkflowMessageRows(t, env.store, workflowID)
	if messagesAfter-messagesBefore != 1 {
		t.Fatalf("stored messages went from %d to %d; want exactly one new row", messagesBefore, messagesAfter)
	}
	rows, err := env.store.ListWorkflowMessages(context.Background(), workflowID, 100, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	row := rows[len(rows)-1]
	if row.SenderKind != domain.WorkflowMessageSenderAgent {
		t.Fatalf("sender kind = %q, want agent", row.SenderKind)
	}
	if row.SenderTaskID == nil || string(*row.SenderTaskID) != m2TaskIDForAttempt(t, env, domain.WorkflowAttemptID(identityA.attemptID)) {
		t.Fatalf("sender task = %v, want attempt A's task", row.SenderTaskID)
	}

	// The CDC row is the trigger's, and its payload key set is the trigger's too.
	events := m2ChangeLogMessageEvents(t, env.store, messageID)
	if len(events) != 1 {
		t.Fatalf("change_log rows for message %s = %d, want exactly 1", messageID, len(events))
	}
	if events[0].Type != "workflow_message_created" {
		t.Fatalf("event type = %q, want workflow_message_created", events[0].Type)
	}
	if str(events[0].Payload["kind"]) != "workflow_message" {
		t.Errorf("payload kind = %v, want workflow_message", events[0].Payload["kind"])
	}
	if str(events[0].Payload["workflowId"]) != identityA.workflowID {
		t.Errorf("payload workflowId = %v, want %s", events[0].Payload["workflowId"], identityA.workflowID)
	}
	for _, secret := range []string{identityA.lease, identityA.capability} {
		if strings.Contains(string(mustJSON(t, events[0].Payload)), secret) {
			t.Fatal("the CDC payload carries a credential")
		}
	}

	// --- idempotency: the same clientMessageId adds no row ----------------------
	repeat := runRealMCP(t, deps, mcpCallLine(t, 3, "workflow_message_send", sendArgs))[0]
	if repeat.Result.IsError {
		t.Fatalf("repeated send failed: %s", repeat.text())
	}
	var repeatPayload map[string]any
	if err := json.Unmarshal([]byte(repeat.text()), &repeatPayload); err != nil {
		t.Fatal(err)
	}
	if str(repeatPayload["id"]) != messageID {
		t.Fatalf("repeat returned message %q, want the original %q", repeatPayload["id"], messageID)
	}
	if boolean(repeatPayload["created"]) {
		t.Error("a replay reported created=true")
	}
	if got := m2CountWorkflowMessageRows(t, env.store, workflowID); got != messagesAfter {
		t.Fatalf("a replayed send added a row: %d then %d", messagesAfter, got)
	}
	if got := len(m2ChangeLogMessageEvents(t, env.store, messageID)); got != 1 {
		t.Fatalf("a replayed send emitted another CDC event: %d", got)
	}

	// --- messages list reads it back -------------------------------------------
	listedMessages := runRealMCP(t, deps, mcpCallLine(t, 4, "workflow_messages_list", map[string]any{"limit": 10}))[0]
	if listedMessages.Result.IsError {
		t.Fatalf("workflow_messages_list failed: %s", listedMessages.text())
	}
	if !strings.Contains(listedMessages.text(), messageID) {
		t.Fatalf("message list does not contain %s: %s", messageID, listedMessages.text())
	}

	// --- task requests: create, get, list --------------------------------------
	created := runRealMCP(t, deps, mcpCallLine(t, 5, "workflow_task_request_create", map[string]any{
		"requestId": "mcp-e2e-req-1", "requestKind": "create_task",
		"payload": map[string]any{"title": "another task"},
	}))[0]
	if created.Result.IsError {
		t.Fatalf("workflow_task_request_create failed: %s", created.text())
	}
	var createdPayload map[string]any
	if err := json.Unmarshal([]byte(created.text()), &createdPayload); err != nil {
		t.Fatal(err)
	}
	if str(createdPayload["state"]) != "pending" {
		t.Fatalf("new request state = %v, want pending", createdPayload["state"])
	}

	got := runRealMCP(t, deps, mcpCallLine(t, 6, "workflow_task_request_get", map[string]any{
		"requestId": "mcp-e2e-req-1",
	}))[0]
	if got.Result.IsError {
		t.Fatalf("workflow_task_request_get failed: %s", got.text())
	}
	var gotPayload map[string]any
	if err := json.Unmarshal([]byte(got.text()), &gotPayload); err != nil {
		t.Fatal(err)
	}
	if str(gotPayload["id"]) != "mcp-e2e-req-1" || str(gotPayload["state"]) != "pending" {
		t.Fatalf("read-back request = %v, want the pending request", gotPayload)
	}

	listedRequests := runRealMCP(t, deps, mcpCallLine(t, 7, "workflow_task_requests_list", map[string]any{"limit": 10}))[0]
	if listedRequests.Result.IsError {
		t.Fatalf("workflow_task_requests_list failed: %s", listedRequests.text())
	}
	if !strings.Contains(listedRequests.text(), "mcp-e2e-req-1") {
		t.Fatalf("request list does not contain the created request: %s", listedRequests.text())
	}

	// --- cross-workflow isolation ----------------------------------------------
	// A second workflow exists; A's credentials must not reach it. This half uses the
	// HTTP client directly because it proves the SERVER refuses, not that a tool exists.
	otherWorkflowID, _ := env.createWorkflow(t, "mcp-e2e-other")
	for _, route := range []string{"/messages", "/task-requests"} {
		crossed := env.doWithHeaders(t, http.MethodGet,
			"/workflows/"+string(otherWorkflowID)+route, "",
			map[string]string{
				principal.HeaderAgentCapability: identityA.capability,
				principal.HeaderSessionID:       identityA.sessionID,
			})
		if crossed.Status != http.StatusNotFound && crossed.Status != http.StatusForbidden {
			t.Fatalf("GET %s with A's headers: status=%d, want 404 or 403; body=%s",
				route, crossed.Status, crossed.Raw)
		}
	}

	// --- no secret anywhere in any reply ---------------------------------------
	allReplies := []mcpToolReply{sent, repeat, listedMessages, created, got, listedRequests, listed}
	for _, reply := range allReplies {
		text := reply.text()
		for _, secret := range []string{identityA.lease, identityA.capability} {
			if secret != "" && strings.Contains(text, secret) {
				t.Fatalf("a reply body contains a credential: %s", text)
			}
		}
	}
}

// m2TaskIDForAttempt reads the task an attempt belongs to.
func m2TaskIDForAttempt(t *testing.T, env *m2Env, attemptID domain.WorkflowAttemptID) string {
	t.Helper()
	attempt, found, err := env.store.GetWorkflowAttempt(context.Background(), attemptID)
	if err != nil || !found {
		t.Fatalf("read attempt %s: found=%v err=%v", attemptID, found, err)
	}
	return string(attempt.TaskID)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
