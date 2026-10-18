package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

func TestRoleProviderPinMapsSessionKind(t *testing.T) {
	cfg := domain.ProjectConfig{
		Worker:       domain.RoleOverride{Provider: "https://worker.example"},
		Orchestrator: domain.RoleOverride{Provider: domain.ProviderDirect},
	}
	if got := RoleProviderPin(domain.KindWorker, cfg); got != "https://worker.example" {
		t.Fatalf("worker pin = %q", got)
	}
	if got := RoleProviderPin(domain.KindOrchestrator, cfg); got != domain.ProviderDirect {
		t.Fatalf("orchestrator pin = %q", got)
	}
	if got := RoleProviderPin(domain.KindOrchestrator, domain.ProjectConfig{}); got != "" {
		t.Fatalf("pinless role = %q, want empty", got)
	}
}

func TestApplyRoleProviderPinOverlaysLaunchEnv(t *testing.T) {
	m := &Manager{}
	env := map[string]string{"ANTHROPIC_BASE_URL": "https://settings.example"}
	m.applyRoleProviderPin(context.Background(), env, "proj", domain.KindWorker, domain.ProjectConfig{
		Worker: domain.RoleOverride{Provider: domain.ProviderDirect},
	})
	if env["ANTHROPIC_BASE_URL"] != "" {
		t.Fatalf("direct pin should shadow the base URL, got %q", env["ANTHROPIC_BASE_URL"])
	}

	// A pinless role with no configured gateway entry must not touch the env.
	env = map[string]string{"ANTHROPIC_BASE_URL": "https://settings.example"}
	m.applyRoleProviderPin(context.Background(), env, "proj", domain.KindWorker, domain.ProjectConfig{})
	if env["ANTHROPIC_BASE_URL"] != "https://settings.example" {
		t.Fatalf("pinless role changed the env: %v", env)
	}
}

// A pinless role resolves against the stored default gateway entry — the same
// precedence as the pin path, project scope first.
func TestApplyRoleProviderPinAppliesDefaultEntry(t *testing.T) {
	m := &Manager{providerEntries: func(ctx context.Context) []agentcreds.GatewayEntry {
		return []agentcreds.GatewayEntry{
			{Scope: agentcreds.GatewayScopeApp, BaseURL: "https://app.example", Token: "app-token"},
			{Scope: agentcreds.GatewayScopeProject, ProjectID: "proj", BaseURL: "https://project.example", Token: "project-token", Model: "glm-5"},
		}
	}}
	env := map[string]string{}
	m.applyRoleProviderPin(context.Background(), env, "proj", domain.KindWorker, domain.ProjectConfig{})
	if env["ANTHROPIC_BASE_URL"] != "https://project.example" || env["ANTHROPIC_AUTH_TOKEN"] != "project-token" || env["ANTHROPIC_MODEL"] != "glm-5" {
		t.Fatalf("default entry env = %v, want the project-scope entry", env)
	}

	// An explicit pin wins over the default entry.
	env = map[string]string{}
	m.applyRoleProviderPin(context.Background(), env, "proj", domain.KindWorker, domain.ProjectConfig{
		Worker: domain.RoleOverride{Provider: domain.ProviderDirect},
	})
	if env["ANTHROPIC_BASE_URL"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatalf("direct pin env = %v, want empty shadows", env)
	}
}
