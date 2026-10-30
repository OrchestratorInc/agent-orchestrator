package muse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus reports whether the official Meta provider has a local API key or
// OAuth login. Credential values are only tested for non-emptiness and are
// never returned or logged.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if status, ok, err := museLocalAuthStatus(ctx); err != nil || ok {
		return status, err
	}
	return ports.AgentAuthStatusUnknown, nil
}

const museAPIKeyEnvVar = "META_API_KEY" //nolint:gosec // environment variable name, not a credential

// museLocalAuthStatus checks Muse's own auth.json first; META_API_KEY in the
// environment is the fallback when it holds no usable credential.
func museLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	status, ok, err := museStoredAuthStatus(ctx)
	if err == nil && ok && status.HasCredential() {
		return status, true, nil
	}
	if authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, museAPIKeyEnvVar) != "" {
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return status, ok, err
}

func museStoredAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	path, ok := museAuthPath()
	if !ok {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	status, identity, ok, err := museAuthJSON(path)
	if status == ports.AgentAuthStatusAuthorized {
		ports.RecordAuthEvidence(ctx, identity.Merge(authprobe.FileSource(path)))
	}
	return status, ok, err
}

// museAuthPath mirrors the official launcher: MUSE_AUTH_PATH wins, then the
// XDG config root, then ~/.config/muse/auth.json.
func museAuthPath() (string, bool) {
	if path := strings.TrimSpace(os.Getenv("MUSE_AUTH_PATH")); path != "" {
		return path, true
	}
	if root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); root != "" {
		return filepath.Join(root, "muse", "auth.json"), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".config", "muse", "auth.json"), true
}

func museAuthJSONStatus(path string) (ports.AgentAuthStatus, bool, error) {
	status, _, ok, err := museAuthJSON(path)
	return status, ok, err
}

// museAuthJSON also reports how the stored credential signs in and, for a
// login, who signed in.
func museAuthJSON(path string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // user-selected Muse config path
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, false, err
	}

	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, false, nil
	}
	var auth struct {
		Providers struct {
			Meta struct {
				Mechanism    string `json:"mechanism"`
				AccessToken  string `json:"access_token"`
				APIKey       string `json:"api_key"`
				UserEmail    string `json:"user_email"`
				UserFullName string `json:"user_full_name"`
			} `json:"meta"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, false, err
	}
	meta := auth.Providers.Meta
	switch strings.ToLower(strings.TrimSpace(meta.Mechanism)) {
	case "oauth":
		if strings.TrimSpace(meta.AccessToken) != "" {
			return ports.AgentAuthStatusAuthorized, domain.AgentAccountIdentity{Email: meta.UserEmail, Name: meta.UserFullName, Method: domain.AgentAccountMethodAccount}, true, nil
		}
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, false, nil
	case "api_key", "api-key", "apikey":
		if strings.TrimSpace(meta.APIKey) != "" {
			return ports.AgentAuthStatusAuthorized, domain.AgentAccountIdentity{Method: domain.AgentAccountMethodAPIKey}, true, nil
		}
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, false, nil
	}
	return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, false, nil
}
