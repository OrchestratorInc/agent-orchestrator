package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The agent-facing workflow tools, driven through the real stdio server over an
// httptest daemon. Nothing here starts a provider.

const (
	envMCPLeaseToken   = "AO_WORKFLOW_LEASE"
	headerAttemptLease = "X-AO-Attempt-Lease"
	testWorkflowID     = "workflow-mcp"
	testSessionID      = "session-mcp-a"
	testAttemptID      = "attempt-mcp-a"
	testLeaseToken     = "lease-secret-value"
	testCapability     = "capability-secret-value"
	testMessagePath    = "/api/v1/workflows/" + testWorkflowID + "/messages"
)

// mcpDaemonCapture records every request an `ao mcp` server made.
type mcpDaemonCapture struct {
	mu       sync.Mutex
	requests []mcpDaemonRequest
}

type mcpDaemonRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

func (c *mcpDaemonCapture) add(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, mcpDaemonRequest{
		Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery,
		Header: req.Header.Clone(), Body: string(body),
	})
}

func (c *mcpDaemonCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *mcpDaemonCapture) all() []mcpDaemonRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]mcpDaemonRequest(nil), c.requests...)
}

// newWorkflowMCPDaemon starts a stub daemon and points the CLI at it.
func newWorkflowMCPDaemon(
	t *testing.T,
	handler func(w http.ResponseWriter, r *http.Request, capture *mcpDaemonCapture),
) (*mcpDaemonCapture, Deps) {
	t.Helper()
	capture := &mcpDaemonCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.add(r)
		handler(w, r, capture)
	}))
	t.Cleanup(srv.Close)
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, srv)
	return capture, Deps{ProcessAlive: func(int) bool { return true }}
}

// runWorkflowMCP drives the real stdio server in-process and returns its replies.
func runWorkflowMCP(t *testing.T, deps Deps, lines ...string) []mcpReply {
	t.Helper()
	replies, err := serveMCPLines(context.Background(), deps, lines...)
	if err != nil {
		t.Fatalf("ServeMCP: %v", err)
	}
	return replies
}

// serveMCPLines is the shared driver: it feeds newline-delimited JSON to the
// exported seam and decodes the replies.
func serveMCPLines(ctx context.Context, deps Deps, lines ...string) ([]mcpReply, error) {
	var out strings.Builder
	if err := ServeMCP(ctx, deps, strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		return nil, err
	}
	var replies []mcpReply
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var reply mcpReply
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			return nil, err
		}
		replies = append(replies, reply)
	}
	return replies, nil
}

// setWorkflowMCPEnv installs the identity a workflow session's `ao mcp` receives.
func setWorkflowMCPEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AO_SESSION_ID", testSessionID)
	t.Setenv(envMCPWorkflowID, testWorkflowID)
	t.Setenv(envMCPTaskID, "task-mcp-a")
	t.Setenv(envMCPAttemptID, testAttemptID)
	t.Setenv(envMCPLeaseToken, "")
	t.Setenv(envMCPCapability, testCapability)
}

// clearWorkflowMCPEnv models an ordinary (non-workflow) session.
func clearWorkflowMCPEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"AO_SESSION_ID", envMCPWorkflowID, envMCPTaskID, envMCPAttemptID, envMCPLeaseToken, envMCPCapability} {
		t.Setenv(key, "")
	}
}

func workflowToolNames() []string {
	return []string{
		"workflow_message_send", "workflow_messages_list", "workflow_task_request_create",
		"workflow_task_request_get", "workflow_task_requests_list",
	}
}

// TestMCPWorkflowToolsListedOnlyForWorkflowSessions is the identity gate at the
// discovery layer: a session without a workflow id is never offered tools it would
// only be refused.
func TestMCPWorkflowToolsListedOnlyForWorkflowSessions(t *testing.T) {
	clearWorkflowMCPEnv(t)
	capture, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, _ *http.Request, _ *mcpDaemonCapture) {
		http.Error(w, "unexpected", http.StatusTeapot)
	})
	plain := runWorkflowMCP(t, deps, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)[0].Result.Tools
	for _, tool := range plain {
		if slices.Contains(workflowToolNames(), tool.Name) {
			t.Fatalf("non-workflow session was offered %q", tool.Name)
		}
	}
	if capture.count() != 0 {
		t.Fatalf("tools/list made %d HTTP request(s); it must make none", capture.count())
	}

	setWorkflowMCPEnv(t)
	withWorkflow := runWorkflowMCP(t, deps, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)[0].Result.Tools
	var names []string
	for _, tool := range withWorkflow {
		names = append(names, tool.Name)
	}
	for _, want := range workflowToolNames() {
		if !slices.Contains(names, want) {
			t.Fatalf("workflow session is missing %q; got %v", want, names)
		}
	}
}

// TestMCPWorkflowSchemasDeclareNoIdentity is the "the model cannot forge identity"
// proof, asserted on the wire schema rather than on prose.
func TestMCPWorkflowSchemasDeclareNoIdentity(t *testing.T) {
	forbidden := []string{
		"workflowId", "workflowID", "attemptId", "attemptID", "lease", "leaseToken",
		"capability", "sender", "senderKind", "fromTaskId", "token",
	}
	for _, tool := range workflowToolSchemas {
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("%v has no input schema", tool["name"])
		}
		properties, _ := schema["properties"].(map[string]any)
		for name := range properties {
			for _, banned := range forbidden {
				if strings.EqualFold(name, banned) {
					t.Errorf("%v exposes identity field %q; the model must not be able to name it", tool["name"], name)
				}
			}
		}
		// The wire form is what a client sees, so serialize and search it too.
		encoded, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, banned := range []string{`"workflowId"`, `"attemptId"`, `"lease"`, `"capability"`, `"sender"`} {
			if strings.Contains(text, banned) {
				t.Errorf("%v serialized schema contains a forbidden identity key %s", tool["name"], banned)
			}
		}
	}
}

// TestMCPWorkflowMessageSendUsesEnvironmentIdentity 捕获真实 MCP HTTP 请求，旧环境
// 即使残留 lease 也不得外发；Session capability 与非秘密身份仍需完整发送。
func TestMCPWorkflowMessageSendUsesEnvironmentIdentity(t *testing.T) {
	setWorkflowMCPEnv(t)
	t.Setenv(envMCPLeaseToken, testLeaseToken)
	capture, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, r *http.Request, _ *mcpDaemonCapture) {
		if r.URL.Path != testMessagePath || r.Method != http.MethodPost {
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"message":{"id":"msg-1","workflowId":"`+testWorkflowID+
			`","fromTaskId":"task-mcp-a","senderKind":"agent","kind":"progress",`+
			`"deliveryState":"persisted","clientMessageId":"client-1"},"created":true}`)
	})

	reply := runWorkflowMCP(t, deps, mcpCall("workflow_message_send", map[string]any{
		"kind": "progress", "body": "half done", "toTaskId": "task-mcp-b", "clientMessageId": "client-1",
	}))[0]
	if reply.Result.IsError {
		t.Fatalf("tool reported an error: %+v", reply.Result.Content)
	}
	requests := capture.all()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	request := requests[0]
	if got := request.Header.Get(headerAgentCapability); got != testCapability {
		t.Errorf("%s = %q, want the environment capability", headerAgentCapability, got)
	}
	if got := request.Header.Get(headerAttemptID); got != testAttemptID {
		t.Errorf("%s = %q, want the environment attempt", headerAttemptID, got)
	}
	if got := request.Header.Get("X-AO-Session-Id"); got != testSessionID {
		t.Errorf("session header = %q, want the environment session", got)
	}
	if got := request.Header.Get(headerAttemptLease); got != "" {
		t.Error("legacy attempt lease was sent to the daemon")
	}
	// The workflow id is a path segment supplied by the environment, and the body
	// carries no identity at all.
	var body map[string]any
	if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
		t.Fatalf("request body %q: %v", request.Body, err)
	}
	for _, banned := range []string{"workflowId", "attemptId", "senderKind", "sender", "lease", "capability"} {
		if _, present := body[banned]; present {
			t.Errorf("request body declares %q; identity must never come from arguments", banned)
		}
	}
	if body["clientMessageId"] != "client-1" || body["toTaskId"] != "task-mcp-b" {
		t.Errorf("body = %v, want the caller's clientMessageId and toTaskId", body)
	}
	// The result echoes the daemon's answer and no secret.
	text := resultText(t, reply)
	for _, secret := range []string{testLeaseToken, testCapability} {
		if strings.Contains(text, secret) {
			t.Fatalf("tool result leaked a credential: %s", text)
		}
	}
}

// TestMCPWorkflowRejectsANamedWorkflowArgument proves an argument that tries to
// name another workflow is ignored rather than honored: the URL always comes from
// the environment.
func TestMCPWorkflowRejectsANamedWorkflowArgument(t *testing.T) {
	setWorkflowMCPEnv(t)
	capture, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, _ *http.Request, _ *mcpDaemonCapture) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"messages":[],"nextCursor":""}`)
	})
	runWorkflowMCP(t, deps, mcpCall("workflow_messages_list", map[string]any{
		"limit": 10, "workflowId": "someone-elses-workflow", "attemptId": "attempt-other",
	}))
	requests := capture.all()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if requests[0].Path != testMessagePath {
		t.Fatalf("path = %q, want the environment's workflow %q", requests[0].Path, testMessagePath)
	}
	if requests[0].Query != "limit=10" {
		t.Fatalf("query = %q, want limit=10", requests[0].Query)
	}
}

// TestMCPWorkflowClientMessageIDIsForwardedUnchanged proves idempotency is the
// caller's key, never a derived or secret value.
func TestMCPWorkflowClientMessageIDIsForwardedUnchanged(t *testing.T) {
	setWorkflowMCPEnv(t)
	capture, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, _ *http.Request, _ *mcpDaemonCapture) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"message":{"id":"msg-9","clientMessageId":"chosen-key"},"created":false}`)
	})
	const chosen = "chosen-key"
	for range 2 {
		reply := runWorkflowMCP(t, deps, mcpCall("workflow_message_send", map[string]any{
			"kind": "result", "body": "done", "clientMessageId": chosen,
		}))[0]
		if reply.Result.IsError {
			t.Fatalf("tool error: %+v", reply.Result.Content)
		}
	}
	requests := capture.all()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	for _, request := range requests {
		var body map[string]any
		if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
			t.Fatal(err)
		}
		if body["clientMessageId"] != chosen {
			t.Fatalf("clientMessageId = %v, want %q", body["clientMessageId"], chosen)
		}
	}
}

// TestMCPWorkflowRefusalIsReadableAndSecretFree proves a 403 envelope becomes a
// tool result naming the status and the public code, with no credential in it.
func TestMCPWorkflowRefusalIsReadableAndSecretFree(t *testing.T) {
	setWorkflowMCPEnv(t)
	_, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, _ *http.Request, _ *mcpDaemonCapture) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"COLLAB_NOT_AUTHORIZED","message":"The caller is not authorized to act on this resource"}`)
	})
	reply := runWorkflowMCP(t, deps, mcpCall("workflow_messages_list", map[string]any{}))[0]
	if !reply.Result.IsError {
		t.Fatal("a 403 must produce an isError tool result")
	}
	if reply.Error != nil {
		t.Fatalf("a daemon refusal must not be a protocol error: %+v", reply.Error)
	}
	text := resultText(t, reply)
	if !strings.Contains(text, "403") || !strings.Contains(text, "COLLAB_NOT_AUTHORIZED") {
		t.Fatalf("refusal text = %q, want the status and the public code", text)
	}
	for _, secret := range []string{testLeaseToken, testCapability} {
		if strings.Contains(text, secret) {
			t.Fatalf("refusal leaked a credential: %s", text)
		}
	}
}

// TestMCPWorkflowToolWithoutIdentityMakesNoRequest is the "no HTTP call" guarantee.
func TestMCPWorkflowToolWithoutIdentityMakesNoRequest(t *testing.T) {
	clearWorkflowMCPEnv(t)
	capture, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, _ *http.Request, _ *mcpDaemonCapture) {
		http.Error(w, "unexpected", http.StatusTeapot)
	})
	for _, name := range workflowToolNames() {
		reply := runWorkflowMCP(t, deps, mcpCall(name, map[string]any{
			"kind": "progress", "clientMessageId": "x", "requestId": "r", "requestKind": "create_task",
		}))[0]
		if !reply.Result.IsError {
			t.Fatalf("%s: an identity-less session must get an isError result", name)
		}
		if text := resultText(t, reply); !strings.Contains(text, "not a workflow attempt") {
			t.Fatalf("%s: result = %q, want a readable not-a-workflow-attempt message", name, text)
		}
	}
	if capture.count() != 0 {
		t.Fatalf("identity-less calls made %d HTTP request(s); they must make none", capture.count())
	}
}

// TestMCPWorkflowTaskRequestTools proves create/get/list hit the right routes and
// read the daemon's envelope back.
func TestMCPWorkflowTaskRequestTools(t *testing.T) {
	setWorkflowMCPEnv(t)
	capture, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, r *http.Request, _ *mcpDaemonCapture) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/task-requests"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"request":{"id":"req-1","workflowId":"`+testWorkflowID+
				`","requesterTaskId":"task-mcp-a","requestKind":"create_task","state":"pending","revision":1},"created":true}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/task-requests/req-1"):
			_, _ = io.WriteString(w, `{"request":{"id":"req-1","workflowId":"`+testWorkflowID+
				`","requesterTaskId":"task-mcp-a","requestKind":"create_task","state":"pending","revision":1}}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/task-requests"):
			_, _ = io.WriteString(w, `{"requests":[{"id":"req-1","state":"pending","revision":1}],"nextCursor":""}`)
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	})

	created := runWorkflowMCP(t, deps, mcpCall("workflow_task_request_create", map[string]any{
		"requestId": "req-1", "requestKind": "create_task", "payload": map[string]any{"title": "another"},
	}))[0]
	if created.Result.IsError {
		t.Fatalf("create: %+v", created.Result.Content)
	}
	if !strings.Contains(resultText(t, created), `"state":"pending"`) {
		t.Fatalf("create result = %q, want the pending state", resultText(t, created))
	}

	got := runWorkflowMCP(t, deps, mcpCall("workflow_task_request_get", map[string]any{"requestId": "req-1"}))[0]
	if got.Result.IsError || !strings.Contains(resultText(t, got), `"id":"req-1"`) {
		t.Fatalf("get result = %q", resultText(t, got))
	}

	listed := runWorkflowMCP(t, deps, mcpCall("workflow_task_requests_list", map[string]any{"limit": 5, "offset": 5}))[0]
	if listed.Result.IsError || !strings.Contains(resultText(t, listed), `"requests"`) {
		t.Fatalf("list result = %q", resultText(t, listed))
	}

	requests := capture.all()
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(requests))
	}
	if requests[2].Query != "limit=5&offset=5" {
		t.Fatalf("list query = %q, want limit=5&offset=5", requests[2].Query)
	}
	// 每条请求都携带 Session capability 和非秘密标识，不携带租约。
	for _, request := range requests {
		if request.Header.Get(headerAgentCapability) == "" ||
			request.Header.Get(headerAttemptID) == "" ||
			request.Header.Get("X-AO-Session-Id") == "" {
			t.Fatalf("%s %s omitted an identity header", request.Method, request.Path)
		}
		if request.Header.Get(headerAttemptLease) != "" {
			t.Fatal("legacy attempt lease was sent to the daemon")
		}
	}
}

// resultText joins every text block of a tool result.
func resultText(t *testing.T, reply mcpReply) string {
	t.Helper()
	var parts []string
	for _, block := range reply.Result.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
