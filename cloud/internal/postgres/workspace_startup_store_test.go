package postgres

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
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

// Each failure the worker reports is a new one: a retried Setup that fails
// with the same code and message moves the error's time, so the desktop can
// tell the new failure from the one it retried.
func TestWorkspaceStartupErrorTimeMovesOnARepeatedFailure(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running'
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	record := func() time.Time {
		t.Helper()
		if err := store.RecordWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
			domain.WorkspaceStartupErrorProjectSetup, "Setup step 1 failed: exit status 3.", "missing secret"); err != nil {
			t.Fatalf("record: %v", err)
		}
		session, err := store.GetSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID)
		if err != nil || session.StartupErrorAt == nil {
			t.Fatalf("session = %+v (%v)", session, err)
		}
		return *session.StartupErrorAt
	}
	first := record()
	time.Sleep(10 * time.Millisecond)
	if again := record(); !again.After(first) {
		t.Fatalf("a repeated failure kept its time: %s, then %s", first, again)
	}
}

// A message queued behind a failed checkout or Setup waits for a retry, so it
// neither keeps the sandbox up nor marks the session busy; once the worker
// reports the workspace ready, the same queued message does both again.
func TestQueuedTurnBehindAFailedWorkspaceStartDoesNotKeepTheSandboxUp(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	// Arranged as the organization, so forced row-level security allows it
	// under a role that does not bypass it.
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	// The claim runs in the service context, which ao_turns' row-level
	// security does not admit: under a role that does not bypass it no turn
	// keeps a claim alive at all (see
	// TestQueuedTurnBehindAFailedWorkspaceStartupIsNotWorkInFlight).
	var bypassesRLS bool
	if err := admin.QueryRow(ctx, `SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&bypassesRLS); err != nil {
		t.Fatal(err)
	}
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
	keptAlive := func() bool {
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
	keepAlive := func() bool {
		t.Helper()
		kept := keptAlive()
		if !bypassesRLS && kept {
			t.Fatal("a claim read a turn the service context cannot see")
		}
		return kept || !bypassesRLS
	}
	dropped := func() bool {
		t.Helper()
		return !keptAlive()
	}

	if !keepAlive() {
		t.Fatal("a queued message on a healthy session must keep its sandbox up")
	}
	if err := store.RecordWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		domain.WorkspaceStartupErrorProjectSetup, "The project's Setup script failed at step 1 (exit status 1).", ""); err != nil {
		t.Fatalf("record: %v", err)
	}
	if !dropped() {
		t.Fatal("a message queued behind a failed Setup kept the sandbox up")
	}
	// A retry the person asked for runs Setup again while the failure stays
	// recorded: the worker's busy mark keeps the sandbox up meanwhile, so the
	// retry is not paused mid-run.
	exec(`UPDATE ao_sandboxes SET last_busy_at = now() WHERE session_id = $1`, fixture.sessionID)
	if !keepAlive() {
		t.Fatal("a queued message during a running retry must keep the sandbox up")
	}
	exec(`UPDATE ao_sandboxes SET last_busy_at = now() - interval '5 minutes' WHERE session_id = $1`, fixture.sessionID)
	if !dropped() {
		t.Fatal("a retry that is no longer running kept the sandbox up")
	}
	if err := store.ClearWorkspaceStartupError(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !keepAlive() {
		t.Fatal("once the workspace is ready the queued message must keep the sandbox up again")
	}
}

// The startup error codes the session reads and the turn queries match in SQL
// are hand-written copies of domain.WorkspaceStartupErrorCodes. A code added
// there must reach every copy, or a task queued behind that failure keeps its
// session reading active and its VM awake.
func TestWorkspaceStartupErrorCodeSQLListsMatchTheDomain(t *testing.T) {
	want := append([]string(nil), domain.WorkspaceStartupErrorCodes()...)
	slices.Sort(want)
	parse := func(t *testing.T, list string) []string {
		t.Helper()
		var codes []string
		for _, item := range strings.Split(list, ",") {
			item = strings.TrimSpace(item)
			if len(item) < 2 || item[0] != '\'' || item[len(item)-1] != '\'' {
				t.Fatalf("SQL list item %q is not a string literal", item)
			}
			codes = append(codes, item[1:len(item)-1])
		}
		slices.Sort(codes)
		return codes
	}
	inLists := regexp.MustCompile(`startup_error_code, ''\) IN \(([^)]*)\)`)
	for name, query := range map[string]string{
		"workspaceStartupErrorCodesSQL": "COALESCE(x.startup_error_code, '') IN (" + workspaceStartupErrorCodesSQL + ")",
		"sessionSelect":                 sessionSelect,
		"queuedBehindFailedStartupSQL":  queuedBehindFailedStartupSQL("turn", "sandbox"),
	} {
		t.Run(name, func(t *testing.T) {
			matches := inLists.FindAllStringSubmatch(query, -1)
			if len(matches) == 0 {
				t.Fatal("no startup error code list found")
			}
			for _, match := range matches {
				if got := parse(t, match[1]); !slices.Equal(got, want) {
					t.Fatalf("SQL lists startup error codes %v, want %v", got, want)
				}
			}
		})
	}
}
