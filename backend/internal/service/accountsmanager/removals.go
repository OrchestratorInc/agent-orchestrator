package accountsmanager

import (
	"context"
	"errors"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PrepareAccountRemoval serializes deletion intent against account choices.
func (s *Service) PrepareAccountRemoval(ctx context.Context, operationID, accountID string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, bool, error) {
	s.choiceMu.Lock()
	defer s.choiceMu.Unlock()
	store, ok := s.routingStore.(ports.AccountsManagerRemovalStore)
	if !ok {
		return domain.AccountsManagerRemoval{}, false, core.ErrUnavailable
	}
	if _, _, err := s.resolve(ctx, accountID); err != nil && !errors.Is(err, core.ErrCredentialNotFound) {
		return domain.AccountsManagerRemoval{}, false, err
	}
	return store.CreateAccountsManagerRemoval(ctx, operationID, accountID, revision, confirmed)
}

// FinalizeAccountRemoval requires all stop and revocation acknowledgements before credential deletion.
func (s *Service) FinalizeAccountRemoval(ctx context.Context, id string) error {
	s.choiceMu.Lock()
	defer s.choiceMu.Unlock()
	store, ok := s.routingStore.(ports.AccountsManagerRemovalStore)
	if !ok {
		return core.ErrUnavailable
	}
	op, found, err := store.GetAccountsManagerRemoval(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrAccountsManagerRemovalConflict
	}
	if op.Phase == domain.AccountsManagerRemovalComplete {
		return nil
	}
	if !op.StopStarted || !op.BindingsRevoked || op.Phase == domain.AccountsManagerRemovalCancelled {
		return domain.ErrAccountsManagerRemovalConflict
	}
	if err := store.ValidateAccountsManagerRemoval(ctx, op.ID); err != nil {
		return err
	}
	for _, session := range op.Impact.Sessions {
		if !session.Stopped {
			return domain.ErrAccountsManagerAccountInUse
		}
	}
	if op.Phase != domain.AccountsManagerRemovalRevoked {
		if err := s.synchronizeBindings(ctx); err != nil {
			return err
		}
		client, ref, err := s.resolve(ctx, op.AccountID)
		if err != nil && !errors.Is(err, core.ErrCredentialNotFound) {
			return err
		}
		if err == nil {
			if err := client.RemoveCredential(ctx, ref); err != nil {
				return err
			}
		}
		if err := store.RecordAccountsManagerRemovalRevoked(ctx, op.ID); err != nil {
			return err
		}
	}
	return store.CompleteAccountsManagerRemoval(ctx, op.ID)
}

func (s *Service) admitAccountMutation(ctx context.Context, id string) error {
	store, ok := s.routingStore.(ports.AccountsManagerRemovalStore)
	if !ok {
		return nil
	}
	deleting, err := store.AccountsManagerAccountDeleting(ctx, id)
	if err != nil {
		return err
	}
	if deleting {
		return domain.ErrAccountsManagerAccountDeleting
	}
	return nil
}
