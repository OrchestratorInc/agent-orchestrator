package kimchi

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

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}

	if _, err := p.ResolveBinary(ctx); err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, err
	}

	if strings.TrimSpace(os.Getenv("KIMCHI_API_KEY")) != "" {
		authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, "KIMCHI_API_KEY")
		return ports.AgentAuthStatusAuthorized, nil
	}
	status, configErr := kimchiConfigAuthStatus(ctx, kimchiGlobalConfigPath())
	if configErr == nil && status.HasCredential() {
		return status, nil
	}
	authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, "KIMCHI_API_KEY")
	return status, configErr
}

func kimchiGlobalConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "kimchi", "config.json")
}

func kimchiConfigAuthStatus(ctx context.Context, configPath string) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if configPath == "" {
		return ports.AgentAuthStatusUnknown, nil
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // path is user's own kimchi config
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, err
	}
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return ports.AgentAuthStatusUnknown, nil //nolint:nilerr // malformed config → can't determine auth status
	}

	if key := kimchiExtractAPIKey(config); key != "" {
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(configPath).WithMethod(domain.AgentAccountMethodAPIKey))
		return ports.AgentAuthStatusAuthorized, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func kimchiExtractAPIKey(config map[string]json.RawMessage) string {
	for _, field := range []string{"apiKey", "api_key"} {
		raw, ok := config[field]
		if !ok {
			continue
		}
		var key string
		if err := json.Unmarshal(raw, &key); err != nil {
			continue
		}
		if strings.TrimSpace(key) != "" {
			return key
		}
	}
	return ""
}
