package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/primeagent"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
)

type runtimeCredentialAgent struct {
	fakeAgent
	dataDir string
}

func (a runtimeCredentialAgent) RuntimeCredentialEnv(dataDir string, _ map[string]string) map[string]string {
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

// Prime sessions run with a profile under the data directory, so discovery
// must read that one: a login only in ~/.prime/agent lists models sessions
// can't use.
func TestModelDiscoveryRequestReadsThePrimeSessionProfile(t *testing.T) {
	dataDir := t.TempDir()
	plugin := primeagent.New()
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.HarnessPrimeAgent,
		Manifest: plugin.Manifest(),
		Agent:    plugin,
	}}, nil, nil)
	svc.dataDir = dataDir
	request, err := svc.modelDiscoveryRequest(context.Background(), "prime-agent", "", "/bin/prime-agent")
	if err != nil {
		t.Fatal(err)
	}
	session := map[string]string{}
	plugin.AugmentRuntimeEnv(session, dataDir)
	const profile = "PRIME_AGENT_CODING_AGENT_DIR"
	if request.Env[profile] == "" || request.Env[profile] != session[profile] {
		t.Fatalf("discovery profile = %q, want the session profile %q", request.Env[profile], session[profile])
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

// A harness on a custom endpoint lists the models of that endpoint: discovery
// carries its base URL and key, and blanks the saved login so the adapter does
// not list the subscription's models instead.
func TestModelDiscoveryRequestUsesSavedCustomEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("claude-code"),
		Manifest: adapters.Manifest{ID: "claude-code", Name: "Claude Code"},
		Agent:    runtimeCredentialAgent{dataDir: dataDir},
	}}, nil, nil)
	svc.dataDir = dataDir
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{
		BaseURL: "https://gateway.example.com/anthropic", APIKey: "sk-endpoint", Model: "gateway-model",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	request, err := svc.modelDiscoveryRequest(context.Background(), "claude-code", "", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.Env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com/anthropic" || request.Env["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" ||
		request.Env["ANTHROPIC_MODEL"] != "gateway-model" {
		t.Fatalf("discovery env = %v, want the saved endpoint", keysWithValues(request.Env))
	}
	if value, ok := request.Env["CLAUDE_CODE_OAUTH_TOKEN"]; !ok || value != "" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q (set %v), want it blanked beside the endpoint", value, ok)
	}

	// A cloud credential scope still reflects only the pushed credential.
	request, err = svc.modelDiscoveryRequest(context.Background(), "claude-code", "@cred:anthropic_api_key", "/bin/claude")
	if err != nil || len(request.Env) != 0 {
		t.Fatalf("credential-scope discovery env = %v, err = %v; want none", keysWithValues(request.Env), err)
	}
}

// An endpoint every launch rejects fails discovery, so the cached catalog
// stays instead of being replaced by the models of an unused login.
func TestModelDiscoveryRequestFailsOnUnusableCustomEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("claude-code"),
		Manifest: adapters.Manifest{ID: "claude-code", Name: "Claude Code"},
		Agent:    runtimeCredentialAgent{dataDir: dataDir},
	}}, nil, nil)
	svc.dataDir = dataDir
	path := filepath.Join(dataDir, "harness-endpoints", "claude-code", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.modelDiscoveryRequest(context.Background(), "claude-code", "", "/bin/claude"); err == nil {
		t.Fatal("discovery must fail while the saved endpoint is unusable")
	}
}

// keysWithValues reports env without secret values, for failure messages.
func keysWithValues(env map[string]string) map[string]bool {
	out := make(map[string]bool, len(env))
	for key, value := range env {
		out[key] = value != ""
	}
	return out
}
