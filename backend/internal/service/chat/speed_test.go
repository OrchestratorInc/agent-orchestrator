package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type speedConversation struct{ *fakeConversation }

func (c speedConversation) ListModels(context.Context) ([]ports.ChatModel, error) {
	return []ports.ChatModel{{ID: "m", Default: true, ServiceTiers: []ports.ModelServiceTier{{ID: "priority"}}}}, nil
}

func TestSpeedSettingsValidateAndPreserveOmittedChoice(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: speedConversation{newFakeConversation()}}},
		Log:     slog.New(slog.DiscardHandler), NewID: func() string { return "speed-conversation" },
	})
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), Model: "m", SpeedMode: domain.SpeedModeNormal}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		request domain.ConversationSettings
		want    domain.SpeedMode
		invalid bool
	}{
		{domain.ConversationSettings{Model: "m", SpeedMode: domain.SpeedModeFast}, domain.SpeedModeFast, false},
		{domain.ConversationSettings{Model: "m", ReasoningEffort: "high"}, domain.SpeedModeFast, false},
		{domain.ConversationSettings{Model: "m", SpeedMode: domain.SpeedModeUltrafast}, domain.SpeedModeFast, true},
		{domain.ConversationSettings{Model: "m", SpeedMode: "turbo"}, domain.SpeedModeFast, true},
		{domain.ConversationSettings{Model: "m", SpeedMode: domain.SpeedModeNormal}, domain.SpeedModeNormal, false},
	} {
		settings, err := svc.SetTurnSettings(ctx, testSession, step.request)
		if step.invalid {
			if !errors.Is(err, ports.ErrUnsupportedSpeed) {
				t.Fatalf("invalid choice error = %v", err)
			}
		} else if err != nil || settings.SpeedMode != step.want {
			t.Fatalf("settings = %+v, error = %v", settings, err)
		}
		stored, err := st.ConversationForSession(ctx, testSession)
		if err != nil || stored.Settings.SpeedMode != step.want {
			t.Fatalf("durable speed = %q, error = %v", stored.Settings.SpeedMode, err)
		}
	}
}
