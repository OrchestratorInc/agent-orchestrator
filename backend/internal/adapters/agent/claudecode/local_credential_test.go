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
// setup-token login under a fresh AO data directory.
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

// Choosing the setup token in AO's login menu is an explicit sign-in, so it
// signs sessions in even when Claude Code has a login of its own.
func TestAugmentRuntimeEnvSignsInWithChosenSetupToken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{"no other login", func(*testing.T) {}},
		{"native credential in the daemon environment", func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-native")
		}},
		{"native credential in Claude settings", func(t *testing.T) {
			writeClaudeAuthSettings(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-api03-from-settings",
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := withSavedSetupToken(t)
			tc.setup(t)
			env := map[string]string{}
			(&Plugin{}).AugmentRuntimeEnv(env, dataDir)
			if env["CLAUDE_CODE_OAUTH_TOKEN"] != testSetupToken {
				t.Fatal("the chosen setup token was not used to sign in")
			}
		})
	}
}

func TestAugmentRuntimeEnvKeepsLaunchSpecificCredentials(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"project credential", map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api03-project"}},
		{"account config folder", map[string]string{claudeConfigDirEnv: "/accounts/work"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := withSavedSetupToken(t)
			(&Plugin{}).AugmentRuntimeEnv(tc.env, dataDir)
			if _, injected := tc.env["CLAUDE_CODE_OAUTH_TOKEN"]; injected {
				t.Fatal("the machine-wide setup token replaced a credential chosen for this launch")
			}
		})
	}
}

func TestRemoveLocalOAuthTokenForgetsSetupToken(t *testing.T) {
	dataDir := withSavedSetupToken(t)
	for range 2 { // a second removal finds nothing and still succeeds
		if err := RemoveLocalOAuthToken(dataDir); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{}
	(&Plugin{}).AugmentRuntimeEnv(env, dataDir)
	if _, injected := env["CLAUDE_CODE_OAUTH_TOKEN"]; injected {
		t.Fatal("a removed setup token still signed sessions in")
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

// Work outside a session (model discovery, the computer's account check) asks
// for the same credential a session would get.
func TestRuntimeCredentialEnvReturnsSavedSetupToken(t *testing.T) {
	dataDir := withSavedSetupToken(t)
	got := New().RuntimeCredentialEnv(dataDir)
	if len(got) != 1 || got["CLAUDE_CODE_OAUTH_TOKEN"] != testSetupToken {
		t.Fatalf("RuntimeCredentialEnv = %v, want only the saved setup token", got)
	}
	if got := New().RuntimeCredentialEnv(t.TempDir()); got != nil {
		t.Fatalf("RuntimeCredentialEnv without a saved token = %v, want nil", got)
	}
}

const testCloudToken = "sk-ant-oat01-cloud-setup-token"

// withCloudToken isolates Claude's native credential sources and saves the
// token AO Cloud sign-in writes, as the desktop app does.
func withCloudToken(t *testing.T) string {
	t.Helper()
	clearClaudeCredentialEnv(t)
	// Prevent a macOS keychain helper from finding real user credentials.
	t.Setenv("PATH", "")
	dataDir := t.TempDir()
	path := CloudOAuthTokenPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testCloudToken), 0o600); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

// Nobody chose the AO Cloud token for local sessions, so it must never shadow
// Claude Code's own login (#5878).
func TestAugmentRuntimeEnvUsesCloudTokenOnlyWithoutNativeLogin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		want  bool
	}{
		{"no other login", func(*testing.T) {}, true},
		{"native credential in the daemon environment", func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-native")
		}, false},
		{"native credential in Claude settings", func(t *testing.T) {
			writeClaudeAuthSettings(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-api03-from-settings",
			})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := withCloudToken(t)
			tc.setup(t)
			env := map[string]string{}
			(&Plugin{}).AugmentRuntimeEnv(env, dataDir)
			if got := env["CLAUDE_CODE_OAUTH_TOKEN"] == testCloudToken; got != tc.want {
				t.Fatalf("cloud token injected = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChosenSetupTokenOutranksCloudToken(t *testing.T) {
	dataDir := withCloudToken(t)
	if err := SaveLocalOAuthToken(dataDir, []byte(testSetupToken)); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	(&Plugin{}).AugmentRuntimeEnv(env, dataDir)
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != testSetupToken {
		t.Fatal("the token chosen in the login menu did not sign the session in")
	}
}

// Signing in through options 1-3 forgets only the menu's token; the AO Cloud
// copy stays for the cloud flow and already defers to the new native login.
func TestRemoveLocalOAuthTokenKeepsCloudToken(t *testing.T) {
	dataDir := withCloudToken(t)
	if err := SaveLocalOAuthToken(dataDir, []byte(testSetupToken)); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLocalOAuthToken(dataDir); err != nil {
		t.Fatal(err)
	}
	if got := readTokenFile(CloudOAuthTokenPath(dataDir)); got != testCloudToken {
		t.Fatalf("cloud token = %q after removing the menu token", got)
	}
}
