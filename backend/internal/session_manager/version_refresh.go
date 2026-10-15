package sessionmanager

import (
	"context"
	"errors"
	"fmt"
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

// versionRefresher is the Chat side of a move to a new harness release.
type versionRefresher interface {
	SwitchToInstalledRelease(ctx context.Context, id domain.SessionID, minIdle time.Duration, wake bool) (bool, error)
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
// warm prompt cache is not thrown away. wake starts it again right away; the
// caller passes false when idle hibernation would put it to sleep anyway.
func (m *Manager) RefreshStaleChats(ctx context.Context, minIdle time.Duration, wake bool) error {
	store, ok := m.store.(staleBinaryStore)
	if !ok {
		return nil
	}
	refresher, ok := m.chat.(versionRefresher)
	if !ok {
		return nil
	}
	ids, err := store.ListVersionRefreshChatCandidates(ctx, time.Now().UTC().Add(-minIdle))
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := m.refreshStaleChat(ctx, refresher, id, minIdle, wake); err != nil {
			errs = append(errs, fmt.Errorf("move chat %s to the installed release: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) refreshStaleChat(ctx context.Context, refresher versionRefresher, id domain.SessionID, minIdle time.Duration, wake bool) error {
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
	// The wake, if any, runs in the background and waits for this claim.
	_, err = refresher.SwitchToInstalledRelease(operationCtx, id, minIdle, wake)
	return err
}
