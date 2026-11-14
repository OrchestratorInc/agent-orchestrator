package deepseekharness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

const credentialsPathEnv = "DSH_CREDENTIALS_PATH"

const deepseekCredentialKey = "DEEPSEEK_API_KEY" //nolint:gosec // a credential's name, not a credential value

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if status, ok, err := deepseekLocalAuthStatus(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	} else if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func deepseekLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	status, ok, err := deepseekStoredAuthStatus(ctx)
	if err == nil && ok && status.HasCredential() {
		return status, true, nil
	}
	if authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, deepseekCredentialKey) != "" {
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return status, ok, err
}

func deepseekStoredAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	path, err := deepseekCredentialsPath()
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	status, ok, err := deepseekCredentialsStatus(path)
	if status == ports.AgentAuthStatusAuthorized {
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey))
	}
	return status, ok, err
}

func deepseekCredentialsPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(credentialsPathEnv)); override != "" {
		return override, nil
	}
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil || resolved == "" {
			return "", err
		}
		home = filepath.Join(resolved, ".dsh")
	}
	return filepath.Join(home, ".credentials.yaml"), nil
}

func deepseekCredentialsStatus(path string) (ports.AgentAuthStatus, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the Harness credential store, resolved above
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	var store any
	if err := yaml.Unmarshal(data, &store); err != nil {
		return ports.AgentAuthStatusUnknown, false, fmt.Errorf("parse DeepSeek Harness credential store: %w", err)
	}
	if !supportedStoreLayout(store) {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if credentialEntryHasSecret(store, deepseekCredentialKey) {
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

func supportedStoreLayout(store any) bool {
	root, ok := store.(map[string]any)
	if !ok {
		return false
	}
	switch version := root["version"].(type) {
	case int:
		return version == 1
	case float64:
		return version == 1
	case string:
		return strings.TrimSpace(version) == "1"
	default:
		return false
	}
}

func credentialEntryHasSecret(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for entryKey, child := range typed {
			if entryKey == key && secretValuePresent(child) {
				return true
			}
			if credentialEntryHasSecret(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if credentialEntryHasSecret(child, key) {
				return true
			}
		}
	}
	return false
}

func secretValuePresent(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case map[string]any:
		for entryKey, child := range typed {
			switch strings.ToLower(entryKey) {
			case "secret", "value", "key", "token", "payload":
				if secretValuePresent(child) {
					return true
				}
			}
		}
		return false
	case []any:
		for _, child := range typed {
			if secretValuePresent(child) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func CredentialsPath() (string, error) {
	path, err := deepseekCredentialsPath()
	if err == nil && path == "" {
		err = errors.New("DeepSeek Harness home is unavailable")
	}
	return path, err
}
