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

func TestParentlessSpawnWritesNothing(t *testing.T) {
	svc, st, fc := actionFixture(t)
	fc.spawnRecord = domain.SessionRecord{ID: "child", ProjectID: "mer", DisplayName: "Reviewer", Harness: "codex"}
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: "codex", ClientRequestID: "solo"}
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(st.activities) != 0 {
		t.Fatalf("activities = %+v, want no row in the child chat", st.activities)
	}
}

func TestTerminateRecordsOnSpawnParent(t *testing.T) {
	svc, st, fc := actionFixture(t)
	fc.spawnRecord = domain.SessionRecord{ID: "child", ProjectID: "mer", DisplayName: "Reviewer", Harness: "codex"}
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: "parent", ClientRequestID: "req-spawn"}
	if _, _, _, err := svc.Spawn(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	st.sessions["child"] = domain.SessionRecord{ID: "child", ProjectID: "mer", DisplayName: "Reviewer", CleanupGeneration: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Kill(ctx, "child"); err != nil {
		t.Fatal(err)
	}
	terminated := actionsNamed(st.activities, actionSessionTerminated)
	if len(terminated) != 1 || terminated[0].ConversationID != "conv-parent" {
		t.Fatalf("terminate = %+v, want the parent conversation", terminated)
	}
}

func TestRequestKillDoesNotConfirmPendingCleanup(t *testing.T) {
	svc, st, fc := actionFixture(t)
	fc.requestKillResult = &sessionmanager.KillResult{CleanupPending: true}
	result, err := svc.RequestKill(context.Background(), "mer-1")
	if err != nil || !result.CleanupPending {
		t.Fatalf("request kill = %+v, %v; want pending cleanup", result, err)
	}
	if terminated := actionsNamed(st.activities, actionSessionTerminated); len(terminated) != 0 {
		t.Fatalf("pending cleanup recorded completed termination: %+v", terminated)
	}

	fc.requestKillResult = &sessionmanager.KillResult{Freed: true}
	if _, err := svc.RequestKill(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	terminated := actionsNamed(st.activities, actionSessionTerminated)
	if len(terminated) != 1 || terminated[0].Status != domain.ActivityStatusCompleted {
		t.Fatalf("confirmed termination = %+v, want one completed action", terminated)
	}
}

func TestTerminateRenameRestoreAndClaim(t *testing.T) {
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

	svc.recordClaimed(ctx, "mer-1", domain.PullRequest{URL: "https://github.com/acme/repo/pull/7", Number: 7, Title: "Readable actions"}, domain.ActivityStatusCompleted, nil)
	svc.recordClaimed(ctx, "mer-1", domain.PullRequest{URL: "https://github.com/acme/repo/pull/7", Number: 7, Title: "Readable actions"}, domain.ActivityStatusCompleted, nil)
	failedCtx := context.WithValue(ctx, middleware.RequestIDKey, "req-claim-bad")
	svc.recordClaimed(failedCtx, "mer-1", domain.PullRequest{URL: "javascript:alert(1)", Number: 8, Title: "bad"}, domain.ActivityStatusFailed, errors.New("no"))
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

func TestFailedReclaimKeepsConfirmedClaim(t *testing.T) {
	svc, st, _ := actionFixture(t)
	ctx := context.Background()
	pr := domain.PullRequest{URL: "https://github.com/acme/repo/pull/7", Number: 7, Title: "Readable actions"}
	svc.recordClaimed(ctx, "mer-1", pr, domain.ActivityStatusCompleted, nil)
	failedCtx := context.WithValue(ctx, middleware.RequestIDKey, "req-reclaim")
	svc.recordClaimed(failedCtx, "mer-1", pr, domain.ActivityStatusFailed, ErrPRNotOpen)
	claims := actionsNamed(st.activities, actionPullRequestClaimed)
	if len(claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(claims))
	}
	if claims[0].Status != domain.ActivityStatusCompleted {
		t.Fatalf("confirmed claim settled as %q", claims[0].Status)
	}
	if claims[1].Status != domain.ActivityStatusFailed {
		t.Fatalf("failed reclaim status = %q", claims[1].Status)
	}
}

func TestClaimAdmissionRejectionsWriteNoAction(t *testing.T) {
	svc, st, _ := actionFixture(t)
	rec := st.sessions["mer-1"]
	rec.Metadata.WorkspacePath = "/ws/mer-1"
	st.sessions["mer-1"] = rec
	project := st.projects["mer"]
	project.RepoOriginURL = "https://github.com/acme/repo"
	st.projects["mer"] = project
	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "req-claim-admission")

	if _, err := svc.ClaimPR(ctx, "mer-1", "https://github.com/other/repo/pull/7", ClaimPROptions{}); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("repository mismatch = %v, want ErrProjectMismatch", err)
	}
	if _, err := svc.ClaimPR(ctx, "mer-1", "https://github.com/acme/repo/pull/7", ClaimPROptions{}); !errors.Is(err, ErrSCMUnavailable) {
		t.Fatalf("missing SCM = %v, want ErrSCMUnavailable", err)
	}
	if got := actionsNamed(st.activities, actionPullRequestClaimed); len(got) != 0 {
		t.Fatalf("preflight rejections wrote claim actions: %+v", got)
	}

	started := NewWithDeps(Deps{Store: st, SCM: fakeSCM{fetchErr: ports.ErrSCMNotFound}, PRClaimer: &fakePRClaimer{}})
	if _, err := started.ClaimPR(ctx, "mer-1", "https://github.com/acme/repo/pull/7", ClaimPROptions{}); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("attempted claim = %v, want ErrPRNotFound", err)
	}
	if got := actionsNamed(st.activities, actionPullRequestClaimed); len(got) != 1 || got[0].Status != domain.ActivityStatusFailed {
		t.Fatalf("attempted claim actions = %+v, want one failure", got)
	}
}

func TestRenameCycleKeepsEachMutation(t *testing.T) {
	svc, st, _ := actionFixture(t)
	ctx := context.Background()
	for _, name := range []string{"Ship", "Reviewer", "Ship"} {
		if err := svc.Rename(ctx, "mer-1", name); err != nil {
			t.Fatal(err)
		}
	}
	renames := actionsNamed(st.activities, actionSessionRenamed)
	if len(renames) != 3 {
		t.Fatalf("renames = %d, want 3", len(renames))
	}
	last := decodeAction(t, renames[2])
	if last.PreviousDisplayName != "Reviewer" || last.DisplayName != "Ship" {
		t.Fatalf("last rename = %+v", last)
	}
	if err := svc.Rename(ctx, "mer-1", "Ship"); err != nil {
		t.Fatal(err)
	}
	if len(actionsNamed(st.activities, actionSessionRenamed)) != 3 {
		t.Fatal("unchanged rename created another activity")
	}
}

func TestActionErrorTextUsesAPIMessage(t *testing.T) {
	if got := actionErrorText(ErrPRNotOpen); got != "PR is not open" {
		t.Fatalf("error text = %q", got)
	}
	if got := actionErrorText(errors.New("plain failure")); got != "plain failure" {
		t.Fatalf("error text = %q", got)
	}
}

func TestStandaloneSessionHref(t *testing.T) {
	got := sessionActionHref("", "standalone-1")
	if got != "ao://sessions/__standalone__/standalone-1" {
		t.Fatalf("href = %q", got)
	}
	if safeActionHref(got) != got {
		t.Fatalf("href rejected: %q", got)
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
