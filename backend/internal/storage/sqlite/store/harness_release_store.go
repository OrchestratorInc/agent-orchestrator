package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SetManagedHarnessRelease records the AO-managed release new launches of
// harness start from. Every later launch records it as the release it runs.
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

// ListHarnessReleasesInUse lists the managed releases of harness that a live
// session's current launch still runs.
func (s *Store) ListHarnessReleasesInUse(ctx context.Context, harness domain.AgentHarness) ([]string, error) {
	releases, err := s.qr.ListHarnessReleasesInUse(ctx, string(harness))
	if err != nil {
		return nil, fmt.Errorf("list releases of %s in use: %w", harness, err)
	}
	return releases, nil
}

// PruneSessionHarnessReleases forgets launches that are no longer current.
func (s *Store) PruneSessionHarnessReleases(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.PruneSessionHarnessReleases(ctx); err != nil {
		return fmt.Errorf("prune session harness releases: %w", err)
	}
	return nil
}

// CarryHarnessRelease records that newLaunch runs the release oldLaunch ran.
// A live Chat reconnect calls it before the new controller generation is
// written: the provider process is the same one, so the trigger that records
// the current release for a new launch must not claim it moved.
func (s *Store) CarryHarnessRelease(ctx context.Context, id domain.SessionID, oldLaunch, newLaunch string) error {
	if oldLaunch == "" || newLaunch == "" || oldLaunch == newLaunch {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CarryHarnessReleaseToLaunch(ctx, gen.CarryHarnessReleaseToLaunchParams{
		NewLaunch: newLaunch, SessionID: string(id), OldLaunch: oldLaunch,
	}); err != nil {
		return fmt.Errorf("carry release of %s: %w", id, err)
	}
	return nil
}

// CarryStaleBinary moves a session's stale-release mark from oldLaunch to
// newLaunch after a live Chat reconnect, so the reconnected process still
// moves to the installed release once idle.
func (s *Store) CarryStaleBinary(ctx context.Context, id domain.SessionID, oldLaunch, newLaunch string) error {
	if oldLaunch == "" || newLaunch == "" || oldLaunch == newLaunch {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.CarryStaleBinaryToLaunch(ctx, gen.CarryStaleBinaryToLaunchParams{
		NewLaunch: newLaunch, SessionID: id, OldLaunch: oldLaunch,
	}); err != nil {
		return fmt.Errorf("carry stale release mark of %s: %w", id, err)
	}
	return nil
}
