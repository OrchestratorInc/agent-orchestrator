package agentcreds

import (
	"context"
	"errors"
	"slices"
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

// fakeKeychain answers like `security`: items are searched in the order they
// were added, and a lookup without -a returns the first one for the service.
func fakeKeychain(items ...[3]string) commandRunner {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		service := args[slices.Index(args, "-s")+1]
		account := ""
		if at := slices.Index(args, "-a"); at >= 0 {
			account = args[at+1]
		}
		for _, item := range items {
			if item[0] == service && (account == "" || item[1] == account) {
				return []byte(item[2]), nil
			}
		}
		return nil, errors.New("item not found")
	}
}

func TestKeychainPrefersClaudeCodeAccountOverItemAddedFirst(t *testing.T) {
	const service = "Claude Code-credentials"
	current := [3]string{service, "someone", `{"claudeAiOauth":{"accessToken":"current-token"}}`}
	for _, tc := range []struct {
		name  string
		stale string
	}{
		{"stale item holds no token", `{"mcpOAuth":{"server":{"clientId":"id"}}}`},
		{"stale item holds an old token", `{"claudeAiOauth":{"accessToken":"stale-token"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cred, found := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
				Env: envFrom(map[string]string{"USER": "someone"}), GOOS: "darwin", AllowKeychain: true,
				Runner: fakeKeychain([3]string{service, "unknown", tc.stale}, current),
			})
			if !found || cred.Secret != "current-token" {
				t.Fatalf("credential resolved = %v, secret = %q", found, cred.Secret)
			}
		})
	}
}

func TestKeychainStillReadsItemFiledUnderAnotherAccount(t *testing.T) {
	cred, found := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"USER": "someone"}), GOOS: "darwin", AllowKeychain: true,
		Runner: fakeKeychain([3]string{"Claude Code-credentials", "unknown", `{"claudeAiOauth":{"accessToken":"only-token"}}`}),
	})
	if !found || cred.Secret != "only-token" {
		t.Fatalf("credential resolved = %v, secret = %q", found, cred.Secret)
	}
}

// A signed-out account item is Claude Code's answer; another account's token
// must not stand in for it.
func TestKeychainDoesNotReplaceAccountItemWithAnotherAccounts(t *testing.T) {
	_, found := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"USER": "someone"}), GOOS: "darwin", AllowKeychain: true,
		Runner: fakeKeychain(
			[3]string{"Claude Code-credentials", "unknown", `{"claudeAiOauth":{"accessToken":"stale-token"}}`},
			[3]string{"Claude Code-credentials", "someone", `{"mcpOAuth":{}}`},
		),
	})
	if found {
		t.Fatal("resolved another account's credential over the account's own item")
	}
}

func TestKeychainAccountFollowsClaudeCodeRule(t *testing.T) {
	for _, tc := range []struct{ user, want string }{
		{"someone", "someone"},
		{"first.last-1_x", "first.last-1_x"},
		{"has space", keychainFallbackAccount},
	} {
		if got := keychainAccount(ResolveOptions{Env: envFrom(map[string]string{"USER": tc.user})}); got != tc.want {
			t.Errorf("USER=%q account = %q, want %q", tc.user, got, tc.want)
		}
	}
}
