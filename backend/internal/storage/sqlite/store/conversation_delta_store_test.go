package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestConversationDeltaIncludesSettledRewritesOutsideLivePage(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		seedToolTurn(t, s, conversation, session, fmt.Sprintf("delta-%d", i), 1, histClock.Add(time.Duration(i)*time.Second))
	}
	before, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := s.LoadConversationSnapshotDelta(ctx, conversation, before.Conversation.LatestSequence+1, before.ChangeCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if quiet.AfterSequence == nil || len(quiet.Messages) != 0 || len(quiet.Activities) != 0 {
		t.Fatalf("quiet delta: %#v", quiet)
	}
	// A provider can revise an already completed command whose sequence is on an
	// older loaded page. State/earliest-active heuristics would miss this write.
	if err := s.UpsertActivity(ctx, conversation, "provider-delta-0", domain.ConversationActivity{ID: "delta-0-tool-0", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, Summary: "corrected output", ProviderItemID: "delta-0-exec-0"}, histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	delta, err := s.LoadConversationSnapshotDelta(ctx, conversation, before.Conversation.LatestSequence+1, before.ChangeCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if delta.AfterSequence == nil || len(delta.Activities) != 1 || delta.Activities[0].Summary != "corrected output" || delta.Activities[0].Sequence >= before.OldestSequence || delta.ChangeCursor <= before.ChangeCursor {
		t.Fatalf("settled rewrite delta: %#v", delta)
	}
	foundTurn := false
	for _, turn := range delta.Turns {
		if turn.ID == delta.Activities[0].TurnID {
			foundTurn = true
		}
	}
	if !foundTurn {
		t.Fatal("rewritten older row lacks its unchanged turn metadata")
	}
	fresh, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range fresh.Activities {
		if a.ID == delta.Activities[0].ID && (a.Revision != delta.Activities[0].Revision || a.Summary != delta.Activities[0].Summary) {
			t.Fatal("delta differs from fresh snapshot")
		}
	}
	fullBytes, _ := json.Marshal(before)
	deltaBytes, _ := json.Marshal(delta)
	t.Logf("fixture measured full page bytes=%d delta bytes=%d; one delta request (not a real-turn benchmark)", len(fullBytes), len(deltaBytes))
}

func TestConversationDeltaSequenceCompatibilityAndCoverage(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: fmt.Sprintf("m%d", i), Text: "prompt", Origin: domain.MessageOriginHuman}, fmt.Sprintf("t%d", i), histClock); err != nil {
			t.Fatal(err)
		}
	}
	delta, err := s.LoadConversationSnapshotDelta(ctx, conversation, 4, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if delta.AfterSequence == nil || len(delta.Messages) != 2 || delta.Messages[0].Sequence != 4 {
		t.Fatalf("inclusive compatibility: %#v", delta)
	}
	fallback, err := s.LoadConversationSnapshotDelta(ctx, conversation, 1, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.AfterSequence != nil || !fallback.HasMoreBefore || len(fallback.Messages) != 2 {
		t.Fatalf("coverage fallback: %#v", fallback)
	}
}

func TestConversationDeltaCancelRequiresReplacement(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: "queued-message", Text: "remove", Origin: domain.MessageOriginHuman}, "queued", histClock); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelQueuedTurnByID(ctx, conversation, "queued", histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	delta, err := s.LoadConversationSnapshotDelta(ctx, conversation, before.Conversation.LatestSequence+1, before.ChangeCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if delta.AfterSequence != nil || len(delta.Messages) != 0 {
		t.Fatalf("cancel must replace: %#v", delta)
	}
}

func TestConversationDeltaStreamsAndSettlesWithoutSequenceChange(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: "prompt", Text: "run", Origin: domain.MessageOriginHuman}, "turn", histClock); err != nil {
		t.Fatal(err)
	}
	if err := s.BindTurnToProvider(ctx, "turn", "provider", histClock); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAssistantDelta(ctx, conversation, "reply", "provider", "initial", "reply", histClock); err != nil {
		t.Fatal(err)
	}
	page, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	merged := map[string]domain.ConversationMessage{}
	for _, m := range page.Messages {
		merged[m.ID] = m
	}
	// Simulate repeated output over >30s using fixture timestamps, without claiming
	// this synthetic regression is the requested real agent/network measurement.
	for i := 1; i <= 35; i++ {
		if err := s.AppendAssistantDelta(ctx, conversation, "reply", "provider", " output", "reply", histClock.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
		next, err := s.LoadConversationSnapshotDelta(ctx, conversation, page.Conversation.LatestSequence+1, page.ChangeCursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		if next.AfterSequence == nil || len(next.Messages) != 1 {
			t.Fatalf("stream delta %d: %#v", i, next)
		}
		for _, m := range next.Messages {
			merged[m.ID] = m
		}
		page = next
	}
	if err := s.SettleTurn(ctx, conversation, "provider", domain.TurnStateCompleted, "", histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	last, err := s.LoadConversationSnapshotDelta(ctx, conversation, page.Conversation.LatestSequence+1, page.ChangeCursor, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range last.Messages {
		merged[m.ID] = m
	}
	fresh, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Messages) != len(merged) {
		t.Fatal("merged row count differs")
	}
	for _, m := range fresh.Messages {
		got := merged[m.ID]
		if got.Revision != m.Revision || got.Text != m.Text || got.Streaming != m.Streaming {
			t.Fatalf("merged row %s differs", m.ID)
		}
	}
}

func TestConversationDeltaApprovalAndInputResolution(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: "prompt", Text: "run", Origin: domain.MessageOriginHuman}, "turn", histClock); err != nil {
		t.Fatal(err)
	}
	if err := s.BindTurnToProvider(ctx, "turn", "provider", histClock); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.ActivityKind{domain.ActivityKindApproval, domain.ActivityKindUserInput} {
		id := string(kind)
		if err := s.UpsertActivity(ctx, conversation, "provider", domain.ConversationActivity{ID: id, Kind: kind, Status: domain.ActivityStatusPending, Summary: "waiting", ProviderItemID: id, RequestID: id}, histClock); err != nil {
			t.Fatal(err)
		}
		before, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 200)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertActivity(ctx, conversation, "provider", domain.ConversationActivity{ID: id, Kind: kind, Status: domain.ActivityStatusResolved, Summary: "answered", ProviderItemID: id, RequestID: id}, histClock.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		delta, err := s.LoadConversationSnapshotDelta(ctx, conversation, before.Conversation.LatestSequence+1, before.ChangeCursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		if delta.AfterSequence == nil || len(delta.Activities) != 1 || delta.Activities[0].Status != domain.ActivityStatusResolved {
			t.Fatalf("resolution missing: %#v", delta)
		}
	}
}

func TestConversationDeltaIncludesChangedOlderTurnMetadata(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: "limited-prompt", Text: "work", Origin: domain.MessageOriginHuman}, "limited", histClock); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurnByID(ctx, "limited", domain.TurnStateFailed, "Usage limit reached; resets in 1 hour", histClock); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		seedToolTurn(t, s, conversation, session, fmt.Sprintf("later-%d", i), 0, histClock.Add(time.Duration(i+1)*time.Second))
	}
	before, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	reset := histClock.Add(time.Hour)
	if err := s.SetTurnAutoResume(ctx, conversation, "limited", &reset, true, ""); err != nil {
		t.Fatal(err)
	}
	delta, err := s.LoadConversationSnapshotDelta(ctx, conversation, before.Conversation.LatestSequence+1, before.ChangeCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if delta.AfterSequence == nil {
		t.Fatal("expected mergeable delta")
	}
	for _, turn := range delta.Turns {
		if turn.ID == "limited" {
			if turn.UsageLimit == nil || !turn.UsageLimit.AutoResume {
				t.Fatalf("missing changed metadata: %+v", turn)
			}
			return
		}
	}
	t.Fatal("changed older turn omitted")
}

func TestConversationDeltaRollbackRequiresReplacement(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		seedToolTurn(t, s, conversation, session, fmt.Sprintf("rollback-%d", i), 1, histClock.Add(time.Duration(i)*time.Second))
	}
	before, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollbackTurns(ctx, conversation, "rollback-2", histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	delta, err := s.LoadConversationSnapshotDelta(ctx, conversation, before.Conversation.LatestSequence+1, before.ChangeCursor, 200)
	if err != nil {
		t.Fatal(err)
	}
	if delta.AfterSequence != nil {
		t.Fatal("rollback must return unmarked replacement")
	}
	for _, m := range delta.Messages {
		if m.TurnID != "rollback-1" {
			t.Fatalf("discarded message leaked: %+v", m)
		}
	}
}

func TestConversationDeltaQueuedEditKeepsSequence(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: "edit-prompt", Text: "before", Origin: domain.MessageOriginHuman}, "edit", histClock); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateQueuedTurnMessage(ctx, conversation, "edit", "after", "", 0, histClock.Add(time.Second), domain.ConversationQueuedEditDelivery{}); err != nil {
		t.Fatal(err)
	}
	delta, err := s.LoadConversationSnapshotDelta(ctx, conversation, before.Conversation.LatestSequence+1, before.ChangeCursor, 200)
	if err != nil {
		t.Fatal(err)
	}
	if delta.AfterSequence == nil || len(delta.Messages) != 1 || delta.Messages[0].Text != "after" || delta.Messages[0].Sequence != before.Messages[0].Sequence || delta.Messages[0].Revision <= before.Messages[0].Revision {
		t.Fatalf("queued edit missing: %#v", delta)
	}
}

func TestConversationDeltaDoesNotRevivePromotedTurnMetadata(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	seedProject(t, s, "hist")
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateConversation(ctx, "conv-1", domain.ConversationScopeSession, "hist", session.ID, histClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, session.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	seedToolTurn(t, s, conversation.ID, session.ID, "retired", 1, histClock)
	seedToolTurn(t, s, conversation.ID, session.ID, "target", 0, histClock.Add(time.Second))
	// Seed a stored activity still referring to a promoted-away source. Normal
	// reads omit that source's metadata even though the old activity is visible.
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := db.ExecContext(ctx, "UPDATE conversation_turns SET promoted_to_turn_id = ? WHERE id = ?", "target", "retired"); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadConversationSnapshotPage(ctx, conversation.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertActivity(ctx, conversation.ID, "provider-retired", domain.ConversationActivity{ID: "retired-tool-0", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, Summary: "late correction", ProviderItemID: "retired-exec-0"}, histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	delta, err := s.LoadConversationSnapshotDelta(ctx, conversation.ID, before.Conversation.LatestSequence+1, before.ChangeCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if delta.AfterSequence == nil || len(delta.Activities) != 1 {
		t.Fatalf("expected changed-row delta: %#v", delta)
	}
	fresh, err := s.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	visible := make(map[string]bool)
	for _, turn := range fresh.Turns {
		visible[turn.ID] = true
	}
	for _, turn := range delta.Turns {
		if !visible[turn.ID] {
			t.Fatalf("delta revived turn %s absent from fresh snapshot", turn.ID)
		}
	}
}
