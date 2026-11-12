package copilot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCopilotLocalAuthStatusAuthorizedWithBYOKProvider(t *testing.T) {
	clearCopilotAuthProbeEnv(t)
	t.Setenv("COPILOT_PROVIDER_BASE_URL", "http://localhost:11434")
	t.Setenv("COPILOT_MODEL", "llama3.2")

	status, ok, err := copilotLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

func TestCopilotLocalAuthStatusUsesCopilotHome(t *testing.T) {
	clearCopilotAuthProbeEnv(t)
	dir := t.TempDir()
	t.Setenv("COPILOT_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"authToken":"oauth-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	status, ok, err := copilotLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

func TestCopilotLocalAuthStatusFallsBackToGHTokenWhenConfigIsMalformed(t *testing.T) {
	clearCopilotAuthProbeEnv(t)
	dir := t.TempDir()
	t.Setenv("COPILOT_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{malformed`), 0o600); err != nil {
		t.Fatal(err)
	}

	previous := authprobe.CmdRunner
	t.Cleanup(func() { authprobe.CmdRunner = previous })
	authprobe.CmdRunner = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "gh" || !reflect.DeepEqual(args, []string{"auth", "token"}) {
			return nil, errors.New("unexpected auth probe command")
		}
		return []byte("gho_test-token"), nil
	}

	status, ok, err := copilotLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

// Copilot CLI 1.0.109 writes config.json with JSONC-style comments and stores
// loggedInUsers as an array of account objects, keeping the OAuth token in the
// OS credential store (issue #8219). The probe must recognize that stored
// login as account metadata without reading credential secrets.
func TestCopilotConfigAuthStatusReadsJSONCArrayOfLogins(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{
	  // Copilot configuration comment
	  "loggedInUsers": [
	    {
	      "host": "github.com",
	      "login": "example-user",
	      "endpoints": {"api": "https://api.individual.githubcopilot.com//route"}
	    }
	  ]
	}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	status, ok, err := copilotConfigAuthStatus(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusConfigured)
	}
}

func TestCopilotConfigAuthStatusReadsLegacyMapOfLogins(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"loggedInUsers":{"github.com:octocat":{"login":"octocat"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	status, ok, err := copilotConfigAuthStatus(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusConfigured)
	}
}

// A comment-looking sequence inside a string is data, not a comment: mangling
// it would break JSON decoding instead of yielding a false login.
func TestCopilotConfigAuthStatusJSONCKeepsStringsIntact(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{
	  // leading comment
	  "defaultModel": "gpt//double-slash", /* block comment */
	  "loggedInUsers": [], // trailing comma follows
	}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	status, ok, err := copilotConfigAuthStatus(configPath)
	if err != nil {
		t.Fatalf("JSONC parsing mangled string contents: %v", err)
	}
	if ok || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
	}
}

// Empty or stale login records are not evidence of any usable login.
func TestCopilotConfigAuthStatusIgnoresEmptyLoginRecords(t *testing.T) {
	for name, config := range map[string]string{
		"empty array":    `{"loggedInUsers":[]}`,
		"null entries":   `{"loggedInUsers":[null,null]}`,
		"empty objects":  `{"loggedInUsers":[{},{}]}`,
		"empty strings":  `{"loggedInUsers":["",""]}`,
		"empty map":      `{"loggedInUsers":{}}`,
		"null map value": `{"loggedInUsers":{"github.com":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}

			status, ok, err := copilotConfigAuthStatus(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if ok || status != ports.AgentAuthStatusUnknown {
				t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
			}
		})
	}
}

// Comments do not make a truncated document parseable; the GitHub CLI token
// fallback must stay reachable behind such a config.
func TestCopilotLocalAuthStatusFallsBackToGHFromMalformedJSONC(t *testing.T) {
	clearCopilotAuthProbeEnv(t)
	dir := t.TempDir()
	t.Setenv("COPILOT_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("// header comment\n{\"loggedInUsers\": ["), 0o600); err != nil {
		t.Fatal(err)
	}

	previous := authprobe.CmdRunner
	t.Cleanup(func() { authprobe.CmdRunner = previous })
	authprobe.CmdRunner = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "gh" || !reflect.DeepEqual(args, []string{"auth", "token"}) {
			return nil, errors.New("unexpected auth probe command")
		}
		return []byte("gho_test-token"), nil
	}

	status, ok, err := copilotLocalAuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

func clearCopilotAuthProbeEnv(t *testing.T) {
	t.Helper()
	clearCopilotAuthEnv(t)
	for _, name := range []string{"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_API_KEY", "COPILOT_MODEL", "COPILOT_HOME"} {
		t.Setenv(name, "")
	}
}
