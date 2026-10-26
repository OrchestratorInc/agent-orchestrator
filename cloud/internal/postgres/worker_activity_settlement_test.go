package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

func TestChatTurnCompletionSettlesWorkerActivity(t *testing.T) {
	for _, outcome := range []string{"completed", "cancelled", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			store, admin, fixture := openNotificationTestStore(t)
			ctx := context.Background()
			if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET interface = 'chat'
				WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID); err != nil {
				t.Fatal(err)
			}
			principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
			if _, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID,
				"chat-turn-"+outcome, "hello", domain.ChatTurnSettings{}); err != nil {
				t.Fatal(err)
			}
			turn, claimed, err := store.ClaimWorkerTurn(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch)
			if err != nil || !claimed {
				t.Fatalf("claim: claimed=%v err=%v", claimed, err)
			}
			assertCloudActivity(t, store, fixture, contract.ActivityActive)
			if finished, err := store.FinishWorkerTurn(ctx, fixture.orgID, fixture.sessionID,
				fixture.workerID, turn.ID, fixture.epoch, turn.Attempt, outcome, "provider failed"); err != nil || finished {
				t.Fatalf("finish: alreadyFinished=%v err=%v", finished, err)
			}
			assertCloudActivity(t, store, fixture, contract.ActivityIdle)
		})
	}
}

func TestStaleWorkerFailureCannotSettleCurrentActivity(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	requestID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_worker_requests
		(id, org_id, session_id, worker_epoch, kind, status, attempt_count, expires_at, lease_until)
		VALUES ($1, $2, $3, $4, 'terminal.input', 'claimed', 1,
			now() + interval '1 minute', now() + interval '1 minute')`,
		requestID, fixture.orgID, fixture.sessionID, fixture.epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET activity_state = 'active'
		WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.FailWorkerRequest(ctx, fixture.orgID, fixture.sessionID, "retired-worker",
		requestID, fixture.epoch, 1, "TERMINAL_FAILED", "old callback"); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("stale failure error = %v, want ErrStaleWorker", err)
	}
	assertCloudActivity(t, store, fixture, contract.ActivityActive)
}

func TestFailedTerminalInputSettlesOnlyItsOwnActivity(t *testing.T) {
	for _, test := range []struct {
		name       string
		afterInput string
		want       contract.ActivityState
	}{
		{name: "failed input becomes idle", want: contract.ActivityIdle},
		{name: "unrelated session edit still becomes idle", afterInput: "metadata", want: contract.ActivityIdle},
		{name: "newer agent signal stays active", afterInput: "agent", want: contract.ActivityActive},
		{name: "newer input stays active", afterInput: "input", want: contract.ActivityActive},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, admin, fixture := openNotificationTestStore(t)
			ctx := context.Background()
			requestID := uuid.NewString()
			tx, err := admin.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `INSERT INTO ao_worker_requests
				(id, org_id, session_id, worker_epoch, kind, status, attempt_count, expires_at, lease_until)
				VALUES ($1, $2, $3, $4, 'terminal.input', 'claimed', 1,
					now() + interval '1 minute', now() + interval '1 minute')`,
				requestID, fixture.orgID, fixture.sessionID, fixture.epoch); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `UPDATE ao_sessions
				SET activity_state = 'active', activity_source_request_id = $3, updated_at = now()
				WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID, requestID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			switch test.afterInput {
			case "metadata":
				_, err = admin.Exec(ctx, `UPDATE ao_sessions SET display_name = 'Renamed', updated_at = now()
					WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID)
			case "agent":
				_, err = admin.Exec(ctx, `UPDATE ao_sessions SET activity_source_request_id = NULL, updated_at = now()
					WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID)
			case "input":
				_, err = admin.Exec(ctx, `UPDATE ao_sessions SET activity_source_request_id = $3, updated_at = now()
					WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID, uuid.NewString())
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.FailWorkerRequest(ctx, fixture.orgID, fixture.sessionID, fixture.workerID,
				requestID, fixture.epoch, 1, "TERMINAL_FAILED", "input was not delivered"); err != nil {
				t.Fatal(err)
			}
			assertCloudActivity(t, store, fixture, test.want)
		})
	}
}

func TestDisconnectedWorkerStopsReportingWorking(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET activity_state = 'active'
		WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.DisconnectSessionWorkers(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	assertCloudActivity(t, store, fixture, contract.ActivityIdle)
	current, err := store.WorkerConnectionCurrent(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch)
	if err != nil {
		t.Fatal(err)
	}
	if current {
		t.Fatal("disconnected worker still owns the session")
	}
}

func TestDisconnectSettlesRunningChatTurnFromRetiredWorker(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET interface = 'chat'
		WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if _, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID,
		"chat-before-crash", "hello", domain.ChatTurnSettings{}); err != nil {
		t.Fatal(err)
	}
	turn, claimed, err := store.ClaimWorkerTurn(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch)
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	assertCloudActivity(t, store, fixture, contract.ActivityActive)
	if err := store.DisconnectSessionWorkers(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	assertCloudActivity(t, store, fixture, contract.ActivityIdle)
	if _, err := store.FinishWorkerTurn(ctx, fixture.orgID, fixture.sessionID, fixture.workerID,
		turn.ID, fixture.epoch, turn.Attempt, "failed", "late failure"); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("late completion error = %v, want ErrStaleWorker", err)
	}
}

func assertCloudActivity(t *testing.T, store *Store, fixture notificationFixture, want contract.ActivityState) {
	t.Helper()
	session, err := store.GetSession(context.Background(),
		domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.ActivityState != want {
		t.Fatalf("activity = %q, want %q", session.ActivityState, want)
	}
	if want == contract.ActivityIdle && session.Status(time.Now(), nil) != contract.StatusIdle {
		t.Fatalf("status = %q, want idle", session.Status(time.Now(), nil))
	}
}

// A turn claims with no approval mode its effective mode does not allow, so
// the worker never rejects it (for example "bypass exceeds session permission
// cap" for a share grantee capped at standard in a trusted session); the
// worker then derives the approval from the effective mode.
func TestClaimWorkerTurnClampsTheApprovalModeToTheEffectiveMode(t *testing.T) {
	for _, tc := range []struct {
		name, harness, sessionMode, modeCap string
		settings                            domain.ChatTurnSettings
		wantMode, wantApproval              string
	}{
		{"trusted keeps bypass", "claude-code", "trusted", "", domain.ChatTurnSettings{ApprovalMode: "bypass-permissions"}, "trusted", "bypass-permissions"},
		{"grantee capped at standard", "claude-code", "trusted", "standard", domain.ChatTurnSettings{ApprovalMode: "bypass-permissions"}, "standard", ""},
		{"standard keeps accept-edits", "claude-code", "standard", "", domain.ChatTurnSettings{ApprovalMode: "accept-edits"}, "standard", "accept-edits"},
		{"codex full access under standard", "codex", "trusted", "standard", domain.ChatTurnSettings{ApprovalMode: "default"}, "standard", ""},
		{"plan turn", "claude-code", "trusted", "", domain.ChatTurnSettings{Mode: "read-only", ApprovalMode: "default"}, "read-only", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, admin, fixture := openNotificationTestStore(t)
			ctx := context.Background()
			execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sessions SET interface = 'chat', harness = $3, mode = $4
				WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID, tc.harness, tc.sessionMode)
			principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
			if _, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID,
				uuid.NewString(), "hello", tc.settings); err != nil {
				t.Fatal(err)
			}
			if tc.modeCap != "" {
				execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_turns SET mode_cap = $3 WHERE org_id = $1 AND session_id = $2`,
					fixture.orgID, fixture.sessionID, tc.modeCap)
			}
			turn, claimed, err := store.ClaimWorkerTurn(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch)
			if err != nil || !claimed {
				t.Fatalf("claim = %v, %v", claimed, err)
			}
			if turn.Mode != tc.wantMode || turn.ApprovalMode != tc.wantApproval {
				t.Fatalf("claimed mode = %q approval = %q, want %q %q", turn.Mode, turn.ApprovalMode, tc.wantMode, tc.wantApproval)
			}
		})
	}
}
