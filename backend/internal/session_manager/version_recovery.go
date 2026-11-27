package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		obsolete := errors.Is(err, ErrNotFound) || rec.IsTerminated || rec.MovedTo != nil ||
			current.Mode != move.Mode || current.Harness != move.Harness || current.NativeID != move.NativeID
		completedGeneration := !obsolete && current.SourceLaunch != move.SourceLaunch && rec.StaleBinary == nil
		if current.Mode == domain.SessionModeChat {
			// A claim can precede history import and publication. A live owned
			// controller and a successful drain establish completion, not the
			// changed generation alone. Keep queued work after a failed drain.
			completedGeneration = completedGeneration && m.chat != nil && m.chat.HasLiveChatController(rec.ID)
			if completedGeneration {
				if err := m.chat.DrainChatQueue(ctx, rec.ID); err != nil {
					m.logger.Warn("version move queue delivery still pending", "sessionID", rec.ID, "error", err)
					m.versionRetries.done(rec.ID)
					continue
				}
			}
		}
		if obsolete || completedGeneration {
			if err := store.DeleteSessionVersionMove(ctx, move.SessionID); err != nil {
				return nil, err
			}
			m.versionRetries.done(move.SessionID)
			continue
		}
		pending[move.SessionID] = move
		if move.Mode == domain.SessionModeTUI && move.Stage == domain.VersionMoveStopping {
			stopped, abandoned, err := m.recoverStoppingTerminal(ctx, rec)
			if errors.Is(err, errAgentOperationInProgress) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if abandoned {
				delete(pending, move.SessionID)
				m.versionRetries.done(move.SessionID)
				continue
			}
			if stopped {
				move.Stage, rec.Activity.State = domain.VersionMoveRetry, domain.ActivityExited
				pending[move.SessionID] = move
			}
		}
		if move.Stage != domain.VersionMoveRetry || move.Attempts >= maxVersionResumeAttempts {
			m.versionRetries.done(move.SessionID)
			if move.Version != "" && (rec.StaleBinary == nil || rec.StaleBinary.SwitchFailedAt == nil) {
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
func (m *Manager) recoverStoppingTerminal(ctx context.Context, original domain.SessionRecord) (stopped, abandoned bool, err error) {
	if err := m.beginAgentOperation(ctx, original.ID, agentOperationResume); err != nil {
		return false, false, err
	}
	defer m.endAgentOperation(original.ID, agentOperationResume)
	store, err := m.versionMoveStoreForOwner(ctx, original, domain.VersionMoveStopping)
	if err != nil || store == nil {
		return false, false, err
	}
	rec, err := m.getRecord(ctx, original.ID)
	if err != nil {
		return false, false, err
	}
	if rec.IsTerminated || rec.Metadata.RuntimeLaunchID != original.Metadata.RuntimeLaunchID ||
		rec.Metadata.AgentSessionID != original.Metadata.AgentSessionID || rec.Harness != original.Harness ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return false, false, nil
	}
	probe := m.runtime.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{Handle: runtimeHandle(rec.Metadata), SessionID: rec.ID, Generation: rec.Metadata.RuntimeLaunchID})
	if probe.Liveness == ports.FencedAlive {
		// No replacement started; a crash before shutdown must not strand the
		// healthy source behind an unfinished stop or block the next update.
		return false, true, store.DeleteSessionVersionMove(ctx, rec.ID)
	}
	if probe.Liveness != ports.FencedDead {
		return false, false, nil
	}
	if err := m.recordAgentExited(ctx, rec); err != nil {
		return false, false, err
	}
	if err := m.versionMoveStage(ctx, rec.ID, domain.VersionMoveRetry); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (m *Manager) attemptVersionMove(ctx context.Context, id domain.SessionID, moves map[domain.SessionID]domain.SessionVersionMove) (bool, error) {
	if _, ok := moves[id]; ok {
		store, ok := m.store.(ports.SessionVersionMoveStore)
		if !ok {
			return false, errors.New("version move store unavailable")
		}
		allowed, err := store.AttemptSessionVersionMove(ctx, id, maxVersionResumeAttempts)
		if err != nil {
			return false, fmt.Errorf("record retry for %s: %w", id, err)
		}
		return allowed, nil
	}
	return m.versionRetries.attempt(id), nil
}

// A preflight error has not started a replacement. If shutdown was attempted,
// require proof that the source still owns its runtime before discarding intent.
func (m *Manager) cancelUnstartedVersionMove(ctx context.Context, rec domain.SessionRecord, requireLiveSource bool) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	store, err := m.versionMoveStoreForOwner(cleanup, rec, domain.VersionMoveStopping)
	if err != nil || store == nil {
		return err
	}
	if requireLiveSource {
		probe := m.runtime.ProbeFencedRuntime(cleanup, ports.FencedRuntimeRef{Handle: runtimeHandle(rec.Metadata), SessionID: rec.ID, Generation: rec.Metadata.RuntimeLaunchID})
		if probe.Liveness != ports.FencedAlive {
			return nil
		}
	}
	return store.DeleteSessionVersionMove(cleanup, rec.ID)
}

// Runtime errors may follow a partial restart. An exact probe distinguishes an
// untouched source, a dead replacement, and uncertain ownership; an error alone
// never authorizes another launch.
func (m *Manager) recoverFailedVersionMoveStart(ctx context.Context, rec domain.SessionRecord, handle ports.RuntimeHandle, launchID string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	store, err := m.versionMoveStoreForOwner(cleanup, rec, domain.VersionMoveStarting)
	if err != nil || store == nil {
		return err
	}
	source := runtimeHandle(rec.Metadata)
	probe := m.runtime.ProbeFencedRuntime(cleanup, ports.FencedRuntimeRef{Handle: source, SessionID: rec.ID, Generation: rec.Metadata.RuntimeLaunchID})
	if probe.Liveness == ports.FencedAlive {
		return store.DeleteSessionVersionMove(cleanup, rec.ID)
	}
	if handle.ID == "" {
		handle = source
	}
	probe = m.runtime.ProbeFencedRuntime(cleanup, ports.FencedRuntimeRef{Handle: handle, SessionID: rec.ID, Generation: launchID})
	if probe.Liveness == ports.FencedDead {
		if _, err := m.preserveFailedReconcileRelaunch(cleanup, rec); err != nil {
			return err
		}
		return store.SetSessionVersionMoveStage(cleanup, rec.ID, domain.VersionMoveRetry)
	}
	return nil
}

func (m *Manager) versionMoveStoreForOwner(ctx context.Context, rec domain.SessionRecord, stage string) (ports.SessionVersionMoveStore, error) {
	store, ok := m.store.(ports.SessionVersionMoveStore)
	if !ok {
		return nil, nil
	}
	moves, err := store.ListSessionVersionMoves(ctx)
	if err != nil {
		return nil, err
	}
	current := domain.NewSessionVersionMove(rec)
	for _, move := range moves {
		if move.SessionID == rec.ID && move.Stage == stage && move.SourceLaunch == current.SourceLaunch &&
			move.Mode == current.Mode && move.Harness == current.Harness && move.NativeID == current.NativeID {
			return store, nil
		}
	}
	return nil, nil
}
