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
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The same claim idle hibernation takes: a restore, kill or switch in
	// progress keeps the session as it is until the next sweep.
	if err := m.beginAgentOperation(operationCtx, id, agentOperationHibernate); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return nil
		}
		return err
	}
	defer m.endAgentOperation(id, agentOperationHibernate)
	active, err := m.hasActiveInterfaceTransition(operationCtx, id)
	if err != nil || active {
		return err
	}
	// The wake runs in the background and waits for this claim.
	_, err = refresher.SwitchToInstalledRelease(operationCtx, id, minIdle)
	return err
}

// maxConcurrentVersionMoves bounds sessions restarted at once, as Account
// Manager's session moves do (#6274): each restart starts a provider process.
const maxConcurrentVersionMoves = 4

// forEachVersionMove runs move for each session, at most four at a time.
func (m *Manager) forEachVersionMove(ctx context.Context, ids []domain.SessionID, move func(context.Context, domain.SessionID) error) error {
	slots := make(chan struct{}, maxConcurrentVersionMoves)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, id := range ids {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return errors.Join(append(errs, ctx.Err())...)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
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
		if err != nil || rec.IsTerminated || !versionMoveStopped(rec, m.chat) {
			m.versionRetries.done(id)
			continue
		}
		if !m.versionRetries.attempt(id) {
			continue
		}
		if _, err := m.ResumeAgentWithMode(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("resume %s after a failed version move: %w", id, err))
			continue
		}
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

// launchFactsCarrier keeps a session's release facts across a live reconnect.
type launchFactsCarrier interface {
	CarryHarnessRelease(ctx context.Context, id domain.SessionID, oldLaunch, newLaunch string) error
	CarryStaleBinary(ctx context.Context, id domain.SessionID, oldLaunch, newLaunch string) error
}

// carryHarnessRelease runs before the reconnect writes its new generation, so
// the launch trigger finds the old release already recorded and keeps it.
func (m *Manager) carryHarnessRelease(ctx context.Context, id domain.SessionID, oldLaunch, newLaunch string) {
	carrier, ok := m.store.(launchFactsCarrier)
	if !ok {
		return
	}
	if err := carrier.CarryHarnessRelease(context.WithoutCancel(ctx), id, oldLaunch, newLaunch); err != nil {
		m.logger.Warn("could not carry the running release across a Chat reconnect", "sessionID", id, "error", err)
	}
}

func (m *Manager) carryStaleBinary(ctx context.Context, id domain.SessionID, oldLaunch, newLaunch string) {
	carrier, ok := m.store.(launchFactsCarrier)
	if !ok {
		return
	}
	if err := carrier.CarryStaleBinary(context.WithoutCancel(ctx), id, oldLaunch, newLaunch); err != nil {
		m.logger.Warn("could not carry the stale release mark across a Chat reconnect", "sessionID", id, "error", err)
	}
}
