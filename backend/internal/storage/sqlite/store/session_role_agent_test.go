package store_test

import (
	"context"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// The project's role agent recorded at spawn survives reads and generic
// session writes, and only SetSessionRoleAgent moves it, publishing a change.
func TestSessionRoleAgentRoundTripsAndOnlyItsWriterMovesIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "role-agent")
	rec := sampleRecord("role-agent")
	rec.Kind = domain.KindOrchestrator
	rec.Harness = domain.HarnessCodex
	rec.Metadata.RoleAgent = domain.HarnessCodex
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, ok, err := s.GetSession(ctx, session.ID)
	if err != nil || !ok || got.Metadata.RoleAgent != domain.HarnessCodex {
		t.Fatalf("role agent = %q ok=%v err=%v, want %q", got.Metadata.RoleAgent, ok, err, domain.HarnessCodex)
	}

	// A switch writes the session through the generic update first.
	got.Harness = domain.HarnessClaudeCode
	got.Metadata.RoleAgent = ""
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatalf("update session: %v", err)
	}
	got, _, _ = s.GetSession(ctx, session.ID)
	if got.Metadata.RoleAgent != domain.HarnessCodex {
		t.Fatalf("role agent after generic update = %q, want it kept as %q", got.Metadata.RoleAgent, domain.HarnessCodex)
	}

	// The switch records it after the agent changed, so the board must hear of it.
	baseSeq, _ := s.LatestSeq(ctx)
	if ok, err := s.SetSessionRoleAgent(ctx, session.ID, domain.HarnessClaudeCode); err != nil || !ok {
		t.Fatalf("set role agent: ok=%v err=%v", ok, err)
	}
	events, err := s.EventsAfter(ctx, baseSeq, 100)
	if err != nil {
		t.Fatalf("read CDC: %v", err)
	}
	if len(events) != 1 || string(events[0].Type) != "session_updated" {
		t.Fatalf("events = %+v, want one session_updated", events)
	}
	sessions, err := s.ListSessions(ctx, domain.ProjectID("role-agent"))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list sessions = %d err=%v, want 1", len(sessions), err)
	}
	if sessions[0].Metadata.RoleAgent != domain.HarnessClaudeCode {
		t.Fatalf("listed role agent = %q, want %q", sessions[0].Metadata.RoleAgent, domain.HarnessClaudeCode)
	}
}
