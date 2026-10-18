package postgres

import (
	"context"
	"testing"
	"time"
)

// A live worker's heartbeat defers supervision of a running sandbox, but it
// must not defer the deletion of its own sandbox. The worker heartbeats every
// 20 seconds until its compute is gone; when every heartbeat pushed the row
// 30 seconds out, a deletion that needed a second reconcile tick (Coder runs
// the project's Cleanup before deleting) was never claimed again.
func TestWorkerHeartbeatDoesNotDeferASandboxDeletion(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	const owner = "heartbeat-delete-test-owner"

	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes
		SET desired_state = 'deleted', observed_state = 'deleting',
			deletion_requested_at = now(), reconcile_after = now() + interval '2 seconds',
			reconcile_lease_owner = '', reconcile_lease_until = NULL
		WHERE session_id = $1 AND org_id = $2`, fixture.sessionID, fixture.orgID); err != nil {
		t.Fatalf("request deletion: %v", err)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, "test", fixture.epoch, nil); err != nil {
		t.Fatalf("mark worker seen: %v", err)
	}
	var after time.Time
	if err := admin.QueryRow(ctx, `SELECT reconcile_after FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if time.Until(after) > 10*time.Second {
		t.Fatalf("heartbeat moved a deleting sandbox's reconcile to %s from now", time.Until(after).Round(time.Second))
	}

	// Once due, the deleting sandbox is claimed even though its worker just
	// heartbeated again.
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET reconcile_after = now() - interval '1 second'
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, "test", fixture.epoch, nil); err != nil {
		t.Fatalf("mark worker seen: %v", err)
	}
	claimed, err := store.ClaimSandboxes(ctx, owner, 100, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, record := range claimed {
		found = found || record.SessionID == fixture.sessionID
	}
	if !found {
		t.Fatal("the deleting sandbox was not claimed after a heartbeat")
	}

	// A running sandbox keeps the historical deferral.
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes
		SET desired_state = 'running', observed_state = 'running', deletion_requested_at = NULL,
			reconcile_after = now(), reconcile_lease_owner = '', reconcile_lease_until = NULL
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, "test", fixture.epoch, nil); err != nil {
		t.Fatalf("mark worker seen: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT reconcile_after FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if until := time.Until(after); until < 20*time.Second {
		t.Fatalf("running sandbox reconcile_after = %s from now, want about 30s", until.Round(time.Second))
	}
}
