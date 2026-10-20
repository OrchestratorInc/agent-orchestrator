package freebuff

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus reports whether Freebuff has a signed-in account. `freebuff
// login` writes the "default" profile of credentials.json in Freebuff's config
// directory, holding either the token itself or a pointer to the OS keychain;
// CODEBUFF_API_KEY is Freebuff's environment fallback.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return localAuthStatus(ctx, configDir(nil))
}

func localAuthStatus(ctx context.Context, dir string) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if dir != "" {
		status, err := credentialsAuthStatus(ctx, filepath.Join(dir, "credentials.json"))
		if err != nil {
			return ports.AgentAuthStatusUnknown, err
		}
		if status == ports.AgentAuthStatusAuthorized {
			return status, nil
		}
	}
	if authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, "CODEBUFF_API_KEY") != "" {
		return ports.AgentAuthStatusAuthorized, nil
	}
	return ports.AgentAuthStatusUnauthorized, nil
}

func credentialsAuthStatus(ctx context.Context, path string) (ports.AgentAuthStatus, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	profile, ok := defaultProfile(data)
	if !ok || (strings.TrimSpace(profile.AuthToken) == "" && profile.TokenStore != "keychain") {
		// Freebuff treats a missing, unparsable or tokenless profile as signed out.
		return ports.AgentAuthStatusUnauthorized, nil
	}
	evidence := authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAccount)
	evidence.Email = profile.Email
	evidence.Name = strings.TrimSpace(profile.Name)
	ports.RecordAuthEvidence(ctx, evidence)
	return ports.AgentAuthStatusAuthorized, nil
}

type credentialsProfile struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	AuthToken  string `json:"authToken"`
	TokenStore string `json:"tokenStore"`
}

// defaultProfile reads the "default" profile `freebuff login` writes.
func defaultProfile(data []byte) (credentialsProfile, bool) {
	var profiles map[string]json.RawMessage
	var profile credentialsProfile
	if json.Unmarshal(data, &profiles) != nil {
		return profile, false
	}
	raw, ok := profiles["default"]
	if !ok || json.Unmarshal(raw, &profile) != nil {
		return profile, false
	}
	return profile, true
}
