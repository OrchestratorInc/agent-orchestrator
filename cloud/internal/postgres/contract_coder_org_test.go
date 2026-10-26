package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A customer on Coder (WorkOS metadata capabilities=coder; 11x has no billing
// key, others billing=contract) runs on AO's managed Coder and on its own Coder
// deployment. With billing enforced and no plan, none of its Coder sessions is
// billed, needs a plan, is paused for being over a plan, is seen by the billed
// idle pause, or is refused a wake.
func TestCoderCustomerCoderIsNeverBilled(t *testing.T) {
	for name, billing := range map[string]string{"no billing key": "", "billing=contract": domain.OrgBillingContract} {
		t.Run(name, func(t *testing.T) { testCoderCustomerCoderIsNeverBilled(t, billing) })
	}
}

func testCoderCustomerCoderIsNeverBilled(t *testing.T, orgBilling string) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	store.EnableBilling(7*24*time.Hour, time.Time{})
	principal := domain.Principal{
		UserID: fixture.userID, Provider: "local", TeamOrgID: fixture.orgID,
		OrgCapabilities: []string{"coder"}, OrgBilling: orgBilling,
	}
	// A member's request records the organization's billing for the enforcer.
	if _, err := store.BillingSummary(ctx, principal, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	connectionID := uuid.NewString()
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_provider_connections (id, org_id, provider, label, encrypted_secret, secret_nonce, validation_state)
		VALUES ($1, $2, 'coder', 'default', '\x00', '\x00', 'valid')`, connectionID, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	create := func(kind, connection string) domain.Session {
		t.Helper()
		session, err := store.CreateSession(ctx, principal, fixture.orgID, "contract-"+uuid.NewString(), 10, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: kind, Harness: "claude-code", DisplayName: kind,
			Provider: "coder", SandboxConnectionID: connection, CoderCapability: true,
		})
		if err != nil {
			t.Fatalf("%s on Coder (connection %q) without a plan: %v", kind, connection, err)
		}
		return session
	}
	managed := create("orchestrator", "")
	own := create("worker", connectionID)
	worker, err := store.CreateOrchestratorChild(ctx, fixture.orgID, managed.ID, "contract-child-"+uuid.NewString(), 10,
		domain.CreateSession{Kind: "worker", Harness: "claude-code", DisplayName: "child", Provider: "coder",
			SandboxConnectionID: connectionID})
	if err != nil {
		t.Fatalf("spawned worker on the organization's own Coder: %v", err)
	}
	ids := []string{managed.ID, own.ID, worker.ID}
	for _, id := range ids {
		if sandboxBilled(t, admin, id) {
			t.Fatalf("coder session %s of a contract coder organization was marked billed", id)
		}
	}

	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running'
		WHERE session_id = ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 0 {
		t.Fatalf("PauseOverLimit = %d, %v; want nothing paused", paused, err)
	}
	refs, err := store.RunningSandboxSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	billed := map[string]bool{}
	for _, ref := range refs {
		billed[ref.SessionID] = ref.Billed
	}
	for _, id := range ids {
		if isBilled, running := billed[id]; !running || isBilled {
			t.Fatalf("coder session %s: running %v, billed for the idle pause %v; want running and unbilled", id, running, isBilled)
		}
	}

	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'paused', observed_state = 'stopped'
		WHERE session_id = ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := store.WakeAllowed(ctx, principal, fixture.orgID, id); err != nil {
			t.Fatalf("waking coder session %s without a plan: %v", id, err)
		}
	}
}

// The 00083 backfill pins a Coder customer's projects to Coder:
// one that ran on managed Coder, one that ran on its own Coder, and one with
// Coder dev-kit config that never ran. None moves to Freestyle.
func TestCoderCustomerProjectsStayOnCoder(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	migration, err := os.ReadFile("migrations/00083_project_sandbox_provider_backfill.sql")
	if err != nil {
		t.Fatal(err)
	}
	body := string(migration)
	body = body[strings.Index(body, "-- +goose StatementBegin")+len("-- +goose StatementBegin") : strings.Index(body, "-- +goose StatementEnd")]
	connectionID := uuid.NewString()
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_provider_connections (id, org_id, provider, label, encrypted_secret, secret_nonce, validation_state)
		VALUES ($1, $2, 'coder', 'default', '\x00', '\x00', 'valid')`, connectionID, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	project := func(config string) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := admin.Exec(ctx, `INSERT INTO ao_projects (id, org_id, display_name, repository_url, config)
			VALUES ($1, $2, 'Contract', $3, $4::jsonb)`, id, fixture.orgID, "https://example.test/"+id, config); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ran := func(projectID, connection string) {
		t.Helper()
		id := uuid.NewString()
		if _, err := admin.Exec(ctx, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id)
			VALUES ($1, $2, $3, 'orchestrator', 'claude-code', 'Session', 'main', $4)`, id, fixture.orgID, projectID, fixture.userID); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `INSERT INTO ao_sandboxes (session_id, org_id, provider, provider_connection_id)
			VALUES ($1, $2, 'coder', NULLIF($3, '')::uuid)`, id, fixture.orgID, connection); err != nil {
			t.Fatal(err)
		}
	}
	managed := project(`{"coder":{"templateId":"ao-azure-vm"}}`)
	ran(managed, "")
	own := project(`{"coder":{"templateId":"ao-11x-replica","workspaceUser":true}}`)
	ran(own, connectionID)
	neverRan := project(`{"coder":{"templateId":"ao-11x-replica"}}`)
	if _, err := admin.Exec(ctx, body); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	for name, id := range map[string]string{"managed Coder": managed, "own Coder": own, "never ran": neverRan} {
		var provider *string
		if err := admin.QueryRow(ctx, `SELECT config->>'sandboxProvider' FROM ao_projects WHERE id = $1`, id).Scan(&provider); err != nil {
			t.Fatal(err)
		}
		if provider == nil || *provider != "coder" {
			t.Errorf("%s project pinned to %v, want coder", name, provider)
		}
	}
}
