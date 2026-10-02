package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountReuseReader interface {
	GetAccountsManagerSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error)
	GetLatestAccountsManagerSwitch(context.Context, domain.SessionID) (domain.AccountsManagerSwitch, bool, error)
}

func (s *Service) reuseOrchestratorWithAccount(ctx context.Context, cfg ports.SpawnConfig, existing domain.Session) (domain.Session, int, int, error) {
	if cfg.Account == nil {
		return existing, 0, 0, nil
	}
	if !cfg.Account.Valid() {
		return domain.Session{}, 0, 0, toSpawnAPIError(domain.ErrAccountsManagerSelectionInvalid)
	}
	if cfg.Harness != "" && cfg.Harness != existing.Harness {
		return domain.Session{}, 0, 0, toSpawnAPIError(domain.ErrAccountsManagerBindingConflict)
	}
	var provider domain.AccountsManagerProvider
	switch existing.Harness {
	case domain.HarnessCodex:
		provider = domain.AccountsManagerProviderCodex
	case domain.HarnessClaudeCode:
		provider = domain.AccountsManagerProviderClaude
	default:
		return domain.Session{}, 0, 0, toSpawnAPIError(domain.ErrAccountsManagerSelectionInvalid)
	}
	reader, supported := s.store.(accountReuseReader)
	if !supported {
		return domain.Session{}, 0, 0, toSpawnAPIError(domain.ErrAccountsManagerSelectionUnavailable)
	}
	binding, found, err := reader.GetAccountsManagerSessionRoute(ctx, existing.ID, provider)
	if err != nil {
		return domain.Session{}, 0, 0, err
	}
	if !found || binding.SessionID != existing.ID || binding.Provider != provider || binding.Revision <= 0 ||
		binding.Blocked || binding.Mode != cfg.Account.Mode || binding.AccountID != cfg.Account.AccountID {
		return domain.Session{}, 0, 0, toSpawnAPIError(domain.ErrAccountsManagerBindingConflict)
	}
	op, found, err := reader.GetLatestAccountsManagerSwitch(ctx, existing.ID)
	if err != nil {
		return domain.Session{}, 0, 0, err
	}
	if found && (op.ID == "" || op.SessionID != existing.ID || op.Provider != provider || !op.Phase.Terminal()) {
		return domain.Session{}, 0, 0, toSpawnAPIError(domain.ErrAccountsManagerBindingConflict)
	}
	// A completed journal must not validate the pre-commit account snapshot.
	current, stillPresent, err := reader.GetAccountsManagerSessionRoute(ctx, existing.ID, provider)
	if err != nil {
		return domain.Session{}, 0, 0, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Session{}, 0, 0, err
	}
	if !stillPresent || current.SessionID != binding.SessionID || current.Provider != binding.Provider ||
		current.Revision != binding.Revision || current.Mode != binding.Mode || current.AccountID != binding.AccountID || current.Blocked != binding.Blocked {
		return domain.Session{}, 0, 0, toSpawnAPIError(domain.ErrAccountsManagerBindingConflict)
	}
	return existing, 0, 0, nil
}
