package openinterpreter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthStatusSelectedCustomProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native login fixture requires a POSIX shell")
	}
	const configured = `model_provider = "custom"
[model_providers.custom]
env_key = "AO_INTERPRETER_TEST_KEY"
`
	for _, tc := range []struct {
		name   string
		config string
		key    string
		login  string
		want   ports.AgentAuthStatus
	}{
		{"selected credential", configured, "fixture-key", "", ports.AgentAuthStatusConfigured},
		{"missing credential", configured, "", "", ports.AgentAuthStatusUnknown},
		{"whitespace credential", configured, " \t\n", "", ports.AgentAuthStatusUnknown},
		{"native auth required", configured + "requires_openai_auth = true\n", "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"conflicting auth helper", configured + "[model_providers.custom.auth]\ncommand = \"credential-helper\"\n", "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"conflicting AWS auth", configured + "[model_providers.custom.aws]\nregion = \"us-east-1\"\n", "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"reserved Bedrock override", strings.ReplaceAll(configured, "custom", "amazon-bedrock"), "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"reserved Bedrock runtime override", strings.ReplaceAll(configured, "custom", "amazon-bedrock-runtime"), "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"unrelated credential", `model_provider = "other"
[model_providers.custom]
env_key = "AO_INTERPRETER_TEST_KEY"
[model_providers.other]
env_key = "AO_INTERPRETER_MISSING_TEST_KEY"
`, "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"no explicit selection", `[model_providers.custom]
env_key = "AO_INTERPRETER_TEST_KEY"
`, "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"selected provider absent", `model_provider = "missing"
[model_providers.custom]
env_key = "AO_INTERPRETER_TEST_KEY"
`, "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"no env key", `model_provider = "custom"
[model_providers.custom]
base_url = "https://example.invalid/v1"
`, "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"provider name is exact", strings.Replace(configured, `model_provider = "custom"`, `model_provider = " custom "`, 1), "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"env name is exact", strings.Replace(configured, `"AO_INTERPRETER_TEST_KEY"`, `" AO_INTERPRETER_TEST_KEY "`, 1), "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"named profile ambiguous", "profile = \"other\"\n" + configured + "\n[profiles.other]\nmodel_provider = \"other\"\n", "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"malformed config", configured + "malformed = [", "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"oversized config", configured + "#" + strings.Repeat("x", 1<<20), "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"missing config", "", "fixture-key", "", ports.AgentAuthStatusUnknown},
		{"native login preserved", "malformed = [", "", "Logged in using ChatGPT", ports.AgentAuthStatusConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("INTERPRETER_HOME", home)
			t.Setenv("AO_INTERPRETER_TEST_KEY", tc.key)
			t.Setenv("AO_INTERPRETER_MISSING_TEST_KEY", "")
			t.Setenv("AO_INTERPRETER_TEST_LOGIN", tc.login)
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(t.TempDir(), "interpreter")
			script := "#!/bin/sh\n[ \"$1\" = login ] && [ \"$2\" = status ] || exit 2\nif [ -n \"$AO_INTERPRETER_TEST_LOGIN\" ]; then\n  printf '%s\\n' \"$AO_INTERPRETER_TEST_LOGIN\"\n  exit 0\nfi\nprintf 'Not logged in\\n'\nexit 1\n"
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			p := &Plugin{resolvedBinary: binary}
			got, err := p.AuthStatus(context.Background())
			if err != nil || got != tc.want {
				t.Fatalf("AuthStatus = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
