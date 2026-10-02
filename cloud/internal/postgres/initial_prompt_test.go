package postgres

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCreateSessionInitialPromptDelivery(t *testing.T) {
	for _, test := range []struct {
		name          string
		harness       string
		kind          string
		interfaceMode domain.SessionInterface
		prompt        string
		wantTurns     int
	}{
		{"codex chat", "codex", "worker", domain.SessionInterfaceChat, "Implement the initial task", 1},
		{"claude chat", "claude-code", "worker", domain.SessionInterfaceChat, "Implement the initial task", 1},
		{"cursor chat", "cursor", "worker", domain.SessionInterfaceChat, "Implement the initial task", 1},
		{"orchestrator chat", "codex", "orchestrator", domain.SessionInterfaceChat, "Implement the initial task", 1},
		{"default terminal", "codex", "worker", "", "Implement the initial task", 0},
		{"explicit terminal", "codex", "worker", domain.SessionInterfaceTUI, "Implement the initial task", 0},
		{"opencode terminal", "opencode", "worker", "", "Implement the initial task", 0},
		{"empty chat", "codex", "worker", domain.SessionInterfaceChat, "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, f := openNotificationTestStore(t)
			ctx := context.Background()
			principal := domain.Principal{UserID: f.userID, Provider: "local"}
			key := "initial-prompt-" + uuid.NewString()
			input := domain.CreateSession{
				ProjectID: f.projectID, Kind: test.kind, Harness: test.harness,
				DisplayName: test.name, Provider: "docker", Interface: test.interfaceMode,
				Prompt: test.prompt, Model: "selected-model", Mode: "trusted",
			}
			session, err := store.CreateSession(ctx, principal, f.orgID, key, 10, input)
			if err != nil {
				t.Fatal(err)
			}
			// Retrying creation must not append or queue the first instruction again.
			retried, err := store.CreateSession(ctx, principal, f.orgID, key, 10, input)
			if err != nil || retried.ID != session.ID {
				t.Fatalf("creation retry session=%s err=%v", retried.ID, err)
			}
			if err := store.withTenant(ctx, principal, f.orgID, func(tx pgx.Tx) error {
				var turns, messages int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_turns WHERE session_id=$1`, session.ID).Scan(&turns); err != nil {
					return err
				}
				if turns != test.wantTurns {
					t.Fatalf("initial queued turns=%d, want %d", turns, test.wantTurns)
				}
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_events WHERE session_id=$1 AND type='chat.user_message' AND payload->>'text'=$2`, session.ID, test.prompt).Scan(&messages); err != nil {
					return err
				}
				wantMessages := 1
				if test.prompt == "" {
					wantMessages = 0
				}
				if messages != wantMessages {
					t.Fatalf("initial history messages=%d, want %d", messages, wantMessages)
				}
				_, err := tx.Exec(ctx, `INSERT INTO ao_worker_connections (session_id, org_id, sandbox_id, epoch, worker_id, version)
					VALUES ($1, $2, $1, $3, $4, 'test')`, session.ID, f.orgID, f.epoch, f.workerID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			turn, claimed, err := store.ClaimWorkerTurn(ctx, f.orgID, session.ID, f.workerID, f.epoch)
			if err != nil || claimed != (test.wantTurns == 1) {
				t.Fatalf("initial turn claimed=%v err=%v", claimed, err)
			}
			if claimed {
				if turn.Prompt != test.prompt || turn.Model != "selected-model" || turn.Mode != "trusted" || turn.Harness != test.harness || turn.Attempt != 1 {
					t.Fatalf("initial worker turn = %+v", turn)
				}
				if err := store.withTenant(ctx, principal, f.orgID, func(tx pgx.Tx) error {
					var linked bool
					if err := tx.QueryRow(ctx, `SELECT payload->>'turnId'=$2 FROM ao_events WHERE session_id=$1 AND sequence=$3`, session.ID, turn.ID, turn.UserEventSequence).Scan(&linked); err != nil {
						return err
					}
					if !linked {
						t.Fatal("initial history message is not linked to the claimed turn")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
