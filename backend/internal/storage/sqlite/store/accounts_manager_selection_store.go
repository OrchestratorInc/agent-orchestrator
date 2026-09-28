package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ ports.AccountsManagerSessionCreator = (*Store)(nil)

// CreateSessionWithAccount prevents an unbound session from becoming visible on restart.
func (s *Store) CreateSessionWithAccount(ctx context.Context, seed domain.SessionRecord, choice domain.AccountsManagerAccountChoice) (domain.SessionRecord, bool, error) {
	provider, err := initialAccountProvider(seed, choice)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var created domain.SessionRecord
	var fresh bool
	err = s.inTx(ctx, "create session with account", func(q *gen.Queries) error {
		if err := admitAccountSelection(ctx, q, choice.AccountID); err != nil {
			return err
		}
		var err error
		created, fresh, err = createSessionWithQueries(ctx, q, seed)
		if err != nil {
			return err
		}
		if !fresh {
			binding, err := s.getAccountsManagerSessionRoute(ctx, q, created.ID, provider)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err != nil || created.Harness != seed.Harness || created.Mode != domain.NormalizeSessionMode(seed.Mode) || binding.Blocked || binding.Mode != choice.Mode || binding.AccountID != choice.AccountID {
				return domain.ErrAccountsManagerBindingConflict
			}
			return nil
		}
		return insertInitialAccountBinding(ctx, q, created.ID, provider, choice)
	})
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	return created, fresh, nil
}

// PromoteTaskPreparationWithAccount keeps a failed choice hidden and unlaunchable.
func (s *Store) PromoteTaskPreparationWithAccount(ctx context.Context, id domain.SessionID, seed domain.SessionRecord, choice domain.AccountsManagerAccountChoice) (bool, error) {
	provider, err := initialAccountProvider(seed, choice)
	if err != nil {
		return false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "promote preparation with account", func(q *gen.Queries) error {
		if err := admitAccountSelection(ctx, q, choice.AccountID); err != nil {
			return err
		}
		promoted, err := promoteTaskPreparationWithQueries(ctx, q, id, seed)
		if err != nil {
			return err
		}
		if !promoted {
			return domain.ErrAccountsManagerBindingConflict
		}
		return insertInitialAccountBinding(ctx, q, id, provider, choice)
	})
	return err == nil, err
}

func initialAccountProvider(seed domain.SessionRecord, choice domain.AccountsManagerAccountChoice) (domain.AccountsManagerProvider, error) {
	var provider domain.AccountsManagerProvider
	switch seed.Harness {
	case domain.HarnessCodex:
		provider = domain.AccountsManagerProviderCodex
	case domain.HarnessClaudeCode:
		provider = domain.AccountsManagerProviderClaude
	}
	if !provider.Valid() || !choice.Valid() {
		return "", domain.ErrAccountsManagerSelectionInvalid
	}
	return provider, nil
}

func insertInitialAccountBinding(ctx context.Context, q *gen.Queries, id domain.SessionID, provider domain.AccountsManagerProvider, choice domain.AccountsManagerAccountChoice) error {
	now := time.Now().UTC()
	rows, err := q.InsertAccountsManagerSessionRoute(ctx, gen.InsertAccountsManagerSessionRouteParams{
		SessionID: string(id), Provider: string(provider), ConnectionMode: string(choice.Mode), AccountID: choice.AccountID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return domain.ErrAccountsManagerBindingConflict
	}
	return nil
}
