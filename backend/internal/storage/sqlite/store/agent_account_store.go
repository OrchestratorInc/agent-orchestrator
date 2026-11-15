package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ ports.AgentAccountStore = (*Store)(nil)

func (s *Store) ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error) {
	rows, err := s.qr.ListAgentAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agent accounts: %w", err)
	}
	out := make([]domain.AgentAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AgentAccount{
			ID: row.ID, Provider: domain.AgentAccountProvider(row.Provider), Home: row.Home,
			Email: row.Email, Plan: row.Plan, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

func (s *Store) CreateAgentAccount(ctx context.Context, a domain.AgentAccount) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CreateAgentAccount(ctx, gen.CreateAgentAccountParams{
		ID: a.ID, Provider: string(a.Provider), Home: a.Home, Email: a.Email, Plan: a.Plan, CreatedAt: a.CreatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("create agent account %s: %w", a.ID, err)
	}
	return nil
}

func (s *Store) DeleteAgentAccount(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "delete agent account", func(q *gen.Queries) error {
		if err := q.DeleteAgentAccountDefaultFor(ctx, id); err != nil {
			return err
		}
		return q.DeleteAgentAccount(ctx, id)
	})
}

func (s *Store) GetAgentAccountDefault(ctx context.Context, p domain.AgentAccountProvider) (string, bool, error) {
	id, err := s.qr.GetAgentAccountDefault(ctx, string(p))
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s account default: %w", p, err)
	}
	return id, true, nil
}

func (s *Store) SetAgentAccountDefault(ctx context.Context, p domain.AgentAccountProvider, accountID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetAgentAccountDefault(ctx, gen.SetAgentAccountDefaultParams{Provider: string(p), AccountID: accountID}); err != nil {
		return fmt.Errorf("set %s account default: %w", p, err)
	}
	return nil
}

func (s *Store) BindSessionAgentAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider, accountID string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, fmt.Sprintf("bind session %s to %s account", id, p), func(q *gen.Queries) error {
		binding := gen.GetSessionAgentAccountParams{SessionID: string(id), Provider: string(p)}
		previous, previousErr := q.GetSessionAgentAccount(ctx, binding)
		if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
			return previousErr
		}
		if err := q.BindSessionAgentAccount(ctx, gen.BindSessionAgentAccountParams{
			SessionID: string(id), Provider: string(p), AccountID: accountID, BoundAt: at.UTC(),
		}); err != nil {
			return err
		}
		if errors.Is(previousErr, sql.ErrNoRows) {
			return nil
		}
		current, err := q.GetSessionAgentAccount(ctx, binding)
		if err != nil {
			return err
		}
		if current == previous {
			return nil
		}
		return q.CancelSessionTurnAutoResume(ctx, gen.CancelSessionTurnAutoResumeParams{
			SessionID: id, Provider: string(p),
		})
	})
}

func (s *Store) GetSessionAgentAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (string, bool, error) {
	accountID, err := s.qr.GetSessionAgentAccount(ctx, gen.GetSessionAgentAccountParams{SessionID: string(id), Provider: string(p)})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read session %s %s account: %w", id, p, err)
	}
	return accountID, true, nil
}

func (s *Store) AgentAccountUsageSince(ctx context.Context, since time.Time) ([]domain.AgentAccountUsage, error) {
	since = since.UTC()
	rows, err := s.qr.AgentAccountUsageSince(ctx, gen.AgentAccountUsageSinceParams{Since: sql.NullTime{Time: since, Valid: true}})
	if err != nil {
		return nil, fmt.Errorf("read agent account usage: %w", err)
	}
	deleted, err := s.qr.DeletedSessionAccountUsageSince(ctx, since.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("read deleted sessions' agent account usage: %w", err)
	}
	out := make([]domain.AgentAccountUsage, 0, len(rows)+len(deleted))
	for _, row := range rows {
		out = append(out, domain.AgentAccountUsage{
			AccountID: row.AccountID, Day: row.Day, Model: row.ModelID,
			InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens, OutputTokens: row.OutputTokens,
			CostNanos: row.EstimatedCostNanos,
		})
	}
	for _, row := range deleted {
		out = append(out, domain.AgentAccountUsage{
			AccountID: row.AccountID, Day: row.Day, Model: row.ModelID,
			InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens, OutputTokens: row.OutputTokens,
			CostNanos: row.EstimatedCostNanos,
		})
	}
	return out, nil
}

func foldDeletedSessionAccountUsage(ctx context.Context, q *gen.Queries, id domain.SessionID) error {
	rows, err := q.AgentAccountUsageSince(ctx, gen.AgentAccountUsageSinceParams{
		Since: sql.NullTime{Time: time.Time{}, Valid: true}, SessionID: string(id),
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := q.AddDeletedSessionAccountUsage(ctx, gen.AddDeletedSessionAccountUsageParams(row)); err != nil {
			return err
		}
	}
	return nil
}
