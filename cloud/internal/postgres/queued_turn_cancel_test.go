package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCancelQueuedTurnFinishesWithoutWorker(t *testing.T) {
	for _, initialState := range []string{"queued", "cancel_requested"} {
		t.Run(initialState, func(t *testing.T) {
			store, _, f := openNotificationTestStore(t)
			ctx := context.Background()
			principal := domain.Principal{UserID: f.userID, Provider: "local"}
			event, err := store.SendMessage(ctx, principal, f.orgID, f.sessionID, "cancel-queued", "Keep this in history", domain.ChatTurnSettings{})
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				TurnID string `json:"turnId"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if err := store.withTenant(ctx, principal, f.orgID, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE ao_turns SET state = $2 WHERE id = $1`, payload.TurnID, initialState)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := store.RequestTurnCancellation(ctx, principal, f.orgID, f.sessionID, payload.TurnID); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.withTenant(ctx, principal, f.orgID, func(tx pgx.Tx) error {
				var state string
				var attempts, cancelled, messages int
				var finished bool
				if err := tx.QueryRow(ctx, `SELECT state, attempt_count, completed_at IS NOT NULL FROM ao_turns WHERE id = $1`, payload.TurnID).Scan(&state, &attempts, &finished); err != nil {
					return err
				}
				if state != "completed" || attempts != 0 || !finished {
					t.Fatalf("turn state=%s attempts=%d finished=%v; want completed without execution", state, attempts, finished)
				}
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_events WHERE session_id=$1 AND type='chat.turn_interrupted' AND payload->>'turnId'=$2 AND payload->>'cancelled'='true'`, f.sessionID, payload.TurnID).Scan(&cancelled); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_events WHERE session_id=$1 AND type='chat.user_message' AND payload->>'turnId'=$2`, f.sessionID, payload.TurnID).Scan(&messages); err != nil {
					return err
				}
				if cancelled != 1 || messages != 1 {
					t.Fatalf("cancelled events=%d retained messages=%d, want 1 each", cancelled, messages)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, claimed, err := store.ClaimWorkerTurn(ctx, f.orgID, f.sessionID, f.workerID, f.epoch); err != nil || claimed {
				t.Fatalf("cancelled turn claimed=%v err=%v", claimed, err)
			}
		})
	}
}

func TestSharedQueuedTurnCancellationPermissions(t *testing.T) {
	for _, role := range []string{"editor", "viewer"} {
		t.Run(role, func(t *testing.T) {
			store, admin, f := openNotificationTestStore(t)
			ctx := context.Background()
			owner := domain.Principal{UserID: f.userID, Provider: "local"}
			recipient := domain.Principal{UserID: uuid.NewString(), Provider: "local"}
			linkID := uuid.NewString()
			hash := sha256.Sum256([]byte(linkID))
			if _, err := admin.Exec(ctx, `INSERT INTO ao_users (id, auth_provider, external_user_id, email, display_name, password_hash)
				VALUES ($1::uuid, 'local', $1::text, $1::text || '@example.test', 'Recipient', 'hash')`, recipient.UserID); err != nil {
				t.Fatal(err)
			}
			if err := store.withTenant(ctx, owner, f.orgID, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `INSERT INTO ao_project_share_links (id, org_id, project_id, session_id, created_by_user_id, token_hash, role)
					VALUES ($1,$2,$3,$4,$5,$6,$7)`, linkID, f.orgID, f.projectID, f.sessionID, f.userID, hash[:], role); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `INSERT INTO ao_project_share_grants (share_link_id, org_id, project_id, session_id, user_id, shared_by_user_id, role)
					VALUES ($1,$2,$3,$4,$5,$6,$7)`, linkID, f.orgID, f.projectID, f.sessionID, recipient.UserID, f.userID, role)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			event, err := store.SendMessage(ctx, owner, f.orgID, f.sessionID, "shared-cancel", "queued", domain.ChatTurnSettings{})
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				TurnID string `json:"turnId"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			err = store.RequestTurnCancellation(ctx, recipient, f.orgID, f.sessionID, payload.TurnID)
			if role == "editor" && err != nil {
				t.Fatalf("shared editor cannot cancel: %v", err)
			}
			if role == "viewer" && !errors.Is(err, ErrForbidden) {
				t.Fatalf("viewer cancellation err=%v, want forbidden", err)
			}
		})
	}
}

func TestCancelRunningTurnStillWaitsForWorker(t *testing.T) {
	store, _, f := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: f.userID, Provider: "local"}
	if _, err := store.SendMessage(ctx, principal, f.orgID, f.sessionID, "running", "Run", domain.ChatTurnSettings{}); err != nil {
		t.Fatal(err)
	}
	turn, claimed, err := store.ClaimWorkerTurn(ctx, f.orgID, f.sessionID, f.workerID, f.epoch)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if err := store.RequestTurnCancellation(ctx, principal, f.orgID, f.sessionID, turn.ID); err != nil {
		t.Fatal(err)
	}
	requested, err := store.WorkerTurnCancellationRequested(ctx, f.orgID, f.sessionID, f.workerID, turn.ID, f.epoch, turn.Attempt)
	if err != nil || !requested {
		t.Fatalf("cancellation requested=%v err=%v", requested, err)
	}
}
