package accountsmanager

import (
	"context"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ValidateAgentAccountTarget requires an explicit usable target without selecting a fallback.
func (s *Service) ValidateAgentAccountTarget(ctx context.Context, mode domain.AccountsManagerConnectionMode, provider domain.AccountsManagerProvider, id, model string) error {
	if !provider.Valid() {
		return core.ErrUnsupportedProvider
	}
	if mode == domain.AccountsManagerNative && id == "" {
		return nil
	}
	if mode != domain.AccountsManagerManaged || id == "" {
		return core.ErrInvalidCredential
	}
	if err := s.admitAccountMutation(ctx, id); err != nil {
		return err
	}
	snapshot, err := s.Refresh(ctx)
	if err != nil {
		return err
	}
	account, found := accountByID(snapshot.Accounts, id)
	if !found || account.Provider != core.Provider(provider) || !accountUsable(account, model, time.Now()) {
		return ErrRoutingAccountUnavailable
	}
	return nil
}

// AdmitAgentAccountSwitch serializes idempotent intent with credential mutations.
func (s *Service) AdmitAgentAccountSwitch(ctx context.Context, op domain.AccountsManagerSwitch, model string) (domain.AccountsManagerSwitch, bool, error) {
	s.choiceMu.Lock()
	defer s.choiceMu.Unlock()
	store, ok := s.routingStore.(ports.AccountsManagerSwitchStore)
	if !ok {
		return domain.AccountsManagerSwitch{}, false, core.ErrUnavailable
	}
	previous, found, err := store.GetAccountsManagerSwitch(ctx, op.ID)
	if err != nil {
		return domain.AccountsManagerSwitch{}, false, err
	}
	if found {
		if !previous.SameRequest(op) {
			return previous, false, domain.ErrAccountsManagerSwitchConflict
		}
		return previous, false, nil
	}
	if err := s.ValidateAgentAccountTarget(ctx, op.TargetMode, op.Provider, op.TargetAccountID, model); err != nil {
		return domain.AccountsManagerSwitch{}, false, err
	}
	return store.CreateAccountsManagerSwitch(ctx, op)
}

// CommitAgentAccountSwitch revalidates the target before changing its binding.
func (s *Service) CommitAgentAccountSwitch(ctx context.Context, id, model string) (domain.AccountsManagerSwitch, error) {
	s.choiceMu.Lock()
	defer s.choiceMu.Unlock()
	store, ok := s.routingStore.(ports.AccountsManagerSwitchStore)
	if !ok {
		return domain.AccountsManagerSwitch{}, core.ErrUnavailable
	}
	op, found, err := store.GetAccountsManagerSwitch(ctx, id)
	if err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	if !found {
		return domain.AccountsManagerSwitch{}, domain.ErrAccountsManagerSwitchConflict
	}
	if err := s.ValidateAgentAccountTarget(ctx, op.TargetMode, op.Provider, op.TargetAccountID, model); err != nil {
		return op, err
	}
	return store.CommitAccountsManagerSwitch(ctx, id)
}

// SynchronizeAgentBindings publishes durable admission fences to the runner.
func (s *Service) SynchronizeAgentBindings(ctx context.Context) error {
	return s.synchronizeBindings(ctx)
}

// AgentAccountSwitchPending includes unresolved recovery obligations.
func (s *Service) AgentAccountSwitchPending(ctx context.Context, id domain.SessionID) (bool, error) {
	store, ok := s.routingStore.(ports.AccountsManagerSwitchStore)
	if !ok {
		return false, core.ErrUnavailable
	}
	op, found, err := store.GetLatestAccountsManagerSwitch(ctx, id)
	return found && !op.Phase.Terminal(), err
}
