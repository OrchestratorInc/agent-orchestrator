package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// The startup error is stored under the reconcile lease, exposed on session
// reads and on claimed sandboxes, cleared by the worker's first heartbeat, and
// reset by a user retry that re-arms a parked sandbox.
func TestSandboxStartupErrorLifecycle(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	const owner = "startup-error-test-owner"

	setSandbox := func(query string, args ...any) {
		t.Helper()
		args = append([]any{fixture.sessionID, fixture.orgID}, args...)
		if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET `+query+` WHERE session_id = $1 AND org_id = $2`, args...); err != nil {
			t.Fatalf("update sandbox: %v", err)
		}
	}
	// The worker connection seeded by the fixture would make the session look
	// started; this test is about a worker that never checked in.
	setSandbox(`desired_state = 'running', observed_state = 'provisioning', worker_last_seen_at = NULL,
		reconcile_lease_owner = $3, reconcile_lease_until = now() + interval '1 minute'`, owner)

	if err := store.RecordSandboxStartupError(ctx, "someone-else", fixture.orgID, fixture.sessionID,
		"workspace_not_ready", "nope"); !errors.Is(err, ErrSandboxLeaseLost) {
		t.Fatalf("record without the lease = %v, want ErrSandboxLeaseLost", err)
	}
	const message = "Your Coder workspace wasn't ready after 20 minutes."
	if err := store.RecordSandboxStartupError(ctx, owner, fixture.orgID, fixture.sessionID,
		"workspace_not_ready", message); err != nil {
		t.Fatalf("record startup error: %v", err)
	}
	session, err := store.GetSession(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.StartupErrorCode != "workspace_not_ready" || session.StartupErrorMessage != message ||
		session.StartupErrorAt == nil || time.Since(*session.StartupErrorAt) > time.Minute {
		t.Fatalf("session startup error = %q/%q/%v", session.StartupErrorCode, session.StartupErrorMessage, session.StartupErrorAt)
	}
	firstAt := *session.StartupErrorAt
	// Re-recording the same error keeps its original timestamp.
	if err := store.RecordSandboxStartupError(ctx, owner, fixture.orgID, fixture.sessionID,
		"workspace_not_ready", message); err != nil {
		t.Fatal(err)
	}
	if session, _ = store.GetSession(ctx, principal, fixture.orgID, fixture.sessionID); !session.StartupErrorAt.Equal(firstAt) {
		t.Fatalf("repeated record moved startup_error_at from %s to %s", firstAt, session.StartupErrorAt)
	}

	// The reconciler sees the recorded code on its claim.
	setSandbox(`reconcile_lease_owner = '', reconcile_lease_until = NULL, reconcile_after = now() - interval '1 hour'`)
	claimed, err := store.ClaimSandboxes(ctx, owner, 100, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, record := range claimed {
		if record.SessionID == fixture.sessionID {
			found = true
			if record.StartupErrorCode != "workspace_not_ready" {
				t.Fatalf("claimed startup error code = %q", record.StartupErrorCode)
			}
		}
	}
	if !found {
		t.Fatal("sandbox was not claimed")
	}

	// A parked sandbox can be retried: it re-enters provisioning with a fresh
	// startup window and the error cleared.
	setSandbox(`observed_state = 'terminated', startup_attempts = 3, last_error = 'parked'`)
	if err := store.RetrySessionStartup(ctx, principal, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("retry startup: %v", err)
	}
	var observed, lastError, code string
	var attempts int
	var startedAt *time.Time
	if err := admin.QueryRow(ctx, `SELECT observed_state, last_error, startup_error_code, startup_attempts, startup_started_at
		FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&observed, &lastError, &code, &attempts, &startedAt); err != nil {
		t.Fatal(err)
	}
	if observed != domain.SandboxObservedProvisioning || lastError != "" || code != "" || attempts != 0 ||
		startedAt == nil || time.Since(*startedAt) > time.Minute {
		t.Fatalf("after retry: observed=%q lastError=%q code=%q attempts=%d startedAt=%v",
			observed, lastError, code, attempts, startedAt)
	}

	// The worker's first heartbeat clears a recorded error.
	setSandbox(`reconcile_lease_owner = $3, reconcile_lease_until = now() + interval '1 minute'`, owner)
	if err := store.RecordSandboxStartupError(ctx, owner, fixture.orgID, fixture.sessionID,
		"terminal_unavailable", "still starting"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, "test", fixture.epoch, nil); err != nil {
		t.Fatalf("mark worker seen: %v", err)
	}
	session, err = store.GetSession(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.StartupErrorCode != "" || session.StartupErrorMessage != "" || session.StartupErrorAt != nil {
		t.Fatalf("heartbeat left startup error %q/%q/%v", session.StartupErrorCode, session.StartupErrorMessage, session.StartupErrorAt)
	}

	// Once the worker has checked in there is nothing to retry.
	if err := store.RetrySessionStartup(ctx, principal, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry after check-in = %v, want ErrConflict", err)
	}
}
