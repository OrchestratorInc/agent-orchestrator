package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func actionFixture(t *testing.T) (*Service, *fakeStore, *fakeCommander) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.conversations = map[domain.SessionID]domain.ConversationRecord{
		"parent": {ID: "conv-parent", SessionID: "parent"},
		"child":  {ID: "conv-child", SessionID: "child"},
		"mer-1":  {ID: "conv-mer", SessionID: "mer-1"},
	}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", DisplayName: "Reviewer", Harness: "codex", CleanupGeneration: 3,
	}
	fc := &fakeCommander{}
	return NewWithDeps(Deps{Manager: fc, Store: st}), st, fc
}

func TestSpawnRecordsOneParentActionAndDedupsReplay(t *testing.T) {
	svc, st, fc := actionFixture(t)
	fc.spawnRecord = domain.SessionRecord{ID: "child", ProjectID: "mer", DisplayName: "Reviewer", Harness: "codex"}
	cfg := ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: "codex",
		ParentSessionID: "parent", ClientRequestID: "req-spawn", ClientRequestHash: "hash",
	}
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	st.sessions["child"] = domain.SessionRecord{
		ID: "child", ProjectID: "mer", ClientRequestID: "req-spawn", ClientRequestHash: "hash", ClientRequestCommitted: true,
	}
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(st.activities) != 1 {
		t.Fatalf("activities = %d, want 1", len(st.activities))
	}
	activity := st.activities[0]
	if activity.Kind != domain.ActivityKindAOAction || activity.Status != domain.ActivityStatusCompleted {
		t.Fatalf("activity = %+v", activity)
	}
	if activity.ConversationID != "conv-parent" {
		t.Fatalf("conversation = %s, want parent", activity.ConversationID)
	}
	detail := decodeAction(t, activity)
	if detail.Action != actionSessionSpawned || detail.SourceSessionID != "parent" || detail.TargetSessionID != "child" {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Href != "ao://sessions/mer/child" || detail.Harness != "codex" || detail.DisplayName != "Reviewer" {
		t.Fatalf("detail = %+v", detail)
	}
	if fc.spawnCalls != 2 {
		t.Fatalf("spawn calls = %d, want 2 before the committed replay", fc.spawnCalls)
	}
}

func TestSpawnFailureAndCancellationStayOneAction(t *testing.T) {
	svc, st, fc := actionFixture(t)
	fc.spawnErr = context.Canceled
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: "parent", ClientRequestID: "spawn-1"}
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err == nil {
		t.Fatal("expected cancellation")
	}
	fc.spawnErr = errors.New("workspace failed")
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err == nil {
		t.Fatal("expected failure")
	}
	if len(st.activities) != 1 {
		t.Fatalf("activities = %d, want the client request to settle one row", len(st.activities))
	}
	if st.activities[0].Status != domain.ActivityStatusFailed {
		t.Fatalf("status = %s, want failed after the later failure", st.activities[0].Status)
	}
	detail := decodeAction(t, st.activities[0])
	if detail.Action != actionSessionSpawned || detail.TargetSessionID != "" {
		t.Fatalf("detail = %+v", detail)
	}

	fc.spawnErr = context.Canceled
	cfg.ClientRequestID = "spawn-2"
	_, _, _, _ = svc.Spawn(context.Background(), cfg)
	if len(st.activities) != 2 || st.activities[1].Status != domain.ActivityStatusCancelled {
		t.Fatalf("activities = %+v", st.activities)
	}
}

func TestTerminateRenameRestoreSwitchAndClaim(t *testing.T) {
	svc, st, fc := actionFixture(t)
	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "req-1")
	if _, err := svc.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(st.activities) != 1 || decodeAction(t, st.activities[0]).Action != actionSessionTerminated {
		t.Fatalf("terminate activities = %+v", st.activities)
	}

	fc.killErr = context.Canceled
	if _, err := svc.RequestKill(ctx, "mer-1"); err == nil {
		t.Fatal("expected cancel")
	}
	if len(st.activities) != 2 || st.activities[1].Status != domain.ActivityStatusCancelled {
		t.Fatalf("cancel activities = %+v", st.activities)
	}

	if err := svc.Rename(ctx, "mer-1", "Ship"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rename(ctx, "mer-1", "Ship"); err != nil {
		t.Fatal(err)
	}
	renames := actionsNamed(st.activities, actionSessionRenamed)
	if len(renames) != 1 {
		t.Fatalf("renames = %+v", renames)
	}
	renamed := decodeAction(t, renames[0])
	if renamed.PreviousDisplayName != "Reviewer" || renamed.DisplayName != "Ship" {
		t.Fatalf("rename detail = %+v", renamed)
	}

	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", DisplayName: "Ship", CleanupGeneration: 4,
	}
	fc.restoreResult = sessionmanager.RestoreResult{Session: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", DisplayName: "Ship", CleanupGeneration: 4}}
	if _, err := svc.Restore(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Restore(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(actionsNamed(st.activities, actionSessionRestored)) != 1 {
		t.Fatalf("restores = %+v", st.activities)
	}

	fc.switchRecord = domain.AgentSwitch{
		ID: "switch-1", State: domain.AgentSwitchCompleted, FromHarness: "codex", TargetHarness: "claude",
	}
	if _, err := svc.SwitchAgent(ctx, "mer-1", SwitchAgentInput{TargetHarness: "claude", IdempotencyKey: "same"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SwitchAgent(ctx, "mer-1", SwitchAgentInput{TargetHarness: "claude", IdempotencyKey: "same"}); err != nil {
		t.Fatal(err)
	}
	switches := actionsNamed(st.activities, actionSessionAgentSwitched)
	if len(switches) != 1 || switches[0].Revision != 2 {
		t.Fatalf("switches = %+v", switches)
	}
	switched := decodeAction(t, switches[0])
	if switched.PreviousHarness != "codex" || switched.Harness != "claude" {
		t.Fatalf("switch detail = %+v", switched)
	}

	svc.recordClaimed(ctx, "mer-1", domain.PullRequest{URL: "https://github.com/acme/repo/pull/7", Number: 7, Title: "Readable actions"}, domain.ActivityStatusCompleted, nil)
	svc.recordClaimed(ctx, "mer-1", domain.PullRequest{URL: "https://github.com/acme/repo/pull/7", Number: 7, Title: "Readable actions"}, domain.ActivityStatusCompleted, nil)
	svc.recordClaimed(ctx, "mer-1", domain.PullRequest{URL: "javascript:alert(1)", Number: 8, Title: "bad"}, domain.ActivityStatusFailed, errors.New("no"))
	claims := actionsNamed(st.activities, actionPullRequestClaimed)
	if len(claims) != 2 {
		t.Fatalf("claims = %d", len(claims))
	}
	safe := decodeAction(t, claims[0])
	if safe.Href != "https://github.com/acme/repo/pull/7" || safe.PRNumber != 7 {
		t.Fatalf("claim = %+v", safe)
	}
	if decodeAction(t, claims[1]).Href != "" {
		t.Fatalf("unsafe href stored: %+v", decodeAction(t, claims[1]))
	}
}

func decodeAction(t *testing.T, activity domain.ConversationActivity) actionDetail {
	t.Helper()
	var detail actionDetail
	if err := json.Unmarshal(activity.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}

func actionsNamed(activities []domain.ConversationActivity, action string) []domain.ConversationActivity {
	var matched []domain.ConversationActivity
	for _, activity := range activities {
		var detail actionDetail
		if json.Unmarshal(activity.Detail, &detail) != nil {
			continue
		}
		if detail.Action == action {
			matched = append(matched, activity)
		}
	}
	return matched
}
