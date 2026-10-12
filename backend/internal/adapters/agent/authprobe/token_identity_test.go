package authprobe

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenIdentityReadsDisplayClaimsOnly(t *testing.T) {
	jwt := func(payload string) string {
		return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
	}
	for _, tt := range []struct {
		token, email, name string
	}{
		{jwt(`{"email":"dev@example.com","name":"Ada Lovelace"}`), "dev@example.com", "Ada Lovelace"},
		{jwt(`{"email":"dev@example.com","firstName":"Ada","lastName":"Lovelace"}`), "dev@example.com", "Ada Lovelace"},
		{jwt(`{"sub":"123"}`), "", ""},
		{"not-a-jwt", "", ""},
	} {
		if email, name := TokenIdentity(tt.token); email != tt.email || name != tt.name {
			t.Fatalf("TokenIdentity = (%q, %q), want (%q, %q)", email, name, tt.email, tt.name)
		}
	}
}

func TestGitHubCLIUserReadsTheActiveHostLogin(t *testing.T) {
	for name, hosts := range map[string]string{
		"four-space indent": "github.com:\n    git_protocol: https\n    users:\n        octocat:\n    user: octocat\n",
		"two-space indent":  "github.com:\n  users:\n    other:\n  user: octocat\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("GH_CONFIG_DIR", dir)
			if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte(hosts), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := GitHubCLIUser(); got != "octocat" {
				t.Fatalf("GitHubCLIUser = %q, want octocat", got)
			}
		})
	}
}
