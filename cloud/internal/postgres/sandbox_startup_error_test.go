package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
			if record.StartupErrorCode != "workspace_not_ready" ||
				record.StartupErrorAt == nil || !record.StartupErrorAt.Equal(firstAt) {
				t.Fatalf("claimed startup error = %q at %v, want at %s", record.StartupErrorCode, record.StartupErrorAt, firstAt)
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

// A failure opens the startup window of a sandbox whose worker never checked
// in, without moving one already open, so compute that keeps failing to be
// created is bounded by the startup budget. A checked-in worker's sandbox is
// not given a startup window by a failed probe.
func TestSandboxFailureOpensStartupWindowForUnstartedWorker(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	// Arranged and read as the organization, so forced row-level security
	// shows the rows to a test role that does not bypass it.
	admin = orgAdminPool(t, admin, fixture.orgID)
	const owner = "startup-window-test-owner"
	arm := func(query string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET `+query+`,
			reconcile_lease_owner = $3, reconcile_lease_until = now() + interval '1 minute'
			WHERE session_id = $1 AND org_id = $2`, fixture.sessionID, fixture.orgID, owner); err != nil {
			t.Fatalf("update sandbox: %v", err)
		}
	}
	startedAt := func() *time.Time {
		t.Helper()
		var at *time.Time
		if err := admin.QueryRow(ctx, `SELECT startup_started_at FROM ao_sandboxes WHERE session_id = $1`,
			fixture.sessionID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	fail := func() {
		t.Helper()
		if err := store.RecordSandboxFailure(ctx, owner, fixture.orgID, fixture.sessionID, "", "create failed"); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}

	arm(`desired_state = 'running', observed_state = 'requested', provider_environment_id = NULL,
		worker_last_seen_at = NULL, startup_started_at = NULL`)
	fail()
	opened := startedAt()
	if opened == nil || time.Since(*opened) > time.Minute {
		t.Fatalf("startup window after a failed create = %v, want opened now", opened)
	}

	arm(`startup_started_at = now() - interval '7 minutes'`)
	fail()
	if at := startedAt(); at == nil || time.Since(*at) < 6*time.Minute {
		t.Fatalf("a repeated failure moved the startup window to %v", at)
	}

	arm(`observed_state = 'running', worker_last_seen_at = now(), startup_started_at = NULL`)
	fail()
	if at := startedAt(); at != nil {
		t.Fatalf("a checked-in worker's failure opened a startup window at %v", at)
	}
}

// The failed build the reconciler acted on is stored under the reconcile
// lease, as one key of bootstrap_context, and read back on the next claim, so
// each failure is acted on once.
func TestSandboxProviderFailureIsFencedAndClaimed(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	// Arranged and read as the organization, so forced row-level security
	// shows the rows to a test role that does not bypass it.
	admin = orgAdminPool(t, admin, fixture.orgID)
	const owner = "provider-failure-test-owner"
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running',
		bootstrap_context = bootstrap_context || '{"kept": "yes"}'::jsonb,
		reconcile_lease_owner = $3, reconcile_lease_until = now() + interval '1 minute'
		WHERE session_id = $1 AND org_id = $2`, fixture.sessionID, fixture.orgID, owner); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSandboxProviderFailure(ctx, "someone-else", fixture.orgID, fixture.sessionID,
		"build-1"); !errors.Is(err, ErrSandboxLeaseLost) {
		t.Fatalf("record without the lease = %v, want ErrSandboxLeaseLost", err)
	}
	if err := store.RecordSandboxProviderFailure(ctx, owner, fixture.orgID, fixture.sessionID, "build-1#restarted"); err != nil {
		t.Fatalf("record provider failure: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET reconcile_lease_owner = '', reconcile_lease_until = NULL,
		reconcile_after = now() - interval '1 hour' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimSandboxes(ctx, owner, 100, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, record := range claimed {
		if record.SessionID == fixture.sessionID {
			found = true
			if record.ProviderFailureID != "build-1#restarted" {
				t.Fatalf("claimed provider failure = %q", record.ProviderFailureID)
			}
		}
	}
	if !found {
		t.Fatal("sandbox was not claimed")
	}
	// It is one key of bootstrap_context: the rest of the object is kept, and
	// a second write replaces the key.
	if err := store.RecordSandboxProviderFailure(ctx, owner, fixture.orgID, fixture.sessionID, "build-2"); err != nil {
		t.Fatalf("record second provider failure: %v", err)
	}
	var stored, other string
	if err := admin.QueryRow(ctx, `SELECT bootstrap_context ->> 'providerFailureId', bootstrap_context ->> 'kept'
		FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&stored, &other); err != nil {
		t.Fatal(err)
	}
	if stored != "build-2" || other != "yes" {
		t.Fatalf("bootstrap_context providerFailureId = %q kept = %q", stored, other)
	}
}

// The Cleanup commands a worker was given are stored as one key of
// bootstrap_context, fenced by the worker's live connection: the reconciler's
// providerFailureId (written either side of it) and the rest of the object
// are kept, a stale worker writes nothing, a long Cleanup is bounded, and the
// reconciler's claim reads them back.
func TestInstalledCleanupCommandsAreFencedAndKeyOnly(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	// Arranged and read as the organization, so forced row-level security
	// shows the rows to a test role that does not bypass it.
	admin = orgAdminPool(t, admin, fixture.orgID)
	const owner = "installed-cleanup-test-owner"
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running',
		bootstrap_context = bootstrap_context || '{"kept": "yes"}'::jsonb,
		reconcile_lease_owner = $3, reconcile_lease_until = now() + interval '1 minute'
		WHERE session_id = $1 AND org_id = $2`, fixture.sessionID, fixture.orgID, owner); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSandboxProviderFailure(ctx, owner, fixture.orgID, fixture.sessionID, "build-1"); err != nil {
		t.Fatal(err)
	}
	launched := []string{`export DEPLOY_TOKEN=FAKEVAL97x; printf '%s\n' "$DEPLOY_TOKEN"`, "make down"}
	if err := store.RecordInstalledCleanupCommands(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch+1,
		launched); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("record for a replaced worker = %v, want ErrStaleWorker", err)
	}
	if err := store.RecordInstalledCleanupCommands(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, launched); err != nil {
		t.Fatalf("record installed cleanup: %v", err)
	}
	if err := store.RecordSandboxProviderFailure(ctx, owner, fixture.orgID, fixture.sessionID, "build-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET reconcile_lease_owner = '', reconcile_lease_until = NULL,
		reconcile_after = now() - interval '1 hour' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimSandboxes(ctx, owner, 100, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, record := range claimed {
		if record.SessionID != fixture.sessionID {
			continue
		}
		found = true
		installed, ok := domain.InstalledCleanupCommands(record.BootstrapContext)
		if !ok || strings.Join(installed, "\n") != strings.Join(launched, "\n") || record.ProviderFailureID != "build-2" {
			t.Fatalf("claimed installed cleanup = %q (%v), provider failure = %q", installed, ok, record.ProviderFailureID)
		}
	}
	if !found {
		t.Fatal("sandbox was not claimed")
	}
	var kept string
	if err := admin.QueryRow(ctx, `SELECT bootstrap_context ->> 'kept' FROM ao_sandboxes WHERE session_id = $1`,
		fixture.sessionID).Scan(&kept); err != nil || kept != "yes" {
		t.Fatalf("bootstrap_context kept = %q, %v", kept, err)
	}

	// A Cleanup longer than the limit keeps its first commands that fit.
	long := make([]string, 40)
	for index := range long {
		long[index] = strings.Repeat("x", 1000)
	}
	if err := store.RecordInstalledCleanupCommands(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, long); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := admin.QueryRow(ctx, `SELECT bootstrap_context -> $2 FROM ao_sandboxes WHERE session_id = $1`,
		fixture.sessionID, domain.InstalledCleanupKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var bounded []string
	if err := json.Unmarshal(stored, &bounded); err != nil || len(bounded) == 0 || len(bounded) >= len(long) ||
		len(stored) > domain.InstalledCleanupLimit+len(bounded) {
		t.Fatalf("bounded cleanup: %d commands, %d bytes, %v", len(bounded), len(stored), err)
	}
}
