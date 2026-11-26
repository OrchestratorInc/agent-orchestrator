package chat

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

// PreTaskCompactionPolicyFor exposes the service's compact-before-task rule for
// a Chat session, so tests can check who it applies to without a provider.
func (s *Service) PreTaskCompactionPolicyFor(target domain.SessionID) PreTaskCompactionPolicy {
	return s.preTaskCompactionPolicy(target)
}

// SetPreTaskCompactionDeadline shortens how long a queued task waits for a
// compaction whose turn never starts.
func (c *Controller) SetPreTaskCompactionDeadline(d time.Duration) {
	c.mu.Lock()
	c.preTaskDeadline = d
	c.mu.Unlock()
}
