package session

import (
	"context"
	"errors"

	accountcore "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type accountsManagerControlCommander interface {
	StartAccountsManagerSwitch(context.Context, domain.SessionID, sessionmanager.AccountsManagerSwitchConfig) (domain.AccountsManagerSwitch, error)
	RetryAccountsManagerSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error)
	CancelAccountsManagerSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error)
	StartAccountsManagerRemoval(context.Context, string, string, int64, bool) (domain.AccountsManagerRemoval, error)
	RetryAccountsManagerRemoval(context.Context, string) (domain.AccountsManagerRemoval, error)
	CancelAccountsManagerRemoval(context.Context, string) (domain.AccountsManagerRemoval, error)
}

// InitialAccountSelectionAvailable requires the production creation boundary.
func (s *Service) InitialAccountSelectionAvailable() bool {
	capability, ok := s.manager.(interface{ InitialAccountSelectionAvailable() bool })
	return ok && capability.InitialAccountSelectionAvailable()
}

type accountsManagerControlReader interface {
	GetAccountsManagerSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error)
	GetAccountsManagerSwitch(context.Context, string) (domain.AccountsManagerSwitch, bool, error)
	GetLatestAccountsManagerSwitch(context.Context, domain.SessionID) (domain.AccountsManagerSwitch, bool, error)
	AccountsManagerRemovalImpact(context.Context, string) (domain.AccountsManagerRemovalImpact, error)
	GetAccountsManagerRemoval(context.Context, string) (domain.AccountsManagerRemoval, bool, error)
}

func (s *Service) accountControlDeps() (accountsManagerControlCommander, accountsManagerControlReader, error) {
	manager, supported := s.manager.(accountsManagerControlCommander)
	reader, durable := s.store.(accountsManagerControlReader)
	if !supported || !durable {
		return nil, nil, apierr.NotImplemented("NOT_IMPLEMENTED", "Account controls are unavailable")
	}
	return manager, reader, nil
}

func accountControlError(err error) error {
	if errors.Is(err, accountcore.ErrUnavailable) {
		return accountcore.ErrUnavailable
	}
	return toAPIError(err)
}

func (s *Service) SessionAccount(ctx context.Context, id domain.SessionID) (domain.AccountsManagerSessionRoute, *domain.AccountsManagerSwitch, error) {
	_, reader, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, nil, err
	}
	record, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, nil, accountControlError(err)
	}
	if !found || record.IsTaskPreparation {
		return domain.AccountsManagerSessionRoute{}, nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	var provider domain.AccountsManagerProvider
	switch record.Harness {
	case domain.HarnessCodex:
		provider = domain.AccountsManagerProviderCodex
	case domain.HarnessClaudeCode:
		provider = domain.AccountsManagerProviderClaude
	default:
		return domain.AccountsManagerSessionRoute{}, nil, apierr.Conflict("ACCOUNT_CONTROL_UNSUPPORTED", "Session provider does not support managed accounts", nil)
	}
	binding, found, err := reader.GetAccountsManagerSessionRoute(ctx, id, provider)
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, nil, accountControlError(err)
	}
	if !found || binding.SessionID != id || binding.Provider != provider {
		return domain.AccountsManagerSessionRoute{}, nil, apierr.NotFound("ACCOUNT_BINDING_NOT_FOUND", "Session account binding was not found")
	}
	op, found, err := reader.GetLatestAccountsManagerSwitch(ctx, id)
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, nil, accountControlError(err)
	}
	current, stillPresent, err := reader.GetAccountsManagerSessionRoute(ctx, id, provider)
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, nil, accountControlError(err)
	}
	// A journal read must not pair a completed switch with its old committed binding.
	if !stillPresent || current.SessionID != binding.SessionID || current.Provider != binding.Provider ||
		current.Revision != binding.Revision || current.Mode != binding.Mode || current.AccountID != binding.AccountID || current.Blocked != binding.Blocked {
		return domain.AccountsManagerSessionRoute{}, nil, domain.ErrAccountsManagerBindingConflict
	}
	if found {
		if op.SessionID != id || op.Provider != provider {
			return domain.AccountsManagerSessionRoute{}, nil, domain.ErrAccountsManagerSwitchConflict
		}
		return binding, &op, nil
	}
	return binding, nil, nil
}

func (s *Service) StartAccountSwitch(ctx context.Context, input domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, error) {
	manager, _, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	op, err := manager.StartAccountsManagerSwitch(ctx, input.SessionID, sessionmanager.AccountsManagerSwitchConfig{
		OperationID: input.ID, ExpectedRevision: input.SourceRevision, Mode: input.TargetMode,
		AccountID: input.TargetAccountID, Policy: input.Policy, NewConversation: input.NewConversation,
	})
	return op, accountControlError(err)
}

func (s *Service) AccountSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	_, reader, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	op, found, err := reader.GetAccountsManagerSwitch(ctx, operationID)
	if err != nil {
		return domain.AccountsManagerSwitch{}, accountControlError(err)
	}
	if !found || op.ID != operationID || op.SessionID != id {
		return domain.AccountsManagerSwitch{}, apierr.NotFound("ACCOUNT_SWITCH_NOT_FOUND", "Account switch was not found")
	}
	return op, nil
}

func (s *Service) RetryAccountSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	if _, err := s.AccountSwitch(ctx, id, operationID); err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	manager, _, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	op, err := manager.RetryAccountsManagerSwitch(ctx, id, operationID)
	return op, accountControlError(err)
}

func (s *Service) CancelAccountSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	if _, err := s.AccountSwitch(ctx, id, operationID); err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	manager, _, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	op, err := manager.CancelAccountsManagerSwitch(ctx, id, operationID)
	return op, accountControlError(err)
}

func (s *Service) AccountRemovalImpact(ctx context.Context, accountID string) (domain.AccountsManagerRemovalImpact, error) {
	_, reader, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerRemovalImpact{}, err
	}
	impact, err := reader.AccountsManagerRemovalImpact(ctx, accountID)
	return impact, accountControlError(err)
}

func (s *Service) StartAccountRemoval(ctx context.Context, operationID, accountID string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, error) {
	manager, _, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerRemoval{}, err
	}
	op, err := manager.StartAccountsManagerRemoval(ctx, operationID, accountID, revision, confirmed)
	return op, accountControlError(err)
}

func (s *Service) AccountRemoval(ctx context.Context, operationID string) (domain.AccountsManagerRemoval, error) {
	_, reader, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerRemoval{}, err
	}
	op, found, err := reader.GetAccountsManagerRemoval(ctx, operationID)
	if err != nil {
		return domain.AccountsManagerRemoval{}, accountControlError(err)
	}
	if !found || op.ID != operationID {
		return domain.AccountsManagerRemoval{}, apierr.NotFound("ACCOUNT_REMOVAL_NOT_FOUND", "Account removal was not found")
	}
	return op, nil
}

func (s *Service) RetryAccountRemoval(ctx context.Context, operationID string) (domain.AccountsManagerRemoval, error) {
	if _, err := s.AccountRemoval(ctx, operationID); err != nil {
		return domain.AccountsManagerRemoval{}, err
	}
	manager, _, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerRemoval{}, err
	}
	op, err := manager.RetryAccountsManagerRemoval(ctx, operationID)
	return op, accountControlError(err)
}

func (s *Service) CancelAccountRemoval(ctx context.Context, operationID string) (domain.AccountsManagerRemoval, error) {
	if _, err := s.AccountRemoval(ctx, operationID); err != nil {
		return domain.AccountsManagerRemoval{}, err
	}
	manager, _, err := s.accountControlDeps()
	if err != nil {
		return domain.AccountsManagerRemoval{}, err
	}
	op, err := manager.CancelAccountsManagerRemoval(ctx, operationID)
	return op, accountControlError(err)
}
