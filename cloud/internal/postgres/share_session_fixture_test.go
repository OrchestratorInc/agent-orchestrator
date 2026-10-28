package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// seedTestUser inserts a local user with displayName and removes it after the
// test.
func seedTestUser(t *testing.T, admin *pgxpool.Pool, displayName string) domain.Principal {
	t.Helper()
	ctx := context.Background()
	userID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users (
		id, auth_provider, external_user_id, email, display_name, password_hash
	) VALUES ($1::uuid, 'local', $1::uuid::text, $1::uuid::text || '@example.test', $2, 'hash')`,
		userID, displayName); err != nil {
		t.Fatalf("seed user %s: %v", displayName, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, userID)
	})
	return domain.Principal{UserID: userID, Provider: "local", Email: userID + "@example.test", DisplayName: displayName}
}

// shareSessionWith shares the fixture's session through a real share link
// and returns the guest who redeemed it with role ("viewer" or "editor").
// Grants cascade away with the fixture's organization.
func shareSessionWith(t *testing.T, store *Store, admin *pgxpool.Pool, fixture notificationFixture, role, guestName string) domain.Principal {
	t.Helper()
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	guest := seedTestUser(t, admin, guestName)
	// The grant references the guest, so it must go before the guest does.
	t.Cleanup(func() {
		execAsOrg(admin, fixture.orgID, `DELETE FROM ao_project_share_grants WHERE org_id = $1 AND user_id = $2`, fixture.orgID, guest.UserID)
	})
	_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID, domain.CreateShareLink{
		SessionID: fixture.sessionID, Role: role, AccessScope: "anyone", Interaction: "interact",
	})
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}
	if _, err := store.RedeemProjectShareLink(ctx, guest, fixture.orgID, token); err != nil {
		t.Fatalf("redeem share link: %v", err)
	}
	return guest
}

func execAsOrg(admin *pgxpool.Pool, orgID, query string, args ...any) {
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
		return
	}
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}
