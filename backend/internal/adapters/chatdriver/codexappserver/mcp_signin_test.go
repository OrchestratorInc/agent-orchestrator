package codexappserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSignInMCPServerAsksCodexForThePage(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo(codexproto.MethodMcpServerOauthLogin, `{"authorizationUrl":"https://mcp.example/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A52136%2Fcallback"}`)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	page, err := conv.(ports.ChatMCPSigner).SignInMCPServer(context.Background(), "tracker")
	if err != nil || page != "https://mcp.example/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A52136%2Fcallback" {
		t.Fatalf("SignInMCPServer = %q, %v", page, err)
	}
	sent := srv.awaitFrame(func(f frame) bool { return f.Method == codexproto.MethodMcpServerOauthLogin })
	var params codexproto.McpServerOauthLoginParams
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Name != "tracker" || params.ThreadID == nil || *params.ThreadID != "thread-1" || params.TimeoutSecs == nil || *params.TimeoutSecs != 600 {
		t.Fatalf("params = %+v", params)
	}
	if !conv.Capabilities().Has(ports.ChatCapabilityMCPSignIn) {
		t.Fatal("the driver does not declare the sign-in")
	}
}

func TestStartupOfAServerWithNoLoginReadsAsNeedsSignIn(t *testing.T) {
	cases := map[string]string{
		`{"name":"tracker","status":"failed","error":"The tracker MCP server is not logged in. Run ` + "`codex mcp login tracker`" + `."}`: mcpNeedsSignIn,
		`{"name":"tracker","status":"failed","failureReason":"reauthenticationRequired","error":"token refused"}`:                          mcpNeedsSignIn,
		`{"name":"tracker","status":"failed","error":"spawn tracker ENOENT"}`:                                                              "failed",
		`{"name":"tracker","status":"ready"}`: "ready",
	}
	for params, want := range cases {
		events := normalizeNotification(notification{Method: codexproto.MethodMcpServerStartupStatusUpdated, Params: json.RawMessage(params)}, time.Now())
		if len(events) != 1 || len(events[0].MCPServers) != 1 || events[0].MCPServers[0].Status != want {
			t.Fatalf("%s -> %+v, want status %q", params, events, want)
		}
	}
}

func TestACompletedSignInReloadsTheServersWhenNoTurnRuns(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo(codexproto.MethodConfigMcpServerReload, `{}`)
	srv.respondTo(codexproto.MethodMcpServerOauthLogin, `{"authorizationUrl":"https://mcp.example/authorize"}`)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()
	reloads := func() int {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		count := 0
		for _, f := range srv.seen {
			if f.Method == codexproto.MethodConfigMcpServerReload {
				count++
			}
		}
		return count
	}
	settle := func() {
		t.Helper()
		if _, err := conv.(ports.ChatMCPSigner).SignInMCPServer(context.Background(), "tracker"); err != nil {
			t.Fatalf("SignInMCPServer: %v", err)
		}
	}

	srv.push(`{"method":"mcpServer/oauthLogin/completed","params":{"name":"tracker","success":false,"error":"timed out waiting for OAuth callback"}}`)
	srv.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"inProgress","items":[]}}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	if srv.sentMethod(codexproto.MethodConfigMcpServerReload) {
		t.Fatal("a sign-in that did not complete reloaded the servers")
	}
	srv.push(`{"method":"mcpServer/oauthLogin/completed","params":{"name":"tracker","success":true}}`)
	srv.push(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","items":[]}}}`)
	nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	srv.awaitFrame(func(f frame) bool { return f.Method == codexproto.MethodConfigMcpServerReload })
	settle()
	if got := reloads(); got != 1 {
		t.Fatalf("reloads after the turn = %d, want 1", got)
	}

	srv.push(`{"method":"mcpServer/oauthLogin/completed","params":{"name":"tracker","success":true}}`)
	deadline := time.Now().Add(3 * time.Second)
	for reloads() != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("reloads = %d, want 2: a chat with no running turn must reload at once", reloads())
		}
		time.Sleep(10 * time.Millisecond)
	}
	settle()
}
