package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// PutAccountsManagerRoutingPolicy atomically replaces the policy and its ordered account IDs.
func (s *Store) PutAccountsManagerRoutingPolicy(ctx context.Context, policy domain.AccountsManagerRoutingPolicy) error {
	if !policy.Provider.Valid() {
		return fmt.Errorf("put accounts manager routing policy: invalid provider")
	}
	seen := make(map[string]struct{}, len(policy.AccountIDs))
	for _, id := range policy.AccountIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("put accounts manager routing policy: empty account id")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("put accounts manager routing policy: duplicate account id")
		}
		seen[id] = struct{}{}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "put accounts manager routing policy", func(q *gen.Queries) error {
		for _, id := range policy.AccountIDs {
			if err := admitAccountSelection(ctx, q, id); err != nil {
				return err
			}
		}
		enabled := int64(0)
		if policy.Enabled {
			enabled = 1
		}
		if err := q.UpsertAccountsManagerRoutingPolicy(ctx, gen.UpsertAccountsManagerRoutingPolicyParams{
			Provider: string(policy.Provider), Enabled: enabled, UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		if err := q.DeleteAccountsManagerRoutingPolicyAccounts(ctx, string(policy.Provider)); err != nil {
			return err
		}
		for position, id := range policy.AccountIDs {
			if err := q.InsertAccountsManagerRoutingPolicyAccount(ctx, gen.InsertAccountsManagerRoutingPolicyAccountParams{
				Provider: string(policy.Provider), AccountID: strings.TrimSpace(id), Position: int64(position),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetAccountsManagerRoutingPolicy returns a disabled empty policy when none is stored.
func (s *Store) GetAccountsManagerRoutingPolicy(ctx context.Context, provider domain.AccountsManagerProvider) (domain.AccountsManagerRoutingPolicy, error) {
	if !provider.Valid() {
		return domain.AccountsManagerRoutingPolicy{}, fmt.Errorf("get accounts manager routing policy: invalid provider")
	}
	row, err := s.qr.GetAccountsManagerRoutingPolicy(ctx, string(provider))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountsManagerRoutingPolicy{Provider: provider, AccountIDs: []string{}}, nil
	}
	if err != nil {
		return domain.AccountsManagerRoutingPolicy{}, fmt.Errorf("get accounts manager routing policy: %w", err)
	}
	ids, err := s.qr.ListAccountsManagerRoutingPolicyAccounts(ctx, string(provider))
	if err != nil {
		return domain.AccountsManagerRoutingPolicy{}, fmt.Errorf("list accounts manager routing policy accounts: %w", err)
	}
	return domain.AccountsManagerRoutingPolicy{Provider: provider, Enabled: row.Enabled != 0, AccountIDs: ids}, nil
}

// GetOrCreateAccountsManagerSessionRoute preserves the first pin; the bool reports insertion.
func (s *Store) GetOrCreateAccountsManagerSessionRoute(ctx context.Context, route domain.AccountsManagerSessionRoute) (domain.AccountsManagerSessionRoute, bool, error) {
	if route.Mode == "" {
		route.Mode = domain.AccountsManagerManaged
	}
	if !validAccountsManagerBinding(route) {
		return domain.AccountsManagerSessionRoute{}, false, fmt.Errorf("create accounts manager session route: invalid route")
	}
	if route.CreatedAt.IsZero() {
		route.CreatedAt = time.Now().UTC()
	}
	if route.UpdatedAt.IsZero() {
		route.UpdatedAt = route.CreatedAt
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if existing, err := s.getAccountsManagerSessionRoute(ctx, s.qw, route.SessionID, route.Provider); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.AccountsManagerSessionRoute{}, false, err
	}
	if err := admitAccountSelection(ctx, s.qw, route.AccountID); err != nil {
		return domain.AccountsManagerSessionRoute{}, false, err
	}
	rows, err := s.qw.InsertAccountsManagerSessionRoute(ctx, gen.InsertAccountsManagerSessionRouteParams{
		SessionID: string(route.SessionID), Provider: string(route.Provider), ConnectionMode: string(route.Mode), AccountID: strings.TrimSpace(route.AccountID), CreatedAt: route.CreatedAt, UpdatedAt: route.UpdatedAt,
	})
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, false, fmt.Errorf("insert accounts manager session route: %w", err)
	}
	got, err := s.getAccountsManagerSessionRoute(ctx, s.qw, route.SessionID, route.Provider)
	return got, rows == 1, err
}

// GetAccountsManagerSessionRoute distinguishes an absent pin from a storage error.
func (s *Store) GetAccountsManagerSessionRoute(ctx context.Context, sessionID domain.SessionID, provider domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error) {
	route, err := s.getAccountsManagerSessionRoute(ctx, s.qr, sessionID, provider)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountsManagerSessionRoute{}, false, nil
	}
	return route, err == nil, err
}

func (s *Store) getAccountsManagerSessionRoute(ctx context.Context, q *gen.Queries, sessionID domain.SessionID, provider domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, error) {
	row, err := q.GetAccountsManagerSessionRoute(ctx, gen.GetAccountsManagerSessionRouteParams{SessionID: string(sessionID), Provider: string(provider)})
	if errors.Is(err, sql.ErrNoRows) {
		removed, removedErr := q.GetAccountsManagerRemovedChoice(ctx, gen.GetAccountsManagerRemovedChoiceParams{SessionID: string(sessionID), Provider: string(provider)})
		if removedErr != nil {
			return domain.AccountsManagerSessionRoute{}, removedErr
		}
		return domain.AccountsManagerSessionRoute{SessionID: sessionID, Provider: provider, Mode: domain.AccountsManagerManaged, AccountID: removed.AccountID, Revision: removed.Revision, Blocked: true, CreatedAt: removed.CreatedAt, UpdatedAt: removed.UpdatedAt}, nil
	}
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, err
	}
	blocked, err := q.AccountsManagerAccountDeleting(ctx, row.AccountID)
	if err != nil {
		return domain.AccountsManagerSessionRoute{}, err
	}
	return domain.AccountsManagerSessionRoute{SessionID: domain.SessionID(row.SessionID), Provider: domain.AccountsManagerProvider(row.Provider), Mode: domain.AccountsManagerConnectionMode(row.ConnectionMode), Revision: row.Revision, AccountID: row.AccountID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Blocked: blocked}, nil
}

func validAccountsManagerBinding(route domain.AccountsManagerSessionRoute) bool {
	return route.SessionID != "" && route.Provider.Valid() &&
		((route.Mode == domain.AccountsManagerNative && route.AccountID == "") ||
			(route.Mode == domain.AccountsManagerManaged && strings.TrimSpace(route.AccountID) != ""))
}

// CompareAndSwapAccountsManagerSessionRoute commits intent, not controller readiness.
func (s *Store) CompareAndSwapAccountsManagerSessionRoute(ctx context.Context, route domain.AccountsManagerSessionRoute, expectedRevision int64) (domain.AccountsManagerSessionRoute, error) {
	if !validAccountsManagerBinding(route) || expectedRevision <= 0 {
		return domain.AccountsManagerSessionRoute{}, domain.ErrAccountsManagerBindingConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.AccountsManagerSessionRoute
	err := s.inTx(ctx, "change accounts manager session binding", func(q *gen.Queries) error {
		if err := admitAccountSelection(ctx, q, route.AccountID); err != nil {
			return err
		}
		current, err := s.getAccountsManagerSessionRoute(ctx, q, route.SessionID, route.Provider)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return domain.ErrAccountsManagerBindingConflict
		}
		if removal, err := q.GetAccountsManagerAccountRemoval(ctx, current.AccountID); err == nil && removal.Phase != string(domain.AccountsManagerRemovalComplete) {
			return domain.ErrAccountsManagerAccountDeleting
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		removed, err := q.ClearAccountsManagerRemovedChoice(ctx, gen.ClearAccountsManagerRemovedChoiceParams{SessionID: string(route.SessionID), Provider: string(route.Provider), Revision: expectedRevision})
		if err != nil {
			return err
		}
		if removed == 1 {
			now := time.Now().UTC()
			if _, err := q.InsertAccountsManagerSessionRoute(ctx, gen.InsertAccountsManagerSessionRouteParams{SessionID: string(route.SessionID), Provider: string(route.Provider), ConnectionMode: string(route.Mode), AccountID: route.AccountID, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			result, err = s.getAccountsManagerSessionRoute(ctx, q, route.SessionID, route.Provider)
			return err
		}
		rows, err := q.CompareAndSwapAccountsManagerSessionRoute(ctx, gen.CompareAndSwapAccountsManagerSessionRouteParams{
			SessionID: string(route.SessionID), Provider: string(route.Provider), ConnectionMode: string(route.Mode), AccountID: strings.TrimSpace(route.AccountID), UpdatedAt: time.Now().UTC(), Revision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return domain.ErrAccountsManagerBindingConflict
		}
		result, err = s.getAccountsManagerSessionRoute(ctx, q, route.SessionID, route.Provider)
		return err
	})
	return result, err
}

// AccountsManagerBindings reads the clock and rows from the same transaction.
func (s *Store) AccountsManagerBindings(ctx context.Context) (domain.AccountsManagerBindingSnapshot, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var snapshot domain.AccountsManagerBindingSnapshot
	err := s.inTx(ctx, "read accounts manager bindings", func(q *gen.Queries) error {
		revision, err := q.GetAccountsManagerBindingRevision(ctx)
		if err != nil {
			return err
		}
		rows, err := q.ListAccountsManagerSessionBindings(ctx)
		if err != nil {
			return err
		}
		snapshot.Revision = revision
		snapshot.Bindings = make([]domain.AccountsManagerSessionRoute, 0, len(rows))
		for _, row := range rows {
			snapshot.Bindings = append(snapshot.Bindings, domain.AccountsManagerSessionRoute{SessionID: domain.SessionID(row.SessionID), Provider: domain.AccountsManagerProvider(row.Provider), Mode: domain.AccountsManagerConnectionMode(row.ConnectionMode), Revision: row.Revision, AccountID: row.AccountID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Blocked: row.Blocked})
		}
		return nil
	})
	return snapshot, err
}
