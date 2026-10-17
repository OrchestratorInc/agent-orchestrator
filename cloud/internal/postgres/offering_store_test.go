package postgres

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

// A child spawned by an orchestrator on a bring-your-own Coder connection must
// land on that same connection. OrchestratorPlacement returns the parent's
// provider, connection, project config, and the org's Coder connections, and a
// child created with the inherited connection carries it on its sandbox row.
func TestOrchestratorPlacementInheritsBYOCoderConnection(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	coderConfig := json.RawMessage(`{"baseUrl":"https://coder.acme.test","owner":"acme-bot","templateId":"3b3f373d-c42d-5313-a57e-b2abe56e2fe3"}`)
	connection, err := store.UpsertProviderConnection(
		ctx, principal, fixture.orgID, "coder", "default", []byte("ciphertext"), []byte("nonce"), coderConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	projectConfig := `{"coder":{"templateId":"5c5f373d-c42d-5313-a57e-b2abe56e2fe3","workspaceNamePrefix":"acme"}}`
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE ao_sessions SET kind = 'orchestrator' WHERE org_id = $1 AND id = $2`, []any{fixture.orgID, fixture.sessionID}},
		{`UPDATE ao_sandboxes SET provider = 'coder', provider_connection_id = $3 WHERE org_id = $1 AND session_id = $2`, []any{fixture.orgID, fixture.sessionID, connection.ID}},
		{`UPDATE ao_projects SET config = $3::jsonb WHERE org_id = $1 AND id = $2`, []any{fixture.orgID, fixture.projectID, projectConfig}},
	} {
		if _, err := admin.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed %q: %v", statement.query, err)
		}
	}

	placement, err := store.OrchestratorPlacement(ctx, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if placement.Provider != "coder" || placement.ConnectionID != connection.ID {
		t.Fatalf("placement = %+v, want coder on connection %s", placement, connection.ID)
	}
	if cfg, ok := domain.DecodeProjectCoderConfig(placement.ProjectConfig); !ok || cfg.WorkspaceNamePrefix != "acme" {
		t.Fatalf("project config = %s, want the project's coder config", placement.ProjectConfig)
	}
	if len(placement.CoderConnections) != 1 || placement.CoderConnections[0].ID != connection.ID {
		t.Fatalf("coder connections = %+v, want the org's single connection", placement.CoderConnections)
	}

	child, err := store.CreateOrchestratorChild(ctx, fixture.orgID, fixture.sessionID, uuid.NewString(), 100, domain.CreateSession{
		Kind: "worker", Harness: "codex", DisplayName: "child", Prompt: "go", Mode: "trusted", DeniedCommands: []string{},
		Provider: "coder", SandboxConnectionID: placement.ConnectionID,
		ResourceProfile: json.RawMessage(`{}`), BootstrapContext: json.RawMessage(`{}`), Release: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var childProvider, childConnection string
	if err := admin.QueryRow(ctx,
		`SELECT provider, COALESCE(provider_connection_id::text, '') FROM ao_sandboxes WHERE org_id = $1 AND session_id = $2`,
		fixture.orgID, child.ID,
	).Scan(&childProvider, &childConnection); err != nil {
		t.Fatal(err)
	}
	if childProvider != "coder" || childConnection != connection.ID {
		t.Fatalf("child sandbox = %s on %q, want coder on the parent's connection %s", childProvider, childConnection, connection.ID)
	}
}

func TestOrchestratorPlacementRejectsNonOrchestrator(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	if _, err := store.OrchestratorPlacement(context.Background(), fixture.orgID, fixture.sessionID); err != ErrForbidden {
		t.Fatalf("err = %v, want ErrForbidden for a worker session", err)
	}
}

// UpsertWorkOSUser records which AO organization the token's WorkOS
// organization maps to, so capabilities from WorkOS metadata apply to that
// organization only.
func TestUpsertWorkOSUserSetsTeamOrgID(t *testing.T) {
	store, _, _ := openNotificationTestStore(t)
	ctx := context.Background()
	team, err := store.UpsertWorkOSUser(ctx, domain.Principal{
		Provider: "workos", ExternalID: "user_" + uuid.NewString(), Email: uuid.NewString() + "@example.test",
		DisplayName: "Team", ExternalOrgID: "org_" + uuid.NewString(), OrgName: "Acme", OrgRole: "member",
	})
	if err != nil {
		t.Fatal(err)
	}
	if team.TeamOrgID == "" {
		t.Fatal("TeamOrgID not set for a principal with a WorkOS organization")
	}
	memberships, err := store.ListMemberships(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, membership := range memberships {
		found = found || membership.OrgID == team.TeamOrgID
	}
	if !found {
		t.Fatalf("TeamOrgID %s is not among the principal's memberships %+v", team.TeamOrgID, memberships)
	}

	personal, err := store.UpsertWorkOSUser(ctx, domain.Principal{
		Provider: "workos", ExternalID: "user_" + uuid.NewString(), Email: uuid.NewString() + "@example.test",
		DisplayName: "Solo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if personal.TeamOrgID != "" {
		t.Fatalf("TeamOrgID = %q for a principal without a WorkOS organization, want empty", personal.TeamOrgID)
	}
}
