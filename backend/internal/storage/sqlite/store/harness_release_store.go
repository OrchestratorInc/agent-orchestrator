package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func (s *Store) SetManagedHarnessRelease(ctx context.Context, harness domain.AgentHarness, release string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetManagedHarnessRelease(ctx, gen.SetManagedHarnessReleaseParams{
		Harness: string(harness), Release: release, UpdatedAt: at,
	}); err != nil {
		return fmt.Errorf("record managed release of %s: %w", harness, err)
	}
	return nil
}

func (s *Store) ListHarnessReleasesInUse(ctx context.Context, harness domain.AgentHarness) ([]string, error) {
	releases, err := s.qr.ListHarnessReleasesInUse(ctx, string(harness))
	if err != nil {
		return nil, fmt.Errorf("list releases of %s in use: %w", harness, err)
	}
	return releases, nil
}

func (s *Store) PruneSessionHarnessReleases(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.PruneSessionHarnessReleases(ctx); err != nil {
		return fmt.Errorf("prune session harness releases: %w", err)
	}
	return nil
}

func (s *Store) ClaimLiveChatControllerGeneration(ctx context.Context, id domain.SessionID, generation string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, fmt.Sprintf("claim live chat controller generation for %s", id), func(q *gen.Queries) error {
		previous, err := q.GetChatControllerGeneration(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("chat session not found")
		}
		if err != nil {
			return err
		}
		rows, err := q.ClaimChatControllerGeneration(ctx, gen.ClaimChatControllerGenerationParams{
			ControllerGeneration: generation,
			ID:                   id,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return errors.New("chat session not found")
		}
		if previous == "" || previous == generation {
			return nil
		}
		if err := q.CarryHarnessReleaseToLaunch(ctx, gen.CarryHarnessReleaseToLaunchParams{
			NewLaunch: generation, SessionID: string(id), OldLaunch: previous,
		}); err != nil {
			return fmt.Errorf("carry release: %w", err)
		}
		if _, err := q.CarryStaleBinaryToLaunch(ctx, gen.CarryStaleBinaryToLaunchParams{
			NewLaunch: generation, SessionID: id, OldLaunch: previous,
		}); err != nil {
			return fmt.Errorf("carry stale release mark: %w", err)
		}
		return nil
	})
}
