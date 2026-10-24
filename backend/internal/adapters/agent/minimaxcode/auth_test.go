package minimaxcode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func clearAuthCredentialEnv(t *testing.T) {
	t.Helper()
	for _, name := range authCredentialEnvNames {
		t.Setenv(name, "")
	}
}

func TestLocalAuthStatus(t *testing.T) {
	clearAuthCredentialEnv(t)
	for _, tc := range []struct {
		name, config, state, file string
		want                      ports.AgentAuthStatus
	}{
		{name: "no saved credentials", want: ports.AgentAuthStatusUnauthorized},
		{name: "logged out", config: "minimaxModelSource: token_plan\ndefaultModel: minimax/MiniMax-M2.5\n", state: `{"status":"anonymous"}`, want: ports.AgentAuthStatusUnauthorized},
		{name: "signed in", state: `{"status":"authenticated"}`, want: ports.AgentAuthStatusConfigured},
		{name: "login in flight", state: `{"status":"authorizing"}`, want: ports.AgentAuthStatusUnknown},
		{name: "corrupt state", state: "{", want: ports.AgentAuthStatusUnknown},
		{name: "API key", config: "minimax_api:\n  apiKey: fixture-key\n", want: ports.AgentAuthStatusUnknown},
		{name: "custom provider", config: "custom_provider:\n  local: {}\n", want: ports.AgentAuthStatusUnknown},
		{name: "legacy provider", config: "provider:\n  other: {}\n", want: ports.AgentAuthStatusUnknown},
		{name: "API model source", config: "minimaxModelSource: minimax_api_key\n", want: ports.AgentAuthStatusUnknown},
		{name: "alternate model", config: "defaultModel: openai-codex/model\n", want: ports.AgentAuthStatusUnknown},
		{name: "corrupt config", config: "invalid: [", want: ports.AgentAuthStatusUnknown},
		{name: "codex credentials", file: "codex-auth.json", want: ports.AgentAuthStatusUnknown},
		{name: "runtime credentials", file: "local-runtime.auth.json", want: ports.AgentAuthStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.config != "" {
				write("config.yaml", tc.config)
			}
			if tc.state != "" {
				write(filepath.Join("auth", "prod", "en", "mcode-public", "auth-state.json"), tc.state)
			}
			if tc.file != "" {
				write(tc.file, "{}")
			}
			if got := localAuthStatus(dir); got != tc.want {
				t.Fatalf("auth status = %q, want %q", got, tc.want)
			}
		})
	}
	for _, name := range authCredentialEnvNames {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "fixture-credential")
			if got := localAuthStatus(t.TempDir()); got != ports.AgentAuthStatusUnknown {
				t.Fatalf("environment credential reported %q", got)
			}
		})
	}
}
