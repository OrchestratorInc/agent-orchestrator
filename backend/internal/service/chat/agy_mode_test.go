package chat_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type agyModeConversation struct {
	*fakeConversation
	mode string
}

func (c *agyModeConversation) ListConfigOptions(context.Context) ([]ports.ChatConfigOption, error) {
	return c.catalog(), nil
}

func (c *agyModeConversation) SetConfigOption(_ context.Context, id string, value ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error) {
	if id == "mode" {
		c.mode = value.Select
	}
	return c.catalog(), nil
}

func (c *agyModeConversation) catalog() []ports.ChatConfigOption {
	return []ports.ChatConfigOption{{
		ID: "mode", Current: ports.ChatConfigOptionValue{Select: c.mode},
		Choices: []ports.ChatConfigOptionChoice{
			{Value: "default"}, {Value: "accept-edits"},
			{Value: "auto_edit"}, {Value: "yolo"},
		},
	}}
}

func TestSetConfigOptionHonorsAgyAcceptEditsSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous domain.PermissionMode
		configID string
		value    string
		want     domain.PermissionMode
	}{
		{"explicit from auto", domain.PermissionModeAuto, "mode", "accept-edits", domain.PermissionModeAcceptEdits},
		{"explicit from bypass", domain.PermissionModeBypassPermissions, "mode", "accept-edits", domain.PermissionModeAcceptEdits},
		{"passive echo keeps auto", domain.PermissionModeAuto, "model", "model-pick", domain.PermissionModeAuto},
		{"passive echo keeps bypass", domain.PermissionModeBypassPermissions, "model", "model-pick", domain.PermissionModeBypassPermissions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			created, err := st.CreateSession(ctx, domain.SessionRecord{
				ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessAgy,
				Mode: domain.SessionModeChat, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			})
			if err != nil {
				t.Fatal(err)
			}
			id := created.ID
			conv := &agyModeConversation{fakeConversation: newFakeConversation(), mode: "accept-edits"}
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
				Log:     slog.New(slog.DiscardHandler), NewID: uuid.NewString,
			})
			t.Cleanup(func() { _ = svc.Stop(ctx, id) })
			if _, err := svc.Start(ctx, chatsvc.StartConfig{
				SessionID: id, ProjectID: testProject, Harness: domain.HarnessAgy,
				WorkspacePath: t.TempDir(), Permissions: tc.previous,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.SetConfigOption(ctx, id, tc.configID, ports.ChatConfigOptionValue{Select: tc.value}); err != nil {
				t.Fatal(err)
			}
			stored, err := st.ConversationForSession(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Settings.ApprovalMode != tc.want {
				t.Fatalf("stored approval = %q, want %q", stored.Settings.ApprovalMode, tc.want)
			}
		})
	}
}
