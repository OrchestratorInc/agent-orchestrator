package agentcreds

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func envFrom(values map[string]string) Env {
	return func(name string) string { return values[name] }
}

func TestFirstPartyPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		wantSource string
		wantKind   Kind
	}{
		{
			name: "oauth token wins over api key",
			env: map[string]string{
				"CLAUDE_CODE_OAUTH_TOKEN": "oat", "ANTHROPIC_API_KEY": "key", "ANTHROPIC_AUTH_TOKEN": "auth",
			},
			wantSource: "CLAUDE_CODE_OAUTH_TOKEN", wantKind: KindOAuthToken,
		},
		{
			name:       "api key wins over auth token",
			env:        map[string]string{"ANTHROPIC_API_KEY": "key", "ANTHROPIC_AUTH_TOKEN": "auth"},
			wantSource: "ANTHROPIC_API_KEY", wantKind: KindAPIKey,
		},
		{
			name:       "auth token is last of the env sources",
			env:        map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth"},
			wantSource: "ANTHROPIC_AUTH_TOKEN", wantKind: KindAuthToken,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
				Env: envFrom(tc.env), ConfigDir: t.TempDir(),
			})
			if !ok {
				t.Fatal("expected a credential")
			}
			if cred.Source != tc.wantSource {
				t.Fatalf("source = %q, want %q", cred.Source, tc.wantSource)
			}
			if cred.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", cred.Kind, tc.wantKind)
			}
		})
	}
}

func TestCredentialsFileIsSourceFive(t *testing.T) {
	dir := t.TempDir()
	content := `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-from-file"}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(nil), ConfigDir: dir, GOOS: "linux",
	})
	if !ok {
		t.Fatal("expected the credentials file to resolve")
	}
	if cred.Kind != KindOAuthToken || cred.Secret != "sk-ant-oat01-from-file" {
		t.Fatalf("credential = %+v", cred)
	}
	if cred.Source != "credentials-file" {
		t.Fatalf("source = %q, want credentials-file", cred.Source)
	}
}

func TestClaudeConfigDirIgnoresXDGConfigHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := claudeConfigDir(ResolveOptions{
		Env:  envFrom(map[string]string{"XDG_CONFIG_HOME": t.TempDir()}),
		GOOS: "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".claude"); got != want {
		t.Fatalf("config dir = %q, want %q", got, want)
	}
}

func TestEnvBeatsCredentialsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"from-file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, _ := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"ANTHROPIC_API_KEY": "from-env"}), ConfigDir: dir, GOOS: "linux",
	})
	if cred.Secret != "from-env" {
		t.Fatalf("secret came from %q, want the environment", cred.Source)
	}
}

func TestKeychainCanBeDisabledWithoutBreakingResolution(t *testing.T) {
	dir := t.TempDir()
	opts := ResolveOptions{Env: envFrom(nil), ConfigDir: dir, GOOS: "darwin", AllowKeychain: false,
		Runner: func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("the keychain must not be read when it is disallowed")
			return nil, nil
		}}
	if _, ok := ResolveLocal(context.Background(), ProviderFirstParty, opts); ok {
		t.Fatal("no credential should resolve with no env, no file, and no keychain")
	}

	opts.Env = envFrom(map[string]string{"ANTHROPIC_API_KEY": "key"})
	if _, ok := ResolveLocal(context.Background(), ProviderFirstParty, opts); !ok {
		t.Fatal("env-sourced credentials must resolve regardless of keychain policy")
	}
}

func TestKeychainFailureFallsThroughToFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"from-file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(nil), ConfigDir: dir, GOOS: "darwin", AllowKeychain: true,
		Runner: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("security: SecKeychainSearchCopyNext: user interaction is not allowed")
		},
	})
	if !ok || cred.Secret != "from-file" {
		t.Fatalf("credential = %+v, want the file fallback", cred)
	}
}

func TestKeychainSuccessIsSourceFive(t *testing.T) {
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(nil), ConfigDir: t.TempDir(), GOOS: "darwin", AllowKeychain: true,
		Runner: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-keychain"}}`), nil
		},
	})
	if !ok || cred.Source != "keychain" || cred.Kind != KindOAuthToken {
		t.Fatalf("credential = %+v, want a keychain-sourced oauth token", cred)
	}
}

func TestKeychainManagedKeyResolvesAsAPIKey(t *testing.T) {
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"HOME": t.TempDir()}), GOOS: "darwin", AllowKeychain: true,
		Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) >= 3 && args[2] == "Claude Code" {
				return []byte("sk-ant-api03-managed-key"), nil
			}
			return nil, errors.New("security: item not found")
		},
	})
	if !ok {
		t.Fatal("expected the managed key to resolve")
	}
	if cred.Kind != KindAPIKey {
		t.Fatalf("kind = %q, want %q", cred.Kind, KindAPIKey)
	}
	if cred.Source != "keychain" {
		t.Fatalf("source = %q, want keychain", cred.Source)
	}
	if cred.Secret != "sk-ant-api03-managed-key" {
		t.Fatalf("secret = %q, want the managed key", cred.Secret)
	}
}

func TestKeychainEmptyCredentialsFallsThroughToManagedKey(t *testing.T) {
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"HOME": t.TempDir()}), GOOS: "darwin", AllowKeychain: true,
		Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			switch {
			case len(args) >= 3 && args[2] == "Claude Code-credentials":
				return []byte("{}"), nil
			case len(args) >= 3 && args[2] == "Claude Code":
				return []byte("sk-ant-api03-fallback"), nil
			default:
				return nil, errors.New("security: item not found")
			}
		},
	})
	if !ok {
		t.Fatal("expected the managed-key fallback to resolve")
	}
	if cred.Kind != KindAPIKey {
		t.Fatalf("kind = %q, want %q", cred.Kind, KindAPIKey)
	}
	if cred.Secret != "sk-ant-api03-fallback" {
		t.Fatalf("secret = %q, want the fallback key", cred.Secret)
	}
}

func TestKeychainSetupTokenFromClassifiedAsOAuth(t *testing.T) {
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"HOME": t.TempDir()}), GOOS: "darwin", AllowKeychain: true,
		Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			switch {
			case len(args) >= 3 && args[2] == "Claude Code-credentials":
				return []byte("{}"), nil
			case len(args) >= 3 && args[2] == "Claude Code":
				return []byte("sk-ant-oat01-setup-token"), nil
			default:
				return nil, errors.New("security: item not found")
			}
		},
	})
	if !ok {
		t.Fatal("expected the setup token to resolve")
	}
	if cred.Kind != KindOAuthToken {
		t.Fatalf("kind = %q, want %q", cred.Kind, KindOAuthToken)
	}
	if cred.Source != "keychain" {
		t.Fatalf("source = %q, want keychain", cred.Source)
	}
	if cred.Secret != "sk-ant-oat01-setup-token" {
		t.Fatalf("secret = %q", cred.Secret)
	}
}

func TestKeychainIsMacOnly(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
				Env: envFrom(nil), ConfigDir: t.TempDir(), GOOS: goos, AllowKeychain: true,
				Runner: func(context.Context, string, ...string) ([]byte, error) {
					t.Fatalf("%s must not read a keychain", goos)
					return nil, nil
				},
			})
		})
	}
}

func TestResolveProvider(t *testing.T) {
	tests := []struct {
		name     string
		reported string
		env      map[string]string
		want     Provider
		wantOK   bool
	}{
		{name: "reported wins", reported: "bedrock", want: ProviderBedrock, wantOK: true},
		{name: "vertex", reported: "vertex", want: ProviderVertex, wantOK: true},
		{name: "foundry", reported: "foundry", want: ProviderFoundry, wantOK: true},
		{name: "aws alias is not canonical", reported: "aws", wantOK: false},
		{name: "google alias is not canonical", reported: "google", wantOK: false},
		{name: "azure alias is not canonical", reported: "azure", wantOK: false},
		{
			name: "project provider overrides stale CLI report", reported: "firstParty",
			env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, want: ProviderBedrock, wantOK: true,
		},
		{name: "false bedrock flag is disabled", reported: "vertex", env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "false"}, want: ProviderVertex, wantOK: true},
		{name: "non-standard truthy flag is disabled", env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "on"}, want: ProviderFirstParty, wantOK: true},
		{name: "foundry requires its flag", env: map[string]string{"ANTHROPIC_FOUNDRY_API_KEY": "stale"}, want: ProviderFirstParty, wantOK: true},
		{name: "foundry flag selects foundry", env: map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1", "ANTHROPIC_FOUNDRY_API_KEY": "key"}, want: ProviderFoundry, wantOK: true},
		{name: "conflicting project providers are rejected", env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_VERTEX": "true"}, wantOK: false},
		{name: "empty defaults to first party", want: ProviderFirstParty, wantOK: true},
		{
			name: "bedrock inferred from env when the CLI could not be asked",
			env:  map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, want: ProviderBedrock, wantOK: true,
		},
		{
			name: "base url means gateway",
			env:  map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.internal"},
			want: ProviderGateway, wantOK: true,
		},
		{name: "an unknown provider is not guessed", reported: "some-future-provider", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveProvider(tc.reported, ResolveOptions{Env: envFrom(tc.env)})
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("provider = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnsupportedProvidersDoNotResolveCredentialsInProcess(t *testing.T) {
	for _, env := range []map[string]string{
		{"AWS_BEARER_TOKEN_BEDROCK": "bt", "AWS_REGION": "us-east-1"},
		{"AWS_ACCESS_KEY_ID": "AKIA", "AWS_SECRET_ACCESS_KEY": "secret", "AWS_REGION": "eu-west-1"},
		{"AWS_PROFILE": "sso-profile"},
	} {
		if credential, ok := ResolveLocal(context.Background(), ProviderBedrock, ResolveOptions{Env: envFrom(env)}); ok {
			t.Fatalf("Bedrock credential resolved in-process: %+v", credential)
		}
	}
	for _, provider := range []Provider{ProviderVertex, ProviderFoundry} {
		if credential, ok := ResolveLocal(context.Background(), provider, ResolveOptions{Env: envFrom(map[string]string{
			"GOOGLE_OAUTH_ACCESS_TOKEN": "token", "ANTHROPIC_FOUNDRY_API_KEY": "key",
		})}); ok {
			t.Fatalf("%s credential resolved in-process: %+v", provider, credential)
		}
	}
}

func TestResolveLocalReadsCredentialFileSynchronously(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"accessToken":"token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	credential, ok := ResolveLocal(ctx, ProviderFirstParty, ResolveOptions{
		Env: envFrom(nil), ConfigDir: dir, GOOS: "linux",
	})
	if !ok || credential.Secret != "token" {
		t.Fatalf("credential = %+v, ok = %v", credential, ok)
	}
}

func TestAPIKeyHelperIsNeverExecuted(t *testing.T) {
	_, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env:       envFrom(map[string]string{"ANTHROPIC_API_KEY_HELPER": "/bin/echo leak"}),
		ConfigDir: t.TempDir(), GOOS: "linux",
		Runner: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			t.Fatalf("no command may be executed during resolution, got %q", name)
			return nil, nil
		},
	})
	if ok {
		t.Fatal("a key helper must not produce a credential")
	}
}

func TestStoredLoginCarriesExpiryAndRenewability(t *testing.T) {
	expiresAt := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	content := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-stored","refreshToken":"sk-ant-ort01-stored","expiresAt":%d}}`, expiresAt.UnixMilli())
	for name, opts := range map[string]ResolveOptions{
		"credentials file": {GOOS: "linux"},
		"keychain": {GOOS: "darwin", AllowKeychain: true, Runner: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(content), nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			opts.Env = envFrom(nil)
			opts.ConfigDir = dir
			cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, opts)
			if !ok {
				t.Fatal("expected the stored login to resolve")
			}
			if !cred.ExpiresAt.Equal(expiresAt) || !cred.Renewable {
				t.Fatalf("expiry metadata = (%v, %v), want (%v, true)", cred.ExpiresAt, cred.Renewable, expiresAt)
			}
			if cred.ExpiredButRenewable(expiresAt.Add(-time.Minute)) {
				t.Fatal("a token before its expiry must not read as expired")
			}
			if !cred.ExpiredButRenewable(expiresAt) {
				t.Fatal("a token at its expiry with a refresh token must read as expired but renewable")
			}
		})
	}
}

func TestExpiredLoginIsRenewableOnlyWhileItsRefreshTokenIsInDate(t *testing.T) {
	expiresAt := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	refreshExpiresAt := expiresAt.Add(21 * 24 * time.Hour)
	dir := t.TempDir()
	content := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-stored","refreshToken":"sk-ant-ort01-stored","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
		expiresAt.UnixMilli(), refreshExpiresAt.UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{Env: envFrom(nil), ConfigDir: dir, GOOS: "linux"})
	if !ok || !cred.RefreshExpiresAt.Equal(refreshExpiresAt) {
		t.Fatalf("refresh expiry = %v ok=%v, want %v", cred.RefreshExpiresAt, ok, refreshExpiresAt)
	}
	if !cred.ExpiredButRenewable(refreshExpiresAt.Add(-time.Minute)) {
		t.Fatal("an expired access token with an in-date refresh token must read as renewable")
	}
	if cred.ExpiredButRenewable(refreshExpiresAt) {
		t.Fatal("an expired refresh token cannot renew the login")
	}
}

func TestExpiredLoginWithoutRefreshTokenIsNotRenewable(t *testing.T) {
	dir := t.TempDir()
	content := `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-stored","expiresAt":1000}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{Env: envFrom(nil), ConfigDir: dir, GOOS: "linux"})
	if !ok {
		t.Fatal("expected the stored login to resolve")
	}
	if cred.ExpiredButRenewable(time.Now()) {
		t.Fatal("an expired token with no refresh token cannot be renewed by Claude Code")
	}
	env, _ := ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(map[string]string{"ANTHROPIC_API_KEY": "key"}), ConfigDir: dir, GOOS: "linux",
	})
	if env.ExpiredButRenewable(time.Now()) || !env.ExpiresAt.IsZero() {
		t.Fatal("environment credentials carry no stored expiry")
	}
}
