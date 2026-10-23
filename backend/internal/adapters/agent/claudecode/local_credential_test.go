package claudecode

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

const testSetupToken = "sk-ant-oat01-local-setup-token"

// withSavedSetupToken isolates Claude's native credential sources and saves a
// setup-token under a fresh AO data directory.
func withSavedSetupToken(t *testing.T) string {
	t.Helper()
	clearClaudeCredentialEnv(t)
	// Prevent a macOS keychain helper from finding real user credentials.
	t.Setenv("PATH", "")
	dataDir := t.TempDir()
	if err := SaveLocalOAuthToken(dataDir, []byte(testSetupToken)); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestAugmentRuntimeEnvInjectsSavedTokenWithoutNativeLogin(t *testing.T) {
	dataDir := withSavedSetupToken(t)
	env := map[string]string{}
	(&Plugin{}).AugmentRuntimeEnv(env, dataDir)
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != testSetupToken {
		t.Fatal("saved setup token was not injected for a machine with no native login")
	}
}

func TestAugmentRuntimeEnvDefersToNativeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, env map[string]string)
	}{
		{"process environment", func(t *testing.T, _ map[string]string) {
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-native")
		}},
		{"Claude settings env", func(t *testing.T, _ map[string]string) {
			writeClaudeAuthSettings(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-api03-from-settings",
			})
		}},
		{"account config folder", func(_ *testing.T, env map[string]string) {
			env[claudeConfigDirEnv] = "/accounts/work"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := withSavedSetupToken(t)
			env := map[string]string{}
			tc.setup(t, env)
			(&Plugin{}).AugmentRuntimeEnv(env, dataDir)
			if _, injected := env["CLAUDE_CODE_OAUTH_TOKEN"]; injected {
				t.Fatal("saved setup token shadowed a native Claude credential")
			}
		})
	}
}

func TestAuthStatusForRuntimeProbesSavedSetupToken(t *testing.T) {
	dataDir := withSavedSetupToken(t)
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	previousCommand := claudeAuthCommand
	claudeAuthCommand = func(_ context.Context, _, _ string, env map[string]string) (claudeCommandOutput, error) {
		if env["CLAUDE_CODE_OAUTH_TOKEN"] != testSetupToken {
			t.Error("claude auth status ran without the saved setup token")
		}
		return claudeCommandOutput{Stdout: []byte(`{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}`)}, nil
	}
	t.Cleanup(func() { claudeAuthCommand = previousCommand })
	probed := false
	previousValidator := claudeValidator
	claudeValidator = func() *agentcreds.Validator {
		return agentcreds.New(&http.Client{Transport: claudeTestTransport(func(req *http.Request) (*http.Response, error) {
			probed = true
			status := http.StatusUnauthorized
			if req.Header.Get("authorization") == "Bearer "+testSetupToken {
				status = http.StatusOK
			}
			return &http.Response{
				StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-test"}]}`)), Request: req,
			}, nil
		})})
	}
	t.Cleanup(func() { claudeValidator = previousValidator })

	status, _, _, err := (&Plugin{resolvedBinary: "/fixture/claude"}).AuthStatusForRuntime(context.Background(), dataDir)
	if err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatusForRuntime = %q, err = %v; want the saved token verified", status, err)
	}
	if !probed {
		t.Fatal("the saved setup token was not sent to the provider probe")
	}
}

func TestSaveLocalOAuthTokenReplacesTokenWithoutLeftovers(t *testing.T) {
	dataDir := t.TempDir()
	for _, token := range []string{"sk-ant-oat01-first-token", "sk-ant-oat01-second-token"} {
		if err := SaveLocalOAuthToken(dataDir, []byte(token)); err != nil {
			t.Fatal(err)
		}
		if got := readLocalOAuthToken(dataDir); got != token {
			t.Fatalf("saved token = %q, want %q", got, token)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(LocalOAuthTokenPath(dataDir)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("credential directory has %d entries, want only the token file", len(entries))
	}
}
