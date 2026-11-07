// Package coderapi is AO's thin client for the Coder Agents REST and
// WebSocket API (`/api/v2/chats`). It reads the session the native `coder`
// CLI already holds and never stores or logs it.
package coderapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Environment variables the native coder CLI reads. AO honors the same names
// so a deployment selected for the CLI is the one AO talks to.
const (
	EnvURL          = "CODER_URL"
	EnvSessionToken = "CODER_SESSION_TOKEN"
	EnvConfigDir    = "CODER_CONFIG_DIR"
	// EnvOrganization selects the organization (UUID or name) whose models and
	// chats AO uses when the signed-in user belongs to more than one.
	EnvOrganization = "CODER_AGENTS_ORGANIZATION"
)

// ErrNotLoggedIn means no deployment URL or no session token was found.
var ErrNotLoggedIn = errors.New("coder is not logged in; run `coder login <deployment-url>`")

// Credentials is the deployment and session AO acts with. Token is a secret:
// it must never be logged, persisted by AO, or passed to a child process.
type Credentials struct {
	URL   *url.URL
	Token string
	// Source names where the token came from, for auth evidence. It never
	// contains the token itself.
	Source domain.AgentAccountIdentity
}

// TokenCommand runs `coder login token` and returns its stdout. It exists so
// the token is read through the native CLI from whichever store `coder login`
// chose (config file or OS keyring) instead of AO reading that store itself.
type TokenCommand func(ctx context.Context, env map[string]string) ([]byte, error)

// ResolveCredentials finds the deployment URL and session token the way the
// native CLI does: CODER_URL / CODER_SESSION_TOKEN first, then the CLI's own
// configuration. getenv sees project-scoped overrides before the daemon env.
func ResolveCredentials(ctx context.Context, getenv func(string) string, token TokenCommand) (Credentials, error) {
	rawURL := strings.TrimSpace(getenv(EnvURL))
	urlFromEnv := rawURL != ""
	if rawURL == "" {
		data, err := os.ReadFile(filepath.Join(ConfigDir(getenv), "url"))
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, ErrNotLoggedIn
		}
		if err != nil {
			return Credentials{}, fmt.Errorf("read coder deployment url: %w", err)
		}
		rawURL = strings.TrimSpace(string(data))
	}
	if rawURL == "" {
		return Credentials{}, ErrNotLoggedIn
	}
	base, err := ParseDeploymentURL(rawURL)
	if err != nil {
		return Credentials{}, err
	}
	if value := strings.TrimSpace(getenv(EnvSessionToken)); value != "" {
		return Credentials{
			URL: base, Token: value,
			Source: domain.EnvironmentSource(EnvSessionToken).WithMethod(domain.AgentAccountMethodToken),
		}, nil
	}
	if token == nil {
		return Credentials{}, ErrNotLoggedIn
	}
	env := map[string]string{}
	if urlFromEnv {
		env[EnvURL] = base.String()
	}
	out, err := token(ctx, env)
	if err != nil {
		if ctx.Err() != nil {
			return Credentials{}, ctx.Err()
		}
		// The CLI exits non-zero with "no session token found" when it is not
		// logged in. Any other failure is reported as inconclusive by callers.
		if strings.Contains(strings.ToLower(string(out)), "no session token") {
			return Credentials{}, ErrNotLoggedIn
		}
		return Credentials{}, fmt.Errorf("coder login token: %w", err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return Credentials{}, ErrNotLoggedIn
	}
	return Credentials{
		URL: base, Token: value,
		Source: domain.CommandSource("coder", "login", "token").WithMethod(domain.AgentAccountMethodToken),
	}, nil
}

// ParseDeploymentURL accepts only an absolute http(s) URL without credentials,
// query or fragment, so the base can be joined with API paths safely.
func ParseDeploymentURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse coder deployment url: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("coder deployment url must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("coder deployment url must not carry credentials, a query or a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed, nil
}

// ConfigDir is the native CLI's global config directory: CODER_CONFIG_DIR, or
// the platform config dir joined with "coderv2" (kirsle/configdir semantics).
func ConfigDir(getenv func(string) string) string {
	if dir := strings.TrimSpace(getenv(EnvConfigDir)); dir != "" {
		return dir
	}
	return filepath.Join(platformConfigRoot(runtime.GOOS, getenv), "coderv2")
}

func platformConfigRoot(goos string, getenv func(string) string) string {
	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	switch goos {
	case "windows":
		if dir := strings.TrimSpace(getenv("APPDATA")); dir != "" {
			return dir
		}
		return filepath.Join(home, "AppData", "Roaming")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	default:
		if dir := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); dir != "" {
			return dir
		}
		return filepath.Join(home, ".config")
	}
}
