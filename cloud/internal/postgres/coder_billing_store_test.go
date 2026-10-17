package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func createCoder(store *Store, fixture notificationFixture, kind string, coderCapability bool) (domain.Session, error) {
	return store.CreateSession(context.Background(), domain.Principal{UserID: fixture.userID, Provider: "local"},
		fixture.orgID, "coder-"+uuid.NewString(), 10, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: kind, Harness: "codex", DisplayName: kind,
			Provider: "coder", CoderCapability: coderCapability,
		})
}

func sandboxBilled(t *testing.T, admin *pgxpool.Pool, sessionID string) bool {
	t.Helper()
	var billed bool
	if err := admin.QueryRow(context.Background(),
		`SELECT billed FROM ao_sandboxes WHERE session_id = $1`, sessionID).Scan(&billed); err != nil {
		t.Fatal(err)
	}
	return billed
}

// Coder is billed like Freestyle for an organization without the coder
// capability, and free for one with it.
func TestCoderSessionsAreBilledWithoutTheCoderCapability(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	store.EnableBilling(7*24*time.Hour, time.Time{})
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	if _, err := createCoder(store, fixture, "worker", false); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("coder without the capability or a plan: err = %v, want ErrPlanRequired", err)
	}
	free, err := createCoder(store, fixture, "worker", true)
	if err != nil {
		t.Fatalf("coder with the capability must not need a plan: %v", err)
	}
	if sandboxBilled(t, admin, free.ID) {
		t.Fatal("a coder session of an organization with the coder capability was marked billed")
	}

	subscribe(t, store, fixture.orgID, "active", starterLimits())
	billed, err := createCoder(store, fixture, "worker", false)
	if err != nil {
		t.Fatalf("coder without the capability, on a plan: %v", err)
	}
	if !sandboxBilled(t, admin, billed.ID) {
		t.Fatal("a coder session without the capability was not marked billed")
	}
	freestyle, err := createKind(store, fixture, "orchestrator")
	if err != nil {
		t.Fatal(err)
	}
	if !sandboxBilled(t, admin, freestyle.ID) {
		t.Fatal("a freestyle session was not marked billed")
	}
	summary, err := store.BillingSummary(ctx, principal, fixture.orgID)
	if err != nil || summary.Usage == nil {
		t.Fatalf("summary = %+v, err = %v", summary, err)
	}
	// The billed coder worker and the freestyle orchestrator count; the free
	// coder worker and the fixture's docker sandbox do not.
	if summary.Usage.ActiveWorkers != 1 || summary.Usage.ActiveOrchestrators != 1 {
		t.Fatalf("usage = %+v, want 1 billed worker and 1 billed orchestrator", summary.Usage)
	}
}

// Coder on the organization's own deployment runs on its own capacity, so it
// is never billed, with or without the coder capability.
func TestBringYourOwnCoderSessionsAreNeverBilled(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	store.EnableBilling(7*24*time.Hour, time.Time{})
	connectionID := uuid.NewString()
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_provider_connections (id, org_id, provider, label, encrypted_secret, secret_nonce, validation_state)
		VALUES ($1, $2, 'coder', 'default', '\x00', '\x00', 'valid')`, connectionID, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"},
		fixture.orgID, "byo-"+uuid.NewString(), 10, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "codex", DisplayName: "byo",
			Provider: "coder", SandboxConnectionID: connectionID,
		})
	if err != nil {
		t.Fatalf("own Coder without the capability or a plan: %v", err)
	}
	if sandboxBilled(t, admin, session.ID) {
		t.Fatal("a session on the organization's own Coder was marked billed")
	}
}

// The coder capability is read from a cache that can change between a create
// and its retry; the retry must still replay the first create.
func TestCreateSessionRetryIgnoresAChangedCoderCapability(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	input := domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "codex", DisplayName: "retry",
		Provider: "coder", CoderCapability: true,
	}
	key := "retry-" + uuid.NewString()
	first, err := store.CreateSession(ctx, principal, fixture.orgID, key, 10, input)
	if err != nil {
		t.Fatal(err)
	}
	input.CoderCapability = false
	retried, err := store.CreateSession(ctx, principal, fixture.orgID, key, 10, input)
	if err != nil || retried.ID != first.ID {
		t.Fatalf("retry = %s, %v; want the first session %s", retried.ID, err, first.ID)
	}
}

// A worker an orchestrator spawns inherits the orchestrator's billing, since
// the spawn has no signed-in user to read the coder capability from.
func TestSpawnedCoderWorkerInheritsItsParentsBilling(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	store.EnableBilling(7*24*time.Hour, time.Time{})
	parent, err := createCoder(store, fixture, "orchestrator", true)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateOrchestratorChild(ctx, fixture.orgID, parent.ID, "child-"+uuid.NewString(), 10,
		domain.CreateSession{Kind: "worker", Harness: "codex", DisplayName: "child", Provider: "coder"})
	if err != nil {
		t.Fatalf("a free orchestrator's coder worker must not need a plan: %v", err)
	}
	if sandboxBilled(t, admin, child.ID) {
		t.Fatal("the child of an unbilled orchestrator was marked billed")
	}
}

// The organization's chosen default provider round-trips through its settings
// and only admins may change it.
func TestOrgDefaultSandboxProvider(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if got, err := store.OrgDefaultSandboxProvider(ctx, fixture.orgID); err != nil || got != "" {
		t.Fatalf("before any choice: %q, %v", got, err)
	}
	if err := store.SetOrgDefaultSandboxProvider(ctx, principal, fixture.orgID, "coder"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.OrgDefaultSandboxProvider(ctx, fixture.orgID); err != nil || got != "coder" {
		t.Fatalf("after choosing coder: %q, %v", got, err)
	}
	if err := store.SetOrgDefaultSandboxProvider(ctx, principal, fixture.orgID, "Not A Provider"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed provider: err = %v, want ErrInvalid", err)
	}
	if err := store.SetOrgDefaultSandboxProvider(ctx, principal, fixture.orgID, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := store.OrgDefaultSandboxProvider(ctx, fixture.orgID); err != nil || got != "" {
		t.Fatalf("after clearing: %q, %v", got, err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_org_memberships SET role = 'member' WHERE org_id = $1 AND user_id = $2`, fixture.orgID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOrgDefaultSandboxProvider(ctx, principal, fixture.orgID, "coder"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member change: err = %v, want ErrForbidden", err)
	}
}

// Billed Coder idle-pauses like Freestyle: the idle scanner sees it as billed
// and a person looking at it keeps it awake. Coder of an organization with the
// coder capability is unbilled, so it keeps Coder's keep-warm behavior.
func TestBilledCoderIsIdlePausedLikeFreestyleAndFreeCoderStaysWarm(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	billed, err := createCoder(store, fixture, "worker", false)
	if err != nil {
		t.Fatal(err)
	}
	free, err := createCoder(store, fixture, "worker", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running'
		WHERE session_id = ANY($1)`, []string{billed.ID, free.ID}); err != nil {
		t.Fatal(err)
	}
	refs, err := store.RunningSandboxSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.SessionID == billed.ID || ref.SessionID == free.ID {
			seen[ref.SessionID] = ref.Billed
		}
	}
	if len(seen) != 2 || !seen[billed.ID] || seen[free.ID] {
		t.Fatalf("running refs billed = %v; want the billed coder billed and the free coder not", seen)
	}
	for _, id := range []string{billed.ID, free.ID} {
		if err := store.TouchSessionPresence(ctx, principal, fixture.orgID, id); err != nil {
			t.Fatal(err)
		}
	}
	touched := func(id string) bool {
		var at *time.Time
		if err := admin.QueryRow(ctx, `SELECT last_interaction_at FROM ao_sandboxes WHERE session_id = $1`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at != nil
	}
	if !touched(billed.ID) || touched(free.ID) {
		t.Fatalf("presence touched billed=%v free=%v; want only the billed coder", touched(billed.ID), touched(free.ID))
	}
}
