package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type initialAccountBindingReader interface {
	GetAccountsManagerSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error)
}

func (m *Manager) requireNativeAccount(ctx context.Context, id domain.SessionID, provider domain.AccountsManagerProvider) error {
	if reader, ok := m.store.(initialAccountBindingReader); ok {
		binding, found, err := reader.GetAccountsManagerSessionRoute(ctx, id, provider)
		if err != nil {
			return fmt.Errorf("read session account: %w", err)
		}
		if found {
			if binding.SessionID != id || binding.Provider != provider || binding.Blocked || binding.Mode != domain.AccountsManagerNative {
				return domain.ErrAccountsManagerSelectionUnavailable
			}
			return nil
		}
	}
	if m.accountsManager != nil {
		pinned, err := m.accountsManager.HasAgentSessionRoute(ctx, id, provider)
		if err != nil {
			return err
		}
		enabled, err := m.accountsManager.AgentRoutingEnabled(ctx, provider)
		if err != nil {
			return err
		}
		if pinned || enabled {
			return domain.ErrAccountsManagerSelectionUnavailable
		}
	}
	return nil
}

// InitialAccountSelectionAvailable requires both validation and durable creation.
func (m *Manager) InitialAccountSelectionAvailable() bool {
	_, durable := m.store.(ports.AccountsManagerSessionCreator)
	_, validated := m.accountsManager.(ports.AccountsManagerAccountValidator)
	return durable && validated
}

func (m *Manager) validateInitialAccountChoice(ctx context.Context, cfg ports.SpawnConfig, model string) error {
	if cfg.Account == nil {
		return nil
	}
	provider, supported := accountsManagerProvider(cfg.Harness)
	if !supported || !cfg.Account.Valid() {
		return domain.ErrAccountsManagerSelectionInvalid
	}
	if _, ok := m.store.(ports.AccountsManagerSessionCreator); !ok {
		return domain.ErrAccountsManagerSelectionUnavailable
	}
	if cfg.Account.Mode == domain.AccountsManagerNative {
		return nil
	}
	validator, ok := m.accountsManager.(ports.AccountsManagerAccountValidator)
	if !ok {
		return domain.ErrAccountsManagerSelectionUnavailable
	}
	return validator.ValidateAgentAccountTarget(ctx, cfg.Account.Mode, provider, cfg.Account.AccountID, model)
}

func (m *Manager) createSpawnSeed(ctx context.Context, seed domain.SessionRecord, choice *domain.AccountsManagerAccountChoice) (domain.SessionRecord, bool, error) {
	if choice != nil {
		creator, ok := m.store.(ports.AccountsManagerSessionCreator)
		if !ok {
			return domain.SessionRecord{}, false, domain.ErrAccountsManagerSelectionUnavailable
		}
		return creator.CreateSessionWithAccount(ctx, seed, *choice)
	}
	if seed.AutomationRunID != nil {
		return m.store.CreateAutomationSession(ctx, seed)
	}
	record, err := m.store.CreateSession(ctx, seed)
	return record, err == nil, err
}

func (m *Manager) accountsManagerSpawnRoutingEnabled(ctx context.Context, cfg ports.SpawnConfig) bool {
	if cfg.Account != nil {
		return cfg.Account.Mode == domain.AccountsManagerManaged
	}
	return m.accountsManagerRoutingEnabled(ctx, cfg.Harness)
}
