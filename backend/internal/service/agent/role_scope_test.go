package agent

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

func TestRoleScopeParts(t *testing.T) {
	for _, tc := range []struct {
		scope     string
		projectID string
		role      string
		ok        bool
	}{
		{scope: "proj-1@role:worker", projectID: "proj-1", role: "worker", ok: true},
		{scope: "proj-1@role:reviewer", projectID: "proj-1", role: "reviewer", ok: true},
		{scope: "proj-1", ok: false},
		{scope: "proj-1@role:", ok: false},
		{scope: "@role:worker", ok: false},
		{scope: "@cred:openai_api_key", ok: false},
	} {
		projectID, role, ok := roleScopeParts(tc.scope)
		if ok != tc.ok || projectID != tc.projectID || role != tc.role {
			t.Errorf("roleScopeParts(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.scope, projectID, role, ok, tc.projectID, tc.role, tc.ok)
		}
	}
}

func TestModelCatalogScopePreservesRoleScope(t *testing.T) {
	projects := &fakeProjectLookup{records: map[string]domain.ProjectRecord{
		"proj-1": {ID: "proj-1", Path: "/work/project"},
	}}
	s := &Service{projects: projects}
	got, err := s.modelCatalogScope(context.Background(), "proj-1@role:worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "proj-1@role:worker" {
		t.Fatalf("scope = %q, want the role scope preserved", got)
	}
}

func TestModelDiscoveryRequestFoldsRolePinIntoEnv(t *testing.T) {
	projects := &fakeProjectLookup{records: map[string]domain.ProjectRecord{
		"proj-1": {
			ID:   "proj-1",
			Path: "/work/project",
			Config: domain.ProjectConfig{
				Env:    map[string]string{"ANTHROPIC_MODEL": "project-model"},
				Worker: domain.RoleOverride{Provider: "https://gw.example"},
			},
		},
	}}
	// The pin fold sources its credentials from AO's gateway entries (the
	// daemon owns ~/.claude/settings.json and never reads it back here), the
	// project-scoped entry first, then the app-wide one.
	entries := []agentcreds.GatewayEntry{
		{Scope: agentcreds.GatewayScopeProject, ProjectID: "proj-1",
			BaseURL: "https://gw.example", Token: "tok", Model: "gateway-model"},
		{Scope: agentcreds.GatewayScopeApp,
			BaseURL: "https://app.example", Token: "app-tok", Model: "app-model"},
	}
	s := &Service{
		projects:        projects,
		providerEntries: func(context.Context) []agentcreds.GatewayEntry { return entries },
	}

	request, err := s.modelDiscoveryRequest(context.Background(), "claude-code", "proj-1@role:worker", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.WorkingDir != "/work/project" {
		t.Fatalf("WorkingDir = %q, want the project path", request.WorkingDir)
	}
	if request.Env["ANTHROPIC_BASE_URL"] != "https://gw.example" || request.Env["ANTHROPIC_AUTH_TOKEN"] != "tok" || request.Env["ANTHROPIC_MODEL"] != "gateway-model" {
		t.Fatalf("pin env did not overlay the project env: %v", request.Env)
	}

	// A pinless role resolves against the default gateway entry — project
	// scope first — deterministically, not the plain project env.
	request, err = s.modelDiscoveryRequest(context.Background(), "claude-code", "proj-1@role:reviewer", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.Env["ANTHROPIC_BASE_URL"] != "https://gw.example" || request.Env["ANTHROPIC_AUTH_TOKEN"] != "tok" || request.Env["ANTHROPIC_MODEL"] != "gateway-model" {
		t.Fatalf("pinless role env = %v, want the default gateway resolution", request.Env)
	}

	// With no gateway entries configured at all, a pinless role yields the
	// plain project resolution.
	plain := &Service{projects: projects}
	request, err = plain.modelDiscoveryRequest(context.Background(), "claude-code", "proj-1@role:reviewer", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.Env["ANTHROPIC_MODEL"] != "project-model" || request.Env["ANTHROPIC_BASE_URL"] != "" {
		t.Fatalf("pinless role without entries = %v, want the project resolution", request.Env)
	}
}
