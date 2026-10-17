package workerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestTaskNamingCredentialsAreIsolatedForEveryHarness(t *testing.T) {
	adapter := filepath.Join(t.TempDir(), "adapter")
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := defaultClaudeACPAdapter
	defaultClaudeACPAdapter = &claudeACPAdapter{lookPath: func(string) (string, error) { return adapter, nil }}
	t.Cleanup(func() { defaultClaudeACPAdapter = old })
	t.Setenv("AO_CLOUD_WORKER_TOKEN_FILE", "/private/worker-token")
	t.Setenv("ANTHROPIC_API_KEY", "inherited-provider-secret")
	t.Setenv("UNRELATED_SECRET", "other-secret")
	for _, tc := range []struct{ harness, kind, key, driver string }{
		{"claude-code", "oauth_token", "CLAUDE_CODE_OAUTH_TOKEN", "claude-code"},
		{"cursor", "api_key", "CURSOR_API_KEY", "cursor"},
		{"opencode", "openai_api_key", "OPENAI_API_KEY", "opencode-v2"},
		{"codex", "auth_json", "CODEX_HOME", "codex"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			b := HarnessBuilder{DataDir: t.TempDir(), Env: map[string]string{"PROJECT_SECRET": "project"}}
			secret := "selected-secret"
			if tc.harness == "codex" {
				secret = `{"OPENAI_API_KEY":"selected-secret"}`
			}
			cfg, cleanup, err := b.BuildTaskNaming(context.Background(), worker.LaunchContext{Harness: tc.harness, Model: "selected-model", SystemPrompt: "project rules"}, worker.CredentialResponse{Provider: tc.harness, CredentialType: tc.kind, Secret: secret})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if cfg.Harness != tc.driver || cfg.Model != "selected-model" || cfg.Env[tc.key] == "" {
				t.Fatalf("configuration = %+v", cfg)
			}
			for _, key := range []string{"AO_CLOUD_WORKER_TOKEN_FILE", "UNRELATED_SECRET", "PROJECT_SECRET"} {
				if cfg.Env[key] != "" {
					t.Fatalf("leaked %s", key)
				}
			}
			if cfg.Env["ANTHROPIC_API_KEY"] != "" {
				t.Fatal("inherited another provider's credential")
			}
			if cfg.WorkspacePath == "" || !filepath.IsAbs(cfg.WorkspacePath) || cfg.Env["HOME"] == os.Getenv("HOME") {
				t.Fatal("not a disposable workspace/home")
			}
			if tc.harness == "codex" {
				body, err := os.ReadFile(filepath.Join(cfg.Env["CODEX_HOME"], "auth.json"))
				if err != nil || string(body) != secret {
					t.Fatalf("auth = %q, %v", body, err)
				}
			}
			cleanup()
			if _, err := os.Stat(cfg.DataDir); !os.IsNotExist(err) {
				t.Fatal("disposable credentials retained")
			}
		})
	}
}

func TestTaskNamingCodexPreservesAgentRefreshAuthority(t *testing.T) {
	secret := `{"auth_mode":"chatgpt","tokens":{"access_token":"usable-access","id_token":"opaque-id","account_id":"account-1","refresh_token":"single-use-refresh","future":{"number":123456789012345678901}},"last_refresh":"2026-10-17T00:00:00Z","future_root":true}`
	credential := worker.CredentialResponse{Provider: "codex", CredentialType: "auth_json", Secret: secret}
	b := HarnessBuilder{DataDir: t.TempDir(), ConfigRoot: t.TempDir()}
	launch := worker.LaunchContext{SessionID: "session-1", Harness: "codex", Mode: "standard"}
	cfg, cleanup, err := b.BuildTaskNaming(context.Background(), launch, credential)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	body, err := os.ReadFile(filepath.Join(cfg.Env["CODEX_HOME"], "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("single-use-refresh")) {
		t.Fatal("cosmetic naming received the agent's refresh authority")
	}
	var original, isolated map[string]json.RawMessage
	if err := json.Unmarshal([]byte(secret), &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &isolated); err != nil {
		t.Fatal(err)
	}
	var originalTokens, isolatedTokens map[string]json.RawMessage
	if err := json.Unmarshal(original["tokens"], &originalTokens); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(isolated["tokens"], &isolatedTokens); err != nil {
		t.Fatal(err)
	}
	if string(isolatedTokens["refresh_token"]) != `""` {
		t.Fatal("native Codex requires an empty string refresh token")
	}
	delete(originalTokens, "refresh_token")
	delete(isolatedTokens, "refresh_token")
	delete(original, "tokens")
	delete(isolated, "tokens")
	if !reflect.DeepEqual(original, isolated) || !reflect.DeepEqual(originalTokens, isolatedTokens) {
		t.Fatal("naming changed fields other than refresh authority")
	}
	cleanup()
	command, err := b.BuildInteractive(launch, credential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if command.Cleanup != nil {
		t.Cleanup(command.Cleanup)
	}
	agentAuth, err := os.ReadFile(filepath.Join(command.Env["CODEX_HOME"], "auth.json"))
	if err != nil || string(agentAuth) != secret || credential.Secret != secret {
		t.Fatal("naming changed the real agent's original authentication")
	}
}

func TestTaskNamingCodexAPIKeyAuthJSONIsUnchanged(t *testing.T) {
	secret := "{\n  \"OPENAI_API_KEY\": \"api-key\", \"tokens\": null, \"future\": 1\n}"
	cfg, cleanup, err := (HarnessBuilder{DataDir: t.TempDir()}).BuildTaskNaming(context.Background(), worker.LaunchContext{Harness: "codex"}, worker.CredentialResponse{Provider: "codex", CredentialType: "auth_json", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	body, err := os.ReadFile(filepath.Join(cfg.Env["CODEX_HOME"], "auth.json"))
	if err != nil || string(body) != secret {
		t.Fatal("API key authentication changed")
	}
}

func TestTaskNamingRejectsInvalidCodexAuthBeforeCreatingFiles(t *testing.T) {
	for _, secret := range []string{"private-invalid-auth", "null", "[]", `{"tokens":[]}`, `{"tokens":"private-token-shape"}`} {
		data := t.TempDir()
		_, cleanup, err := (HarnessBuilder{DataDir: data}).BuildTaskNaming(context.Background(), worker.LaunchContext{Harness: "codex"}, worker.CredentialResponse{Provider: "codex", CredentialType: "auth_json", Secret: secret})
		if err == nil || err.Error() != "invalid Codex authentication for task naming" || cleanup != nil {
			t.Fatal("invalid authentication must fail with a content-free error")
		}
		entries, err := os.ReadDir(data)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid authentication wrote files")
		}
	}
}

func TestTaskNamingCodexReusesCredentialLogin(t *testing.T) {
	called := false
	b := HarnessBuilder{DataDir: t.TempDir(), CodexLogin: func(binary, home, kind, secret string) error {
		called = binary == "codex" && kind == "api_key" && secret == "selected-secret" && filepath.IsAbs(home)
		return nil
	}}
	_, cleanup, err := b.BuildTaskNaming(context.Background(), worker.LaunchContext{Harness: "codex"}, worker.CredentialResponse{Provider: "codex", CredentialType: "api_key", Secret: "selected-secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !called {
		t.Fatal("existing credential login was not used")
	}
}

func TestTaskNamingRejectsUnsupportedAndMismatchedCredentialsBeforeCreatingFiles(t *testing.T) {
	for _, tc := range []struct{ harness, provider string }{{"missing", "missing"}, {"codex", "cursor"}} {
		data := t.TempDir()
		_, cleanup, err := (HarnessBuilder{DataDir: data}).BuildTaskNaming(context.Background(), worker.LaunchContext{Harness: tc.harness}, worker.CredentialResponse{Provider: tc.provider, CredentialType: "api_key", Secret: "secret"})
		if err == nil || cleanup != nil {
			t.Fatal("invalid provider accepted")
		}
		entries, err := os.ReadDir(data)
		if err != nil || len(entries) != 0 {
			t.Fatalf("invalid provider wrote files: %v, %v", entries, err)
		}
	}
}
