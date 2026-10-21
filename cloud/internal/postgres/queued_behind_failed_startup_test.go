package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A Chat session whose Setup or checkout failed keeps its task queued for the
// retry, but that queued turn is not work in flight: it does not keep the VM
// alive, hold off idle pause (AO's or the provider's), count as busy for the
// plan, or make the session read active. A queued turn of a session that is
// starting normally, or behind a reconciler startup error, still does, and so
// does one whose worker is retrying the failed step (its busy mark is recent).
func TestQueuedTurnBehindAFailedWorkspaceStartupIsNotWorkInFlight(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_events (org_id, session_id, sequence, type)
		VALUES ($1, $2, 1, 'chat.user_message')`, fixture.orgID, fixture.sessionID)
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_turns (org_id, session_id, user_message_sequence)
		VALUES ($1, $2, 1)`, fixture.orgID, fixture.sessionID)
	execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sessions SET created_at = now() - interval '2 hours'
		WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID)
	var bypassesRLS bool
	if err := admin.QueryRow(ctx, `SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&bypassesRLS); err != nil {
		t.Fatal(err)
	}
	// arrange puts the sandbox back to running and due, with code as its
	// startup error and the worker's last busy mark busyAgo ago (none when
	// zero).
	arrange := func(t *testing.T, code string, busyAgo time.Duration) {
		t.Helper()
		execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running',
			billed = true, reconcile_after = now() - interval '100 years', reconcile_lease_owner = '', reconcile_lease_until = NULL,
			interactive_until = NULL, last_interaction_at = NULL,
			last_busy_at = CASE WHEN $4::bigint = 0 THEN NULL ELSE now() - make_interval(secs => $4::bigint) END,
			startup_error_code = $3, startup_error_message = $3, startup_error_at = CASE WHEN $3 = '' THEN NULL ELSE now() END
			WHERE org_id = $1 AND session_id = $2`, fixture.orgID, fixture.sessionID, code, int64(busyAgo/time.Second))
	}

	for _, tc := range []struct {
		name, code string
		busyAgo    time.Duration
		inFlight   bool
	}{
		{"starting normally", "", 0, true},
		{"reconciler startup error", "worker_never_started", 0, true},
		{"Setup failed", domain.WorkspaceStartupErrorProjectSetup, 0, false},
		{"checkout failed", domain.WorkspaceStartupErrorCheckout, 0, false},
		{"Setup failed, busy before it failed", domain.WorkspaceStartupErrorProjectSetup, 5 * time.Minute, false},
		{"Setup retry running", domain.WorkspaceStartupErrorProjectSetup, 10 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The session reads active only while its turn is in flight.
			arrange(t, tc.code, tc.busyAgo)
			session, err := store.GetSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if active := session.ActivityState == contract.ActivityActive; active != tc.inFlight {
				t.Errorf("session activity = %q, want active %t", session.ActivityState, tc.inFlight)
			}

			// It counts as busy for the plan's running slots only then.
			var busy, found bool
			withOrgTx(t, admin, fixture.orgID, func(tx pgx.Tx) error {
				sessions, err := billedRunningSessionsTx(ctx, tx, fixture.orgID)
				for _, running := range sessions {
					if running.id == fixture.sessionID {
						busy, found = running.busy, true
					}
				}
				return err
			})
			if !found || busy != tc.inFlight {
				t.Errorf("billed running session found %t busy %t, want busy %t", found, busy, tc.inFlight)
			}

			// The reconciler's claim keeps it alive only then. The claim runs in
			// the service context, which ao_turns' row-level security does not
			// admit, so under a role that does not bypass it no turn keeps a
			// claim alive at all (the session's own activity still does).
			claims, err := store.ClaimSandboxes(ctx, "queued-turn-test", 100, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			var claimed *domain.Sandbox
			for index := range claims {
				if claims[index].SessionID == fixture.sessionID {
					claimed = &claims[index]
				}
			}
			if claimed == nil || claimed.KeepAlive != (tc.inFlight && bypassesRLS) {
				t.Fatalf("claim = %+v, want keep alive %t", claimed, tc.inFlight && bypassesRLS)
			}

			// A provider's own idle stop is accepted as AO's pause only when it
			// is not.
			accepted, err := store.AcceptSandboxProviderPause(ctx, "queued-turn-test", fixture.orgID, fixture.sessionID, "", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if accepted == tc.inFlight {
				t.Errorf("provider pause accepted %t, want %t", accepted, !tc.inFlight)
			}

			// AO's idle pause pauses it only when it is not.
			arrange(t, tc.code, tc.busyAgo)
			paused, err := store.PauseIfIdle(ctx, fixture.orgID, fixture.sessionID, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if paused == tc.inFlight {
				t.Errorf("idle paused %t, want %t", paused, !tc.inFlight)
			}
			var desired string
			withOrgTx(t, admin, fixture.orgID, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT desired_state FROM ao_sandboxes WHERE org_id = $1 AND session_id = $2`,
					fixture.orgID, fixture.sessionID).Scan(&desired)
			})
			if want := map[bool]string{true: "running", false: "paused"}[tc.inFlight]; desired != want {
				t.Errorf("desired state = %q, want %q", desired, want)
			}
		})
	}
}
