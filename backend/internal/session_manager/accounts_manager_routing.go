package sessionmanager

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const accountsManagerCodexTokenEnv = "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"

type managedChatPreflighter interface {
	PreflightManagedChat(context.Context, domain.AgentHarness, ports.PermissionMode) error
}

func (m *Manager) preflightManagedChat(ctx context.Context, harness domain.AgentHarness, permissions ports.PermissionMode) error {
	chat, ok := m.chat.(managedChatPreflighter)
	if !ok {
		return fmt.Errorf("%w: Accounts Manager Chat driver unavailable", ports.ErrChatUnsupported)
	}
	return chat.PreflightManagedChat(ctx, harness, permissions)
}

func (m *Manager) preflightSpawnChat(ctx context.Context, cfg ports.SpawnConfig, permissions ports.PermissionMode) error {
	if cfg.Harness == domain.HarnessCodex && cfg.Account != nil && cfg.Account.Mode == domain.AccountsManagerManaged {
		return m.preflightManagedChat(ctx, cfg.Harness, permissions)
	}
	return m.chat.PreflightChat(ctx, cfg.Harness, permissions)
}

func (m *Manager) preflightBoundChat(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, permissions ports.PermissionMode) error {
	if harness == domain.HarnessCodex && m.accountsManager != nil {
		pinned, err := m.accountsManager.HasAgentSessionRoute(ctx, id, domain.AccountsManagerProviderCodex)
		if err != nil {
			return err
		}
		if pinned {
			return m.preflightManagedChat(ctx, harness, permissions)
		}
	}
	return m.chat.PreflightChat(ctx, harness, permissions)
}

func accountsManagerProvider(harness domain.AgentHarness) (domain.AccountsManagerProvider, bool) {
	switch harness {
	case domain.HarnessCodex:
		return domain.AccountsManagerProviderCodex, true
	case domain.HarnessClaudeCode:
		return domain.AccountsManagerProviderClaude, true
	default:
		return "", false
	}
}

func (m *Manager) prepareAccountsManagerRoute(
	ctx context.Context,
	sessionID domain.SessionID,
	harness domain.AgentHarness,
	model string,
	env map[string]string,
) (*ports.AgentProviderRoute, error) {
	provider, supported := accountsManagerProvider(harness)
	if !supported {
		return nil, nil
	}
	if m.accountsManager == nil {
		return nil, m.requireNativeAccount(ctx, sessionID, provider)
	}
	route, err := m.accountsManager.PrepareAgentLaunchRoute(ctx, sessionID, provider, model)
	if err != nil {
		return nil, fmt.Errorf("prepare Accounts Manager route: %w", err)
	}
	if route == nil {
		return nil, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(route.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(route.Token) == "" {
		return nil, fmt.Errorf("prepare Accounts Manager route: incomplete route")
	}
	switch provider {
	case domain.AccountsManagerProviderCodex:
		env[accountsManagerCodexTokenEnv] = route.Token
		return &ports.AgentProviderRoute{BaseURL: baseURL, TokenEnv: accountsManagerCodexTokenEnv}, nil
	case domain.AccountsManagerProviderClaude:
		removeEnvCaseInsensitive(env, "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN")
		env["ANTHROPIC_BASE_URL"] = baseURL
		env["ANTHROPIC_AUTH_TOKEN"] = route.Token
		return &ports.AgentProviderRoute{BaseURL: baseURL, TokenEnv: "ANTHROPIC_AUTH_TOKEN"}, nil // #nosec G101 -- TokenEnv is a variable name, not a secret value.
	default:
		return nil, nil
	}
}

func removeEnvCaseInsensitive(env map[string]string, names ...string) {
	for key := range env {
		for _, name := range names {
			if strings.EqualFold(key, name) {
				delete(env, key)
				break
			}
		}
	}
}

func (m *Manager) accountsManagerRoutingEnabled(ctx context.Context, harness domain.AgentHarness) bool {
	provider, supported := accountsManagerProvider(harness)
	if !supported || m.accountsManager == nil {
		return false
	}
	enabled, err := m.accountsManager.AgentRoutingEnabled(ctx, provider)
	return err == nil && enabled
}

func (m *Manager) checkAccountsManagerChatMode(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) error {
	if harness != domain.HarnessCodex || m.accountsManager == nil {
		return nil
	}
	pinned, err := m.accountsManager.HasAgentSessionRoute(ctx, id, domain.AccountsManagerProviderCodex)
	if err != nil {
		return fmt.Errorf("read Accounts Manager session binding: %w", err)
	}
	if pinned {
		if _, ok := m.chat.(managedChatPreflighter); !ok {
			return fmt.Errorf("%w: Accounts Manager Chat driver unavailable", ports.ErrChatUnsupported)
		}
	}
	return nil
}
