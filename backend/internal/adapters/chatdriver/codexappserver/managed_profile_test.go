package codexappserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedLaunchIsolatesAccountAndProfile(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "synthetic-ambient-key")
	t.Setenv("AO_ACCOUNTS_MANAGER_SESSION_TOKEN", "synthetic-ambient-route")
	root := t.TempDir()
	homes := make(map[string]bool)
	for _, id := range []string{"session-a", "session-b"} {
		cfg := ports.ChatStartConfig{SessionID: domain.SessionID(id), DataDir: root, WorkspacePath: t.TempDir(),
			ControllerGeneration: "generation-" + id,
			ProviderScopeID:      "scope-" + id,
			Route:                &ports.AgentProviderRoute{BaseURL: "http://127.0.0.1:43127", TokenEnv: "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
			Env:                  map[string]string{"AO_ACCOUNTS_MANAGER_SESSION_TOKEN": "synthetic-" + id, "codex_home": "foreign", "openai_api_key": "foreign"}}
		got, err := managedLaunchConfig(t.Context(), "/synthetic/codex", cfg)
		if err != nil {
			t.Fatal(err)
		}
		env := make(map[string]string)
		for _, entry := range got.Env {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
			if strings.EqualFold(key, "OPENAI_API_KEY") || strings.EqualFold(key, "CODEX_HOME") && key != "CODEX_HOME" {
				t.Fatalf("ambient credential selector survived: %s", key)
			}
		}
		home := env["CODEX_HOME"]
		if home == "" || home == os.Getenv("CODEX_HOME") || homes[home] {
			t.Fatal("managed controllers share an ambient or missing profile")
		}
		homes[home] = true
		if rel, err := filepath.Rel(root, home); err != nil || strings.HasPrefix(rel, "..") {
			t.Fatal("profile escaped the state root")
		}
		if env["AO_ACCOUNTS_MANAGER_SESSION_TOKEN"] != "synthetic-"+id {
			t.Fatal("selected route credential was not preserved")
		}
		args := strings.Join(got.Argv, " ")
		for _, required := range []string{"model_provider=", "ao_accounts_manager", "wire_api=", "responses", "requires_openai_auth=false", "app-server"} {
			if !strings.Contains(args, required) {
				t.Fatalf("managed launch lacks %s", required)
			}
		}
		if strings.Contains(args, "synthetic-session") || got.OwnershipFingerprint == "" {
			t.Fatal("secret argv or missing durable owner fingerprint")
		}
	}
}

func TestManagedLaunchRejectsMissingExplicitAuthorization(t *testing.T) {
	t.Setenv("AO_ACCOUNTS_MANAGER_SESSION_TOKEN", "synthetic-ambient-route")
	for _, route := range []*ports.AgentProviderRoute{nil, {BaseURL: "https://example.test", TokenEnv: "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"}, {BaseURL: "http://127.0.0.1:1234", TokenEnv: "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"}} {
		_, err := managedLaunchConfig(t.Context(), "codex", ports.ChatStartConfig{SessionID: "test", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Route: route})
		if err == nil {
			t.Fatal("missing explicit local authorization was accepted")
		}
	}
}

func TestManagedLaunchPublishesFingerprint(t *testing.T) {
	cfg, err := managedLaunchConfig(t.Context(), "codex", managedTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Protocol == "" {
		t.Fatal("raw host argv drops the managed ownership fingerprint")
	}
}

func TestManagedLaunchIdentitySeparatesGenerationFromToken(t *testing.T) {
	cfg := managedTestConfig(t)
	first, err := managedLaunchConfig(t.Context(), "codex", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"same", "token", "generation", "endpoint", "scope"} {
		t.Run(scenario, func(t *testing.T) {
			next := cfg
			switch scenario {
			case "token":
				next.Env = map[string]string{managedCodexTokenEnv: "synthetic-rotated"}
			case "generation":
				next.ControllerGeneration = "replacement"
			case "endpoint":
				next.Route = &ports.AgentProviderRoute{BaseURL: "http://127.0.0.1:43128", TokenEnv: managedCodexTokenEnv}
			case "scope":
				next.ProviderScopeID = "scope-b"
			}
			launch, err := managedLaunchConfig(t.Context(), "codex", next)
			if err != nil {
				t.Fatal(err)
			}
			wantSame := scenario == "same" || scenario == "token"
			if (launch.OwnershipFingerprint == first.OwnershipFingerprint) != wantSame {
				t.Fatal("incorrect managed launch ownership boundary")
			}
		})
	}
}

func TestManagedProfileRejectsLinkedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "managed-codex")); err != nil {
		t.Skip("symlink creation is unavailable", err)
	}
	if _, err := managedProfile(root, "session-a"); err == nil {
		t.Fatal("linked profile directory was accepted")
	}
}

func TestManagedLaunchKeepsLoopbackOffAmbientProxy(t *testing.T) {
	cfg := managedTestConfig(t)
	cfg.Env["HTTP_PROXY"] = "http://proxy.invalid:3128"
	cfg.Env["NO_PROXY"] = "upper.internal"
	cfg.Env["no_proxy"] = "lower.internal"
	launch, err := managedLaunchConfig(t.Context(), "codex", cfg)
	if err != nil {
		t.Fatal(err)
	}
	env := make(map[string]string)
	for _, item := range launch.Env {
		key, value, _ := strings.Cut(item, "=")
		env[key] = value
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		for _, host := range []string{"127.0.0.1", "localhost", "upper.internal", "lower.internal"} {
			if !strings.Contains(","+env[key]+",", ","+host+",") {
				t.Fatal("managed loopback or existing proxy exclusion missing", key, host)
			}
		}
	}
	if env["HTTP_PROXY"] != cfg.Env["HTTP_PROXY"] {
		t.Fatal("non-loopback proxy settings were discarded")
	}
}
