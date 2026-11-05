package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type runtimeCredentialAgent struct {
	fakeAgent
	dataDir string
}

func (a runtimeCredentialAgent) RuntimeCredentialEnv(dataDir string) map[string]string {
	if dataDir != a.dataDir {
		return nil
	}
	return map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-saved"}
}

// A setup token saved through AO's login menu is the only credential on a
// computer with no native Claude login, so local discovery must carry it or the
// catalog finds no credential and goes stale.
func TestModelDiscoveryRequestCarriesRuntimeCredential(t *testing.T) {
	dataDir := t.TempDir()
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("claude-code"),
		Manifest: adapters.Manifest{ID: "claude-code", Name: "Claude Code"},
		Agent:    runtimeCredentialAgent{dataDir: dataDir},
	}}, nil, nil)
	svc.dataDir = dataDir

	request, err := svc.modelDiscoveryRequest(context.Background(), "claude-code", "", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-saved" {
		t.Fatalf("discovery env = %v, want the saved setup token", request.Env)
	}

	// A cloud credential scope reflects the pushed credential, not this computer's.
	request, err = svc.modelDiscoveryRequest(context.Background(), "claude-code", "@cred:anthropic_api_key", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Env) != 0 {
		t.Fatalf("credential-scope discovery env = %v, want none", request.Env)
	}
}

func TestModelDiscoveryRequestFollowsSavedDefaultAccount(t *testing.T) {
	dataDir := t.TempDir()
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.HarnessClaudeCode,
		Manifest: adapters.Manifest{ID: "claude-code", Name: "Claude Code"},
		Agent:    runtimeCredentialAgent{dataDir: dataDir},
	}}, nil, nil)
	svc.dataDir = dataDir
	var home string
	svc.SetSavedDefaultLaunchEnv(func(_ context.Context, harness domain.AgentHarness) (map[string]string, error) {
		if harness != domain.HarnessClaudeCode {
			t.Fatalf("account context requested for %q", harness)
		}
		if home == "" {
			return nil, nil
		}
		return map[string]string{"CLAUDE_CONFIG_DIR": home, "CLAUDE_SECURESTORAGE_CONFIG_DIR": home, "CLAUDE_CODE_OAUTH_TOKEN": ""}, nil
	})
	for _, folder := range []string{"/accounts/team", "/accounts/max", ""} {
		home = folder
		request, err := svc.modelDiscoveryRequest(context.Background(), "claude-code", "project", "/bin/claude")
		if err != nil {
			t.Fatal(err)
		}
		if request.Env["CLAUDE_CONFIG_DIR"] != home || request.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != home {
			t.Fatalf("discovery did not follow account %q", home)
		}
		if home != "" && request.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			t.Fatal("saved account discovery inherited the computer's OAuth token")
		}
		if home == "" && request.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-saved" {
			t.Fatal("computer login lost its runtime credential")
		}
	}

	wantErr := errors.New("account unavailable")
	svc.SetSavedDefaultLaunchEnv(func(context.Context, domain.AgentHarness) (map[string]string, error) { return nil, wantErr })
	if _, err := svc.modelDiscoveryRequest(context.Background(), "claude-code", "", "/bin/claude"); !errors.Is(err, wantErr) {
		t.Fatalf("account error = %v, want %v", err, wantErr)
	}
	if request, err := svc.modelDiscoveryRequest(context.Background(), "claude-code", "@cred:anthropic_api_key", "/bin/claude"); err != nil || len(request.Env) != 0 {
		t.Fatalf("cloud discovery inherited a local account: %v", err)
	}
}

func TestModelCatalogFingerprintsAndDiscoversWithSavedAccount(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, &fakeModelCache{}, discoverer)
	var home string
	svc.SetSavedDefaultLaunchEnv(func(context.Context, domain.AgentHarness) (map[string]string, error) {
		return map[string]string{"CLAUDE_CONFIG_DIR": home, "CLAUDE_SECURESTORAGE_CONFIG_DIR": home}, nil
	})
	for _, folder := range []string{"/accounts/team", "/accounts/max"} {
		home = folder
		catalog, err := svc.RevalidateModels(context.Background(), "claude-code", "project")
		if err != nil || catalog.Stale || len(catalog.Models) == 0 {
			t.Fatalf("saved account catalog stale = %v, error = %v", catalog.Stale, err)
		}
		discoverer.mu.Lock()
		request := discoverer.lastRequest
		discoverer.mu.Unlock()
		fingerprintRequest := discoverer.lastFingerprintRequest.Load()
		if request.Env["CLAUDE_CONFIG_DIR"] != home || fingerprintRequest == nil || fingerprintRequest.Env["CLAUDE_CONFIG_DIR"] != home {
			t.Fatalf("catalog did not fingerprint and discover the current account %q", home)
		}
	}
}
