package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A guest cannot merge a shared project's pull request; they ask, the owner
// is told, and the owner's decision is recorded and told back to the guest.
func TestGuestsAskTheOwnerToMerge(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	alice := redeemProjectLink(t, store, admin, fixture, "editor", "Alice")
	bob := redeemProjectLink(t, store, admin, fixture, "editor", "Bob")
	carol := redeemProjectLink(t, store, admin, fixture, "viewer", "Carol")
	aliceSession := uuid.NewString()
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id, requested_by_user_id)
		VALUES ($1, $2, $3, 'worker', 'codex', 'Login flow', $6, $4, $5)`, aliceSession, fixture.orgID, fixture.projectID, fixture.userID, alice.UserID, "login-"+aliceSession)
	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, aliceSession, "github", "owner/repo", "author", 412,
		"https://github.com/owner/repo/pull/412", "login", "main", "abc123", "Login flow", 10, 2, 3)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.PullRequestForMerge(ctx, alice, fixture.orgID, aliceSession, pr.Number, pr.URL); !errors.Is(err, ErrMergeNeedsApproval) {
		t.Fatalf("Alice merging directly: %v, want ErrMergeNeedsApproval", err)
	}
	if _, err := store.PullRequestForMerge(ctx, owner, fixture.orgID, aliceSession, pr.Number, pr.URL); err != nil {
		t.Fatalf("the owner merging: %v", err)
	}
	if _, err := store.RequestPullRequestMerge(ctx, carol, fixture.orgID, aliceSession, pr.Number, pr.URL); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Carol, who only watches, asking: %v, want ErrForbidden", err)
	}
	if _, err := store.RequestPullRequestMerge(ctx, owner, fixture.orgID, aliceSession, pr.Number, pr.URL); !errors.Is(err, ErrConflict) {
		t.Fatalf("the owner asking: %v, want ErrConflict", err)
	}

	request, err := store.RequestPullRequestMerge(ctx, alice, fixture.orgID, aliceSession, pr.Number, pr.URL)
	if err != nil {
		t.Fatalf("Alice asking: %v", err)
	}
	if request.Status != "pending" || request.RequestedByName != "Alice" || request.PRNumber != 412 || request.SessionName != "Login flow" {
		t.Fatalf("request: %+v", request)
	}
	again, err := store.RequestPullRequestMerge(ctx, alice, fixture.orgID, aliceSession, pr.Number, pr.URL)
	if err != nil || again.ID != request.ID {
		t.Fatalf("asking again: %s, %v; want the pending request %s", again.ID, err, request.ID)
	}
	notifications := func(recipient domain.Principal, kind string) (open, resolved int) {
		t.Helper()
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FILTER (WHERE resolved_at IS NULL), count(*) FILTER (WHERE resolved_at IS NOT NULL)
			FROM ao_notifications WHERE org_id = $1 AND recipient_user_id = $2 AND type = $3`,
			fixture.orgID, recipient.UserID, kind,
		).Scan(&open, &resolved); err != nil {
			t.Fatal(err)
		}
		return open, resolved
	}
	if open, _ := notifications(owner, "merge_requested"); open != 1 {
		t.Fatalf("the owner has %d merge request notifications, want 1", open)
	}

	for who, want := range map[string]struct {
		principal domain.Principal
		count     int
	}{"owner": {owner, 1}, "Alice": {alice, 1}, "Bob": {bob, 0}} {
		listed, err := store.ListProjectMergeRequests(ctx, want.principal, fixture.orgID, fixture.projectID)
		if err != nil || len(listed) != want.count {
			t.Fatalf("%s lists %d, %v; want %d", who, len(listed), err, want.count)
		}
	}

	if _, err := store.PendingMergeRequestForDecision(ctx, alice, fixture.orgID, request.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Alice deciding her own request: %v, want ErrForbidden", err)
	}
	if _, err := store.PendingMergeRequestForDecision(ctx, owner, fixture.orgID, request.ID); err != nil {
		t.Fatalf("the owner deciding: %v", err)
	}
	merged, err := store.DecideMergeRequest(ctx, owner, fixture.orgID, request.ID, "merged")
	if err != nil {
		t.Fatalf("recording the merge: %v", err)
	}
	if merged.Status != "merged" || merged.DecidedByUserID != owner.UserID || merged.DecidedAt == nil {
		t.Fatalf("decided request: %+v", merged)
	}
	if open, resolved := notifications(owner, "merge_requested"); open != 0 || resolved != 1 {
		t.Fatalf("the owner's request notification: %d open, %d resolved", open, resolved)
	}
	if open, _ := notifications(alice, "merge_request_merged"); open != 1 {
		t.Fatalf("Alice was not told her request was merged")
	}
	if _, err := store.DecideMergeRequest(ctx, owner, fixture.orgID, request.ID, "rejected"); !errors.Is(err, ErrConflict) {
		t.Fatalf("deciding twice: %v, want ErrConflict", err)
	}

	second, err := store.RequestPullRequestMerge(ctx, alice, fixture.orgID, aliceSession, pr.Number, pr.URL)
	if err != nil || second.ID == request.ID {
		t.Fatalf("a new request after a decision: %s, %v", second.ID, err)
	}
	if _, err := store.DecideMergeRequest(ctx, owner, fixture.orgID, second.ID, "rejected"); err != nil {
		t.Fatalf("rejecting: %v", err)
	}
	if open, _ := notifications(alice, "merge_request_rejected"); open != 1 {
		t.Fatalf("Alice was not told her request was declined")
	}
}

// Asking for a merge, and deciding it, wakes the live streams of the
// request's session and of the project's orchestrator, whose live state then
// carries a new merge requests version.
func TestMergeRequestsReachLiveStreamsAtOnce(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	orchestrator, err := store.CreateSession(ctx, owner, fixture.orgID, "orch-"+uuid.NewString(), 10, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "orchestrator", Harness: "codex", DisplayName: "Orchestrator",
	})
	if err != nil {
		t.Fatal(err)
	}
	alice := redeemProjectLink(t, store, admin, fixture, "editor", "Alice")
	aliceSession := uuid.NewString()
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id, requested_by_user_id)
		VALUES ($1, $2, $3, 'worker', 'codex', 'Login flow', $6, $4, $5)`, aliceSession, fixture.orgID, fixture.projectID, fixture.userID, alice.UserID, "login-"+aliceSession)
	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, aliceSession, "github", "owner/repo", "author", 9,
		"https://github.com/owner/repo/pull/9", "login", "main", "abc123", "Login flow", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	version := func(sessionID string) string {
		t.Helper()
		state, err := store.SessionLiveState(ctx, owner, fixture.orgID, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		return state.MergeRequestsVersion
	}
	if v := version(orchestrator.ID); v != "" {
		t.Fatalf("a project with no requests has version %q", v)
	}

	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN ao_session_live`); err != nil {
		t.Fatal(err)
	}
	woken := func(want ...string) {
		t.Helper()
		missing := map[string]bool{}
		for _, id := range want {
			missing[id] = true
		}
		for len(missing) > 0 {
			notification, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				t.Fatalf("never woke %v: %v", missing, err)
			}
			delete(missing, notification.Payload)
		}
	}

	request, err := store.RequestPullRequestMerge(ctx, alice, fixture.orgID, aliceSession, pr.Number, pr.URL)
	if err != nil {
		t.Fatal(err)
	}
	woken(aliceSession, orchestrator.ID)
	asked := version(orchestrator.ID)
	if asked == "" || version(aliceSession) != asked {
		t.Fatalf("after asking, versions are %q and %q", asked, version(aliceSession))
	}

	if _, err := store.DecideMergeRequest(ctx, owner, fixture.orgID, request.ID, "rejected"); err != nil {
		t.Fatal(err)
	}
	woken(aliceSession, orchestrator.ID)
	if decided := version(orchestrator.ID); decided == asked {
		t.Fatalf("the version did not change when the request was decided: %q", decided)
	}
}
