package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// sendComposerDraftSamples is how many consecutive captures must show a draft
	// before a send is refused. It mirrors the interface-transition drain
	// (interfaceTransitionSurfaceIdleSamples): a single frame can misread provider
	// chrome (a product label, statusline, or rule) as a draft, so one capture is
	// never enough to block delivery (#5482 / #5486).
	sendComposerDraftSamples = interfaceTransitionSurfaceIdleSamples
	// sendComposerProbeLines bounds the rendered viewport each capture reads.
	sendComposerProbeLines = interfaceTransitionOutputLines
)

// composerHoldsDraft reports whether a TUI session's composer positively holds
// an unsent human draft, so a send must not concatenate its payload with the
// operator's half-typed text and submit both as one prompt (#5711).
//
// Only the screen can prove it. Recency cannot: an interrupt's Stop hook
// refreshes LastActivityAt while the draft stays on screen, and keystrokes typed
// over a direct tmux attach never reach lastInputAt. So when the harness and
// runtime can read the screen, at least one capture always runs, as in the
// interface-transition drain; lastInputAt only delays sampling until the last
// keystroke has settled.
//
// It fails OPEN (returns false, so the send is delivered) on every case it
// cannot prove: a non-TUI session, a durable state that cannot hold a draft
// (anything but idle, waiting-input, or active), a harness or
// runtime that cannot read the screen, a capture error, a provider-owned screen,
// a draft that does not persist across sendComposerDraftSamples captures, or a
// probe that runs out of time.
//
// The refusal follows what the running agent renders, so an agent that paints a
// composer-shaped screen can hold sends in retryable 409s while it does. That is
// accepted: the operator keeps direct terminal access, and a refused send is
// never written.
func (m *Manager) composerHoldsDraft(ctx context.Context, rec domain.SessionRecord, lastInputAt time.Time) bool {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return false
	}
	// Idle, waiting-input, and active turns can all have an editable composer
	// holding an unsent human follow-up draft: the Claude/Codex inspectors report
	// a composer independently of work state, so an active turn is not proof of an
	// empty composer (#5711). Only the screen decides, below. A blocked decision is
	// already refused upstream; every other durable state cannot hold a draft.
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
	// The probe runs under the held input lease, so it is bounded like the
	// drain's unverified-idle proof; running out of time delivers.
	probeCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	// A composer can still show the text of a just-submitted prompt for a moment
	// after Enter, so give the last keystroke its settle window first.
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
			return false // one empty, unknown, or provider-owned frame delivers
		}
	}
	return true
}

// composerFrameIsDraft reports whether a single captured frame positively shows
// an unsent human draft. A provider-owned approval or input request is not a
// human draft even when its rows read as composer text (the drain treats it the
// same way), and an empty or unknown composer is not a draft.
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

// settleAfterInput waits until settle has passed since lastInputAt, so a screen
// captured afterwards has drawn that keystroke. It reports false when ctx ended
// first.
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

// composerProbeGaveUp logs why the probe stopped early and delivers. A caller
// that cancelled its context is refused a write by the guard itself.
func (m *Manager) composerProbeGaveUp(probeCtx context.Context, rec domain.SessionRecord) bool {
	m.logger.Warn("send: composer draft check did not finish; delivering", "sessionID", rec.ID, "error", probeCtx.Err())
	return false
}

// composerProbeTiming shares the interface-transition drain's timings: its poll
// interval between samples, its settle window after the last keystroke, and its
// bound for proving an unverified idle.
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

// composerBusyCheck builds the guard callback for a send. It returns whether the
// composer holds a draft (so the send must be refused) and, when it does not, a
// release the guard calls after the pane write.
//
// The multi-sample probe is read-only (lastTerminalInputAt, never a drain) so it
// never drops the operator's keystrokes. But reading the last keystroke cannot
// stop one arriving between the probe's final empty frame and the paste: the
// guard's input lease is shared admission, not mutual exclusion against terminal
// input. So once the probe proves empty, hold the terminal input fence (keystrokes
// block, not dropped), let the newest keystroke the fence reports settle, repeat
// the draft proof, and keep the fence held through the guard's write. A keystroke
// that landed during the probe is caught by the fenced probe; one arriving after
// waits until the write completes, so neither concatenates with the payload
// (#5711).
func (m *Manager) composerBusyCheck() func(context.Context, domain.SessionRecord) (bool, func()) {
	return func(ctx context.Context, rec domain.SessionRecord) (bool, func()) {
		if m.composerHoldsDraft(ctx, rec, m.lastTerminalInputAt(rec)) {
			return true, nil
		}
		fencedInputAt, release := m.fenceTerminalInput(rec)
		if release == nil {
			// Nothing to fence (non-TUI, no handle, or a gate without the fencer):
			// deliver unsynchronized, exactly as before this mechanism existed.
			return false, nil
		}
		if m.composerHoldsDraft(ctx, rec, fencedInputAt) {
			release()
			return true, nil
		}
		return false, release
	}
}
