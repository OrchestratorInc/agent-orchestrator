package chat

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetNativeHistoryLoadAttemptLimit shortens the per-attempt provider load bound
// for tests and returns a restore function.
func SetNativeHistoryLoadAttemptLimit(limit time.Duration) (restore func()) {
	previous := nativeHistoryLoadAttemptLimit
	nativeHistoryLoadAttemptLimit = limit
	return func() { nativeHistoryLoadAttemptLimit = previous }
}

// SetRenderMeasureTimeout shortens how long publishing waits for heights, for
// tests, and returns a restore function.
func SetRenderMeasureTimeout(timeout time.Duration) (restore func()) {
	previous := renderMeasureTimeout
	renderMeasureTimeout = timeout
	return func() { renderMeasureTimeout = previous }
}

// WaitRenderMeasures waits for the background measures PublishRender started.
func (s *Service) WaitRenderMeasures() { s.renderMeasures.Wait() }

// SetDeltaFlushInterval changes how long streamed deltas may wait to be batched
// for controllers started afterwards, and returns a restore function. Zero writes
// every delta on its own, the way the projector worked before batching.
func SetDeltaFlushInterval(interval time.Duration) (restore func()) {
	previous := deltaFlushInterval
	deltaFlushInterval = interval
	return func() { deltaFlushInterval = previous }
}

// DeltaRunMaxBytes is the most delta text one batched write carries.
const DeltaRunMaxBytes = deltaRunMaxBytes

func SetForcedHandoffInterruptLimit(limit time.Duration) (restore func()) {
	previous := forcedHandoffInterruptLimit
	forcedHandoffInterruptLimit = limit
	return func() { forcedHandoffInterruptLimit = previous }
}

// SetIdleCompactionState makes the controller read a fixed idle compaction
// mode and prompt cache position, as Claude's transcript would report them.
func (c *Controller) SetIdleCompactionState(mode domain.IdleCompactionMode, state ports.PromptCacheState) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.idleProbe = func(context.Context, string) (domain.IdleCompactionMode, ports.PromptCacheState, bool) {
		return mode, state, true
	}
}

// IdleCompactionDue exposes the on-return decision for tests.
func IdleCompactionDue(state ports.PromptCacheState, now time.Time) bool {
	return idleCompactionDue(state, now)
}

// IdleCompactionWindow exposes the beforeCacheExpires window for tests.
func IdleCompactionWindow(state ports.PromptCacheState) (start, end time.Time, ok bool) {
	return idleCompactionWindow(state)
}

// IsCompactCommand exposes the /compact detection for tests.
func IsCompactCommand(text string) bool { return isCompactCommand(text) }
