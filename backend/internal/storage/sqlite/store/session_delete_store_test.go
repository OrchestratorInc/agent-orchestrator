package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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

// A session's usage events cascade away with it, but its spend must stay in the
// agent account usage view, attributed to the same accounts and days. Two
// deleted sessions on the same account, day and model add up.
func TestDeleteTerminatedSessionKeepsAccountUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	first := seedUsageSession(t, s, domain.HarnessClaudeCode)
	second := sampleRecord("usage")
	second.Harness = domain.HarnessClaudeCode
	second, err := s.CreateSession(ctx, second)
	mustNoError(t, err)
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{
		ID: "acct-b", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/acct-b", Email: "b@example.com", CreatedAt: t0,
	}))
	// first moves from the device login to acct-b after a day; second stays on
	// the device login.
	mustNoError(t, s.BindSessionAgentAccount(ctx, first.ID, domain.AgentAccountProviderClaude, "device-claude", t0))
	mustNoError(t, s.BindSessionAgentAccount(ctx, first.ID, domain.AgentAccountProviderClaude, "acct-b", t0.Add(24*time.Hour)))
	for i, sess := range []domain.SessionRecord{first, second} {
		binding := mustUpsertUsageBinding(t, s, sess, t0, domain.UsageBindingRecord{
			NativeRootID: "root", InitialModelID: "claude-x", State: domain.UsageBindingActive,
		})
		source := mustInsertUsageSource(t, s, t0, domain.UsageSourceRecord{
			BindingID: binding.ID, Kind: domain.UsageSourceClaudeMain, NativeSessionID: "native",
			ArtifactPath: fmt.Sprintf("/tmp/claude/%d.jsonl", i), FileIdentity: "dev:ino", State: domain.UsageSourcePending,
		})
		before := anthropicUsageEvent(fmt.Sprintf("before-%d", i), 10, 0, 0, 5)
		before.CreatedAt = t0.Add(time.Hour)
		after := anthropicUsageEvent(fmt.Sprintf("after-%d", i), 20, 0, 0, 7)
		after.CreatedAt = t0.Add(25 * time.Hour)
		mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
			ByteOffset: 100, State: domain.UsageSourceActive, ParserStateJSON: `{}`, UpdatedAt: t0,
		}, []domain.ModelUsageEvent{before, after}))
	}
	totals := func() map[string]domain.AgentAccountUsage {
		t.Helper()
		rows, err := s.AgentAccountUsageSince(ctx, t0.Truncate(24*time.Hour))
		mustNoError(t, err)
		out := map[string]domain.AgentAccountUsage{}
		for _, row := range rows {
			key := row.AccountID + "|" + row.Day + "|" + row.Model
			sum := out[key]
			sum.InputTokens += row.InputTokens
			sum.OutputTokens += row.OutputTokens
			sum.CostNanos += row.CostNanos
			out[key] = sum
		}
		return out
	}
	want := totals()
	if len(want) != 3 {
		t.Fatalf("seeded usage = %+v, want device-claude on both days and acct-b on the second", want)
	}

	for _, sess := range []domain.SessionRecord{first, second} {
		sess.IsTerminated = true
		mustNoError(t, s.UpdateSession(ctx, sess))
		deleted, err := s.DeleteTerminatedSession(ctx, sess.ID)
		if err != nil || !deleted {
			t.Fatalf("delete %s = %v, %v", sess.ID, deleted, err)
		}
		if got := totals(); !reflect.DeepEqual(got, want) {
			t.Fatalf("after deleting %s usage = %+v, want %+v", sess.ID, got, want)
		}
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
