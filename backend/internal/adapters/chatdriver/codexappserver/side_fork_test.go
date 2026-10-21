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

func TestBackgroundForkIsReadOnlyAndOmitsResponseHistory(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/fork", `{"thread":{"id":"suggestion-child"},"model":"test-model"}`)
	conv, err := d.ForkIntoHost(t.Context(), "parent", "native-turn", ports.ChatStartConfig{WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto, ReadOnly: true, DisableNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conv.Close()
	var params map[string]any
	if err := json.Unmarshal(srv.awaitFrame(func(f frame) bool { return f.Method == "thread/fork" }).Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["ephemeral"] != nil || params["excludeTurns"] != true || params["sandbox"] != "read-only" || params["approvalPolicy"] != "never" {
		t.Fatalf("fork policies=%+v", params)
	}
	if conv.(ports.ChatSandboxNetwork).SandboxAllowsNetwork("") {
		t.Fatal("background inherited network access")
	}
}

func TestForkCleanupAcceptsOnlyTheExactMissingRollout(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		code          int
		wantError     bool
	}{
		{"already gone", "no rollout found for thread id child", -32600, false},
		{"wrong child", "no rollout found for thread id other", -32600, true},
		{"wrong error class", "no rollout found for thread id child", -32603, true},
		{"ordinary refusal", "cannot delete an active thread", -32600, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			conv, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer conv.Close()
			srv.replyError("thread/delete", tc.code, tc.message)
			err = conv.(ports.ChatForkDeleter).DeleteFork(t.Context(), "child")
			if (err != nil) != tc.wantError {
				t.Fatalf("cleanup error=%v wantError=%v", err, tc.wantError)
			}
			if err := conv.(ports.ChatForkDeleter).DeleteFork(t.Context(), conv.ProviderConversationID()); err == nil {
				t.Fatal("parent deletion allowed")
			}
		})
	}
}
