package postgres

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Cleanup runs while a session is being deleted, and the control plane
// appends its steps afterwards: a deleted session (terminated, its sandbox's
// deletion requested and then completed) still takes events under the
// organization's RLS context, and its replay serves them.
func TestScriptStepEventsAppendToADeletedSession(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	appendStep := func(step int, status string) {
		t.Helper()
		exitCode, duration := 0, int64(42)
		event := worker.ScriptStepEvent{
			Phase: worker.ScriptPhaseCleanup, RunID: "cleanup-run", Step: step, Total: 2,
			Command: "make down", Status: status, Output: "stopped",
		}
		if status != worker.ScriptStepSkipped {
			event.ExitCode, event.DurationMS = &exitCode, &duration
		}
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendSessionEvent(ctx, fixture.orgID, fixture.sessionID, worker.ScriptStepEventType, payload); err != nil {
			t.Fatalf("append step %d to the deleted session: %v", step, err)
		}
	}

	if err := store.TerminateSession(ctx, principal, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	// The admin pool is held to forced RLS too when it does not own the
	// tables as a superuser, so it reads and writes under the org's context.
	asOrg := func(fn func(pgx.Tx) error) {
		t.Helper()
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, fixture.orgID); err != nil {
			t.Fatal(err)
		}
		if err := fn(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	asOrg(func(tx pgx.Tx) error {
		var desired string
		err := tx.QueryRow(ctx, `SELECT desired_state FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&desired)
		if err == nil && desired != "deleted" {
			t.Fatalf("sandbox desired state = %q, want deleted", desired)
		}
		return err
	})
	// While the reconciler deletes the sandbox.
	appendStep(1, worker.SetupSucceeded)
	// And after it completed the deletion.
	asOrg(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sandboxes SET observed_state = 'deleted', provider_environment_id = NULL
			WHERE session_id = $1`, fixture.sessionID)
		return err
	})
	appendStep(2, worker.ScriptStepSkipped)

	events, _, err := store.ListClientEvents(ctx, principal, fixture.orgID, fixture.sessionID, 0, 100, nil)
	if err != nil {
		t.Fatalf("replay the deleted session's events: %v", err)
	}
	var steps []worker.ScriptStepEvent
	for _, event := range events {
		if event.Type != worker.ScriptStepEventType {
			continue
		}
		var step worker.ScriptStepEvent
		if err := json.Unmarshal(event.Payload, &step); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, step)
	}
	if len(steps) != 2 || steps[0].Step != 1 || steps[0].Status != worker.SetupSucceeded ||
		steps[1].Step != 2 || steps[1].Status != worker.ScriptStepSkipped || steps[0].Phase != worker.ScriptPhaseCleanup {
		t.Fatalf("replayed steps = %+v", steps)
	}

	// Another organization's context sees no such session.
	if _, err := store.AppendSessionEvent(ctx, "00000000-0000-4000-8000-000000000999", fixture.sessionID,
		worker.ScriptStepEventType, json.RawMessage(`{}`)); err == nil {
		t.Fatal("an event was appended to the session under another organization's context")
	}
}

// A Setup or Cleanup step's output and where it ran (user, groups, directory,
// log file, hint) are served only to the organization's members: a share
// grantee gets the event with them stripped and outputHidden set, on replay
// and on the event stream alike (both read ListClientEvents).
func TestScriptOutputIsServedOnlyToMembers(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store = nonBypassStore(t, store, admin)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	exitCode, duration := 3, int64(42)
	step, err := json.Marshal(worker.ScriptStepEvent{
		Phase: worker.ScriptPhaseSetup, RunID: "setup-run", Step: 1, Total: 1, Command: "make deps",
		Status: worker.SetupFailed, ExitCode: &exitCode, DurationMS: &duration, Output: "DB host db.internal:5432",
		LogPath: "/home/ao/.ao/setup/step-1.log", User: "ao-worker", Groups: []string{"ao-worker", "docker"},
		Dir: "/workspace/app", Hint: "This step ran as ao-worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := json.Marshal(worker.SetupEvent{
		Status: worker.SetupFailed, Step: 1, ExitCode: 3, Output: "DB host db.internal:5432", Command: "make deps",
		User: "ao-worker", Groups: []string{"docker"}, Dir: "/workspace/app", LogPath: "/tmp/log", Hint: "ran as ao-worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	for eventType, payload := range map[string][]byte{worker.ScriptStepEventType: step, worker.SetupEventType: summary} {
		if _, err := store.AppendSessionEvent(ctx, fixture.orgID, fixture.sessionID, eventType, payload); err != nil {
			t.Fatal(err)
		}
	}
	_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID,
		domain.CreateShareLink{Role: "viewer", AccessScope: "anyone", Interaction: "view"})
	if err != nil {
		t.Fatal(err)
	}
	guestID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users (id, auth_provider, external_user_id, email, display_name, password_hash)
		VALUES ($1::uuid, 'local', $1::uuid::text, $1::uuid::text || '@example.test', 'Guest', 'hash')`, guestID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, guestID) })
	guest := domain.Principal{UserID: guestID, Provider: "local"}
	if _, err := store.RedeemProjectShareLink(ctx, guest, fixture.orgID, token); err != nil {
		t.Fatal(err)
	}

	payloads := func(principal domain.Principal) map[string]map[string]any {
		t.Helper()
		events, _, err := store.ListClientEvents(ctx, principal, fixture.orgID, fixture.sessionID, 0, 100, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]map[string]any{}
		for _, event := range events {
			var payload map[string]any
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			found[event.Type] = payload
		}
		return found
	}
	member := payloads(owner)
	if member[worker.ScriptStepEventType]["output"] != "DB host db.internal:5432" || member[worker.ScriptStepEventType]["user"] != "ao-worker" ||
		member[worker.SetupEventType]["output"] != "DB host db.internal:5432" || member[worker.ScriptStepEventType]["outputHidden"] != nil {
		t.Fatalf("member payloads = %+v", member)
	}
	shared := payloads(guest)
	for _, eventType := range []string{worker.ScriptStepEventType, worker.SetupEventType} {
		payload := shared[eventType]
		if payload["outputHidden"] != true || payload["command"] != "make deps" || payload["status"] != worker.SetupFailed {
			t.Fatalf("%s payload for a share grantee = %+v", eventType, payload)
		}
		for _, field := range []string{"user", "groups", "dir", "logPath", "hint"} {
			if _, ok := payload[field]; ok {
				t.Fatalf("%s payload for a share grantee carries %s: %+v", eventType, field, payload)
			}
		}
	}
	if shared[worker.ScriptStepEventType]["output"] != "" || shared[worker.ScriptStepEventType]["step"] != float64(1) {
		t.Fatalf("script step for a share grantee = %+v", shared[worker.ScriptStepEventType])
	}
	if _, ok := shared[worker.SetupEventType]["output"]; ok {
		t.Fatalf("setup summary for a share grantee = %+v", shared[worker.SetupEventType])
	}
}

// A step's final event replaces its live updates: appending it deletes the
// step's earlier running events of the same run, and nothing else, under the
// app role's forced RLS. A client reading after a deleted event's sequence
// still gets everything after it.
func TestScriptStepFinalEventDropsItsLiveUpdates(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store = nonBypassStore(t, store, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	appendStep := func(runID string, step int, status, output string) int64 {
		t.Helper()
		event := worker.ScriptStepEvent{Phase: worker.ScriptPhaseSetup, RunID: runID, Step: step, Total: 2,
			Status: status, Output: output}
		if status != worker.ScriptStepRunning {
			exitCode, duration := 0, int64(5)
			event.ExitCode, event.DurationMS = &exitCode, &duration
		}
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		appended, err := store.AppendSessionEvent(ctx, fixture.orgID, fixture.sessionID, worker.ScriptStepEventType, payload)
		if err != nil {
			t.Fatal(err)
		}
		return appended.Sequence
	}
	firstLive := appendStep("run-a", 1, worker.ScriptStepRunning, "")
	appendStep("run-a", 1, worker.ScriptStepRunning, "line 1")
	if _, err := store.AppendSessionEvent(ctx, fixture.orgID, fixture.sessionID, "workspace.changed", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	appendStep("run-b", 1, worker.ScriptStepRunning, "other run")
	appendStep("run-a", 1, worker.SetupSucceeded, "line 1\nline 2")
	appendStep("run-a", 2, worker.ScriptStepRunning, "")

	events, _, err := store.ListClientEvents(ctx, principal, fixture.orgID, fixture.sessionID, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, event := range events {
		if event.Type != worker.ScriptStepEventType {
			kept = append(kept, event.Type)
			continue
		}
		var step worker.ScriptStepEvent
		if err := json.Unmarshal(event.Payload, &step); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, step.RunID+"/"+strconv.Itoa(step.Step)+"/"+step.Status)
	}
	if got := strings.Join(kept, " "); got != "workspace.changed run-b/1/running run-a/1/succeeded run-a/2/running" {
		t.Fatalf("events = %s", got)
	}
	after, _, err := store.ListClientEvents(ctx, principal, fixture.orgID, fixture.sessionID, firstLive, 100, nil)
	if err != nil || len(after) != len(events) {
		t.Fatalf("events after a deleted update's sequence = %d (%v), want %d", len(after), err, len(events))
	}
}

// The types filter serves only the client event types it names; a type that
// is not served to clients matches nothing.
func TestListClientEventsFiltersByType(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	for _, eventType := range []string{"workspace.changed", worker.SetupEventType, "chat.assistant_delta", worker.ScriptStepEventType} {
		if _, err := store.AppendSessionEvent(ctx, fixture.orgID, fixture.sessionID, eventType, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	listed := func(types []string) string {
		t.Helper()
		events, _, err := store.ListClientEvents(ctx, principal, fixture.orgID, fixture.sessionID, 0, 100, types)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, event := range events {
			names = append(names, event.Type)
		}
		return strings.Join(names, " ")
	}
	if got := listed([]string{worker.ScriptStepEventType, worker.SetupEventType}); got != "workspace.setup workspace.script.step" {
		t.Fatalf("filtered = %q", got)
	}
	if got := listed([]string{"worker.secret_internal"}); got != "" {
		t.Fatalf("a type not served to clients = %q", got)
	}
	if got := listed(nil); got != "workspace.changed workspace.setup chat.assistant_delta workspace.script.step" {
		t.Fatalf("unfiltered = %q", got)
	}
}
