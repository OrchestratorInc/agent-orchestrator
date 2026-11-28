package agentcreds

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A setup token AO saved yields to a claude.ai login only while that login
// still works or Claude Code can renew it (#9101).
func TestHasUsableSubscriptionLogin(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	future, past := "1800000600000", "1799999400000"
	for _, tc := range []struct {
		name        string
		credentials string // "" writes no file
		env         map[string]string
		want        bool
	}{
		{"no stored login", "", nil, false},
		{"in date", `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-a","expiresAt":` + future + `}}`, nil, true},
		{"no recorded expiry", `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-a"}}`, nil, true},
		{"expired but renewable", `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-a","refreshToken":"r","expiresAt":` + past + `}}`, nil, true},
		{"expired without refresh token", `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-a","expiresAt":` + past + `}}`, nil, false},
		{"refresh token expired too", `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-a","refreshToken":"r","expiresAt":` + past + `,"refreshTokenExpiresAt":` + past + `}}`, nil, false},
		// Credential variables are not a stored login, so they never count.
		{"only a token in the environment", "", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-env", "ANTHROPIC_API_KEY": "sk-ant-api03-env"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.credentials != "" {
				if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(tc.credentials), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			opts := ResolveOptions{Env: envFrom(tc.env), ConfigDir: dir, GOOS: "linux"}
			if got := HasUsableSubscriptionLogin(context.Background(), opts, now); got != tc.want {
				t.Fatalf("HasUsableSubscriptionLogin = %v, want %v", got, tc.want)
			}
		})
	}
}

// Workspace provider routing must not pick up the user's stored login.
func TestHasUsableSubscriptionLoginHonorsDisabledStoredCredentials(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := ResolveOptions{Env: envFrom(nil), ConfigDir: dir, GOOS: "linux", DisableStoredCredentials: true}
	if HasUsableSubscriptionLogin(context.Background(), opts, time.Now()) {
		t.Fatal("a stored login counted although stored credentials are disabled")
	}
}
