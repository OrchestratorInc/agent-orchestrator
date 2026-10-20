package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

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

	// A retry that fails the same way is a newer failure.
	firstAt := *session.StartupErrorAt
	if err := store.RecordWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		domain.WorkspaceStartupErrorProjectSetup, "Setup step 1 failed: exit status 3.", "missing secret"); err != nil {
		t.Fatalf("record again: %v", err)
	}
	if again, err := store.GetSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID); err != nil ||
		again.StartupErrorAt == nil || !again.StartupErrorAt.After(firstAt) {
		t.Fatalf("repeated failure at = %v (first %v), err %v", again.StartupErrorAt, firstAt, err)
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

// A message queued behind a failed checkout or Setup waits for a retry, so it
// neither keeps the sandbox up nor marks the session busy; once the worker
// reports the workspace ready, the same queued message does both again.
func TestQueuedTurnBehindAFailedWorkspaceStartDoesNotKeepTheSandboxUp(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	const owner = "workspace-startup-keep-alive"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running', interactive_until = NULL WHERE session_id = $1`, fixture.sessionID)
	exec(`UPDATE ao_sessions SET activity_state = 'idle' WHERE id = $1`, fixture.sessionID)
	exec(`INSERT INTO ao_events (org_id, session_id, sequence, type) VALUES ($1, $2, 1, 'chat.user_message')`, fixture.orgID, fixture.sessionID)
	exec(`INSERT INTO ao_turns (org_id, session_id, user_message_sequence) VALUES ($1, $2, 1)`, fixture.orgID, fixture.sessionID)
	keepAlive := func() bool {
		t.Helper()
		exec(`UPDATE ao_sandboxes SET reconcile_lease_owner = '', reconcile_lease_until = NULL,
			reconcile_after = now() - interval '1 hour' WHERE session_id = $1`, fixture.sessionID)
		claimed, err := store.ClaimSandboxes(ctx, owner, 100, time.Minute)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		for _, record := range claimed {
			if record.SessionID == fixture.sessionID {
				return record.KeepAlive
			}
		}
		t.Fatal("sandbox was not claimed")
		return false
	}

	if !keepAlive() {
		t.Fatal("a queued message on a healthy session must keep its sandbox up")
	}
	if err := store.RecordWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		domain.WorkspaceStartupErrorProjectSetup, "The project's Setup script failed at step 1 (exit status 1).", ""); err != nil {
		t.Fatalf("record: %v", err)
	}
	if keepAlive() {
		t.Fatal("a message queued behind a failed Setup kept the sandbox up")
	}
	if err := store.ClearWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !keepAlive() {
		t.Fatal("once the workspace is ready the queued message must keep the sandbox up again")
	}
}
