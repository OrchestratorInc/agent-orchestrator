package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestVersionMoveJournalSurvivesSQLiteReopen(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	var ids []domain.SessionID
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		rec := sampleRecord("mer")
		rec.Mode = mode
		rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Hour)}
		rec.Metadata.ControllerGeneration, rec.Metadata.RuntimeLaunchID = "source-chat", "source-tui"
		rec.Metadata.RuntimeHandleID = "source-handle"
		rec.Metadata.ProviderConversationID, rec.Metadata.AgentSessionID = "native-chat", "native-tui"
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, created.ID)
	}
	if _, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessClaudeCode, "new", now); err != nil {
		t.Fatal(err)
	}
	chat, chatErr := s.ListVersionRefreshChatCandidates(ctx, now)
	tui, tuiErr := s.ListVersionRefreshTerminalCandidates(ctx, now)
	if chatErr != nil || tuiErr != nil || len(chat) != 1 || len(tui) != 1 {
		t.Fatalf("control candidates: chat=%v (%v) tui=%v (%v)", chat, chatErr, tui, tuiErr)
	}
	for _, id := range ids {
		rec, _, err := s.GetSession(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PutSessionVersionMove(ctx, domain.NewSessionVersionMove(rec)); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSessionVersionMoveStage(ctx, id, domain.VersionMoveRetry); err != nil {
			t.Fatal(err)
		}
		if allowed, err := s.AttemptSessionVersionMove(ctx, id, 1); err != nil || !allowed {
			t.Fatalf("first attempt = %v, %v", allowed, err)
		}
		if rec.Mode == domain.SessionModeChat {
			if err := s.ClaimLiveChatControllerGeneration(ctx, id, "failed-claim"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetSessionVersionMoveStage(ctx, id, domain.VersionMoveRetry); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	moves, err := s.ListSessionVersionMoves(ctx)
	if err != nil || len(moves) != 2 {
		t.Fatalf("reopened moves = %+v, %v", moves, err)
	}
	for _, move := range moves {
		if move.Mode == domain.SessionModeChat && move.SourceLaunch != "failed-claim" {
			t.Fatalf("retry lost the failed claim epoch: %+v", move)
		}
		if move.Attempts != 1 || move.Stage != domain.VersionMoveRetry || move.Version != "new" || move.SourceLaunch == "" || move.NativeID == "" {
			t.Fatalf("lost recovery identity or budget: %+v", move)
		}
		if allowed, err := s.AttemptSessionVersionMove(ctx, move.SessionID, 1); err != nil || allowed {
			t.Fatalf("reopened exhausted attempt = %v, %v", allowed, err)
		}
		if err := s.PutSessionVersionMove(ctx, move); err == nil {
			t.Fatal("existing intent was overwritten, resetting budget")
		}
	}
	chat, err = s.ListVersionRefreshChatCandidates(ctx, now)
	if err != nil || len(chat) != 0 {
		t.Fatalf("ordinary chat sweep ignored recovery intent: %v, %v", chat, err)
	}
	tui, err = s.ListVersionRefreshTerminalCandidates(ctx, now)
	if err != nil || len(tui) != 0 {
		t.Fatalf("ordinary terminal sweep ignored recovery intent: %v, %v", tui, err)
	}
	for _, id := range ids {
		if err := s.DeleteSessionVersionMove(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	chat, _ = s.ListVersionRefreshChatCandidates(ctx, now)
	tui, _ = s.ListVersionRefreshTerminalCandidates(ctx, now)
	if len(chat) != 1 || len(tui) != 1 {
		t.Fatalf("journal cleanup hid candidates: chat=%v tui=%v", chat, tui)
	}
}
