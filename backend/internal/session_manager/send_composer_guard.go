package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	sendComposerDraftSamples = interfaceTransitionSurfaceIdleSamples
	sendComposerProbeLines   = interfaceTransitionOutputLines
)

func (m *Manager) composerHoldsDraft(ctx context.Context, rec domain.SessionRecord, lastInputAt time.Time) bool {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return false
	}
	switch rec.Activity.State {
	case domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityActive:
	default:
		return false
	}
	if m.agents == nil {
		return false
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	surfaceInspector, ok := agent.(ports.TerminalSurfaceInspector)
	if !ok {
		return false
	}
	styledOutput, ok := m.runtime.(ports.StyledTerminalOutputReader)
	if !ok {
		return false
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return false
	}

	poll, settle, limit := m.composerProbeTiming()
	probeCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if !settleAfterInput(probeCtx, lastInputAt, settle) {
		return m.composerProbeGaveUp(probeCtx, rec)
	}
	for i := 0; i < sendComposerDraftSamples; i++ {
		if i > 0 {
			select {
			case <-probeCtx.Done():
				return m.composerProbeGaveUp(probeCtx, rec)
			case <-time.After(poll):
			}
		}
		output, err := styledOutput.GetStyledOutput(probeCtx, handle, sendComposerProbeLines)
		if err != nil {
			if probeCtx.Err() != nil {
				return m.composerProbeGaveUp(probeCtx, rec)
			}
			m.logger.Warn("send: composer draft check skipped, screen capture failed; delivering", "sessionID", rec.ID, "error", err)
			return false
		}
		if !composerFrameIsDraft(surfaceInspector.InspectTerminalSurface(output)) {
			return false
		}
	}
	return true
}

func composerFrameIsDraft(observation ports.TerminalSurfaceObservation) bool {
	switch {
	case observation.Work == ports.TerminalSurfaceWorkWaitingInput,
		observation.Work == ports.TerminalSurfaceWorkBlocked:
		return false
	case observation.Composer != ports.TerminalComposerDraft:
		return false
	}
	return true
}

func settleAfterInput(ctx context.Context, lastInputAt time.Time, settle time.Duration) bool {
	if lastInputAt.IsZero() {
		return true
	}
	wait := time.Until(lastInputAt.Add(settle))
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) composerProbeGaveUp(probeCtx context.Context, rec domain.SessionRecord) bool {
	m.logger.Warn("send: composer draft check did not finish; delivering", "sessionID", rec.ID, "error", probeCtx.Err())
	return false
}

func (m *Manager) composerProbeTiming() (poll, settle, limit time.Duration) {
	cfg := m.interfaceTransition
	poll, settle, limit = cfg.pollInterval, cfg.idleSettle, cfg.staleIdleLimit
	if poll <= 0 {
		poll = interfaceTransitionPoll
	}
	if settle <= 0 {
		settle = interfaceTransitionIdleSettle
	}
	if limit <= 0 {
		limit = interfaceTransitionStaleIdleLimit
	}
	return poll, settle, limit
}

func (m *Manager) composerBusyCheck() func(context.Context, domain.SessionRecord) (bool, func()) {
	return func(ctx context.Context, rec domain.SessionRecord) (bool, func()) {
		if m.composerHoldsDraft(ctx, rec, m.lastTerminalInputAt(rec)) {
			return true, nil
		}
		fencedInputAt, release := m.fenceTerminalInput(rec)
		if release == nil {
			return false, nil
		}
		if m.composerHoldsDraft(ctx, rec, fencedInputAt) {
			release()
			return true, nil
		}
		return false, release
	}
}
