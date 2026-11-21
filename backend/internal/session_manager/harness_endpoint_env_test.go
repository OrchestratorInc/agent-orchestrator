package sessionmanager

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func TestRuntimeEnvAppliesCustomHarnessEndpoint(t *testing.T) {
	dir := t.TempDir()
	if err := providerkeys.Save(t.Context(), dir, "ANTHROPIC_API_KEY", "ao-saved"); err != nil {
		t.Fatal(err)
	}
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint", Model: "glm-4.6"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	claude, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if claude["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" || claude["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" || claude["ANTHROPIC_MODEL"] != "glm-4.6" {
		t.Fatalf("custom endpoint not applied: %v", claude)
	}
	if value, ok := claude["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q (set=%v), want blanked beside the endpoint token", value, ok)
	}

	project, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{"ANTHROPIC_MODEL": "project-model"}, "", "claude-code")
	if err != nil || project["ANTHROPIC_MODEL"] != "project-model" {
		t.Fatalf("project env must win: model=%q err=%v", project["ANTHROPIC_MODEL"], err)
	}

	goose, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "goose")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := goose["ANTHROPIC_BASE_URL"]; ok || goose["ANTHROPIC_API_KEY"] != "ao-saved" {
		t.Fatalf("endpoint leaked into another harness or replaced its key: %v", goose)
	}
}

// savedAPIKeyAccount is a session bound to a saved Anthropic API-key account.
type savedAPIKeyAccount struct {
	ports.AgentAccountLaunchEnv
}

func (savedAPIKeyAccount) SessionLaunchEnv(context.Context, domain.SessionID) (map[string]string, error) {
	return map[string]string{"ANTHROPIC_API_KEY": "sk-account", "ANTHROPIC_BASE_URL": "https://api.anthropic.com"}, nil
}

func (a savedAPIKeyAccount) SessionLaunchEnvWith(ctx context.Context, id domain.SessionID, _ string) (map[string]string, error) {
	return a.SessionLaunchEnv(ctx, id)
}

// A bound account's saved key must never reach the custom endpoint: the base
// URL and key both come from the endpoint, whichever auth style it uses.
func TestRuntimeEnvKeepsAccountKeyAwayFromCustomEndpoint(t *testing.T) {
	for _, style := range []harnessendpoint.AuthStyle{harnessendpoint.AuthBearer, harnessendpoint.AuthAPIKey} {
		dir := t.TempDir()
		if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", AuthStyle: style, APIKey: "sk-endpoint"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		manager := &Manager{
			dataDir:       dir,
			executable:    func() (string, error) { return filepath.Join(dir, "ao"), nil },
			logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			agentAccounts: savedAPIKeyAccount{},
		}
		env, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "claude-code")
		if err != nil {
			t.Fatal(err)
		}
		if env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" {
			t.Fatalf("%s: base URL = %q", style, env["ANTHROPIC_BASE_URL"])
		}
		for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
			if env[key] == "sk-account" {
				t.Fatalf("%s: account key reached the endpoint via %s: %v", style, key, env)
			}
		}
		keyVar := map[harnessendpoint.AuthStyle]string{harnessendpoint.AuthBearer: "ANTHROPIC_AUTH_TOKEN", harnessendpoint.AuthAPIKey: "ANTHROPIC_API_KEY"}[style]
		if env[keyVar] != "sk-endpoint" {
			t.Fatalf("%s: %s = %q, want the endpoint key", style, keyVar, env[keyVar])
		}
	}
}
