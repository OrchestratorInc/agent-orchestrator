package agentcreds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Env func(string) string

type ResolveOptions struct {
	Env                      Env
	ConfigDir                string
	AllowKeychain            bool
	Runner                   commandRunner
	GOOS                     string
	WorkingDir               string
	CommandEnv               map[string]string
	DisableStoredCredentials bool
}

func (o ResolveOptions) WithClaudeSettings(ctx context.Context) ResolveOptions {
	settings := ResolveClaudeSettings(ctx, o.WorkingDir, o.CommandEnv, o)
	base := o
	merged := make(map[string]string, len(o.CommandEnv)+len(settings.Env))
	for key, value := range o.CommandEnv {
		merged[key] = value
	}
	for key, value := range settings.Env {
		merged[key] = value
	}
	o.CommandEnv = merged
	if settings.WorkspaceProviderRouting {
		o.DisableStoredCredentials = true
	}
	o.Env = func(key string) string {
		if value, ok := merged[key]; ok {
			return value
		}
		return base.env(key)
	}
	return o
}

func (o ResolveOptions) env(name string) string {
	if o.Env != nil {
		return strings.TrimSpace(o.Env(name))
	}
	return strings.TrimSpace(os.Getenv(name))
}

func (o ResolveOptions) goos() string {
	if o.GOOS != "" {
		return o.GOOS
	}
	return runtime.GOOS
}

func ResolveProvider(reported string, opts ResolveOptions) (Provider, bool) {
	selected := make([]Provider, 0, 3)
	for _, candidate := range []struct {
		name     string
		provider Provider
	}{
		{"CLAUDE_CODE_USE_BEDROCK", ProviderBedrock},
		{"CLAUDE_CODE_USE_VERTEX", ProviderVertex},
		{"CLAUDE_CODE_USE_FOUNDRY", ProviderFoundry},
	} {
		if envTruthy(opts.env(candidate.name)) {
			selected = append(selected, candidate.provider)
		}
	}
	if len(selected) > 1 {
		return "", false
	}
	if len(selected) == 1 {
		return selected[0], true
	}
	if opts.env("ANTHROPIC_BASE_URL") != "" {
		return ProviderGateway, true
	}
	if trimmed := strings.TrimSpace(reported); trimmed != "" {
		return ParseProvider(trimmed)
	}
	return ProviderFirstParty, true
}

func envTruthy(value string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && parsed
}

func ResolveLocal(ctx context.Context, provider Provider, opts ResolveOptions) (Credential, bool) {
	switch provider {
	case ProviderFirstParty:
		return resolveFirstParty(ctx, opts)
	case ProviderGateway:
		cred, ok := resolveFirstParty(ctx, opts)
		if !ok {
			return Credential{}, false
		}
		cred.Provider = ProviderGateway
		cred.BaseURL = opts.env("ANTHROPIC_BASE_URL")
		return cred, true
	default:
		return Credential{}, false
	}
}

var firstPartySources = []struct {
	env  string
	kind Kind
}{
	{"CLAUDE_CODE_OAUTH_TOKEN", KindOAuthToken},
	{"ANTHROPIC_API_KEY", KindAPIKey},
	{"ANTHROPIC_AUTH_TOKEN", KindAuthToken},
}

func resolveFirstParty(ctx context.Context, opts ResolveOptions) (Credential, bool) {
	for _, source := range firstPartySources {
		if secret := opts.env(source.env); secret != "" {
			return Credential{
				Kind: source.kind, Secret: secret, Source: source.env, Provider: ProviderFirstParty,
			}, true
		}
	}
	if opts.DisableStoredCredentials {
		return Credential{}, false
	}
	if stored, source, ok := loadOAuth(ctx, opts); ok {
		return Credential{
			Kind: stored.kind, Secret: stored.token, Source: source, Provider: ProviderFirstParty,
			ExpiresAt: stored.expiresAt, Renewable: stored.renewable, RefreshExpiresAt: stored.refreshExpiresAt,
		}, true
	}
	return Credential{}, false
}

type storedOAuth struct {
	token            string
	kind             Kind
	expiresAt        time.Time
	refreshExpiresAt time.Time
	renewable        bool
}

func loadOAuth(ctx context.Context, opts ResolveOptions) (stored storedOAuth, source string, ok bool) {
	if opts.goos() == "darwin" && opts.AllowKeychain {
		if stored, ok := readKeychain(ctx, opts); ok {
			return stored, "keychain", true
		}
	}
	stored, src, ok := readCredentialsFile(ctx, opts)
	if !ok {
		return storedOAuth{}, "", false
	}
	return stored, src, true
}

func readCredentialsFile(ctx context.Context, opts ResolveOptions) (stored storedOAuth, source string, ok bool) {
	_ = ctx
	dir, err := claudeConfigDir(opts)
	if err != nil {
		return storedOAuth{}, "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		return storedOAuth{}, "", false
	}
	stored, ok = oauthTokenFromCredentialsJSON(data)
	if !ok {
		return storedOAuth{}, "", false
	}
	return stored, "credentials-file", true
}

func oauthTokenFromCredentialsJSON(data []byte) (storedOAuth, bool) {
	type oauthFields struct {
		AccessToken           string  `json:"accessToken"`
		RefreshToken          string  `json:"refreshToken"`
		ExpiresAt             float64 `json:"expiresAt"`
		RefreshTokenExpiresAt float64 `json:"refreshTokenExpiresAt"`
	}
	var payload struct {
		ClaudeAiOauth oauthFields `json:"claudeAiOauth"`
		oauthFields
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return storedOAuth{}, false
	}
	for _, candidate := range []oauthFields{payload.ClaudeAiOauth, payload.oauthFields} {
		token := strings.TrimSpace(candidate.AccessToken)
		if token == "" {
			continue
		}
		stored := storedOAuth{token: token, kind: KindOAuthToken}
		if candidate.ExpiresAt > 0 {
			stored.expiresAt = time.UnixMilli(int64(candidate.ExpiresAt)).UTC()
		}
		if candidate.RefreshTokenExpiresAt > 0 {
			stored.refreshExpiresAt = time.UnixMilli(int64(candidate.RefreshTokenExpiresAt)).UTC()
		}
		stored.renewable = strings.TrimSpace(candidate.RefreshToken) != ""
		return stored, true
	}
	return storedOAuth{}, false
}

func claudeConfigDir(opts ResolveOptions) (string, error) {
	dir := strings.TrimSpace(opts.ConfigDir)
	if dir == "" {
		dir = opts.env("CLAUDE_CONFIG_DIR")
	}
	if dir == "" {
		homeKey := "HOME"
		if opts.goos() == "windows" {
			homeKey = "USERPROFILE"
		}
		home := opts.env(homeKey)
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("agentcreds: resolve home directory: %w", err)
			}
		}
		dir = filepath.Join(home, ".claude")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(strings.TrimSpace(opts.WorkingDir), dir)
	}
	return filepath.Abs(dir)
}
