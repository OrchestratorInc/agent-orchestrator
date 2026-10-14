package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSideForkUsesExactAnchorAndIndependentScope(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/fork", `{"thread":{"id":"side-thread"},"model":"test-model"}`)
	workspace := t.TempDir()
	conv, err := d.ForkIntoHost(context.Background(), "main-thread", "native-turn", ports.ChatStartConfig{
		SessionID: "side-host", WorkspacePath: workspace, Model: "test-model", Effort: "low", Permissions: domain.PermissionMode("read-only"), SystemPrompt: "inherited instructions", ProviderScopeID: "side", ProviderIDsScoped: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conv.Close()
	var params map[string]any
	if err := json.Unmarshal(srv.awaitFrame(func(f frame) bool { return f.Method == "thread/fork" }).Params, &params); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"threadId": "main-thread", "lastTurnId": "native-turn", "cwd": workspace, "model": "test-model", "developerInstructions": "inherited instructions"} {
		if params[key] != want {
			t.Fatalf("%s = %v, want %s", key, params[key], want)
		}
	}
	if conv.ProviderConversationID() != "side-thread" || srv.sentMethod("thread/start") || srv.sentMethod("thread/resume") {
		t.Fatal("fork silently started or resumed a different thread")
	}
	turn, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "side question"})
	if err != nil || turn.ProviderTurnID != "codex:4:sideturn-1" {
		t.Fatalf("side scope: %+v %v", turn, err)
	}
}

func TestSideForkRejectsMissingSourceAndProviderRefusal(t *testing.T) {
	d, srv := newTestDriver(t)
	cfg := ports.ChatStartConfig{WorkspacePath: t.TempDir()}
	if _, err := d.ForkIntoHost(context.Background(), "", "", cfg); err == nil || srv.sentMethod("thread/fork") {
		t.Fatal("missing source accepted")
	}
	srv.replyError("thread/fork", -32600, "cannot fork at that turn")
	if _, err := d.ForkIntoHost(context.Background(), "main", "turn", cfg); err == nil {
		t.Fatal("provider refusal lost")
	}
	if srv.sentMethod("thread/start") {
		t.Fatal("provider refusal silently created an empty thread")
	}
}

func TestSideForkRequiresExactAnchor(t *testing.T) {
	d, srv := newTestDriver(t)
	if _, err := d.ForkIntoHost(t.Context(), "parent", "", ports.ChatStartConfig{WorkspacePath: t.TempDir()}); err == nil || srv.sentMethod("thread/fork") {
		t.Fatal("missing anchor silently copied current context")
	}
}

func TestSideForkRecordsReturnedChildBeforeStarting(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/fork", `{"thread":{"id":"returned-child"},"model":"test-model"}`)
	recordErr := errors.New("journal unavailable")
	recorded := ""
	conv, err := d.ForkIntoHost(t.Context(), "parent", "completed-turn", ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), RecordFork: func(ctx context.Context, id string) error {
			if ctx.Err() != nil {
				t.Fatalf("record context cancelled: %v", ctx.Err())
			}
			recorded = id
			return recordErr
		},
	})
	if recorded != "returned-child" || conv != nil || !errors.Is(err, recordErr) || !strings.Contains(err.Error(), "record Codex fork") {
		t.Fatalf("recorded=%q conversation=%v error=%v", recorded, conv, err)
	}
	if srv.sentMethod("thread/start") || srv.sentMethod("thread/resume") || srv.sentMethod("turn/start") {
		t.Fatal("failed journal accepted child work")
	}
}
