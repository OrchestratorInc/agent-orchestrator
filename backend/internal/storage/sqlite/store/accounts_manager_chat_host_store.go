package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// RecordAccountsManagerChatHost forbids replacing ownership proof within a generation.
func (s *Store) RecordAccountsManagerChatHost(ctx context.Context, id domain.SessionID, provider domain.AccountsManagerProvider, generation, identity string) error {
	decoded, err := hex.DecodeString(identity)
	if err != nil || len(decoded) != 32 || !validSwitchAtom(generation, 128) || !provider.Valid() {
		return domain.ErrAccountsManagerBindingConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "record managed provider host identity", func(q *gen.Queries) error {
		binding, err := s.getAccountsManagerSessionRoute(ctx, q, id, provider)
		if err != nil {
			return err
		}
		if binding.Mode != domain.AccountsManagerManaged || binding.Blocked {
			return domain.ErrAccountsManagerBindingConflict
		}
		if err := q.InsertAccountsManagerChatHost(ctx, gen.InsertAccountsManagerChatHostParams{SessionID: string(id), Generation: generation, HostIdentity: identity}); err != nil {
			return err
		}
		stored, err := q.GetAccountsManagerChatHost(ctx, gen.GetAccountsManagerChatHostParams{SessionID: string(id), Generation: generation})
		if err != nil {
			return err
		}
		if stored != identity {
			return domain.ErrAccountsManagerBindingConflict
		}
		return nil
	})
}

// GetAccountsManagerChatHost requires a generation rather than inferring ownership from a label.
func (s *Store) GetAccountsManagerChatHost(ctx context.Context, id domain.SessionID, generation string) (string, bool, error) {
	identity, err := s.qr.GetAccountsManagerChatHost(ctx, gen.GetAccountsManagerChatHostParams{SessionID: string(id), Generation: generation})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return identity, err == nil, err
}
