package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AccountsManagerRemovalImpact reads matched bindings and revision in one transaction.
func (s *Store) AccountsManagerRemovalImpact(ctx context.Context, id string) (domain.AccountsManagerRemovalImpact, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var impact domain.AccountsManagerRemovalImpact
	err := s.inTx(ctx, "read account removal impact", func(q *gen.Queries) error {
		var err error
		impact, err = removalImpact(ctx, q, id)
		return err
	})
	return impact, err
}

func removalImpact(ctx context.Context, q *gen.Queries, id string) (domain.AccountsManagerRemovalImpact, error) {
	impact := domain.AccountsManagerRemovalImpact{Sessions: []domain.AccountsManagerRemovalSession{}}
	var err error
	impact.Revision, err = q.GetAccountsManagerBindingRevision(ctx)
	if err != nil {
		return impact, err
	}
	bindings, err := q.ListAccountsManagerSessionBindings(ctx)
	if err != nil {
		return impact, err
	}
	for _, binding := range bindings {
		if binding.ConnectionMode != string(domain.AccountsManagerManaged) || binding.AccountID != id {
			continue
		}
		row, err := q.GetSession(ctx, domain.SessionID(binding.SessionID))
		if err != nil {
			return impact, err
		}
		rec := getSessionRowToRecord(row)
		impact.Sessions = append(impact.Sessions, domain.AccountsManagerRemovalSession{SessionID: rec.ID, Provider: domain.AccountsManagerProvider(binding.Provider), BindingRevision: binding.Revision, Owner: rec.ControllerOwner(), RuntimeHandleID: rec.Metadata.RuntimeHandleID})
	}
	return impact, nil
}

// CreateAccountsManagerRemoval admits confirmed intent without recapturing an existing operation.
func (s *Store) CreateAccountsManagerRemoval(ctx context.Context, operationID, accountID string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, bool, error) {
	if !validSwitchAtom(operationID, 128) || !validSwitchAtom(accountID, 512) {
		return domain.AccountsManagerRemoval{}, false, domain.ErrAccountsManagerRemovalConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerRemoval
	created := false
	err := s.inTx(ctx, "create account removal", func(q *gen.Queries) error {
		if row, err := q.GetAccountsManagerRemoval(ctx, operationID); err == nil {
			if row.AccountID != accountID {
				return domain.ErrAccountsManagerRemovalConflict
			}
			result, err = removalFromRow(row)
			return err
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if row, err := q.GetAccountsManagerAccountRemoval(ctx, accountID); err == nil {
			if row.Phase != string(domain.AccountsManagerRemovalComplete) {
				return domain.ErrAccountsManagerRemovalConflict
			}
			result, err = removalFromRow(row)
			return err
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		impact, err := removalImpact(ctx, q, accountID)
		if err != nil {
			return err
		}
		if len(impact.Sessions) > 0 && !confirmed {
			return domain.ErrAccountsManagerAccountInUse
		}
		if confirmed && impact.Revision != revision {
			return domain.ErrAccountsManagerRemovalConflict
		}
		switches, err := q.ListActiveAccountsManagerSwitches(ctx)
		if err != nil {
			return err
		}
		for _, op := range switches {
			if op.SourceAccountID == accountID || op.TargetAccountID == accountID {
				return domain.ErrAccountsManagerSwitchConflict
			}
		}
		encoded, err := json.Marshal(impact)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := q.InsertAccountsManagerRemoval(ctx, gen.InsertAccountsManagerRemovalParams{ID: operationID, AccountID: accountID, Impact: string(encoded), CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		result = domain.AccountsManagerRemoval{ID: operationID, AccountID: accountID, Impact: impact, Phase: domain.AccountsManagerRemovalRequested, CreatedAt: now, UpdatedAt: now}
		created = true
		return nil
	})
	return result, created && err == nil, err
}

func removalFromRow(row gen.AccountsManagerRemoval) (domain.AccountsManagerRemoval, error) {
	op := domain.AccountsManagerRemoval{ID: row.ID, AccountID: row.AccountID, Phase: domain.AccountsManagerRemovalPhase(row.Phase), ErrorCode: row.ErrorCode, StopStarted: row.StopStarted != 0, BindingsRevoked: row.BindingsRevoked != 0, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if err := json.Unmarshal([]byte(row.Impact), &op.Impact); err != nil {
		return op, fmt.Errorf("decode account removal impact: %w", err)
	}
	return op, nil
}

// GetAccountsManagerRemoval distinguishes absence from unreadable durable state.
func (s *Store) GetAccountsManagerRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, bool, error) {
	row, err := s.qr.GetAccountsManagerRemoval(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountsManagerRemoval{}, false, nil
	}
	if err != nil {
		return domain.AccountsManagerRemoval{}, false, err
	}
	op, err := removalFromRow(row)
	return op, err == nil, err
}

// GetAccountsManagerAccountRemoval preserves the account tombstone after completion.
func (s *Store) GetAccountsManagerAccountRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, bool, error) {
	row, err := s.qr.GetAccountsManagerAccountRemoval(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountsManagerRemoval{}, false, nil
	}
	if err != nil {
		return domain.AccountsManagerRemoval{}, false, err
	}
	op, err := removalFromRow(row)
	return op, err == nil, err
}

// ListActiveAccountsManagerRemovals includes outstanding restart obligations.
func (s *Store) ListActiveAccountsManagerRemovals(ctx context.Context) ([]domain.AccountsManagerRemoval, error) {
	rows, err := s.qr.ListActiveAccountsManagerRemovals(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.AccountsManagerRemoval, 0, len(rows))
	for _, row := range rows {
		op, err := removalFromRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, nil
}

func (s *Store) mutateAccountRemoval(ctx context.Context, id string, mutate func(*gen.Queries, *domain.AccountsManagerRemoval) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "update account removal", func(q *gen.Queries) error {
		row, err := q.GetAccountsManagerRemoval(ctx, id)
		if err != nil {
			return err
		}
		op, err := removalFromRow(row)
		if err != nil {
			return err
		}
		terminal := op.Phase.Terminal()
		if err := mutate(q, &op); err != nil {
			return err
		}
		if terminal {
			return nil
		}
		encoded, err := json.Marshal(op.Impact)
		if err != nil {
			return err
		}
		var started, revoked int64
		if op.StopStarted {
			started = 1
		}
		if op.BindingsRevoked {
			revoked = 1
		}
		updated, err := q.UpdateAccountsManagerRemoval(ctx, gen.UpdateAccountsManagerRemovalParams{ID: id, Impact: string(encoded), Phase: string(op.Phase), ErrorCode: op.ErrorCode, StopStarted: started, BindingsRevoked: revoked, UpdatedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if updated != 1 {
			return domain.ErrAccountsManagerRemovalConflict
		}
		return nil
	})
}

// RecordAccountsManagerRemovalStopped requires the captured bindings and owner to match.
func (s *Store) RecordAccountsManagerRemovalStopped(ctx context.Context, id string, sessionID domain.SessionID) error {
	return s.mutateAccountRemoval(ctx, id, func(q *gen.Queries, op *domain.AccountsManagerRemoval) error {
		if !op.StopStarted || op.Phase == domain.AccountsManagerRemovalCancelled {
			return domain.ErrAccountsManagerRemovalConflict
		}
		matched := false
		for i := range op.Impact.Sessions {
			entry := &op.Impact.Sessions[i]
			if entry.SessionID != sessionID {
				continue
			}
			if err := validateRemovalEntry(ctx, q, op.AccountID, *entry); err != nil {
				return err
			}
			entry.Stopped = true
			matched = true
		}
		if matched {
			return nil
		}
		return domain.ErrAccountsManagerRemovalConflict
	})
}

// RecordAccountsManagerRemovalFailure retains irreversible recovery obligations.
func (s *Store) RecordAccountsManagerRemovalFailure(ctx context.Context, id, code string) error {
	if !validSwitchAtom(code, 64) {
		return domain.ErrAccountsManagerRemovalConflict
	}
	return s.mutateAccountRemoval(ctx, id, func(_ *gen.Queries, op *domain.AccountsManagerRemoval) error {
		if op.Phase.Terminal() {
			return nil
		}
		if op.StopStarted && op.Phase != domain.AccountsManagerRemovalRevoked {
			op.Phase = domain.AccountsManagerRemovalRecovery
		}
		op.ErrorCode = code
		return nil
	})
}

// RecordAccountsManagerRemovalRevoked requires every stop acknowledgement.
func (s *Store) RecordAccountsManagerRemovalRevoked(ctx context.Context, id string) error {
	return s.mutateAccountRemoval(ctx, id, func(_ *gen.Queries, op *domain.AccountsManagerRemoval) error {
		if !op.StopStarted || !op.BindingsRevoked || op.Phase == domain.AccountsManagerRemovalCancelled {
			return domain.ErrAccountsManagerRemovalConflict
		}
		for _, entry := range op.Impact.Sessions {
			if !entry.Stopped {
				return domain.ErrAccountsManagerRemovalConflict
			}
		}
		op.Phase, op.ErrorCode = domain.AccountsManagerRemovalRevoked, ""
		return nil
	})
}

// CompleteAccountsManagerRemoval atomically clears bindings and defaults without fallback.
func (s *Store) CompleteAccountsManagerRemoval(ctx context.Context, id string) error {
	return s.mutateAccountRemoval(ctx, id, func(q *gen.Queries, op *domain.AccountsManagerRemoval) error {
		if op.Phase == domain.AccountsManagerRemovalComplete {
			return nil
		}
		if op.Phase != domain.AccountsManagerRemovalRevoked {
			return domain.ErrAccountsManagerRemovalConflict
		}
		if err := validateRemoval(ctx, q, *op); err != nil {
			return err
		}
		if err := q.RememberAccountsManagerRemovedChoices(ctx, gen.RememberAccountsManagerRemovedChoicesParams{UpdatedAt: time.Now().UTC(), AccountID: op.AccountID}); err != nil {
			return err
		}
		if err := q.DeleteAccountsManagerRemovedBindings(ctx, op.AccountID); err != nil {
			return err
		}
		for _, provider := range []domain.AccountsManagerProvider{domain.AccountsManagerProviderCodex, domain.AccountsManagerProviderClaude} {
			choices, err := q.ListAccountsManagerRoutingPolicyAccounts(ctx, string(provider))
			if err != nil {
				return err
			}
			if len(choices) != 1 || choices[0] != op.AccountID {
				continue
			}
			if err := q.UpsertAccountsManagerRoutingPolicy(ctx, gen.UpsertAccountsManagerRoutingPolicyParams{Provider: string(provider), UpdatedAt: time.Now().UTC()}); err != nil {
				return err
			}
			if err := q.DeleteAccountsManagerRoutingPolicyAccounts(ctx, string(provider)); err != nil {
				return err
			}
		}
		op.Phase, op.ErrorCode = domain.AccountsManagerRemovalComplete, ""
		return nil
	})
}

// BeginAccountsManagerRemovalStop closes cancellation after validating every captured binding.
func (s *Store) BeginAccountsManagerRemovalStop(ctx context.Context, id string) error {
	return s.mutateAccountRemoval(ctx, id, func(q *gen.Queries, op *domain.AccountsManagerRemoval) error {
		if op.Phase.Terminal() {
			return domain.ErrAccountsManagerRemovalConflict
		}
		if err := validateRemoval(ctx, q, *op); err != nil {
			return err
		}
		if !op.StopStarted {
			op.StopStarted, op.Phase, op.ErrorCode = true, domain.AccountsManagerRemovalStopping, ""
		}
		return nil
	})
}

// CancelAccountsManagerRemoval cannot cross the durable stopping boundary.
func (s *Store) CancelAccountsManagerRemoval(ctx context.Context, id string) error {
	return s.mutateAccountRemoval(ctx, id, func(_ *gen.Queries, op *domain.AccountsManagerRemoval) error {
		if op.Phase == domain.AccountsManagerRemovalCancelled {
			return nil
		}
		if op.StopStarted || op.Phase != domain.AccountsManagerRemovalRequested {
			return domain.ErrAccountsManagerRemovalConflict
		}
		op.Phase, op.ErrorCode = domain.AccountsManagerRemovalCancelled, ""
		return nil
	})
}

// RecordAccountsManagerRemovalBindingsRevoked preserves the runner acknowledgement before deletion.
func (s *Store) RecordAccountsManagerRemovalBindingsRevoked(ctx context.Context, id string) error {
	return s.mutateAccountRemoval(ctx, id, func(_ *gen.Queries, op *domain.AccountsManagerRemoval) error {
		if !op.StopStarted || op.Phase == domain.AccountsManagerRemovalCancelled {
			return domain.ErrAccountsManagerRemovalConflict
		}
		op.BindingsRevoked = true
		return nil
	})
}

// ValidateAccountsManagerRemoval rejects changed bindings or controller owners without recapture.
func (s *Store) ValidateAccountsManagerRemoval(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "validate account removal", func(q *gen.Queries) error {
		row, err := q.GetAccountsManagerRemoval(ctx, id)
		if err != nil {
			return err
		}
		op, err := removalFromRow(row)
		if err != nil {
			return err
		}
		if op.Phase.Terminal() {
			return domain.ErrAccountsManagerRemovalConflict
		}
		return validateRemoval(ctx, q, op)
	})
}

func validateRemoval(ctx context.Context, q *gen.Queries, op domain.AccountsManagerRemoval) error {
	current, err := removalImpact(ctx, q, op.AccountID)
	if err != nil {
		return err
	}
	if len(current.Sessions) != len(op.Impact.Sessions) {
		return domain.ErrAccountsManagerRemovalConflict
	}
	for _, entry := range op.Impact.Sessions {
		if err := validateRemovalEntry(ctx, q, op.AccountID, entry); err != nil {
			return err
		}
	}
	return nil
}

func validateRemovalEntry(ctx context.Context, q *gen.Queries, accountID string, entry domain.AccountsManagerRemovalSession) error {
	if !entry.Provider.Valid() {
		return domain.ErrAccountsManagerRemovalConflict
	}
	binding, err := q.GetAccountsManagerSessionRoute(ctx, gen.GetAccountsManagerSessionRouteParams{SessionID: string(entry.SessionID), Provider: string(entry.Provider)})
	if err != nil {
		return err
	}
	if binding.AccountID != accountID || binding.Revision != entry.BindingRevision {
		return domain.ErrAccountsManagerRemovalConflict
	}
	row, err := q.GetSession(ctx, entry.SessionID)
	if err != nil {
		return err
	}
	rec := getSessionRowToRecord(row)
	if !entry.OwnsController() {
		current := entry
		current.Owner = rec.ControllerOwner()
		if current.OwnsController() {
			return domain.ErrAccountsManagerRemovalConflict
		}
		return nil
	}
	if rec.ControllerOwner() != entry.Owner || rec.Metadata.RuntimeHandleID != entry.RuntimeHandleID {
		return domain.ErrAccountsManagerRemovalConflict
	}
	return nil
}

// AccountsManagerAccountDeleting includes durable tombstones that forbid resurrection.
func (s *Store) AccountsManagerAccountDeleting(ctx context.Context, id string) (bool, error) {
	deleting, err := s.qr.AccountsManagerAccountDeleting(ctx, id)
	return deleting, err
}

func admitAccountSelection(ctx context.Context, q *gen.Queries, id string) error {
	if id == "" {
		return nil
	}
	deleting, err := q.AccountsManagerAccountDeleting(ctx, id)
	if err != nil {
		return err
	}
	if deleting {
		return domain.ErrAccountsManagerAccountDeleting
	}
	return nil
}
