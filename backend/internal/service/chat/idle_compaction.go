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
// The window assumes a one-hour prompt cache. Compacting inside that hour reads
// the history at the cache-read rate. Waking after the hour, for a worker
// report or a PR nudge, re-writes the whole history at the cache-write rate
// instead, and orchestrators are always woken again.
const IdleBeforeCompaction = 45 * time.Minute

// IdleCompactionDeadline is when the prompt cache is assumed to have expired.
// Compacting later would itself pay the full re-write it exists to avoid.
const IdleCompactionDeadline = 55 * time.Minute

// IdleCompactionMinContext is the smallest context worth compacting. Below it,
// a cold wake costs less than the summary a compaction produces.
const IdleCompactionMinContext int64 = 150_000

// idleCompactionCacheLifetime is the only prompt cache lifetime the window
// fits. A five-minute cache has long expired by IdleBeforeCompaction, and an
// unknown lifetime might have.
const idleCompactionCacheLifetime = time.Hour

// idleCompactionReader is the durable state idle compaction reads beyond the
// chat Store: the project's opt-in, the session's prompt cache lifetime and
// how its latest turn ended.
type idleCompactionReader interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	PromptCacheLifetime(ctx context.Context, sessionID domain.SessionID) (time.Duration, error)
	LatestConversationTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error)
}

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
	lastActive := rec.Activity.LastActivityAt
	if !s.idleCompactionEligible(rec, lastActive) {
		return false, nil
	}
	controller.mu.Lock()
	contextUsed := controller.usage.ContextUsed
	attempted := controller.idleCompactionFor.Equal(lastActive)
	controller.mu.Unlock()
	if contextUsed < IdleCompactionMinContext || attempted {
		return false, nil
	}
	if allowed, err := s.idleCompactionAllowed(ctx, rec, controller); err != nil || !allowed {
		return false, err
	}
	controller.mu.Lock()
	attempted = controller.idleCompactionFor.Equal(lastActive)
	controller.idleCompactionFor = lastActive
	controller.mu.Unlock()
	if attempted {
		return false, nil
	}
	// Admit without waking: an update or version refresh can hibernate the
	// session after the read above, and the sweep must not resume it.
	admitted, release, err := s.admitController(ctx, id, false)
	if err == nil {
		var compacted bool
		compacted, err = s.compactIfStillIdle(ctx, id, admitted, lastActive)
		release()
		if err == nil {
			return compacted, nil
		}
	}
	if errors.Is(err, errAsleep) || errors.Is(err, ErrCompactionWhileBusy) || errors.Is(err, ErrControllerHandoff) {
		// Asleep, or working again; its next idle period gets a new try.
		return false, nil
	}
	return false, err
}

// idleCompactionAllowed reads the durable conditions that do not change while
// the session stays quiet: the project has opted in, the prompt cache lives an
// hour, and the latest turn finished cleanly with no compaction after it.
func (s *Service) idleCompactionAllowed(ctx context.Context, rec domain.SessionRecord, controller *Controller) (bool, error) {
	reader, ok := s.sessions.(idleCompactionReader)
	if !ok {
		return false, errors.New("idle compaction store is unavailable")
	}
	project, found, err := reader.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return false, fmt.Errorf("read project for idle compaction: %w", err)
	}
	if !found || !project.Config.IdleOrchestratorCompaction {
		return false, nil
	}
	lifetime, err := reader.PromptCacheLifetime(ctx, rec.ID)
	if err != nil {
		return false, err
	}
	if lifetime != idleCompactionCacheLifetime {
		return false, nil
	}
	turn, err := reader.LatestConversationTurn(ctx, controller.conversation.ID)
	if errors.Is(err, domain.ErrNoConversationTurn) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A failed, interrupted or usage-limited turn leaves the conversation
	// mid-thought; the person deciding what happens next should see it whole.
	if turn.State != domain.TurnStateCompleted {
		return false, nil
	}
	conversation, err := s.store.ConversationForSession(ctx, rec.ID)
	if err != nil {
		return false, fmt.Errorf("read conversation for idle compaction: %w", err)
	}
	// A compaction since the latest turn already shrank this history.
	if conversation.CompactedAt != nil && !conversation.CompactedAt.Before(turn.RequestedAt) {
		return false, nil
	}
	return true, nil
}

// compactIfStillIdle compacts an admitted controller unless the session has
// moved on or has work a compaction would swallow. Admission can wait while a
// turn runs and finishes, so the quiet period is checked again first.
func (s *Service) compactIfStillIdle(ctx context.Context, id domain.SessionID, controller *Controller, lastActive time.Time) (bool, error) {
	current, err := s.requireChatSession(ctx, id)
	if err != nil {
		return false, err
	}
	if !s.idleCompactionEligible(current, lastActive) {
		return false, nil
	}
	conversationID := controller.conversation.ID
	if _, err := s.store.NextQueuedTurn(ctx, conversationID); err == nil {
		return false, nil
	} else if !errors.Is(err, domain.ErrNoQueuedTurn) {
		return false, fmt.Errorf("check queued chat turns: %w", err)
	}
	if running, err := s.store.ListVisibleRunningTurnProviderIDs(ctx, conversationID); err != nil {
		return false, fmt.Errorf("check running chat turns: %w", err)
	} else if len(running) != 0 {
		return false, nil
	}
	// An approval or question waiting on a person is not idleness.
	if pending, err := s.store.HasPendingConversationInteractions(ctx, conversationID); err != nil {
		return false, fmt.Errorf("check pending chat interactions: %w", err)
	} else if pending {
		return false, nil
	}
	// Background work that outlives the turn reports into this history.
	if inventory, ok := controller.conv.(ports.ChatProviderHibernator); ok {
		ready, err := inventory.CanHibernate(ctx)
		if err != nil {
			return false, fmt.Errorf("check chat provider background work: %w", err)
		}
		if !ready {
			return false, nil
		}
	}
	if _, err := controller.Compact(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// idleCompactionEligible reports whether rec is a quiet orchestrator still in
// the idle period that began at lastActive, inside the warm-cache window.
func (s *Service) idleCompactionEligible(rec domain.SessionRecord, lastActive time.Time) bool {
	if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.HibernatedAt != nil ||
		rec.Activity.State != domain.ActivityIdle || lastActive.IsZero() ||
		!rec.Activity.LastActivityAt.Equal(lastActive) {
		return false
	}
	idle := s.now().Sub(lastActive)
	return idle >= IdleBeforeCompaction && idle < IdleCompactionDeadline
}
