package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestUserModelCatalogSessionUsesCurrentCredentialOwnerAndActiveMembership(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	config := json.RawMessage(`{"credentialType":"api_key"}`)
	if _, err := store.UpsertUserProviderConnection(ctx, principal, "codex", "default",
		[]byte("personal-key"), []byte("personal-nonce"), config); err != nil {
		t.Fatal(err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET created_at = now() - interval '2 minutes' WHERE id = $1`, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.withUser(ctx, fixture.userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_user_provider_connections SET updated_at = now() - interval '1 minute' WHERE user_id = $1 AND provider = 'codex'`, fixture.userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UserModelCatalogSession(ctx, principal, "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session predating credential = %v, want ErrNotFound", err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET created_at = now() WHERE id = $1`, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	orgID, sessionID, err := store.UserModelCatalogSession(ctx, principal, "codex")
	if err != nil || orgID != fixture.orgID || sessionID != fixture.sessionID {
		t.Fatalf("catalog session = (%q, %q, %v), want (%q, %q, nil)", orgID, sessionID, err, fixture.orgID, fixture.sessionID)
	}
	if _, _, err := store.UserModelCatalogSession(ctx, principal, "claude-code"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other provider = %v, want ErrNotFound", err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_org_memberships SET status = 'disabled' WHERE org_id = $1 AND user_id = $2`, fixture.orgID, fixture.userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UserModelCatalogSession(ctx, principal, "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled membership = %v, want ErrNotFound", err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_org_memberships SET status = 'active' WHERE org_id = $1 AND user_id = $2`, fixture.orgID, fixture.userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	otherUserID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users (id, auth_provider, external_user_id, email, display_name, password_hash)
		VALUES ($1, 'local', $1::uuid::text, $1::uuid::text || '@example.test', 'Other User', 'hash')`, otherUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, otherUserID)
	})
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ao_org_memberships (org_id, user_id, role) VALUES ($1, $2, 'member')`, fixture.orgID, otherUserID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET created_by_user_id = $1 WHERE id = $2`, otherUserID, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UserModelCatalogSession(ctx, principal, "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's session = %v, want ErrNotFound", err)
	}
}

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
