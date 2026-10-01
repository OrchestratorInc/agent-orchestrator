package chat

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const managedCodexTokenEnv = "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"

type managedDriverRegistry interface {
	ManagedDriver(domain.AgentHarness) (ports.ChatDriver, error)
}

func (s *Service) managedDriver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	registry, ok := s.drivers.(managedDriverRegistry)
	if !ok {
		return nil, ports.ErrChatUnsupported
	}
	return registry.ManagedDriver(harness)
}

// PreflightManagedChat validates the managed driver without consulting device login.
func (s *Service) PreflightManagedChat(ctx context.Context, harness domain.AgentHarness, permissions ports.PermissionMode) error {
	driver, err := s.managedDriver(harness)
	if err != nil {
		return err
	}
	caps, err := s.driverCapabilities(ctx, harness, driver, true)
	if err != nil {
		return err
	}
	return capabilityAdmissionError(harness, caps, permissions)
}

func (s *Service) prepareManagedCodex(ctx context.Context, cfg StartConfig, env map[string]string) (map[string]string, *ports.AgentProviderRoute, error) {
	prepared, err := s.accountsManager.PrepareAgentLaunchRoute(ctx, cfg.SessionID, domain.AccountsManagerProviderCodex, cfg.Model)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare managed Codex route: %w", err)
	}
	if prepared == nil || prepared.BindingRevision <= 0 || strings.TrimSpace(prepared.Token) == "" || strings.TrimSpace(prepared.BaseURL) == "" {
		return nil, nil, domain.ErrAccountsManagerBindingConflict
	}
	env = maps.Clone(env)
	if env == nil {
		env = make(map[string]string)
	}
	env[managedCodexTokenEnv] = prepared.Token
	return env, &ports.AgentProviderRoute{BaseURL: strings.TrimRight(strings.TrimSpace(prepared.BaseURL), "/"), TokenEnv: managedCodexTokenEnv,
		BindingRevision: prepared.BindingRevision}, nil
}

func (s *Service) stopManagedCodexHost(ctx context.Context, id domain.SessionID, controller *Controller) (bool, error) {
	if s.accountsManager == nil || s.sessions == nil {
		return false, nil
	}
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return true, err
	}
	if !found || record.Harness != domain.HarnessCodex {
		return false, nil
	}
	pinned, err := s.accountsManager.HasAgentSessionRoute(ctx, id, domain.AccountsManagerProviderCodex)
	if err != nil || !pinned {
		return err != nil, err
	}
	store, ok := s.store.(ports.AccountsManagerChatHostStore)
	if !ok || s.stopBoundProviderHost == nil {
		return true, ports.ErrChatRecoveryInconclusive
	}
	generation := record.Metadata.ControllerGeneration
	if controller != nil {
		generation = controller.generation
	}
	identity, found, err := store.GetAccountsManagerChatHost(ctx, id, generation)
	if err != nil {
		return true, err
	}
	if !found || identity == "" {
		return true, ports.ErrChatRecoveryInconclusive
	}
	return true, s.stopBoundProviderHost(ctx, id, identity)
}
