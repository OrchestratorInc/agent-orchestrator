package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (m *Manager) beginVersionMove(ctx context.Context, rec domain.SessionRecord) error {
	if store, ok := m.store.(ports.SessionVersionMoveStore); ok {
		return store.PutSessionVersionMove(ctx, domain.NewSessionVersionMove(rec))
	}
	return nil
}

func (m *Manager) versionMoveStage(ctx context.Context, id domain.SessionID, stage string) error {
	if store, ok := m.store.(ports.SessionVersionMoveStore); ok {
		return store.SetSessionVersionMoveStage(ctx, id, stage)
	}
	return nil
}

func (m *Manager) completeVersionMove(ctx context.Context, id domain.SessionID) error {
	if store, ok := m.store.(ports.SessionVersionMoveStore); ok {
		return store.DeleteSessionVersionMove(ctx, id)
	}
	return nil
}

// recoverVersionMoves rehydrates only confirmed stops. The journal survives
// manager recreation and physical database reopen. A crash while stopping or
// starting cannot establish that no provider survived, so retain that intent
// and expose the existing Retry update/Resume action without launching.
func (m *Manager) recoverVersionMoves(ctx context.Context) (map[domain.SessionID]domain.SessionVersionMove, error) {
	store, ok := m.store.(ports.SessionVersionMoveStore)
	if !ok {
		return nil, nil
	}
	if m.persistentHostReconcileDone != nil {
		select {
		case <-m.persistentHostReconcileDone:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	moves, err := store.ListSessionVersionMoves(ctx)
	if err != nil {
		return nil, err
	}
	pending := make(map[domain.SessionID]domain.SessionVersionMove, len(moves))
	for _, move := range moves {
		rec, err := m.getRecord(ctx, move.SessionID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		current := domain.NewSessionVersionMove(rec)
		if errors.Is(err, ErrNotFound) || rec.IsTerminated || rec.MovedTo != nil ||
			current.Mode != move.Mode || current.Harness != move.Harness || current.NativeID != move.NativeID ||
			current.SourceLaunch != move.SourceLaunch && rec.StaleBinary == nil {
			if err := store.DeleteSessionVersionMove(ctx, move.SessionID); err != nil {
				return nil, err
			}
			m.versionRetries.done(move.SessionID)
			continue
		}
		pending[move.SessionID] = move
		if move.Mode == domain.SessionModeTUI && move.Stage == domain.VersionMoveStopping {
			stopped, err := m.recoverStoppingTerminal(ctx, rec)
			if errors.Is(err, errAgentOperationInProgress) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if stopped {
				move.Stage, rec.Activity.State = domain.VersionMoveRetry, domain.ActivityExited
				pending[move.SessionID] = move
			}
		}
		if move.Stage != domain.VersionMoveRetry || move.Attempts >= maxVersionResumeAttempts {
			m.versionRetries.done(move.SessionID)
			if rec.StaleBinary == nil || rec.StaleBinary.SwitchFailedAt == nil {
				if marker, ok := m.store.(staleTerminalStore); ok {
					if err := marker.SetSessionVersionSwitchFailed(ctx, rec.ID, move.Version, m.clock()); err != nil {
						return nil, err
					}
				}
			}
			m.logger.Warn("interrupted version move needs explicit resume; automatic recovery withheld", "sessionID", rec.ID, "stage", move.Stage, "attempts", move.Attempts)
			continue
		}
		// Exited hooks alone do not authorize replacing a possibly live TUI.
		if move.Mode == domain.SessionModeTUI && versionMoveStopped(rec, m.chat) {
			probe := m.runtime.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{Handle: runtimeHandle(rec.Metadata), SessionID: rec.ID, Generation: rec.Metadata.RuntimeLaunchID})
			if probe.Liveness != ports.FencedDead {
				m.versionRetries.done(move.SessionID)
				continue
			}
		}
		m.versionRetries.note(move.SessionID)
	}
	return pending, nil
}

// Before Starting was journaled, no replacement could have launched. A dead
// source therefore closes the stop-to-start crash window without guessing at
// the liveness of an uncommitted target. Hold the same claim as Resume/Exit.
func (m *Manager) recoverStoppingTerminal(ctx context.Context, original domain.SessionRecord) (bool, error) {
	if err := m.beginAgentOperation(ctx, original.ID, agentOperationResume); err != nil {
		return false, err
	}
	defer m.endAgentOperation(original.ID, agentOperationResume)
	store, ok := m.store.(ports.SessionVersionMoveStore)
	if !ok {
		return false, nil
	}
	moves, err := store.ListSessionVersionMoves(ctx)
	if err != nil {
		return false, err
	}
	stopping := false
	for _, move := range moves {
		if move.SessionID == original.ID && move.Stage == domain.VersionMoveStopping &&
			move.SourceLaunch == original.Metadata.RuntimeLaunchID && move.NativeID == original.Metadata.AgentSessionID {
			stopping = true
		}
	}
	if !stopping {
		return false, nil
	}
	rec, err := m.getRecord(ctx, original.ID)
	if err != nil {
		return false, err
	}
	if rec.IsTerminated || rec.Metadata.RuntimeLaunchID != original.Metadata.RuntimeLaunchID ||
		rec.Metadata.AgentSessionID != original.Metadata.AgentSessionID || rec.Harness != original.Harness ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return false, nil
	}
	probe := m.runtime.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{Handle: runtimeHandle(rec.Metadata), SessionID: rec.ID, Generation: rec.Metadata.RuntimeLaunchID})
	if probe.Liveness != ports.FencedDead {
		return false, nil
	}
	if err := m.recordAgentExited(ctx, rec); err != nil {
		return false, err
	}
	if err := m.versionMoveStage(ctx, rec.ID, domain.VersionMoveRetry); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) attemptVersionMove(ctx context.Context, id domain.SessionID, moves map[domain.SessionID]domain.SessionVersionMove) (bool, error) {
	if _, ok := moves[id]; ok {
		allowed, err := m.store.(ports.SessionVersionMoveStore).AttemptSessionVersionMove(ctx, id, maxVersionResumeAttempts)
		if err != nil {
			return false, fmt.Errorf("record retry for %s: %w", id, err)
		}
		return allowed, nil
	}
	return m.versionRetries.attempt(id), nil
}
