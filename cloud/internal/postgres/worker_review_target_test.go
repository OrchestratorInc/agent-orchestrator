package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

// A worker's `ao review trigger` reviews only its own session's PRs, with the
// session's reviewer and its creator's redeemable credentials.
func TestWorkerReviewTargetResolvesTheSessionsOwnReview(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if _, err := store.UpsertProviderConnection(ctx, principal, fixture.orgID, "codex", "default", []byte("ciphertext"), []byte("nonce"), json.RawMessage(`{"credentialType":"api_key"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_provider_connections SET validation_state = 'valid' WHERE org_id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	own, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 1, "https://github.test/owner/repo/pull/1", "feature", "main", "sha-1", "Own", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id)
		VALUES ($1, $2, $3, 'worker', 'claude-code', 'Other', 'other', $4)`, other, fixture.orgID, fixture.projectID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePullRequestRecord(ctx, fixture.orgID, other, "github", "owner/repo", "author", 2, "https://github.test/owner/repo/pull/2", "other", "main", "sha-2", "Other", 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	reviewer, available, prs, err := store.WorkerReviewTarget(ctx, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if reviewer != "codex" || len(available) != 1 || available[0] != "codex" {
		t.Fatalf("reviewer=%q available=%v", reviewer, available)
	}
	if len(prs) != 1 || prs[0].ID != own.ID {
		t.Fatalf("prs = %+v, want only this session's PR", prs)
	}

	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET reviewer_harness = 'claude-code' WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if reviewer, _, _, err = store.WorkerReviewTarget(ctx, fixture.orgID, fixture.sessionID); err != nil || reviewer != "claude-code" {
		t.Fatalf("selected reviewer=%q err=%v", reviewer, err)
	}

	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated = true WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.WorkerReviewTarget(ctx, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terminated session err = %v, want ErrNotFound", err)
	}
}
