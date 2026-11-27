package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type agyOpeningConversation struct {
	variantModelConversation
	options []ports.ChatConfigOption
}

func (c agyOpeningConversation) ListConfigOptions(context.Context) ([]ports.ChatConfigOption, error) {
	return c.options, nil
}

func TestAgyOpeningModelFallbackIsVisibleAndDurableBeforeFirstPrompt(t *testing.T) {
	for _, live := range []string{"gemini-3.8-flash", "gemini-3.8-flash-high", "claude-sonnet-4-6-thinking"} {
		t.Run(live, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			provider := agyOpeningConversation{
				variantModelConversation: variantModelConversation{newFakeConversation()},
				options: []ports.ChatConfigOption{{
					ID: "model", Category: "model", Type: ports.ChatConfigOptionSelect,
					Current: ports.ChatConfigOptionValue{Select: live},
					Choices: []ports.ChatConfigOptionChoice{{Value: live}},
				}},
			}
			wantEffort := ""
			if live == "gemini-3.8-flash" {
				wantEffort = "low"
				provider.options = append(provider.options, ports.ChatConfigOption{
					ID: "thinking_level", Category: "thought_level", Type: ports.ChatConfigOptionSelect,
					Current: ports.ChatConfigOptionValue{Select: wantEffort},
					Choices: []ports.ChatConfigOptionChoice{{Value: "low"}, {Value: "high"}},
				})
			}
			var ids atomic.Uint64
			var resumed ports.ChatResumeConfig
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st, Reader: fullSnapshotReader(st),
				Drivers: fakeRegistry{driver: fakeDriver{conv: &provider, resumeCfg: &resumed}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   func() string { return fmt.Sprintf("agy-%d", ids.Add(1)) },
			})
			t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
			cfg := chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessAgy,
				WorkspacePath: t.TempDir(), Model: "claude-opus-4-6-thinking", Effort: "high",
				ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
					if result.Conversation.Settings.Model != live || result.Conversation.Settings.ReasoningEffort != wantEffort {
						t.Fatalf("published settings = %+v", result.Conversation.Settings)
					}
					return chatsvc.ControllerCommit{Conversation: result.Conversation}, nil
				},
			}
			controller, err := svc.Start(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			wantNotice := "claude-opus-4-6-thinking isn't available in Agy Chat. Using " + live + " instead."
			assertSaved := func() {
				t.Helper()
				snapshot, err := st.LoadConversationSnapshot(ctx, controller.ConversationID())
				if err != nil || snapshot.Conversation.Settings.Model != live || snapshot.Conversation.Settings.ReasoningEffort != wantEffort {
					t.Fatalf("saved settings = %+v, err = %v", snapshot.Conversation.Settings, err)
				}
				if len(snapshot.Activities) != 1 || snapshot.Activities[0].Kind != domain.ActivityKindSystem || snapshot.Activities[0].Summary != wantNotice {
					t.Fatalf("startup notices = %+v", snapshot.Activities)
				}
			}
			assertSaved()
			if _, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "hello"}); err != nil {
				t.Fatal(err)
			}
			sent := provider.sentMessages()
			if len(sent) != 1 || sent[0].Settings.Model != live || sent[0].Settings.Effort != wantEffort {
				t.Fatalf("first prompt settings = %+v", sent)
			}
			if err := svc.Stop(ctx, testSession); err != nil {
				t.Fatal(err)
			}
			provider.fakeConversation = newFakeConversation()
			// A new driver process sees the saved actual model, even when the
			// session's caller still carries the original catalog selection.
			cfg.ProviderConversationID = provider.ProviderConversationID()
			if _, err := svc.Start(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if resumed.Model != live || resumed.Effort != wantEffort {
				t.Fatalf("resume settings = %+v", resumed)
			}
			assertSaved()
		})
	}
}

func TestAgyOpeningModelKeepsSupportedVariantsAndOtherHarnesses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		harness domain.AgentHarness
		model   string
	}{
		{"split variant", domain.HarnessAgy, "gemini-3.8-flash-low"},
		{"exact model", domain.HarnessAgy, "gemini-3.8-flash"},
		{"other harness", domain.HarnessCodex, "unlisted-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			provider := variantModelConversation{newFakeConversation()}
			svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, Log: slog.New(slog.DiscardHandler), NewID: uuid.NewString})
			t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
			controller, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
				Harness: tc.harness, WorkspacePath: t.TempDir(), Model: tc.model})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := st.LoadConversationSnapshot(ctx, controller.ConversationID())
			if err != nil || snapshot.Conversation.Settings.Model != tc.model || len(snapshot.Activities) != 0 {
				t.Fatalf("settings = %+v, activities = %+v, err = %v", snapshot.Conversation.Settings, snapshot.Activities, err)
			}
		})
	}
}

type failingAgyNoticeStore struct{ chatsvc.Store }

func (s failingAgyNoticeStore) UpsertActivity(context.Context, string, string, domain.ConversationActivity, time.Time) error {
	return errors.New("notice unavailable")
}

func TestAgyOpeningModelCannotSilentlyContinueWhenNoticeCannotBeSaved(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	provider := variantModelConversation{newFakeConversation()}
	svc := chatsvc.New(chatsvc.Options{Store: failingAgyNoticeStore{st}, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, Log: slog.New(slog.DiscardHandler), NewID: uuid.NewString})
	_, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessAgy, WorkspacePath: t.TempDir(), Model: "claude-opus-4-6-thinking"})
	if err == nil || !strings.Contains(err.Error(), "notice unavailable") {
		t.Fatalf("Start = %v, want notice persistence failure", err)
	}
	if _, err := svc.Controller(testSession); !errors.Is(err, chatsvc.ErrNoController) {
		t.Fatalf("controller published after failed notice: %v", err)
	}
	if sent := provider.sentMessages(); len(sent) != 0 {
		t.Fatalf("prompts sent after failed notice: %+v", sent)
	}
}

func TestAgyOpeningModelDoesNotAdoptALiveHostsStaleStartupSnapshot(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	provider := &claudeDefaultsConversation{fakeConversation: newFakeConversation(), live: true, model: "stale-startup-model"}
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, Log: slog.New(slog.DiscardHandler), NewID: uuid.NewString})
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	controller, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessAgy, WorkspacePath: t.TempDir(), Model: "chosen-model"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.LoadConversationSnapshot(ctx, controller.ConversationID())
	if err != nil || snapshot.Conversation.Settings.Model != "chosen-model" || len(snapshot.Activities) != 0 {
		t.Fatalf("settings = %+v, activities = %+v, err = %v", snapshot.Conversation.Settings, snapshot.Activities, err)
	}
}
