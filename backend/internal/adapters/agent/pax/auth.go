package pax

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// providerKeyEnv are the provider API keys Pydantic AI models read from the
// environment when no saved key or sign-in is used.
var providerKeyEnv = []string{
	"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY", "GROQ_API_KEY", "MISTRAL_API_KEY",
	"GITHUB_COPILOT_API_KEY", "GITHUB_COPILOT_API_TOKEN", "COPILOT_GITHUB_TOKEN",
}

// AuthStatus reports configured-only evidence: Pax has no command that
// checks a sign-in or key with its provider. Saved sign-ins and keys live in
// credentials-*.enc (or credentials-*.json without an OS keyring) in Pax's
// folder; AO only checks that such a file exists and never reads it.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	for _, name := range providerKeyEnv {
		value, overridden := ports.AuthEnvironmentOverride(ctx, name)
		if !overridden {
			value = os.Getenv(name)
		}
		if strings.TrimSpace(value) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	dir, err := configDir(ports.AuthEnvironmentOverrides(ctx))
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if savedCredentials(dir) {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func savedCredentials(dir string) bool {
	for _, pattern := range []string{"credentials-*.enc", "credentials-*.json"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			continue
		}
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				return true
			}
		}
	}
	return false
}
