package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// redeemProjectLink shares the fixture's whole project with a new guest
// through a real link and returns them.
func redeemProjectLink(t *testing.T, store *Store, admin *pgxpool.Pool, fixture notificationFixture, role, name string) domain.Principal {
	t.Helper()
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	guest := seedTestUser(t, admin, name)
	t.Cleanup(func() {
		execAsOrg(admin, fixture.orgID, `DELETE FROM ao_project_share_grants WHERE org_id = $1 AND user_id = $2`, fixture.orgID, guest.UserID)
	})
	_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID, domain.CreateShareLink{Role: role, AccessScope: "anyone", Interaction: "interact"})
	if err != nil {
		t.Fatalf("create project link: %v", err)
	}
	if _, err := store.RedeemProjectShareLink(ctx, guest, fixture.orgID, token); err != nil {
		t.Fatalf("redeem project link: %v", err)
	}
	return guest
}

// A session the orchestrator starts while running a guest's request is that
// guest's; one it starts for the owner, or with nobody's request in flight,
// is not. An orchestrator in a terminal has no turns, so whoever holds its
// composer is the one asking.
func TestOrchestratorSessionsRecordTheGuestWhoAskedForThem(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	orchestrator, err := store.CreateSession(ctx, owner, fixture.orgID, "orch-"+uuid.NewString(), 10, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "orchestrator", Harness: "codex", DisplayName: "Orchestrator",
	})
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}
	guest := redeemProjectLink(t, store, admin, fixture, "editor", "Vaanya")

	runNextTurn := func(who domain.Principal) {
		t.Helper()
		execAsOrg(admin, fixture.orgID, `UPDATE ao_turns SET state = 'completed', completed_at = now() WHERE org_id = $1 AND session_id = $2 AND state IN ('queued', 'running')`, fixture.orgID, orchestrator.ID)
		if _, err := store.SendMessageWithLease(ctx, who, fixture.orgID, orchestrator.ID, uuid.NewString(), "start a session", domain.ChatTurnSettings{}, 0); err != nil {
			t.Fatalf("message from %s: %v", who.UserID, err)
		}
		execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_turns SET state = 'running', started_at = now() WHERE org_id = $1 AND session_id = $2 AND state = 'queued'`, fixture.orgID, orchestrator.ID)
	}
	spawn := func() domain.Session {
		t.Helper()
		child, err := store.CreateOrchestratorChild(ctx, fixture.orgID, orchestrator.ID, "child-"+uuid.NewString(), 10,
			domain.CreateSession{Kind: "worker", Harness: "codex", DisplayName: "child"})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		return child
	}

	runNextTurn(guest)
	if child := spawn(); child.RequestedByUserID != guest.UserID || child.RequestedByName != "Vaanya" {
		t.Fatalf("a session started for the guest records %q (%q), want %s", child.RequestedByUserID, child.RequestedByName, guest.UserID)
	} else {
		var read domain.Session
		if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error { return getSession(ctx, tx, fixture.orgID, child.ID, &read) }); err != nil {
			t.Fatal(err)
		}
		if read.RequestedByUserID != guest.UserID || read.RequestedByName != "Vaanya" {
			t.Fatalf("reading the session back gives %q (%q)", read.RequestedByUserID, read.RequestedByName)
		}
	}

	runNextTurn(owner)
	if child := spawn(); child.RequestedByUserID != "" {
		t.Fatalf("a session started for the owner records %q", child.RequestedByUserID)
	}

	execAsOrg(admin, fixture.orgID, `UPDATE ao_turns SET state = 'completed', completed_at = now() WHERE org_id = $1 AND session_id = $2`, fixture.orgID, orchestrator.ID)
	if child := spawn(); child.RequestedByUserID != "" {
		t.Fatalf("a session started with no request in flight records %q", child.RequestedByUserID)
	}
	if _, err := store.AcquireComposerLease(ctx, guest, fixture.orgID, orchestrator.ID); err != nil {
		t.Fatalf("the guest takes the orchestrator's composer: %v", err)
	}
	if child := spawn(); child.RequestedByUserID != guest.UserID {
		t.Fatalf("a session started while the guest holds the terminal records %q", child.RequestedByUserID)
	}
}
