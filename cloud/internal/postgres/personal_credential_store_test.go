package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

func TestWorkerAgentCredentialUsesSessionCreatorsPersonalKey(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	config := json.RawMessage(`{"credentialType":"api_key"}`)
	if _, err := store.UpsertProviderConnection(ctx, principal, fixture.orgID, "codex", "default",
		[]byte("org-key"), []byte("org-nonce"), config); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WorkerAgentCredential(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org-only credential = %v, want ErrNotFound", err)
	}
	if _, err := store.UpsertUserProviderConnection(ctx, principal, "codex", "default",
		[]byte("personal-key"), []byte("personal-nonce"), config); err != nil {
		t.Fatal(err)
	}
	got, err := store.WorkerAgentCredential(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerUserID != fixture.userID || !bytes.Equal(got.EncryptedSecret, []byte("personal-key")) {
		t.Fatalf("worker credential owner=%q secret=%q, want creator's personal key", got.OwnerUserID, got.EncryptedSecret)
	}
}

func TestOrchestratorChildRequiresCreatorsPersonalKey(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET kind = 'orchestrator' WHERE id = $1`, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"credentialType":"api_key"}`)
	if _, err := store.UpsertProviderConnection(ctx, principal, fixture.orgID, "codex", "default",
		[]byte("org-key"), []byte("org-nonce"), config); err != nil {
		t.Fatal(err)
	}
	available, err := store.OrchestratorAgentCredentialAvailable(ctx, fixture.orgID, fixture.sessionID, "codex")
	if err != nil || available {
		t.Fatalf("org-only child credential available=%v err=%v, want false", available, err)
	}
	if _, err := store.UpsertUserProviderConnection(ctx, principal, "codex", "default",
		[]byte("personal-key"), []byte("personal-nonce"), config); err != nil {
		t.Fatal(err)
	}
	available, err = store.OrchestratorAgentCredentialAvailable(ctx, fixture.orgID, fixture.sessionID, "codex")
	if err != nil || !available {
		t.Fatalf("personal child credential available=%v err=%v, want true", available, err)
	}
}
