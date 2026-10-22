package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A Coder session's workspace name is chosen when the session is created,
// stored on its sandbox profile, and read back for the workspace link; a
// session created before names were stored reads back no name, so callers fall
// back to the computed one.
func TestCreateSessionStoresTheCoderWorkspaceName(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.SetCoderReadableWorkspaceNames(true)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	connection, err := store.UpsertProviderConnection(
		ctx, principal, fixture.orgID, "coder", "default", []byte("ciphertext"), []byte("nonce"),
		json.RawMessage(`{"baseUrl":"https://coder.acme.test","owner":"acme-bot","templateId":"3b3f373d-c42d-5313-a57e-b2abe56e2fe3"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	profile := json.RawMessage(`{"coder":{"baseUrl":"https://coder.acme.test","owner":"acme-bot","templateId":"3b3f373d-c42d-5313-a57e-b2abe56e2fe3","durableRoot":"$HOME","workspaceNamePrefix":"acme"}}`)
	storedName := func(sessionID string) string {
		t.Helper()
		var raw json.RawMessage
		withOrgTx(t, admin, fixture.orgID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT resource_profile FROM ao_sandboxes WHERE org_id = $1 AND session_id = $2`, fixture.orgID, sessionID,
			).Scan(&raw)
		})
		decoded, err := sandbox.DecodeCoderSessionProfile(raw)
		if err != nil {
			t.Fatalf("decode stored profile %s: %v", raw, err)
		}
		if decoded.WorkspaceNameIssuedAt == nil {
			t.Fatalf("stored profile %s has no workspaceNameIssuedAt", raw)
		}
		return decoded.WorkspaceName
	}
	create := func(kind, displayName string) domain.Session {
		t.Helper()
		session, err := store.CreateSession(ctx, principal, fixture.orgID, "create-"+uuid.NewString(), 100, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: kind, Harness: "codex", DisplayName: displayName,
			Provider: "coder", SandboxConnectionID: connection.ID,
			ResourceProfile: profile, BootstrapContext: json.RawMessage(`{}`), Release: "test",
		})
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return session
	}

	orchestrator := create("orchestrator", "Orchestrator")
	if got, want := storedName(orchestrator.ID), domain.CoderWorkspaceName("acme", "orchestrator", "", orchestrator.ID); got != want || got != "acme-o-"+idChars(orchestrator.ID, 12) {
		t.Fatalf("orchestrator workspace name = %q, want %q", got, want)
	}
	worker := create("worker", "Fix the login bug")
	if got := storedName(worker.ID); got != "acme-w-fix-"+idChars(worker.ID, 12) {
		t.Fatalf("worker workspace name = %q, want acme-w-fix-%s", got, idChars(worker.ID, 12))
	}
	read, err := store.GetSession(ctx, principal, fixture.orgID, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.BYOCoderWorkspace == nil || read.BYOCoderWorkspace.Name != "acme-w-fix-"+idChars(worker.ID, 12) || read.BYOCoderWorkspace.NamePrefix != "acme" {
		t.Fatalf("workspace ref = %+v, want the stored name", read.BYOCoderWorkspace)
	}

	// A worker an orchestrator spawns is named the same way.
	child, err := store.CreateOrchestratorChild(ctx, fixture.orgID, orchestrator.ID, uuid.NewString(), 100, domain.CreateSession{
		Kind: "worker", Harness: "codex", DisplayName: "Add retries", Prompt: "go", Mode: "trusted", DeniedCommands: []string{},
		Provider: "coder", SandboxConnectionID: connection.ID,
		ResourceProfile: profile, BootstrapContext: json.RawMessage(`{}`), Release: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := storedName(child.ID); got != "acme-w-add-"+idChars(child.ID, 12) {
		t.Fatalf("child workspace name = %q, want acme-w-add-%s", got, idChars(child.ID, 12))
	}

	// A create conflict moves the session to its retry name, once, and only
	// under the reconcile lease while the session has no workspace.
	if _, err := store.UseSandboxCoderWorkspaceRetryName(ctx, "reconciler-a", fixture.orgID, child.ID, storedName(child.ID)); !errors.Is(err, ErrSandboxLeaseLost) {
		t.Fatalf("retry without the lease = %v, want ErrSandboxLeaseLost", err)
	}
	execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sandboxes
		SET reconcile_lease_owner = 'reconciler-a', reconcile_lease_until = now() + interval '1 minute'
		WHERE org_id = $1 AND session_id = $2`, fixture.orgID, child.ID)
	taken := storedName(child.ID)
	if _, err := store.UseSandboxCoderWorkspaceRetryName(ctx, "reconciler-a", fixture.orgID, child.ID, "some-other-name"); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry for a name that is not stored = %v, want ErrConflict", err)
	}
	retried, err := store.UseSandboxCoderWorkspaceRetryName(ctx, "reconciler-a", fixture.orgID, child.ID, taken)
	if err != nil {
		t.Fatal(err)
	}
	_, wantRetry := domain.CoderWorkspaceNames("acme", "worker", "Add retries", child.ID)
	if retried != wantRetry || storedName(child.ID) != wantRetry || len(retried) != domain.MaxCoderWorkspaceName {
		t.Fatalf("retry name = %q, stored %q; want %q", retried, storedName(child.ID), wantRetry)
	}
	if _, err := store.UseSandboxCoderWorkspaceRetryName(ctx, "reconciler-a", fixture.orgID, child.ID, retried); !errors.Is(err, ErrConflict) {
		t.Fatalf("second retry = %v, want ErrConflict", err)
	}

	// A legacy row (written before names were stored) reads back no name.
	execOneRowAsOrg(t, admin, fixture.orgID,
		`UPDATE ao_sandboxes SET resource_profile = resource_profile #- '{coder,workspaceName}' #- '{coder,workspaceNameIssuedAt}'
		WHERE org_id = $1 AND session_id = $2`, fixture.orgID, worker.ID)
	legacy, err := store.GetSession(ctx, principal, fixture.orgID, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.BYOCoderWorkspace == nil || legacy.BYOCoderWorkspace.Name != "" || legacy.BYOCoderWorkspace.NamePrefix != "acme" {
		t.Fatalf("legacy workspace ref = %+v, want the prefix and no stored name", legacy.BYOCoderWorkspace)
	}
}

// Without readable names (the production default until the rollback target
// reads stored names) a new Coder session stores no workspace name, so every
// release computes the same <prefix>-<id> name for it.
func TestCreateSessionStoresNoCoderWorkspaceNameWithoutReadableNames(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	connection, err := store.UpsertProviderConnection(
		ctx, principal, fixture.orgID, "coder", "default", []byte("ciphertext"), []byte("nonce"),
		json.RawMessage(`{"baseUrl":"https://coder.acme.test","owner":"acme-bot","templateId":"3b3f373d-c42d-5313-a57e-b2abe56e2fe3"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, principal, fixture.orgID, "create-"+uuid.NewString(), 100, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "codex", DisplayName: "Fix the login bug",
		Provider: "coder", SandboxConnectionID: connection.ID,
		ResourceProfile:  json.RawMessage(`{"coder":{"baseUrl":"https://coder.acme.test","owner":"acme-bot","templateId":"3b3f373d-c42d-5313-a57e-b2abe56e2fe3","durableRoot":"$HOME","workspaceNamePrefix":"acme"}}`),
		BootstrapContext: json.RawMessage(`{}`), Release: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	withOrgTx(t, admin, fixture.orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT resource_profile FROM ao_sandboxes WHERE org_id = $1 AND session_id = $2`, fixture.orgID, session.ID,
		).Scan(&raw)
	})
	decoded, err := sandbox.DecodeCoderSessionProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkspaceName != "" || decoded.WorkspaceNameRetry != "" || decoded.WorkspaceNameIssuedAt != nil {
		t.Fatalf("stored profile %s names its workspace", raw)
	}
}

// withOrgTx runs fn in an admin transaction scoped to orgID (ao.org_id), so
// forced row-level security shows it the organization's rows even when the
// test role does not bypass it; it commits when fn succeeds.
func withOrgTx(t *testing.T, admin *pgxpool.Pool, orgID string, fn func(pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// orgAdminPool opens a pool on admin's database whose every connection is
// scoped to orgID (ao.org_id), for tests that arrange and read one
// organization's rows with plain statements: forced row-level security then
// shows them the rows even when the test role does not bypass it.
func orgAdminPool(t *testing.T, admin *pgxpool.Pool, orgID string) *pgxpool.Pool {
	t.Helper()
	config := admin.Config().Copy()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SELECT set_config('ao.org_id', $1, false)`, orgID)
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// nonBypassStore is store when the test role is already subject to
// row-level security, and otherwise a store opened as a role that is
// (openRLSStore, which needs CREATEROLE).
func nonBypassStore(t *testing.T, store *Store, admin *pgxpool.Pool) *Store {
	t.Helper()
	var bypass bool
	if err := admin.QueryRow(context.Background(), `SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&bypass); err != nil {
		t.Fatal(err)
	}
	if !bypass {
		return store
	}
	return openRLSStore(t, admin)
}

// execOneRowAsOrg runs a statement as orgID (withOrgTx) and fails the test
// unless it changed exactly one row.
func execOneRowAsOrg(t *testing.T, admin *pgxpool.Pool, orgID, query string, args ...any) {
	t.Helper()
	withOrgTx(t, admin, orgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(context.Background(), query, args...)
		if err == nil && tag.RowsAffected() != 1 {
			err = fmt.Errorf("%d rows changed, want 1", tag.RowsAffected())
		}
		return err
	})
}

// idChars is the first n letters and digits of a session id, as workspace
// names use them.
func idChars(sessionID string, n int) string {
	return strings.ReplaceAll(sessionID, "-", "")[:n]
}
