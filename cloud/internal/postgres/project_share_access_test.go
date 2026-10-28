package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A project link lets its guest write where they ask for work, the
// orchestrator, and in the sessions started for them; everywhere else in the
// project, the owner's sessions and other guests', they watch. A link to one
// session still gives that session what it says.
func TestProjectLinkGuestsWriteOnlyInTheOrchestratorAndTheirOwnSessions(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	ownerSession := fixture.sessionID
	orchestrator, err := store.CreateSession(ctx, owner, fixture.orgID, "orch-"+uuid.NewString(), 10, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "orchestrator", Harness: "codex", DisplayName: "Orchestrator",
	})
	if err != nil {
		t.Fatal(err)
	}
	alice := redeemProjectLink(t, store, admin, fixture, "editor", "Alice")
	bob := redeemProjectLink(t, store, admin, fixture, "editor", "Bob")
	carol := redeemProjectLink(t, store, admin, fixture, "viewer", "Carol")
	sessionFor := func(who domain.Principal) string {
		t.Helper()
		id := uuid.NewString()
		execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id, requested_by_user_id)
			VALUES ($1, $2, $3, 'worker', 'codex', 'Guest work', $6, $4, $5)`, id, fixture.orgID, fixture.projectID, fixture.userID, who.UserID, "guest-"+id)
		return id
	}
	aliceSession, bobSession := sessionFor(alice), sessionFor(bob)

	roleIn := func(who domain.Principal, sessionID string) string {
		t.Helper()
		var role string
		if err := store.withSessionAccess(ctx, who, fixture.orgID, sessionID, func(_ pgx.Tx, access sessionAccess) error {
			role = access.Role
			return nil
		}); err != nil {
			t.Fatalf("access for %s: %v", who.DisplayName, err)
		}
		return role
	}
	for _, c := range []struct {
		who     domain.Principal
		session string
		label   string
		want    string
	}{
		{alice, orchestrator.ID, "the orchestrator", "editor"},
		{alice, aliceSession, "her own session", "editor"},
		{alice, ownerSession, "the owner's session", "viewer"},
		{alice, bobSession, "Bob's session", "viewer"},
		{bob, bobSession, "his own session", "editor"},
		{carol, orchestrator.ID, "the orchestrator", "viewer"},
		{carol, aliceSession, "Alice's session", "viewer"},
		{owner, aliceSession, "Alice's session", "member"},
	} {
		if got := roleIn(c.who, c.session); got != c.want {
			t.Errorf("%s in %s: %s, want %s", c.who.DisplayName, c.label, got, c.want)
		}
	}

	if _, err := store.SendMessageWithLease(ctx, alice, fixture.orgID, ownerSession, uuid.NewString(), "hi", domain.ChatTurnSettings{}, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Alice writing in the owner's session: %v, want ErrForbidden", err)
	}
	if _, err := store.SendMessageWithLease(ctx, alice, fixture.orgID, aliceSession, uuid.NewString(), "hi", domain.ChatTurnSettings{}, 0); err != nil {
		t.Fatalf("Alice writing in her own session: %v", err)
	}

	// A link to the owner's session itself gives that session what it says.
	_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID, domain.CreateShareLink{
		SessionID: ownerSession, Role: "editor", AccessScope: "anyone", Interaction: "interact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RedeemProjectShareLink(ctx, alice, fixture.orgID, token); err != nil {
		t.Fatal(err)
	}
	if got := roleIn(alice, ownerSession); got != "editor" {
		t.Fatalf("Alice with a link to the owner's session: %s, want editor", got)
	}

	people, err := store.SessionParticipants(ctx, owner, fixture.orgID, []string{bobSession})
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, person := range people[bobSession] {
		roles[person.Name] = person.Role
	}
	if roles["Bob"] != "editor" || roles["Alice"] != "viewer" || roles["Carol"] != "viewer" {
		t.Fatalf("participants of Bob's session: %v", roles)
	}
}
