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

// IdleCompactionMinContext is the smallest context worth compacting. Below it,
// a cold wake costs less than the summary a compaction produces.
const IdleCompactionMinContext int64 = 150_000

// IdleCompactionWindow is when, measured from an orchestrator's last activity,
// AO compacts its history: late enough that it has likely stopped for now,
// early enough that its prompt cache is still warm. Compacting while warm reads
// the history at the cache-read rate. Waking after expiry, for a worker report
// or a PR nudge, re-writes the whole history at the cache-write rate instead,
// and orchestrators are always woken again.
type IdleCompactionWindow struct {
	// Start is the shortest quiet period that qualifies.
	Start time.Duration
	// Deadline is when the cache may have expired. Compacting later would
	// itself pay the full re-write it exists to avoid. The margin before the
	// cache lifetime covers the cache counting from when the last request
	// started, while last activity marks when its reply ended.
	Deadline time.Duration
}

// idleCompactionWindows maps each prompt cache lifetime Claude reports to its
// window. Claude Code picks the lifetime per request: one hour on a
// subscription within its limits or with ENABLE_PROMPT_CACHING_1H, five
// minutes on an API key, Bedrock, Vertex or Foundry, on subscription overage,
// or with FORCE_PROMPT_CACHING_5M. So AO reads it from the session's own
// usage rather than assuming one.
var idleCompactionWindows = map[time.Duration]IdleCompactionWindow{
	time.Hour:       {Start: 45 * time.Minute, Deadline: 55 * time.Minute},
	5 * time.Minute: {Start: 2 * time.Minute, Deadline: 3 * time.Minute},
}

// IdleCompactionWindowFor returns the window for a prompt cache lifetime. An
// unknown lifetime has none: AO cannot tell whether the cache is still warm.
func IdleCompactionWindowFor(lifetime time.Duration) (IdleCompactionWindow, bool) {
	window, ok := idleCompactionWindows[lifetime]
	return window, ok
}

// idleCompactionEarliest and idleCompactionLatest bound every window, so the
// sweep can skip a session before reading its cache lifetime.
var idleCompactionEarliest, idleCompactionLatest = func() (time.Duration, time.Duration) {
	var earliest, latest time.Duration
	for _, window := range idleCompactionWindows {
		if earliest == 0 || window.Start < earliest {
			earliest = window.Start
		}
		latest = max(latest, window.Deadline)
	}
	return earliest, latest
}()

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
	if !s.idleCompactionEligible(rec, lastActive, IdleCompactionWindow{Start: idleCompactionEarliest, Deadline: idleCompactionLatest}) {
		return false, nil
	}
	controller.mu.Lock()
	contextUsed := controller.usage.ContextUsed
	attempted := controller.idleCompactionFor.Equal(lastActive)
	controller.mu.Unlock()
	if contextUsed < IdleCompactionMinContext || attempted {
		return false, nil
	}
	window, allowed, err := s.idleCompactionAllowed(ctx, rec, controller)
	if err != nil || !allowed || !s.idleCompactionEligible(rec, lastActive, window) {
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
		compacted, err = s.compactIfStillIdle(ctx, id, admitted, lastActive, window)
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
// the session stays quiet: the project has opted in, Claude reported the
// prompt cache lifetime, and the latest turn finished cleanly with no
// compaction after it. It returns the window for that cache lifetime.
func (s *Service) idleCompactionAllowed(ctx context.Context, rec domain.SessionRecord, controller *Controller) (IdleCompactionWindow, bool, error) {
	reader, ok := s.sessions.(idleCompactionReader)
	if !ok {
		return IdleCompactionWindow{}, false, errors.New("idle compaction store is unavailable")
	}
	project, found, err := reader.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return IdleCompactionWindow{}, false, fmt.Errorf("read project for idle compaction: %w", err)
	}
	if !found || !project.Config.IdleOrchestratorCompaction {
		return IdleCompactionWindow{}, false, nil
	}
	lifetime, err := reader.PromptCacheLifetime(ctx, rec.ID)
	if err != nil {
		return IdleCompactionWindow{}, false, err
	}
	window, known := IdleCompactionWindowFor(lifetime)
	if !known {
		return IdleCompactionWindow{}, false, nil
	}
	turn, err := reader.LatestConversationTurn(ctx, controller.conversation.ID)
	if errors.Is(err, domain.ErrNoConversationTurn) {
		return IdleCompactionWindow{}, false, nil
	}
	if err != nil {
		return IdleCompactionWindow{}, false, err
	}
	// A failed, interrupted or usage-limited turn leaves the conversation
	// mid-thought; the person deciding what happens next should see it whole.
	if turn.State != domain.TurnStateCompleted {
		return IdleCompactionWindow{}, false, nil
	}
	conversation, err := s.store.ConversationForSession(ctx, rec.ID)
	if err != nil {
		return IdleCompactionWindow{}, false, fmt.Errorf("read conversation for idle compaction: %w", err)
	}
	// A compaction since the latest turn already shrank this history.
	if conversation.CompactedAt != nil && !conversation.CompactedAt.Before(turn.RequestedAt) {
		return IdleCompactionWindow{}, false, nil
	}
	return window, true, nil
}

// compactIfStillIdle compacts an admitted controller unless the session has
// moved on or has work a compaction would swallow. Admission can wait while a
// turn runs and finishes, so the quiet period is checked again first.
func (s *Service) compactIfStillIdle(ctx context.Context, id domain.SessionID, controller *Controller, lastActive time.Time, window IdleCompactionWindow) (bool, error) {
	current, err := s.requireChatSession(ctx, id)
	if err != nil {
		return false, err
	}
	if !s.idleCompactionEligible(current, lastActive, window) {
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
// the idle period that began at lastActive, inside window.
func (s *Service) idleCompactionEligible(rec domain.SessionRecord, lastActive time.Time, window IdleCompactionWindow) bool {
	if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.HibernatedAt != nil ||
		rec.Activity.State != domain.ActivityIdle || lastActive.IsZero() ||
		!rec.Activity.LastActivityAt.Equal(lastActive) {
		return false
	}
	idle := s.now().Sub(lastActive)
	return idle >= window.Start && idle < window.Deadline
}
