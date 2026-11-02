package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/mcpapp"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type appStub struct {
	*fakeConversationService
	called bool
}

func (s *appStub) MCPAppSnapshot(context.Context, string, string) (chatsvc.MCPAppSnapshot, error) {
	return chatsvc.MCPAppSnapshot{Ready: true, Arguments: json.RawMessage(`{}`)}, nil
}
func (s *appStub) MCPAppDocument(context.Context, string, string) (mcpapp.Document, error) {
	return mcpapp.Document{HTML: "<h1>App</h1>"}, nil
}
func (s *appStub) MCPAppAction(context.Context, string, string, chatsvc.MCPAppRequest) (chatsvc.MCPAppResponse, error) {
	s.called = true
	return chatsvc.MCPAppResponse{Result: json.RawMessage(`{}`)}, nil
}

func TestMCPAppRoutesAllowSandboxNavigationButRejectOpaqueActions(t *testing.T) {
	svc := &appStub{fakeConversationService: &fakeConversationService{}}
	server := renderRouter(t, t.TempDir(), svc)
	for _, tc := range []struct {
		method, path, origin string
		want                 int
	}{
		{"GET", "/document", "", 200},
		{"POST", "/actions", "null", 403},
		{"GET", "", "null", 403},
	} {
		request, _ := http.NewRequest(tc.method, server.URL+"/api/v1/conversations/c/mcp-apps/a"+tc.path, strings.NewReader(`{"method":"tools/call","params":{"name":"write"}}`))
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		if tc.origin != "" {
			request.Header.Set("Origin", tc.origin)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != tc.want {
			t.Fatalf("%s: status %d %s", tc.path, response.StatusCode, body)
		}
		if tc.path == "/document" && (!strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox allow-scripts") || strings.Contains(response.Header.Get("Content-Security-Policy"), "allow-same-origin")) {
			t.Fatal("document escaped opaque sandbox")
		}
	}
	if svc.called {
		t.Fatal("untrusted page reached an action")
	}
}
