package agentaccount

import (
	"os"
	"path/filepath"
	"testing"
)

const connectorsTestToken = "sk-ant-oat01-connectors-test-token"

// A setup token account signed in again with `claude auth login` keeps its
// token file. The token must not shadow that login: only the login can load
// the user's claude.ai connectors (#9101).
func TestClaudeLaunchEnvPrefersFolderLoginOverSetupToken(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hasLogin  bool
		wantToken bool
	}{
		{"setup token only", false, true},
		{"setup token and a claude.ai login", true, false},
	} {
		// A token the daemon itself holds stays blanked either way.
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-daemon")
		t.Run(tc.name, func(t *testing.T) {
			previous := claudeHasSubscriptionLogin
			claudeHasSubscriptionLogin = func(string) bool { return tc.hasLogin }
			t.Cleanup(func() { claudeHasSubscriptionLogin = previous })

			home := t.TempDir()
			if err := (&Claude{}).SaveSetupToken(home, connectorsTestToken); err != nil {
				t.Fatal(err)
			}
			env := (&Claude{}).LaunchEnv(home)
			value := env["CLAUDE_CODE_OAUTH_TOKEN"]
			if tc.wantToken && value != connectorsTestToken {
				t.Fatal("setup token not used")
			}
			if !tc.wantToken && value != "" {
				t.Fatal("setup token shadows the folder's claude.ai login")
			}
		})
	}
}

// The real check reads the account folder's own stored login, not the
// computer's.
func TestClaudeHasSubscriptionLoginReadsTheAccountFolder(t *testing.T) {
	// No keychain helper: the check falls through to .credentials.json.
	t.Setenv("PATH", "")
	device := t.TempDir()
	t.Setenv("HOME", device)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(device, ".claude"))
	writeLogin := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-login"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLogin(filepath.Join(device, ".claude"))

	home := t.TempDir()
	if claudeHasSubscriptionLogin(home) {
		t.Fatal("the computer's own login counted for an account folder")
	}
	writeLogin(home)
	if !claudeHasSubscriptionLogin(home) {
		t.Fatal("the account folder's login was not found")
	}
}
