package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A startup error the worker reports because it could not prepare the
// workspace survives its heartbeats (which clear a reconciler startup error),
// is visible with its detail on the session, and is cleared only by the
// worker's ready report.
func TestWorkspaceStartupErrorSurvivesHeartbeatsUntilReady(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running'
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	read := func() (code, message, detail string) {
		t.Helper()
		if err := admin.QueryRow(ctx, `SELECT startup_error_code, startup_error_message, startup_error_detail
			FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&code, &message, &detail); err != nil {
			t.Fatal(err)
		}
		return code, message, detail
	}

	if err := store.RecordWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		domain.WorkspaceStartupErrorProjectSetup, "Setup step 1 failed: exit status 3.", "missing secret"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, "test", fixture.epoch, nil); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if code, message, detail := read(); code != domain.WorkspaceStartupErrorProjectSetup ||
		message != "Setup step 1 failed: exit status 3." || detail != "missing secret" {
		t.Fatalf("after heartbeat: %q %q %q", code, message, detail)
	}

	// The session shows the Setup output even on AO's managed compute, where
	// other startup error detail is hidden.
	session, err := store.GetSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.StartupErrorCode != domain.WorkspaceStartupErrorProjectSetup || session.StartupErrorAt == nil ||
		session.StartupErrorDetail != "missing secret" {
		t.Fatalf("session startup error = %q %v %q", session.StartupErrorCode, session.StartupErrorAt, session.StartupErrorDetail)
	}

	// A stale worker cannot record or clear the session's error.
	if err := store.ClearWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch-1); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("stale clear = %v", err)
	}
	if err := store.RecordWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		"worker_never_started", "x", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reconciler code = %v", err)
	}

	if err := store.ClearWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if code, message, detail := read(); code != "" || message != "" || detail != "" {
		t.Fatalf("after ready: %q %q %q", code, message, detail)
	}

	// A heartbeat still clears a startup error the reconciler recorded, and a
	// ready report leaves one alone.
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET startup_error_code = 'worker_never_started',
		startup_error_message = 'never started', startup_error_at = now() WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if code, _, _ := read(); code != "worker_never_started" {
		t.Fatalf("ready report cleared a reconciler error: %q", code)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, "test", fixture.epoch, nil); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if code, _, _ := read(); code != "" {
		t.Fatalf("heartbeat kept a reconciler error: %q", code)
	}
}
