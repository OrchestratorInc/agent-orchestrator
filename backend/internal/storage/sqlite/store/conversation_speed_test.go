package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestConversationSpeedPersistsAndSurvivesLifecycleWrites(t *testing.T) {
	st, sessionID, conversationID := conversationFixture(t)
	ctx := context.Background()
	for _, speed := range []domain.SpeedMode{domain.SpeedModeFast, domain.SpeedModeUltrafast, domain.SpeedModeNormal} {
		if err := st.SetConversationSettings(ctx, conversationID, domain.ConversationSettings{Model: "gpt-test", SpeedMode: speed}, histClock); err != nil {
			t.Fatal(err)
		}
		stored, err := st.ConversationForSession(ctx, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Settings.SpeedMode != speed {
			t.Fatalf("stored speed = %q, want %q", stored.Settings.SpeedMode, speed)
		}
		rec, ok, err := st.GetSession(ctx, sessionID)
		if err != nil || !ok || rec.Metadata.SpeedMode != speed {
			t.Fatalf("session speed = %q, error = %v", rec.Metadata.SpeedMode, err)
		}
		rec.Metadata.SpeedMode = "normal"
		if err := st.UpdateSession(ctx, rec); err != nil {
			t.Fatal(err)
		}
		rec, _, err = st.GetSession(ctx, sessionID)
		if err != nil || rec.Metadata.SpeedMode != speed {
			t.Fatalf("lifecycle write reverted speed: %q, %v", rec.Metadata.SpeedMode, err)
		}
	}
	if err := st.SetConversationSettings(ctx, conversationID, domain.ConversationSettings{}, histClock); err != nil {
		t.Fatal(err)
	}
	stored, err := st.ConversationForSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.SpeedMode != "" {
		t.Fatalf("clearing settings retained speed %q", stored.Settings.SpeedMode)
	}
}

func TestProjectSpeedFollowsReplacementSession(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedProject(t, st, "speed-project")
	rec := sampleRecord("speed-project")
	rec.Kind = domain.KindOrchestrator
	rec.Harness = domain.HarnessCodex
	rec.Mode = domain.SessionModeChat
	first, err := st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := st.CreateConversation(ctx, "project-speed", domain.ConversationScopeProject, rec.ProjectID, first.ID, histClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{SpeedMode: domain.SpeedModeFast}, histClock); err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateConversation(ctx, "replacement", domain.ConversationScopeProject, rec.ProjectID, second.ID, histClock); err != nil {
		t.Fatal(err)
	}
	stored, found, err := st.GetSession(ctx, second.ID)
	if err != nil || !found || stored.Metadata.SpeedMode != domain.SpeedModeFast {
		t.Fatalf("replacement lost selected speed: %q, %v", stored.Metadata.SpeedMode, err)
	}
}
