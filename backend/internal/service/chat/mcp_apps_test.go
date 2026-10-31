package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type appConversation struct {
	*fakeConversation
	appMu sync.Mutex
	calls []string
}

func (c *appConversation) MCPAppTools(_ context.Context, thread, server string) ([]json.RawMessage, error) {
	if thread != "child-thread" || server != "app-server" {
		return nil, fmt.Errorf("wrong origin: %s %s", thread, server)
	}
	return []json.RawMessage{
		json.RawMessage(`{"name":"read","annotations":{"readOnlyHint":true}}`),
		json.RawMessage(`{"name":"write","_meta":{"ui":{"visibility":["app"]}}}`),
		json.RawMessage(`{"name":"hidden","annotations":{"readOnlyHint":true},"_meta":{"ui":{"visibility":["model"]}}}`),
	}, nil
}
func (c *appConversation) MCPAppResource(_ context.Context, thread, server, uri string) (json.RawMessage, error) {
	if thread != "child-thread" || server != "app-server" {
		return nil, fmt.Errorf("wrong origin: %s %s", thread, server)
	}
	return json.RawMessage(`{"contents":[{"uri":"ui://app","mimeType":"text/html;profile=mcp-app","text":"<h1>Saved app</h1>"}]}`), nil
}
func (c *appConversation) MCPAppCall(_ context.Context, thread, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	c.appMu.Lock()
	defer c.appMu.Unlock()
	if thread != "child-thread" || server != "app-server" {
		return nil, fmt.Errorf("wrong origin: %s %s", thread, server)
	}
	c.calls = append(c.calls, tool)
	return json.RawMessage(`{"content":[{"type":"text","text":"OK"}]}`), nil
}

func seedApp(t *testing.T) (*harness, *appConversation, string, string) {
	t.Helper()
	provider := &appConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, provider)
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "Show app"})
	if err != nil {
		t.Fatal(err)
	}
	provider.events <- ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderConversationID: "thread-1", ProviderTurnID: "provider-turn-1", ProviderItemID: "app-item", ActivityKind: domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted, Summary: "Called app", Detail: json.RawMessage(`{"server":"app-server","toolName":"show"}`), MCPApp: &domain.MCPAppData{Server: "app-server", Tool: "show", ResourceURI: "ui://app", ThreadID: "child-thread", Arguments: json.RawMessage(`{"page":1}`), Result: json.RawMessage(`{"content":[],"structuredContent":{"x":1}}`)}}
	provider.events <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderConversationID: "thread-1", ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) > 0 && len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	for _, activity := range snapshot.Activities {
		if activity.ProviderItemID == "app-item" {
			return h, provider, activity.ID, turn.ID
		}
	}
	t.Fatal("app activity missing")
	return nil, nil, "", ""
}

func TestMCPAppApprovalAndServerBoundary(t *testing.T) {
	h, provider, activity, _ := seedApp(t)
	ctx := context.Background()
	conversation := h.ctrl.ConversationID()
	request := chatsvc.MCPAppRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"write","arguments":{"value":1}}`)}
	approval, err := h.svc.MCPAppAction(ctx, conversation, activity, request)
	if err != nil || approval.ApprovalToken == "" {
		t.Fatalf("approval: %+v %v", approval, err)
	}
	provider.appMu.Lock()
	called := len(provider.calls)
	provider.appMu.Unlock()
	if called != 0 {
		t.Fatal("write ran without confirmation")
	}
	request.ApprovalToken = approval.ApprovalToken
	if _, err := h.svc.MCPAppAction(ctx, conversation, activity, request); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.MCPAppAction(ctx, conversation, activity, request); err == nil {
		t.Fatal("reused approval")
	}
	for _, name := range []string{"hidden", "another-server-tool"} {
		if _, err := h.svc.MCPAppAction(ctx, conversation, activity, chatsvc.MCPAppRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"` + name + `"}`)}); err == nil {
			t.Fatalf("allowed %s", name)
		}
	}
	read, err := h.svc.MCPAppAction(ctx, conversation, activity, chatsvc.MCPAppRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"read"}`)})
	if err != nil || read.ApprovalToken != "" || len(read.Result) == 0 {
		t.Fatalf("readonly: %+v %v", read, err)
	}
	if _, err := h.svc.MCPAppAction(ctx, "other-conversation", activity, request); err == nil {
		t.Fatal("crossed conversation boundary")
	}
}

func TestMCPAppSavedDocumentAndContextSurviveStop(t *testing.T) {
	h, provider, activity, _ := seedApp(t)
	ctx := context.Background()
	conversation := h.ctrl.ConversationID()
	snapshot, err := h.svc.MCPAppSnapshot(ctx, conversation, activity)
	if err != nil || !snapshot.Ready || !snapshot.Live {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	for _, text := range []string{"old context", "selected blue"} {
		_, err = h.svc.MCPAppAction(ctx, conversation, activity, chatsvc.MCPAppRequest{Method: "ui/update-model-context", Params: json.RawMessage(`{"content":[{"type":"text","text":"` + text + `"}]}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "Use the selection"}); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	input := provider.sent[len(provider.sent)-1].Text
	provider.mu.Unlock()
	if !strings.Contains(input, "selected blue") || strings.Contains(input, "old context") {
		t.Fatalf("input: %s", input)
	}
	if err = h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	snapshot, err = h.svc.MCPAppSnapshot(ctx, conversation, activity)
	if err != nil || !snapshot.Ready || snapshot.Live {
		t.Fatalf("saved snapshot: %+v %v", snapshot, err)
	}
	doc, err := h.svc.MCPAppDocument(ctx, conversation, activity)
	if err != nil || doc.HTML != "<h1>Saved app</h1>" {
		t.Fatalf("document: %+v %v", doc, err)
	}
	if _, err = h.svc.MCPAppAction(ctx, conversation, activity, chatsvc.MCPAppRequest{Method: "resources/read", Params: json.RawMessage(`{"uri":"ui://app"}`)}); err == nil {
		t.Fatal("stopped app retained actions")
	}
}

func TestMCPAppMessageApprovalBindsExactContent(t *testing.T) {
	h, provider, activity, _ := seedApp(t)
	ctx := context.Background()
	conversation := h.ctrl.ConversationID()
	request := chatsvc.MCPAppRequest{Method: "ui/message", Params: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"Explain Europe"}]}`)}
	approval, err := h.svc.MCPAppAction(ctx, conversation, activity, request)
	if err != nil || approval.ApprovalToken == "" {
		t.Fatalf("message approval: %+v %v", approval, err)
	}
	changed := request
	changed.ApprovalToken = approval.ApprovalToken
	changed.Params = json.RawMessage(`{"role":"user","content":[{"type":"text","text":"Different request"}]}`)
	if _, err := h.svc.MCPAppAction(ctx, conversation, activity, changed); err == nil {
		t.Fatal("approval authorized different message content")
	}
	approval, err = h.svc.MCPAppAction(ctx, conversation, activity, request)
	if err != nil {
		t.Fatal(err)
	}
	request.ApprovalToken = approval.ApprovalToken
	if _, err := h.svc.MCPAppAction(ctx, conversation, activity, request); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.sent) != 2 || provider.sent[1].Text != "Explain Europe" {
		t.Fatalf("unexpected message dispatch: %+v", provider.sent)
	}
}

func TestMCPAppRollbackRevokesActionsAndSnapshots(t *testing.T) {
	h, _, activity, turn := seedApp(t)
	ctx := context.Background()
	conversation := h.ctrl.ConversationID()
	if _, err := h.st.RollbackTurns(ctx, conversation, turn, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.MCPAppSnapshot(ctx, conversation, activity); err == nil {
		t.Fatal("rolled back app remained visible")
	}
	if _, err := h.svc.MCPAppAction(ctx, conversation, activity, chatsvc.MCPAppRequest{Method: "resources/read", Params: json.RawMessage(`{"uri":"ui://app"}`)}); err == nil {
		t.Fatal("rolled back app retained actions")
	}
}
