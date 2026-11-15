package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type staleBinaryStore interface {
	MarkHarnessSessionsStale(ctx context.Context, harness domain.AgentHarness, version string, at time.Time) (int64, error)
	ListVersionRefreshChatCandidates(ctx context.Context, idleBefore time.Time) ([]domain.SessionID, error)
	CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error)
}

type ChatVersionRefresher interface {
	SwitchToInstalledRelease(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error)
}

type ChatWakeWaiter interface {
	WaitForWake(ctx context.Context, id domain.SessionID) error
}

const versionMoveWakeTimeout = 2 * time.Minute

func (m *Manager) MarkHarnessUpdated(ctx context.Context, harness domain.AgentHarness, version string, at time.Time) error {
	store, ok := m.store.(staleBinaryStore)
	if !ok {
		return nil
	}
	marked, err := store.MarkHarnessSessionsStale(ctx, harness, version, at)
	if err != nil {
		return err
	}
	if marked > 0 {
		m.logger.Info("harness update left sessions on the previous release", "harness", harness, "version", version, "sessions", marked)
	}
	return nil
}

func (m *Manager) CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error) {
	store, ok := m.store.(staleBinaryStore)
	if !ok {
		return nil, nil
	}
	return store.CountStaleBinarySessions(ctx)
}

func (m *Manager) RefreshStaleChats(ctx context.Context, minIdle time.Duration) error {
	store, ok := m.store.(staleBinaryStore)
	if !ok {
		return nil
	}
	refresher, ok := m.chat.(ChatVersionRefresher)
	if !ok {
		return nil
	}
	ids, err := store.ListVersionRefreshChatCandidates(ctx, time.Now().UTC().Add(-minIdle))
	if err != nil {
		return err
	}
	return m.forEachVersionMove(ctx, ids, func(ctx context.Context, id domain.SessionID) error {
		if err := m.refreshStaleChat(ctx, refresher, id, minIdle); err != nil {
			return fmt.Errorf("move chat %s to the installed release: %w", id, err)
		}
		return nil
	})
}

func (m *Manager) refreshStaleChat(ctx context.Context, refresher ChatVersionRefresher, id domain.SessionID, minIdle time.Duration) error {
	stopped, err := m.stopStaleChat(ctx, refresher, id, minIdle)
	if err != nil || !stopped {
		return err
	}
	waiter, ok := m.chat.(ChatWakeWaiter)
	if !ok {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, versionMoveWakeTimeout)
	defer cancel()
	if err := waiter.WaitForWake(waitCtx, id); err != nil {
		return fmt.Errorf("wait for the provider to start: %w", err)
	}
	return nil
}

func (m *Manager) stopStaleChat(ctx context.Context, refresher ChatVersionRefresher, id domain.SessionID, minIdle time.Duration) (bool, error) {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := m.beginAgentOperation(operationCtx, id, agentOperationHibernate); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return false, nil
		}
		return false, err
	}
	defer m.endAgentOperation(id, agentOperationHibernate)
	active, err := m.hasActiveInterfaceTransition(operationCtx, id)
	if err != nil || active {
		return false, err
	}
	return refresher.SwitchToInstalledRelease(operationCtx, id, minIdle)
}

const maxConcurrentVersionMoves = 4

type versionMoveSlots struct {
	once  sync.Once
	slots chan struct{}
}

func (v *versionMoveSlots) acquire(ctx context.Context) (func(), error) {
	v.once.Do(func() { v.slots = make(chan struct{}, maxConcurrentVersionMoves) })
	select {
	case v.slots <- struct{}{}:
		return func() { <-v.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) forEachVersionMove(ctx context.Context, ids []domain.SessionID, move func(context.Context, domain.SessionID) error) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, id := range ids {
		release, err := m.versionMoves.acquire(ctx)
		if err != nil {
			wg.Wait()
			return errors.Join(append(errs, err)...)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer release()
			if err := move(ctx, id); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

type versionRetries struct {
	mu       sync.Mutex
	attempts map[domain.SessionID]int
}

const maxVersionResumeAttempts = 10

func (r *versionRetries) note(id domain.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attempts == nil {
		r.attempts = make(map[domain.SessionID]int)
	}
	if _, ok := r.attempts[id]; !ok {
		r.attempts[id] = 0
	}
}

func (r *versionRetries) pending() []domain.SessionID {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]domain.SessionID, 0, len(r.attempts))
	for id := range r.attempts {
		ids = append(ids, id)
	}
	return ids
}

func (r *versionRetries) attempt(id domain.SessionID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts[id]++
	if r.attempts[id] > maxVersionResumeAttempts {
		delete(r.attempts, id)
		return false
	}
	return true
}

func (r *versionRetries) done(id domain.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.attempts, id)
}

func (m *Manager) RetryVersionMoves(ctx context.Context) error {
	var errs []error
	for _, id := range m.versionRetries.pending() {
		rec, err := m.getRecord(ctx, id)
		if err != nil || rec.IsTerminated {
			m.versionRetries.done(id)
			continue
		}
		if !versionMoveStopped(rec, m.chat) {
			if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI &&
				rec.StaleBinary != nil && rec.StaleBinary.SwitchFailedAt != nil {
				continue
			}
			m.versionRetries.done(id)
			continue
		}
		release, err := m.versionMoves.acquire(ctx)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		if !m.versionRetries.attempt(id) {
			release()
			continue
		}
		if _, err := m.resumeAgentWithMode(ctx, id, domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI); err != nil {
			release()
			errs = append(errs, fmt.Errorf("resume %s after a failed version move: %w", id, err))
			continue
		}
		if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat && m.chat != nil {
			if waiter, ok := m.chat.(ChatWakeWaiter); ok {
				waitCtx, cancel := context.WithTimeout(ctx, versionMoveWakeTimeout)
				err := waiter.WaitForWake(waitCtx, id)
				cancel()
				if err != nil {
					release()
					errs = append(errs, fmt.Errorf("wait for retry %s: %w", id, err))
					continue
				}
			}
			if err := m.chat.DrainChatQueue(ctx, id); err != nil {
				m.logger.Warn("could not deliver messages queued while the session was stopped", "sessionID", id, "error", err)
			}
		}
		release()
		m.versionRetries.done(id)
	}
	return errors.Join(errs...)
}

func versionMoveStopped(rec domain.SessionRecord, chat ChatLauncher) bool {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return rec.Activity.State == domain.ActivityExited
	}
	return chat != nil && !chat.HasLiveChatController(rec.ID)
}
