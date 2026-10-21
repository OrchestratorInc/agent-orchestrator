package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// ao_sandboxes.provider_connection_id is ON DELETE SET NULL, so deleting a
// connection a live sandbox runs on would silently move that sandbox onto the
// deployment's own provider. The delete is refused until every sandbox on it
// is fully deleted, and a refused delete leaves the sandbox untouched.
func TestDeleteProviderConnectionRefusedWhileASandboxRunsOnIt(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	connectionID := uuid.NewString()
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_provider_connections (id, org_id, provider, label, encrypted_secret, secret_nonce, validation_state)
		VALUES ($1, $2, 'coder', 'default', '\x00', '\x00', 'valid')`, connectionID, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, principal, fixture.orgID, "byo-"+uuid.NewString(), 10, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "codex", DisplayName: "byo",
		Provider: "coder", SandboxConnectionID: connectionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	sandboxConnection := func() string {
		t.Helper()
		var id *string
		if err := admin.QueryRow(ctx,
			`SELECT provider_connection_id::text FROM ao_sandboxes WHERE session_id = $1`, session.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if id == nil {
			return ""
		}
		return *id
	}
	connectionExists := func() bool {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FROM ao_provider_connections WHERE id = $1`, connectionID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	refused := func(state string) {
		t.Helper()
		err := store.DeleteProviderConnection(ctx, principal, fixture.orgID, "coder", "default")
		if !errors.Is(err, ErrProviderConnectionInUse) || !errors.Is(err, ErrConflict) {
			t.Fatalf("delete with a %s sandbox: err = %v, want ErrProviderConnectionInUse", state, err)
		}
		if !connectionExists() || sandboxConnection() != connectionID {
			t.Fatalf("a refused delete changed the connection or the %s sandbox", state)
		}
	}

	refused("live")
	// Deletion requested but not yet observed: the sandbox still runs.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET desired_state = 'deleted' WHERE session_id = $1`, session.ID); err != nil {
		t.Fatal(err)
	}
	refused("deleting")

	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET observed_state = 'deleted' WHERE session_id = $1`, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProviderConnection(ctx, principal, fixture.orgID, "coder", "default"); err != nil {
		t.Fatalf("delete after every sandbox is deleted: %v", err)
	}
	if connectionExists() {
		t.Fatal("the connection survived its delete")
	}
	if err := store.DeleteProviderConnection(ctx, principal, fixture.orgID, "coder", "default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v, want ErrNotFound", err)
	}
}
