package chat

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Idle compaction keeps a long Claude Chat from paying a full-price rebuild of
// its prompt cache.
//
// Claude caches a conversation's history for an hour on a subscription (five
// minutes on an API key or usage credits). The first request after that
// reprocesses the whole history at full price: on a 900K-token conversation
// one message costs as much as dozens of normal ones. Claude Code compacts
// such conversations itself in an interactive terminal, but not in the agent
// SDK that Chat runs, so a returning user or an automatic PR message paid the
// rebuild in full.
//
// onReturn, the default, compacts before a message reaches a conversation
// whose cache expired. The rebuild still happens once, to write the summary,
// but every request after it, including each tool call of the turn the
// message starts, reads a short history instead of the full one. It never
// adds a request the user or a nudge did not cause.
//
// beforeCacheExpires, a beta, compacts shortly before the cache expires,
// while the history is still read at the cached rate, so no full-price
// rebuild happens at all. It also compacts conversations nobody returns to,
// which is why it is opt-in. It falls back to onReturn when its timer could
// not run (the daemon restarted, the cache lifetime was unknown).

// idleCompactionReadTimeout bounds the transcript read that runs before a
// dispatch, so a slow disk delays a message by at most this much.
const idleCompactionReadTimeout = 2 * time.Second

// idleCompactionWarmWindow is the shortest prompt cache lifetime Claude uses.
// A turn that ended more recently than this left the cache warm.
const idleCompactionWarmWindow = 5 * time.Minute

// idleCompactionProbe reads the current mode and the conversation's prompt
// cache position. found is false when either is unavailable, which always
// means "do not compact".
type idleCompactionProbe func(ctx context.Context, providerConversationID string) (mode domain.IdleCompactionMode, state ports.PromptCacheState, found bool)

// SetIdleCompaction wires the setting that decides when an idle Claude Chat
// compacts. Until it is wired no conversation compacts automatically.
func (s *Service) SetIdleCompaction(mode func(context.Context) domain.IdleCompactionMode) {
	s.idleCompactionMode.Store(&mode)
}

func (s *Service) currentIdleCompaction(ctx context.Context) domain.IdleCompactionMode {
	mode := s.idleCompactionMode.Load()
	if mode == nil || *mode == nil {
		return domain.IdleCompactionOff
	}
	return (*mode)(ctx)
}

// newIdleCompactionProbe binds a probe to one controller's harness and launch
// environment. Only Claude has the cache behavior and the native transcript
// this relies on; every other harness gets no probe.
func (s *Service) newIdleCompactionProbe(harness domain.AgentHarness, env map[string]string) idleCompactionProbe {
	if harness != domain.HarnessClaudeCode || s.drivers == nil {
		return nil
	}
	env = maps.Clone(env)
	return func(ctx context.Context, providerConversationID string) (domain.IdleCompactionMode, ports.PromptCacheState, bool) {
		mode := s.currentIdleCompaction(ctx)
		if mode == domain.IdleCompactionOff || providerConversationID == "" {
			return mode, ports.PromptCacheState{}, false
		}
		driver, err := s.drivers.Driver(harness)
		if err != nil {
			return mode, ports.PromptCacheState{}, false
		}
		reader, ok := driver.(ports.ChatPromptCacheReader)
		if !ok {
			return mode, ports.PromptCacheState{}, false
		}
		readCtx, cancel := context.WithTimeout(ctx, idleCompactionReadTimeout)
		defer cancel()
		state, found, err := reader.PromptCacheState(readCtx, ports.NativeCheckpointRequest{
			ProviderConversationID: providerConversationID, Env: env,
		})
		if err != nil {
			s.log.Debug("idle compaction: prompt cache unreadable", "harness", harness, "error", err)
			return mode, ports.PromptCacheState{}, false
		}
		return mode, state, found
	}
}

// idleCompactionDue reports whether a message arriving now would make the
// provider rebuild a cold cache over the size threshold. An unknown lifetime
// assumes Claude's hour plus slack, so it only ever errs toward leaving a
// conversation alone.
func idleCompactionDue(state ports.PromptCacheState, now time.Time) bool {
	if state.LastRequestAt.IsZero() || state.ContextTokens < domain.IdleCompactionMinContextTokens {
		return false
	}
	ttl := state.TTL
	if ttl <= 0 {
		ttl = domain.IdleCompactionFallbackTTL
	}
	return !now.Before(state.LastRequestAt.Add(ttl))
}

// idleCompactionWindow is when a beforeCacheExpires compaction may run: from
// IdleCompactionLead before the cache expires until it does. ok is false when
// the conversation is too small or the lifetime is unknown, since compacting
// after expiry pays the full-price read this mode exists to avoid.
func idleCompactionWindow(state ports.PromptCacheState) (start, end time.Time, ok bool) {
	if state.ContextTokens < domain.IdleCompactionMinContextTokens {
		return time.Time{}, time.Time{}, false
	}
	end = state.ExpiresAt()
	if end.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	return end.Add(-domain.IdleCompactionLead), end, true
}

// isCompactCommand reports a message that is itself a compaction, with or
// without instructions. Compacting ahead of it would charge for two.
func isCompactCommand(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) < len("/compact") || !strings.EqualFold(text[:len("/compact")], "/compact") {
		return false
	}
	rest := text[len("/compact"):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\n' || rest[0] == '\t'
}

// compactBeforeDispatchLocked starts a compaction when the message about to
// be dispatched would otherwise rebuild a cold cache over the threshold. It
// returns true when a compaction started; the caller then leaves the message
// queued, and the compaction's completion drains it. Callers hold sendMu.
func (c *Controller) compactBeforeDispatchLocked(ctx context.Context, text string) bool {
	if c.idleProbe == nil || isCompactCommand(text) || c.busy() || c.handoffActive() {
		return false
	}
	compactor, ok := c.conv.(ports.ChatCompactor)
	if !ok || !c.Capabilities().Has(ports.ChatCapabilityCompaction) {
		return false
	}
	// Answer from memory when it can: a context known to be small, or a turn
	// that ended minutes ago, inside any cache lifetime. Only a send that may
	// really be cold pays for the transcript read.
	c.mu.Lock()
	contextUsed, lastTurnEndedAt := c.usage.ContextUsed, c.lastTurnEndedAt
	c.mu.Unlock()
	if contextUsed > 0 && contextUsed < domain.IdleCompactionMinContextTokens {
		return false
	}
	if !lastTurnEndedAt.IsZero() && c.now().Sub(lastTurnEndedAt) < idleCompactionWarmWindow {
		return false
	}
	mode, state, found := c.idleProbe(ctx, c.conv.ProviderConversationID())
	if !found || mode == domain.IdleCompactionOff || !idleCompactionDue(state, c.now()) {
		return false
	}
	return c.startIdleCompactionLocked(ctx, compactor, state, "on return")
}

// startIdleCompactionLocked compacts once per idle spell: a compaction that
// failed, or that left the transcript unchanged, is not retried for the same
// request, so a message is never held behind a loop. Callers hold sendMu.
func (c *Controller) startIdleCompactionLocked(ctx context.Context, compactor ports.ChatCompactor, state ports.PromptCacheState, reason string) bool {
	c.mu.Lock()
	if c.idleCompactedFor.Equal(state.LastRequestAt) {
		c.mu.Unlock()
		return false
	}
	c.idleCompactedFor = state.LastRequestAt
	c.compactionPending = true
	c.idleCompacting = true
	c.mu.Unlock()
	if _, err := compactor.Compact(ctx); err != nil {
		c.mu.Lock()
		c.compactionPending = false
		c.idleCompacting = false
		c.mu.Unlock()
		c.log.Warn("idle compaction did not start", "session", c.sessionID, "reason", reason, "error", err)
		return false
	}
	c.log.Info("idle compaction started", "session", c.sessionID, "reason", reason,
		"contextTokens", state.ContextTokens, "lastRequestAt", state.LastRequestAt, "cacheTTL", state.TTL)
	return true
}

// finishIdleCompaction reports whether the turn that just ended was an
// automatic compaction, and clears the mark. Its outcome must not hold the
// queue: the message behind it would have been sent without it.
func (c *Controller) finishIdleCompaction() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	was := c.idleCompacting
	c.idleCompacting = false
	return was
}

// idleCompactionTimers holds one beforeCacheExpires timer per session. Timers
// live in memory: after a daemon restart the onReturn fallback covers them.
type idleCompactionTimers struct {
	mu     sync.Mutex
	timers map[domain.SessionID]*time.Timer
}

func (t *idleCompactionTimers) replace(id domain.SessionID, timer *time.Timer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if previous := t.timers[id]; previous != nil {
		previous.Stop()
	}
	if timer == nil {
		delete(t.timers, id)
		return
	}
	if t.timers == nil {
		t.timers = make(map[domain.SessionID]*time.Timer)
	}
	t.timers[id] = timer
}

// scheduleIdleCompaction is the controller's turn-ended hook. Planning reads
// the transcript, so it never runs on the projection goroutine.
func (s *Service) scheduleIdleCompaction(id domain.SessionID, probe idleCompactionProbe, providerConversationID string) {
	go s.planIdleCompaction(id, probe, providerConversationID)
}

// planIdleCompaction arms the session's beforeCacheExpires timer after a turn
// ended, replacing any earlier one. It reads the transcript, so it runs off
// the projection goroutine.
func (s *Service) planIdleCompaction(id domain.SessionID, probe idleCompactionProbe, providerConversationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), idleCompactionReadTimeout)
	defer cancel()
	mode, state, found := probe(ctx, providerConversationID)
	if !found || mode != domain.IdleCompactionBeforeCacheExpires {
		s.idleTimers.replace(id, nil)
		return
	}
	start, end, ok := idleCompactionWindow(state)
	now := s.now()
	if !ok || !now.Before(end) {
		s.idleTimers.replace(id, nil)
		return
	}
	s.idleTimers.replace(id, time.AfterFunc(max(start.Sub(now), 0), func() {
		s.runIdleCompaction(id, probe, providerConversationID, state.LastRequestAt)
	}))
}

// runIdleCompaction compacts a conversation shortly before its cache expires.
// A request since the timer was armed re-plans it instead; a hibernated
// session is woken first, which keeps its cache (the resumed provider sends
// the same prefix).
func (s *Service) runIdleCompaction(id domain.SessionID, probe idleCompactionProbe, providerConversationID string, planned time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), domain.IdleCompactionLead)
	defer cancel()
	mode, state, found := probe(ctx, providerConversationID)
	if !found || mode != domain.IdleCompactionBeforeCacheExpires {
		return
	}
	if !state.LastRequestAt.Equal(planned) {
		go s.planIdleCompaction(id, probe, providerConversationID)
		return
	}
	start, end, ok := idleCompactionWindow(state)
	if now := s.now(); !ok || now.Before(start) || !now.Before(end) {
		return
	}
	record, err := s.requireChatSession(ctx, id)
	if err != nil || record.IsTerminated || record.ProvisionState.IsProvisioning() {
		return
	}
	if record.HibernatedAt != nil {
		if err := s.wakeHibernated(ctx, id); err != nil {
			s.log.Info("idle compaction: could not wake session", "session", id, "error", err)
			return
		}
	}
	controller, err := s.Controller(id)
	if err != nil || controller.conv.ProviderConversationID() != providerConversationID {
		return
	}
	controller.compactBeforeExpiry(ctx, state, end)
}

// compactBeforeExpiry starts a beforeCacheExpires compaction when the
// conversation is still exactly as the timer found it: idle, nothing queued,
// no background work that would report into the next turn.
func (c *Controller) compactBeforeExpiry(ctx context.Context, state ports.PromptCacheState, end time.Time) {
	if c.reviewID != "" || c.idleProbe == nil {
		return
	}
	compactor, ok := c.conv.(ports.ChatCompactor)
	if !ok || !c.Capabilities().Has(ports.ChatCapabilityCompaction) {
		return
	}
	if hibernator, ok := c.conv.(ports.ChatProviderHibernator); ok {
		if idle, err := hibernator.CanHibernate(ctx); err != nil || !idle {
			return
		}
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.busy() || c.handoffActive() || !c.now().Before(end) {
		return
	}
	if _, err := c.store.NextQueuedTurn(ctx, c.conversation.ID); !errors.Is(err, domain.ErrNoQueuedTurn) {
		return
	}
	c.startIdleCompactionLocked(ctx, compactor, state, "before cache expires")
}
