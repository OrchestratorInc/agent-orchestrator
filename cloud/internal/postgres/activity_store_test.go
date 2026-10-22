package postgres

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// openAppRoleStore opens a Store as a NOSUPERUSER NOBYPASSRLS role, the way
// the control plane runs in production, so forced row-level security applies.
// The default test URL is usually a superuser, which bypasses RLS entirely.
func openAppRoleStore(t *testing.T, admin *pgxpool.Pool) (*Store, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	role := "ao_activity_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	quoted := pgx.Identifier{role}.Sanitize()
	for _, statement := range []string{
		"CREATE ROLE " + quoted + " LOGIN PASSWORD 'activity' NOSUPERUSER NOBYPASSRLS",
		"GRANT USAGE ON SCHEMA public TO " + quoted,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + quoted,
		"GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO " + quoted,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	parsed, err := url.Parse(admin.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, "activity")
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw.Close()
		store.Close()
		_, _ = admin.Exec(context.Background(), "REVOKE ALL ON ALL TABLES IN SCHEMA public FROM "+quoted)
		_, _ = admin.Exec(context.Background(), "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA public FROM "+quoted)
		_, _ = admin.Exec(context.Background(), "REVOKE ALL ON SCHEMA public FROM "+quoted)
		_, _ = admin.Exec(context.Background(), "DROP ROLE "+quoted)
	})
	if err := store.ValidateRuntimeRole(ctx); err != nil {
		t.Fatalf("app role must be subject to RLS: %v", err)
	}
	return store, raw
}

func seedOtherOrg(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	orgID := uuid.NewString()
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ao_organizations (id, auth_provider, slug, display_name, kind)
		VALUES ($1, 'local', $2, 'Other', 'team')`, orgID, "other-"+orgID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_organizations WHERE id = $1`, orgID)
	})
	return orgID
}

func TestActivityEventsAreTenantScopedUnderForcedRLS(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store, raw := openAppRoleStore(t, admin)
	ctx := context.Background()
	otherOrg := seedOtherOrg(t, admin)

	if err := store.RecordActivityEvent(ctx, domain.ActivityEvent{
		OrgID: fixture.orgID, UserID: fixture.userID, SessionID: fixture.sessionID,
		Kind: domain.ActivitySandboxReady, Provider: "coder",
		Attrs: map[string]any{"ttr_ms": 4200, "phase": "create"},
	}); err != nil {
		t.Fatalf("record fixture org event: %v", err)
	}
	// Non-UUID optional identifiers are stored as NULL rather than failing.
	if err := store.RecordActivityEvent(ctx, domain.ActivityEvent{
		OrgID: fixture.orgID, UserID: "not-a-uuid", Kind: domain.ActivityProjectCreated,
	}); err != nil {
		t.Fatalf("record with a bad user id: %v", err)
	}
	if err := store.RecordActivityEvent(ctx, domain.ActivityEvent{
		OrgID: otherOrg, Kind: domain.ActivityOrgCreated,
	}); err != nil {
		t.Fatalf("record other org event: %v", err)
	}
	if err := store.RecordActivityEvent(ctx, domain.ActivityEvent{OrgID: "nope", Kind: "org.created"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid org error = %v, want ErrInvalid", err)
	}

	events, err := store.ListActivityEvents(ctx, fixture.orgID, ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("fixture org sees %d events, want 2: %+v", len(events), events)
	}
	for _, event := range events {
		if event.OrgID != fixture.orgID {
			t.Fatalf("cross-tenant row leaked: %+v", event)
		}
	}
	ready, err := store.ListActivityEvents(ctx, fixture.orgID, ActivityFilter{SessionID: fixture.sessionID})
	if err != nil || len(ready) != 1 || ready[0].Kind != domain.ActivitySandboxReady ||
		ready[0].UserID != fixture.userID || ready[0].Attrs["ttr_ms"] != float64(4200) {
		t.Fatalf("session filter = %+v, %v", ready, err)
	}

	// Without a tenant or service identity, forced RLS hides every row.
	var visible int
	if err := raw.QueryRow(ctx, `SELECT count(*) FROM ao_activity_events`).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("an unscoped app-role read saw %d rows, want 0", visible)
	}
	// A tenant cannot write a row for another organization.
	tx, err := raw.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ao_activity_events (org_id, kind) VALUES ($1, 'org.created')`, otherOrg); err == nil {
		t.Fatal("a tenant inserted an activity row for another organization")
	}
	_ = tx.Rollback(ctx)

	// Retention prune runs under the service context across organizations.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_activity_events SET created_at = now() - interval '100 days' WHERE org_id = $1`, otherOrg); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.PruneActivityEvents(ctx, time.Now().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted < 1 {
		t.Fatalf("prune deleted %d rows, want the 100-day-old one", deleted)
	}
	if kept, err := store.ListActivityEvents(ctx, fixture.orgID, ActivityFilter{}); err != nil || len(kept) != 2 {
		t.Fatalf("prune removed recent rows: %d, %v", len(kept), err)
	}
}

func TestActivityAttrsRespectTheColumnBound(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	err := store.RecordActivityEvent(context.Background(), domain.ActivityEvent{
		OrgID: fixture.orgID, Kind: domain.ActivityBootstrapStage,
		Attrs: map[string]any{"huge": strings.Repeat("x", 20000)},
	})
	if err == nil {
		t.Fatal("an oversized attrs payload must be rejected by the column check (the recorder bounds it first)")
	}
}

func TestFirstSignInRecordsOrgCreatedOnce(t *testing.T) {
	store, admin, _ := openNotificationTestStore(t)
	appStore, _ := openAppRoleStore(t, admin)
	ctx := context.Background()
	var mu sync.Mutex
	var logged []domain.ActivityEvent
	appStore.SetActivityLogger(func(_ context.Context, event domain.ActivityEvent) {
		mu.Lock()
		logged = append(logged, event)
		mu.Unlock()
	})
	external := "user_" + uuid.NewString()
	principal := domain.Principal{
		Provider: "workos", ExternalID: external, Email: external + "@example.test", DisplayName: "Signup",
	}
	first, err := appStore.UpsertWorkOSUser(ctx, principal)
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(),
			`DELETE FROM ao_organizations WHERE owner_user_id = $1`, first.UserID)
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, first.UserID)
	})
	if _, err := appStore.UpsertWorkOSUser(ctx, principal); err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	report, err := store.SupportLookup(ctx, SupportQuery{Email: strings.ToUpper(principal.Email)})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Users) != 1 || len(report.Users[0].Orgs) != 1 {
		t.Fatalf("report = %+v", report)
	}
	org := report.Users[0].Orgs[0]
	created := 0
	for _, event := range org.Activity {
		if event.Kind == domain.ActivityOrgCreated {
			created++
			if event.UserID != first.UserID || event.Attrs["org_kind"] != "personal" {
				t.Fatalf("org.created = %+v", event)
			}
		}
	}
	if created != 1 {
		t.Fatalf("org.created recorded %d times, want once: %+v", created, org.Activity)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || logged[0].Kind != domain.ActivityOrgCreated {
		t.Fatalf("activity logger saw %+v, want one org.created", logged)
	}
}

func TestTeamSignInRecordsOrgCreatedAndMemberJoined(t *testing.T) {
	_, admin, _ := openNotificationTestStore(t)
	store, _ := openAppRoleStore(t, admin)
	ctx := context.Background()
	externalOrg := "org_" + uuid.NewString()
	owner := domain.Principal{Provider: "workos", ExternalID: "user_" + uuid.NewString(),
		Email: uuid.NewString() + "@example.test", ExternalOrgID: externalOrg, OrgName: "Team", OrgRole: "owner"}
	member := domain.Principal{Provider: "workos", ExternalID: "user_" + uuid.NewString(),
		Email: uuid.NewString() + "@example.test", ExternalOrgID: externalOrg, OrgName: "Team", OrgRole: "member"}
	ownerPrincipal, err := store.UpsertWorkOSUser(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	memberPrincipal, err := store.UpsertWorkOSUser(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertWorkOSUser(ctx, member); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_organizations WHERE external_org_id = $1`, externalOrg)
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id IN ($1, $2)`,
			ownerPrincipal.UserID, memberPrincipal.UserID)
	})
	report, err := store.SupportLookup(ctx, SupportQuery{Email: member.Email})
	if err != nil || len(report.Users) != 1 || len(report.Users[0].Orgs) != 1 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	counts := map[string]int{}
	for _, event := range report.Users[0].Orgs[0].Activity {
		counts[event.Kind]++
	}
	if counts[domain.ActivityOrgCreated] != 1 || counts[domain.ActivityMemberJoined] != 2 {
		t.Fatalf("activity counts = %v, want one org.created and two member.joined", counts)
	}
}

func TestSupportLookupAndBYOStartupDetail(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	connectionID := uuid.NewString()
	owner := "reconciler-" + uuid.NewString()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`SELECT set_config('ao.org_id', $1, true)`, []any{fixture.orgID}},
		{`INSERT INTO ao_provider_connections (id, org_id, provider, label, encrypted_secret, secret_nonce, validation_state)
			VALUES ($1, $2, 'coder', 'default', '\x00', '\x00', 'valid')`, []any{connectionID, fixture.orgID}},
		{`UPDATE ao_sandboxes SET provider = 'coder', provider_connection_id = $1, reconcile_lease_owner = $2,
			reconcile_lease_until = now() + interval '1 minute', provider_environment_id = 'ws-' || $3::text,
			resource_profile = '{"coder":{"baseUrl":"https://coder.acme.example.com","owner":"dev","workspaceNamePrefix":"dev"}}'
			WHERE session_id = $3`, []any{connectionID, owner, fixture.sessionID}},
	} {
		if _, err := tx.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := store.RecordSandboxStartupErrorDetail(ctx, "someone-else", fixture.orgID, fixture.sessionID, "x"); !errors.Is(err, ErrSandboxLeaseLost) {
		t.Fatalf("detail without the lease: %v, want ErrSandboxLeaseLost", err)
	}
	if err := store.RecordSandboxStartupError(ctx, owner, fixture.orgID, fixture.sessionID,
		"bootstrap_failed", "The AO worker could not be started."); err != nil {
		t.Fatal(err)
	}
	detail := "code: bootstrap_failed\nstage: worker_install\n" + strings.Repeat("é", 3000)
	if err := store.RecordSandboxStartupErrorDetail(ctx, owner, fixture.orgID, fixture.sessionID, detail); err != nil {
		t.Fatalf("record detail: %v", err)
	}
	session, err := store.GetSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.StartupErrorCode != "bootstrap_failed" || session.StartupErrorDetail == "" ||
		len(session.StartupErrorDetail) > maxStartupErrorDetail*2 {
		t.Fatalf("BYO session startup error = %q / %d bytes of detail", session.StartupErrorCode, len(session.StartupErrorDetail))
	}
	if ref := session.BYOCoderWorkspace; ref == nil || ref.BaseURL != "https://coder.acme.example.com" ||
		ref.Owner != "dev" || ref.NamePrefix != "dev" {
		t.Fatalf("BYO session coder workspace = %+v", ref)
	}

	if err := store.RecordActivityEvent(ctx, domain.ActivityEvent{
		OrgID: fixture.orgID, SessionID: fixture.sessionID, Kind: domain.ActivitySessionStartFailed,
		Provider: "coder", Attrs: map[string]any{"code": "bootstrap_failed"},
	}); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]SupportQuery{
		"email":   {Email: fixture.userID + "@EXAMPLE.test"},
		"org":     {OrgID: fixture.orgID},
		"session": {SessionID: fixture.sessionID},
	} {
		report, err := store.SupportLookup(ctx, query)
		if err != nil {
			t.Fatalf("%s lookup: %v", name, err)
		}
		orgs := report.Orgs
		if name == "email" {
			if len(report.Users) != 1 {
				t.Fatalf("email lookup users = %+v", report.Users)
			}
			orgs = report.Users[0].Orgs
		}
		if len(orgs) != 1 || orgs[0].OrgID != fixture.orgID || orgs[0].CoderConnection == nil ||
			orgs[0].CoderConnection.ConnectionID != connectionID {
			t.Fatalf("%s lookup orgs = %+v", name, orgs)
		}
		if len(orgs[0].Sessions) != 1 || orgs[0].Sessions[0].StartupErrorDetail == "" ||
			orgs[0].Sessions[0].ConnectionID != connectionID {
			t.Fatalf("%s lookup sessions = %+v", name, orgs[0].Sessions)
		}
		if len(orgs[0].Activity) == 0 || orgs[0].Activity[0].Kind != domain.ActivitySessionStartFailed {
			t.Fatalf("%s lookup activity = %+v", name, orgs[0].Activity)
		}
	}
	if _, err := store.SupportLookup(ctx, SupportQuery{SessionID: uuid.NewString()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session lookup = %v, want ErrNotFound", err)
	}
	if _, err := store.SupportLookup(ctx, SupportQuery{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty lookup = %v, want ErrInvalid", err)
	}
	if report, err := store.SupportLookup(ctx, SupportQuery{Email: "nobody-" + uuid.NewString() + "@example.test"}); err != nil || len(report.Users) != 0 {
		t.Fatalf("unknown email = %+v, %v", report, err)
	}

	// A managed (non-BYO) sandbox never exposes the operator detail.
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET provider_connection_id = NULL WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	session, err = store.GetSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.StartupErrorDetail != "" {
		t.Fatalf("managed session exposed operator detail %q", session.StartupErrorDetail)
	}
	if session.BYOCoderWorkspace != nil {
		t.Fatalf("managed session exposed its coder workspace %+v", session.BYOCoderWorkspace)
	}
}

func TestSandboxReadinessReportsStartupTiming(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET startup_started_at = now() - interval '42 seconds',
		worker_last_seen_at = NULL WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	readiness, err := store.SandboxReadiness(ctx, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Provider != "docker" || readiness.SessionKind != "worker" || readiness.StartupStartedAt == nil ||
		readiness.PreviousWorkerSeenAt != nil || readiness.SessionCreatedAt.IsZero() {
		t.Fatalf("readiness = %+v", readiness)
	}
	if _, err := store.SandboxReadiness(ctx, fixture.orgID, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing sandbox = %v, want ErrNotFound", err)
	}
}
