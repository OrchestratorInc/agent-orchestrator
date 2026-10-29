package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeleteTerminatedSessionRemovesRecordAndNeverReusesItsID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "del")
	older := terminalSession(t, s, "del", 0)
	newest := terminalSession(t, s, "del", 0)
	upsertFacts(t, s, domain.SessionCleanupRecord{SessionID: newest.ID, WorkspaceDisposition: domain.DispositionRemoved})
	if _, err := s.CreateConversation(ctx, "conv-newest", domain.ConversationScopeSession, "del", newest.ID, time.Now()); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	before, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteTerminatedSession(ctx, newest.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v; want deleted", deleted, err)
	}
	if _, ok, err := s.GetSession(ctx, newest.ID); err != nil || ok {
		t.Fatalf("session still present: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetSessionCleanupFacts(ctx, newest.ID); err != nil || ok {
		t.Fatalf("cleanup facts survived the session: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetSession(ctx, older.ID); err != nil || !ok {
		t.Fatalf("unrelated session removed: ok=%v err=%v", ok, err)
	}

	events, err := s.EventsAfter(ctx, before, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != cdc.EventSessionUpdated || events[0].SessionID != "" || events[0].ProjectID != "del" {
		t.Fatalf("events = %+v, want one project-level session_updated", events)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload["deletedSessionId"] != string(newest.ID) {
		t.Fatalf("payload = %s (%v), want the deleted session id", events[0].Payload, err)
	}

	next, err := s.CreateSession(ctx, sampleRecord("del"))
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == newest.ID {
		t.Fatalf("new session reused deleted id %s", next.ID)
	}
}

func TestDeleteTerminatedSessionRefusesLiveSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "del")
	live, err := s.CreateSession(ctx, sampleRecord("del"))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := s.DeleteTerminatedSession(ctx, live.ID)
	if err != nil || deleted {
		t.Fatalf("delete live = %v, %v; want refused", deleted, err)
	}
	if deleted, err := s.DeleteTerminatedSession(ctx, "del-404"); err != nil || deleted {
		t.Fatalf("delete missing = %v, %v; want not deleted", deleted, err)
	}
	if _, ok, err := s.GetSession(ctx, live.ID); err != nil || !ok {
		t.Fatalf("live session removed: ok=%v err=%v", ok, err)
	}
}

// A project orchestrator conversation outlives each orchestrator session;
// deleting one would cascade its turns out of that shared history.
func TestDeleteTerminatedSessionRefusesSharedConversationHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "del")
	orchestrator := terminalSession(t, s, "del", 0)
	now := time.Now()
	conversation, err := s.CreateConversation(ctx, "conv-project", domain.ConversationScopeProject, "del", orchestrator.ID, now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := s.AdoptProviderTurn(ctx, conversation.ID, orchestrator.ID, "gen-1", "turn-1", "provider-turn-1", now); err != nil {
		t.Fatalf("adopt turn: %v", err)
	}

	deleted, err := s.DeleteTerminatedSession(ctx, orchestrator.ID)
	if !errors.Is(err, domain.ErrSessionHistoryShared) || deleted {
		t.Fatalf("delete = %v, %v; want ErrSessionHistoryShared", deleted, err)
	}
	if has, err := s.HasConversationTurns(ctx, conversation.ID); err != nil || !has {
		t.Fatalf("shared turns lost: has=%v err=%v", has, err)
	}
}
