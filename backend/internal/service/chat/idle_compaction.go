package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// IdleBeforeCompaction is how long an orchestrator stays quiet before AO
// compacts its history.
//
// Claude Code writes its prompt cache with a one-hour lifetime. Compacting
// inside that hour reads the history at the cache-read rate. Waking after the
// hour, for a worker report or a PR nudge, re-writes the whole history at the
// cache-write rate instead, and orchestrators are always woken again.
const IdleBeforeCompaction = 45 * time.Minute

// IdleCompactionDeadline is when the prompt cache is assumed to have expired.
// Compacting later would itself pay the full re-write it exists to avoid.
const IdleCompactionDeadline = 55 * time.Minute

// IdleCompactionMinContext is the smallest context worth compacting. Below it,
// a cold wake costs less than the summary a compaction produces.
const IdleCompactionMinContext int64 = 150_000

// CompactIdleOrchestrators compacts every live orchestrator that has been
// idle long enough with a large context, while its prompt cache is still warm.
func (s *Service) CompactIdleOrchestrators(ctx context.Context) error {
	s.mu.RLock()
	ids := make([]domain.SessionID, 0, len(s.controllers))
	for id := range s.controllers {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	slices.Sort(ids)
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		if _, err := s.CompactIdleOrchestrator(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("compact idle orchestrator %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// CompactIdleOrchestrator compacts one orchestrator if it qualifies. It is
// tried at most once per idle period: a failure is not retried until the
// session has done new work and gone quiet again.
func (s *Service) CompactIdleOrchestrator(ctx context.Context, id domain.SessionID) (bool, error) {
	controller, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		// Stopped since the sweep listed it: there is nothing warm to compact.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if controller.State() != ports.ChatControllerReady ||
		!controller.Capabilities().Has(ports.ChatCapabilityCompaction) {
		return false, nil
	}
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		return false, err
	}
	if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.HibernatedAt != nil ||
		rec.Activity.State != domain.ActivityIdle {
		return false, nil
	}
	lastActive := rec.Activity.LastActivityAt
	if lastActive.IsZero() {
		return false, nil
	}
	idle := s.now().Sub(lastActive)
	if idle < IdleBeforeCompaction || idle >= IdleCompactionDeadline {
		return false, nil
	}
	controller.mu.Lock()
	contextUsed := controller.usage.ContextUsed
	attempted := controller.idleCompactionFor.Equal(lastActive)
	if contextUsed >= IdleCompactionMinContext && !attempted {
		controller.idleCompactionFor = lastActive
	}
	controller.mu.Unlock()
	if contextUsed < IdleCompactionMinContext || attempted {
		return false, nil
	}
	if _, err := s.Compact(ctx, id); err != nil {
		if errors.Is(err, ErrCompactionWhileBusy) || errors.Is(err, ErrControllerHandoff) {
			// The session is working again; its next idle period gets a new try.
			return false, nil
		}
		return false, err
	}
	return true, nil
}
