package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type staleTerminalStore interface {
	ListVersionRefreshTerminalCandidates(ctx context.Context, idleBefore time.Time) ([]domain.SessionID, error)
	SetSessionVersionSwitchFailed(ctx context.Context, id domain.SessionID, version string, at time.Time) error
}

// terminalViewers reports whether a client shows a terminal.
type terminalViewers interface {
	TerminalOnScreen(terminalID string) bool
}

// RefreshStaleTerminals restarts idle terminal sessions that still run a
// replaced harness release on the installed one. A restart is the Resume
// action run in place: the agent process is replaced and its conversation
// resumed natively, while the session, worktree, reviewer, shells and browser
// stay as they are. A session is left alone while anyone is attached to its
// terminal on screen, while it has been active within minIdle, while its terminal has
// input newer than its idle report, while its agent runs any shell command,
// or when its conversation cannot be resumed natively.
func (m *Manager) RefreshStaleTerminals(ctx context.Context, minIdle time.Duration) error {
	store, ok := m.store.(staleTerminalStore)
	if !ok {
		return nil
	}
	ids, err := store.ListVersionRefreshTerminalCandidates(ctx, m.clock().Add(-minIdle))
	if err != nil {
		return err
	}
	return m.forEachVersionMove(ctx, ids, func(ctx context.Context, id domain.SessionID) error {
		if err := m.refreshStaleTerminal(ctx, store, id, minIdle); err != nil {
			return fmt.Errorf("restart terminal %s on the installed release: %w", id, err)
		}
		return nil
	})
}

func (m *Manager) refreshStaleTerminal(ctx context.Context, store staleTerminalStore, id domain.SessionID, minIdle time.Duration) error {
	operationCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := m.beginAgentOperation(operationCtx, id, agentOperationResume); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return nil
		}
		return err
	}
	defer m.endAgentOperation(id, agentOperationResume)
	if active, err := m.hasActiveInterfaceTransition(operationCtx, id); err != nil || active {
		return err
	}
	rec, err := m.getRecord(operationCtx, id)
	if err != nil {
		return err
	}
	if !m.terminalRestartEligible(rec, minIdle) {
		return nil
	}
	// Close the terminal's input path, then require the idle report to be newer
	// than any keystroke accepted before the gate: text being typed is never lost.
	lastInputAt, releaseInput := m.beginTerminalInputDrain(rec)
	if releaseInput != nil {
		defer releaseInput()
	}
	current, err := m.getRecord(operationCtx, id)
	if err != nil {
		return err
	}
	if current.Metadata.RuntimeLaunchID != rec.Metadata.RuntimeLaunchID ||
		!m.terminalRestartEligible(current, minIdle) || !m.terminalIdleForRestart(operationCtx, current, lastInputAt) {
		return nil
	}
	inspector, ok := m.runtime.(ports.WorkloadCommandInspector)
	if !ok {
		return nil
	}
	commands, err := inspector.RunningWorkloadCommands(operationCtx, runtimeHandle(current.Metadata), ports.SupervisedProcessRef{
		SessionID: current.ID, LaunchID: current.Metadata.RuntimeLaunchID,
	})
	if err != nil || commands > 0 {
		// A command the agent started (tests, a dev server) would die with it.
		return nil
	}
	if !terminalRefreshIdentityVerified(current) {
		return nil
	}
	// Hold the harness like any launch, so an update cannot replace the binary
	// while the new process starts.
	releaseHarness, err := m.beginHarnessUse(current.Harness)
	if err != nil {
		return nil
	}
	defer releaseHarness()
	version := current.StaleBinary.Version
	if _, err := m.resumeAgentRecordWithPolicy(operationCtx, "version refresh", current, true); err != nil {
		m.logger.Warn("version_switch_failed: terminal session could not restart on the new harness release",
			"sessionID", id, "harness", current.Harness, "version", version, "error", err)
		m.emitVersionSwitch(current, "tui", "failed")
		m.versionRetries.note(id)
		if recordErr := store.SetSessionVersionSwitchFailed(context.WithoutCancel(ctx), id, version, m.clock()); recordErr != nil {
			return errors.Join(err, recordErr)
		}
		return err
	}
	m.emitVersionSwitch(current, "tui", "succeeded")
	m.logger.Info("terminal session restarted on the new harness release", "sessionID", id, "harness", current.Harness, "version", version)
	return nil
}

// terminalIdleForRestart proves, with the input gate closed, that restarting
// loses nothing typed. An idle report newer than the last input is proof.
// Input that changed nothing (a focus change when a viewer leaves, the
// terminal's reply to a query) sends no new idle report, so it would keep the
// session on the old release for good. Then the provider's screen, sampled
// once that input settled, must show an idle agent with an empty composer.
func (m *Manager) terminalIdleForRestart(ctx context.Context, rec domain.SessionRecord, lastInputAt time.Time) bool {
	if tuiIdleAfterInput(rec, lastInputAt) {
		return true
	}
	if rec.Activity.State != domain.ActivityIdle || m.clock().Before(lastInputAt.Add(interfaceTransitionIdleSettle)) {
		return false
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	inspector, ok := agent.(ports.TerminalSurfaceInspector)
	if !ok {
		return false
	}
	styled, ok := m.runtime.(ports.StyledTerminalOutputReader)
	if !ok {
		return false
	}
	handle := runtimeHandle(rec.Metadata)
	for sample := 0; sample < interfaceTransitionSurfaceIdleSamples; sample++ {
		if sample > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(interfaceTransitionPoll):
			}
		}
		output, err := styled.GetStyledOutput(ctx, handle, interfaceTransitionOutputLines)
		if err != nil {
			return false
		}
		observation := inspector.InspectTerminalSurface(output)
		if observation.Work != ports.TerminalSurfaceWorkIdle || observation.Composer != ports.TerminalComposerEmpty {
			return false
		}
	}
	return true
}

// terminalRestartEligible is the durable-fact and viewer check, read before
// and again after the input gate closes.
func (m *Manager) terminalRestartEligible(rec domain.SessionRecord, minIdle time.Duration) bool {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || rec.IsTerminated || rec.Kind == domain.KindOrchestrator ||
		!m.terminalVersionRefreshSupported(rec.Harness) || rec.StaleBinary == nil || rec.StaleBinary.SwitchFailedAt != nil ||
		rec.Activity.State != domain.ActivityIdle || rec.Activity.LastActivityAt.Add(minIdle).After(m.clock()) {
		return false
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return false
	}
	m.terminalInputGateMu.Lock()
	gate := m.terminalInputGate
	m.terminalInputGateMu.Unlock()
	viewers, ok := gate.(terminalViewers)
	// Without a way to tell, assume someone may be watching.
	return ok && !viewers.TerminalOnScreen(handle.ID)
}

func (m *Manager) terminalVersionRefreshSupported(harness domain.AgentHarness) bool {
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return false
	}
	capability, ok := agent.(ports.AgentTerminalVersionRefresher)
	return ok && capability.SupportsTerminalVersionRefresh()
}

// A stored resume hint may outlive a failed or fresh launch. Only the native
// identity reported for this exact running generation authorizes auto-refresh.
// Terminal-only adapters need no Chat identity or Chat history bridge.
func terminalRefreshIdentityVerified(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.AgentSessionID) != "" &&
		strings.TrimSpace(rec.Metadata.RuntimeLaunchID) != "" &&
		rec.Metadata.AgentSessionIDLaunchID == rec.Metadata.RuntimeLaunchID
}

// verifyRefreshedTerminal waits out immediate CLI argument/history failures.
// Seeing the supervisor spawn once is not startup success: its child can still
// exit on invalid resume arguments. Unknown probes fail closed without treating
// the process as dead or deleting its previous release reference.
func (m *Manager) verifyRefreshedTerminal(ctx context.Context, handle ports.RuntimeHandle, id domain.SessionID, launchID string) error {
	alive, err := m.waitForTargetGeneration(ctx, handle, id, domain.AgentGenerationID(launchID))
	if err != nil || !alive {
		return errors.Join(ErrNotResumable, err)
	}
	settled := time.NewTimer(time.Second)
	defer settled.Stop()
	ticker := time.NewTicker(switchPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-settled.C:
			alive, err := m.exactTargetGenerationAlive(ctx, handle, id, domain.AgentGenerationID(launchID))
			if err != nil || !alive {
				return errors.Join(ErrNotResumable, err)
			}
			return nil
		case <-ticker.C:
			alive, err := m.exactTargetGenerationAlive(ctx, handle, id, domain.AgentGenerationID(launchID))
			if err != nil || !alive {
				return errors.Join(ErrNotResumable, err)
			}
		}
	}
}
