package codexappserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNormalizeMCPAppPreservesFullInputAndResult(t *testing.T) {
	for _, metadata := range []string{`"mcpAppUi":{"resourceUri":"ui://app"}`, `"appContext":{"resourceUri":"ui://app"}`, `"mcpAppResourceUri":"ui://app"`} {
		large := strings.Repeat("a", 50_000)
		ev := normalizeOne(t, codexproto.MethodItemCompleted, `{"threadId":"child-thread","turnId":"t","item":{"id":"app","type":"mcpToolCall","server":"s","tool":"view",`+metadata+`,"arguments":{"query":"`+large+`"},"result":{"content":[],"structuredContent":{"value":"`+large+`"}}}}`)
		if ev.MCPApp == nil || ev.MCPApp.ResourceURI != "ui://app" || ev.MCPApp.ThreadID != "child-thread" || !strings.Contains(string(ev.MCPApp.Arguments), large) || !strings.Contains(string(ev.MCPApp.Result), large) {
			t.Fatal("app payload was dropped or truncated")
		}
	}
}

func TestMCPAppNativeTransportUsesOriginatingThread(t *testing.T) {
	d, server := newTestDriver(t)
	server.respondSequence(codexproto.MethodMcpServerStatusList,
		`{"data":[{"name":"other","tools":{}}],"nextCursor":"next"}`,
		`{"data":[{"name":"analytics","tools":{"show":{"name":"show","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}}}]}`)
	server.respondTo(codexproto.MethodMcpServerResourceRead, `{"contents":[{"uri":"ui://app","mimeType":"text/html;profile=mcp-app","text":"app"}]}`)
	server.respondTo(codexproto.MethodMcpServerToolCall, `{"content":[],"structuredContent":{"value":3}}`)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	apps := conv.(ports.ChatMCPApps)
	tools, err := apps.MCPAppTools(context.Background(), "child", "analytics")
	if err != nil || len(tools) != 1 || !strings.Contains(string(tools[0]), `"readOnlyHint":true`) {
		t.Fatalf("tools: %s %v", tools, err)
	}
	resource, err := apps.MCPAppResource(context.Background(), "child", "analytics", "ui://app")
	if err != nil || !strings.Contains(string(resource), `"contents"`) {
		t.Fatalf("resource: %s %v", resource, err)
	}
	result, err := apps.MCPAppCall(context.Background(), "child", "analytics", "show", json.RawMessage(`{"a":1}`))
	if err != nil || !strings.Contains(string(result), `"value":3`) {
		t.Fatalf("call: %s %v", result, err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	count := 0
	for _, request := range server.seen {
		switch request.Method {
		case codexproto.MethodMcpServerStatusList, codexproto.MethodMcpServerResourceRead, codexproto.MethodMcpServerToolCall:
			var params map[string]json.RawMessage
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			if string(params["threadId"]) != `"child"` {
				t.Fatalf("wrong thread: %s", request.Params)
			}
			if request.Method != codexproto.MethodMcpServerStatusList && string(params["server"]) != `"analytics"` {
				t.Fatalf("wrong server: %s", request.Params)
			}
			count++
		}
	}
	if count != 4 {
		t.Fatalf("requests = %d, want 4", count)
	}
}

func TestNormalizeMCPAppRejectsNonUIResource(t *testing.T) {
	ev := normalizeOne(t, codexproto.MethodItemCompleted, `{"threadId":"t","turnId":"turn","item":{"id":"app","type":"mcpToolCall","server":"s","tool":"view","mcpAppResourceUri":"http://localhost:3001"}}`)
	if ev.MCPApp != nil {
		t.Fatal("accepted non-app resource")
	}
}
