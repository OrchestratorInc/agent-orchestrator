package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A session link reaches only its session; a project link reaches every
// session in the project; someone with neither is refused.
func TestSharedProjectSessionsFollowTheGrant(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	other := uuid.NewString()
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id)
		VALUES ($1, $2, $3, 'worker', 'codex', 'Other', 'other', $4)`, other, fixture.orgID, fixture.projectID, fixture.userID)

	listed := func(who domain.Principal) ([]string, error) {
		t.Helper()
		sessions, err := store.ListSharedProjectSessions(ctx, who, fixture.orgID, fixture.projectID)
		ids := make([]string, 0, len(sessions))
		for _, session := range sessions {
			ids = append(ids, session.ID)
		}
		return ids, err
	}

	sessionGuest := shareSessionWith(t, store, admin, fixture, "editor", "Session Guest")
	if ids, err := listed(sessionGuest); err != nil || len(ids) != 1 || ids[0] != fixture.sessionID {
		t.Fatalf("a session link lists %v, %v; want only %s", ids, err, fixture.sessionID)
	}

	projectGuest := seedTestUser(t, admin, "Project Guest")
	t.Cleanup(func() {
		execAsOrg(admin, fixture.orgID, `DELETE FROM ao_project_share_grants WHERE org_id = $1 AND user_id = $2`, fixture.orgID, projectGuest.UserID)
	})
	_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID, domain.CreateShareLink{Role: "viewer", AccessScope: "anyone"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RedeemProjectShareLink(ctx, projectGuest, fixture.orgID, token); err != nil {
		t.Fatal(err)
	}
	if ids, err := listed(projectGuest); err != nil || len(ids) != 2 {
		t.Fatalf("a project link lists %v, %v; want both sessions", ids, err)
	}

	stranger := seedTestUser(t, admin, "Stranger")
	if _, err := listed(stranger); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a stranger: %v, want ErrForbidden", err)
	}
}
