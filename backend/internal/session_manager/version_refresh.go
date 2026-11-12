package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// staleBinaryStore is the durable stale-release mark. It is optional so stores
// without it simply never move sessions to a new release.
type staleBinaryStore interface {
	MarkHarnessSessionsStale(ctx context.Context, harness domain.AgentHarness, version string, at time.Time) (int64, error)
	ListVersionRefreshChatCandidates(ctx context.Context, idleBefore time.Time) ([]domain.SessionID, error)
	CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error)
}

// ChatVersionRefresher is the Chat side of a move to a new harness release.
type ChatVersionRefresher interface {
	SwitchToInstalledRelease(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error)
}

// ChatWakeWaiter waits for the provider start a version move set off, so the
// move keeps its permit until the new process is up.
type ChatWakeWaiter interface {
	WaitForWake(ctx context.Context, id domain.SessionID) error
}

// versionMoveWakeTimeout bounds how long a move waits for its provider to
// start: the wake's own limit plus delivering what queued meanwhile.
const versionMoveWakeTimeout = 2 * time.Minute

// MarkHarnessUpdated records that an update to version replaced the release
// every live launch of harness started from. The installer calls it while it
// still holds the harness, so no launch on the new release is marked.
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

// CountStaleBinarySessions counts live launches still on a replaced release, per harness.
func (m *Manager) CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error) {
	store, ok := m.store.(staleBinaryStore)
	if !ok {
		return nil, nil
	}
	return store.CountStaleBinarySessions(ctx)
}

// RefreshStaleChats moves idle Chat sessions that still run a replaced harness
// release to the installed one. Each is stopped only under the same checks as
// idle hibernation (no turn, queued message, pending approval, open view or
// background work, and no interface transition), after minIdle of quiet so a
// warm prompt cache is not thrown away, and started again right away, as
// Account Manager moves its chats (#6274): the user finds it ready on the new
// release and never has to restart it, whatever the hibernation setting.
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
	// The provider starts in the background once the claim above is released.
	// Wait for it here, still holding the caller's permit, so no more than
	// maxConcurrentVersionMoves providers start at once.
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

// stopStaleChat stops the provider under the hibernation claim and sets off
// its start on the installed release. It reports whether it stopped it.
func (m *Manager) stopStaleChat(ctx context.Context, refresher ChatVersionRefresher, id domain.SessionID, minIdle time.Duration) (bool, error) {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The same claim idle hibernation takes: a restore, kill or switch in
	// progress keeps the session as it is until the next sweep.
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
	// The wake runs in the background and waits for this claim.
	return refresher.SwitchToInstalledRelease(operationCtx, id, minIdle)
}

// maxConcurrentVersionMoves bounds sessions restarted at once, as Account
// Manager's session moves do (#6274): each restart starts a provider process.
const maxConcurrentVersionMoves = 4

// versionMoveSlots is the permit every move to a new release holds, from a
// sweep or a turn end alike, until the session's new process has started.
type versionMoveSlots struct {
	once  sync.Once
	slots chan struct{}
}

// acquire takes a permit, waiting while maxConcurrentVersionMoves are held.
func (v *versionMoveSlots) acquire(ctx context.Context) (func(), error) {
	v.once.Do(func() { v.slots = make(chan struct{}, maxConcurrentVersionMoves) })
	select {
	case v.slots <- struct{}{}:
		return func() { <-v.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// forEachVersionMove runs move for each session, at most four at a time
// across every move in flight.
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

// versionRetries remembers sessions a version move stopped but could not
// start again, so every sweep tries to resume them, as #6274 does for its
// moves, instead of leaving them stopped until the user notices.
type versionRetries struct {
	mu       sync.Mutex
	attempts map[domain.SessionID]int
}

// maxVersionResumeAttempts gives up on a session whose resume keeps failing;
// its switchFailedAt notice then asks the user to resume it.
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

// attempt counts one try and reports whether another is allowed.
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

// RetryVersionMoves resumes sessions a version move stopped but could not
// start again. A session that came back on its own, was resumed by the user,
// or was ended no longer needs it.
func (m *Manager) RetryVersionMoves(ctx context.Context) error {
	var errs []error
	for _, id := range m.versionRetries.pending() {
		rec, err := m.getRecord(ctx, id)
		if err != nil || rec.IsTerminated {
			m.versionRetries.done(id)
			continue
		}
		if !versionMoveStopped(rec, m.chat) {
			// A failed terminal move can still have inconclusive liveness. Keep
			// the retry intent until stopped state is proven or a successful
			// native resume clears the failure; never replace a possibly live
			// child merely because its startup probe was unknown.
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
		// Automatic terminal restarts must never replace missing native history
		// with a fresh task, even when an update stop has no stale-binary mark.
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
			// Messages sent while it was stopped stayed queued for this start.
			if err := m.chat.DrainChatQueue(ctx, id); err != nil {
				m.logger.Warn("could not deliver messages queued while the session was stopped", "sessionID", id, "error", err)
			}
		}
		release()
		m.versionRetries.done(id)
	}
	return errors.Join(errs...)
}

// versionMoveStopped reports whether a failed version move left rec without
// its agent. A terminal agent exits. A Chat provider that failed to wake keeps
// its idle activity, since hibernation suppresses the stop, so it counts as
// stopped while no controller runs it.
func versionMoveStopped(rec domain.SessionRecord, chat ChatLauncher) bool {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return rec.Activity.State == domain.ActivityExited
	}
	return chat != nil && !chat.HasLiveChatController(rec.ID)
}
