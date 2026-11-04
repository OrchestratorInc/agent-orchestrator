package agentcreds

import (
	"context"
	"errors"
	"testing"
)

func TestKeychainUsesAccountConfigDirectory(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		configDir string
		service   string
	}{
		{"config environment", map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/team"}, "", "Claude Code-credentials-542298b1"},
		{"config override", nil, "/accounts/team", "Claude Code-credentials-542298b1"},
		{"secure storage override", map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/team", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/accounts/max"}, "", "Claude Code-credentials-793ccf24"},
		{"exact path text", map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": "/accounts/team/../team"}, "", "Claude Code-credentials-f1da4ba3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cred, found := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
				Env: envFrom(tc.env), ConfigDir: tc.configDir, GOOS: "darwin", AllowKeychain: true,
				Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
					if args[2] == tc.service {
						return []byte(`{"claudeAiOauth":{"accessToken":"account-token"}}`), nil
					}
					return nil, errors.New("item not found")
				},
			})
			if !found || cred.Secret != "account-token" || cred.Source != "keychain" {
				t.Fatalf("account credential resolved = %v, source = %q", found, cred.Source)
			}
		})
	}
}

func TestAccountKeychainDoesNotFallBackToComputerLogin(t *testing.T) {
	_, found := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/team"}), GOOS: "darwin", AllowKeychain: true,
		Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[2] == "Claude Code-credentials" || args[2] == "Claude Code" {
				t.Error("account lookup read the computer's credential")
				return []byte(`{"accessToken":"computer-token"}`), nil
			}
			return nil, errors.New("item not found")
		},
	})
	if found {
		t.Fatal("a missing account login must not resolve another account's token")
	}
}
