package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestAgentAccountUsageFollowsBindingHistory pins the per-account statistics:
// each usage event counts for the account the session was on when it happened,
// so a session moved from one account to another splits its usage between them.
func TestAgentAccountUsageFollowsBindingHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	session := seedUsageSession(t, s, domain.HarnessClaudeCode)
	source := seedUsageSource(t, s, session, t0)
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{
		ID: "acct-b", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/acct-b", Email: "b@example.com", CreatedAt: t0,
	}))
	mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude, "device-claude", t0))
	mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude, "acct-b", t0.Add(24*time.Hour)))

	before := anthropicUsageEvent("before-move", 10, 0, 0, 5)
	before.CreatedAt = t0.Add(time.Hour)
	after := anthropicUsageEvent("after-move", 20, 0, 0, 7)
	after.CreatedAt = t0.Add(25 * time.Hour)
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
		ByteOffset: 100, State: domain.UsageSourceActive, ParserStateJSON: `{}`, UpdatedAt: t0,
	}, []domain.ModelUsageEvent{before, after}))

	rows, err := s.AgentAccountUsageSince(ctx, t0.Add(-time.Hour))
	mustNoError(t, err)
	got := map[string]domain.AgentAccountUsage{}
	for _, row := range rows {
		got[row.AccountID] = row
	}
	if len(rows) != 2 || got["device-claude"].InputTokens != 10 || got["device-claude"].Day != "2026-10-14" ||
		got["acct-b"].InputTokens != 20 || got["acct-b"].OutputTokens != 7 || got["acct-b"].Day != "2026-10-15" {
		t.Fatalf("usage rows = %+v", rows)
	}

	current, bound, err := s.GetSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude)
	mustNoError(t, err)
	if !bound || current != "acct-b" {
		t.Fatalf("current binding = %q bound=%v, want acct-b", current, bound)
	}

	// Removing the account keeps the session's history attributable and drops
	// any default that named it.
	mustNoError(t, s.SetAgentAccountDefault(ctx, domain.AgentAccountProviderClaude, "acct-b"))
	mustNoError(t, s.DeleteAgentAccount(ctx, "acct-b"))
	if _, ok, err := s.GetAgentAccountDefault(ctx, domain.AgentAccountProviderClaude); err != nil || ok {
		t.Fatalf("default after delete: ok=%v err=%v", ok, err)
	}
}

// A controller handoff must change the mode or the account: an account move is
// a same-mode transition that names both accounts.
func TestInterfaceTransitionsAllowSameModeOnlyForAnAccountMove(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	session := seedUsageSession(t, s, domain.HarnessClaudeCode)
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	move := domain.SessionInterfaceTransition{
		ID: "same-mode", SessionID: session.ID, SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeTUI,
		Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase: domain.SessionInterfaceTransitionRequested, CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := s.CreateSessionInterfaceTransition(ctx, move); err == nil {
		t.Fatal("a same-mode transition without an account change was accepted")
	}
	move.ID, move.SourceAccountID, move.TargetAccountID = "account-move", "device-claude", "acct-b"
	created, ok, err := s.CreateSessionInterfaceTransition(ctx, move)
	mustNoError(t, err)
	if !ok || created.SourceAccountID != "device-claude" || created.TargetAccountID != "acct-b" || !created.AccountMove() {
		t.Fatalf("created = %+v", created)
	}
	active, found, err := s.GetActiveSessionInterfaceTransition(ctx, session.ID)
	mustNoError(t, err)
	if !found || active.TargetAccountID != "acct-b" {
		t.Fatalf("active = %+v", active)
	}
}
