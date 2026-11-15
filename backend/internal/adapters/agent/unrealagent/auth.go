package unrealagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	return p.authStatus(ctx, os.Getenv)
}

func (p *Plugin) AuthStatusWithEnv(ctx context.Context, env map[string]string) (ports.AgentAuthStatus, error) {
	getenv := func(name string) string {
		if value, ok := env[name]; ok {
			return value
		}
		return os.Getenv(name)
	}
	return p.authStatus(ctx, getenv)
}

func (p *Plugin) authStatus(ctx context.Context, getenv func(string) string) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	provider := strings.TrimSpace(getenv("UNREAL_HARNESS_LLM_PROVIDER"))
	if provider == "" {
		provider = "openai"
	}
	keyStatus := func(providerKey string) ports.AgentAuthStatus {
		for _, name := range []string{"UNREAL_HARNESS_LLM_API_KEY", providerKey} {
			if envSet(getenv, name) {
				ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey))
				return credentialStatus(true)
			}
		}
		return credentialStatus(false)
	}
	switch provider {
	case "ollama":
		ports.RecordAuthEvidence(ctx, domain.EnvironmentSource("UNREAL_HARNESS_LLM_PROVIDER"))
		return ports.AgentAuthStatusAuthorized, nil
	case "openai":
		return keyStatus("OPENAI_API_KEY"), nil
	case "openrouter":
		return keyStatus("OPENROUTER_API_KEY"), nil
	case "fireworks":
		return keyStatus("FIREWORKS_API_KEY"), nil
	case "openai-codex":
		if path, ok := codexAuthFile(getenv); ok {
			evidence := authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAccount)
			evidence.Email, evidence.Name = authprobe.CodexAuthFileIdentity(ctx, path)
			ports.RecordAuthEvidence(ctx, evidence)
			return credentialStatus(true), nil
		}
		if envSet(getenv, "OPENAI_CODEX_ACCESS_TOKEN") {
			ports.RecordAuthEvidence(ctx, domain.EnvironmentSource("OPENAI_CODEX_ACCESS_TOKEN").WithMethod(domain.AgentAccountMethodToken))
			return credentialStatus(true), nil
		}
		return credentialStatus(false), nil
	default:
		return ports.AgentAuthStatusUnknown, nil
	}
}

func credentialStatus(present bool) ports.AgentAuthStatus {
	if present {
		return ports.AgentAuthStatusConfigured
	}
	return ports.AgentAuthStatusUnauthorized
}

func envSet(getenv func(string) string, name string) bool {
	return strings.TrimSpace(getenv(name)) != ""
}

func codexAuthFile(getenv func(string) string) (string, bool) {
	path := strings.TrimSpace(getenv("OPENAI_CODEX_AUTH_FILE"))
	if path == "" {
		home := strings.TrimSpace(getenv("CODEX_HOME"))
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil || home == "" {
				return "", false
			}
			home = filepath.Join(home, ".codex")
		}
		path = filepath.Join(home, "auth.json")
	}
	info, err := os.Stat(path)
	return path, err == nil && info.Mode().IsRegular() && info.Size() > 0
}
