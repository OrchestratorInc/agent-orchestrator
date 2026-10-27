package postgres

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// Opening a share link of one's own organization, the creator's own link
// included, leads to the session without making a grant: they already have
// access.
func TestRedeemingALinkOfYourOwnOrganization(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID, domain.CreateShareLink{
		SessionID: fixture.sessionID, Role: "editor", AccessScope: "anyone",
	})
	if err != nil {
		t.Fatal(err)
	}
	countGrants := func() int {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_project_share_grants WHERE org_id = $1`, fixture.orgID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	shared, err := store.RedeemProjectShareLink(ctx, owner, fixture.orgID, token)
	if err != nil {
		t.Fatalf("the creator opening their own link: %v", err)
	}
	if !shared.Member || shared.SessionID != fixture.sessionID || shared.Project.ID != fixture.projectID || shared.SessionName != "Session" {
		t.Fatalf("the creator's redeem = %+v", shared)
	}

	teammate := seedTestUser(t, admin, "Teammate")
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_org_memberships (org_id, user_id, role) VALUES ($1, $2, 'member')`, fixture.orgID, teammate.UserID)
	t.Cleanup(func() {
		execAsOrg(admin, fixture.orgID, `DELETE FROM ao_org_memberships WHERE org_id = $1 AND user_id = $2`, fixture.orgID, teammate.UserID)
	})
	shared, err = store.RedeemProjectShareLink(ctx, teammate, fixture.orgID, token)
	if err != nil || !shared.Member || shared.SessionID != fixture.sessionID {
		t.Fatalf("a teammate's redeem = %+v, %v", shared, err)
	}
	if grants := countGrants(); grants != 0 {
		t.Fatalf("members' redeems made %d grants", grants)
	}

	// Someone outside the organization still gets their own grant.
	guest := shareSessionWith(t, store, admin, fixture, "viewer", "Guest")
	if grants := countGrants(); grants != 1 {
		t.Fatalf("grants after a guest redeemed = %d", grants)
	}
	if _, err := store.ListSharedProjectSessions(ctx, guest, fixture.orgID, fixture.projectID); err != nil {
		t.Fatalf("the guest's shared sessions: %v", err)
	}
}
