package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionLaunchOptionsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "launch")
	session, err := s.CreateSession(ctx, sampleRecord("launch"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if got, err := s.SessionLaunchOptions(ctx, session.ID); err != nil || got != (domain.AgentLaunchOptions{}) {
		t.Fatalf("new session launch options = %+v, %v; want none", got, err)
	}
	want := domain.AgentLaunchOptions{FastMode: true, Ultracode: true, AutoCompactWindow: "200k"}
	if err := s.SetSessionLaunchOptions(ctx, session.ID, want); err != nil {
		t.Fatalf("set launch options: %v", err)
	}
	if got, err := s.SessionLaunchOptions(ctx, session.ID); err != nil || got != want {
		t.Fatalf("launch options = %+v, %v; want %+v", got, err, want)
	}
	if err := s.SetSessionLaunchOptions(ctx, session.ID, domain.AgentLaunchOptions{}); err != nil {
		t.Fatalf("clear launch options: %v", err)
	}
	if got, _ := s.SessionLaunchOptions(ctx, session.ID); got != (domain.AgentLaunchOptions{}) {
		t.Fatalf("cleared launch options = %+v; want none", got)
	}
}

func TestUpdateSessionWithLaunchOptionsWritesBothOrNeither(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "launch")
	session, err := s.CreateSession(ctx, sampleRecord("launch"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	session.Harness = domain.HarnessCodex
	want := domain.AgentLaunchOptions{FastMode: true, HandoffNote: "note"}
	if err := s.UpdateSessionWithLaunchOptions(ctx, session, want); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, _ := s.GetSession(ctx, session.ID)
	if options, _ := s.SessionLaunchOptions(ctx, session.ID); got.Harness != domain.HarnessCodex || options != want {
		t.Fatalf("harness %q, options %+v; want codex with %+v", got.Harness, options, want)
	}
	// A cancelled write moves neither.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	session.Harness = domain.HarnessClaudeCode
	if err := s.UpdateSessionWithLaunchOptions(cancelled, session, domain.AgentLaunchOptions{}); err == nil {
		t.Fatal("a cancelled write succeeded")
	}
	got, _, _ = s.GetSession(ctx, session.ID)
	if options, _ := s.SessionLaunchOptions(ctx, session.ID); got.Harness != domain.HarnessCodex || options != want {
		t.Fatalf("after a failed write: harness %q, options %+v", got.Harness, options)
	}
}
