package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// VersionRefreshPolicy is the daemon's current setting for moving sessions to
// a newly installed harness release.
type VersionRefreshPolicy struct {
	Chats     bool
	Terminals bool
	MinIdle   time.Duration
}

// SetVersionRefreshPolicy late-binds the daemon's version-refresh setting.
func (m *Manager) SetVersionRefreshPolicy(policy func() VersionRefreshPolicy) {
	m.versionPolicyMu.Lock()
	defer m.versionPolicyMu.Unlock()
	m.versionPolicy = policy
}

func (m *Manager) currentVersionPolicy() (VersionRefreshPolicy, bool) {
	m.versionPolicyMu.Lock()
	policy := m.versionPolicy
	m.versionPolicyMu.Unlock()
	if policy == nil {
		return VersionRefreshPolicy{}, false
	}
	return policy(), true
}

// SessionTurnEnded is told by the lifecycle manager that a session went idle.
// A session still on a replaced release is checked again once it has been
// quiet for the policy's MinIdle, so it moves then rather than at the next
// sweep. It never blocks the caller. (Account Manager moves its sessions on
// the same signal, #6274.)
func (m *Manager) SessionTurnEnded(rec domain.SessionRecord) {
	if rec.StaleBinary == nil || rec.StaleBinary.SwitchFailedAt != nil || rec.IsTerminated {
		return
	}
	policy, ok := m.currentVersionPolicy()
	if !ok {
		return
	}
	chat := domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
	if (chat && !policy.Chats) || (!chat && !policy.Terminals) {
		return
	}
	id := rec.ID
	time.AfterFunc(policy.MinIdle+time.Second, func() {
		ctx, cancel := context.WithTimeout(m.versionContext(), 2*time.Minute)
		defer cancel()
		current, err := m.getRecord(ctx, id)
		if err != nil || current.StaleBinary == nil || current.Activity.State != domain.ActivityIdle {
			return
		}
		policy, ok := m.currentVersionPolicy()
		if !ok {
			return
		}
		// The same permit sweeps take, so turn ends cannot start more.
		release, err := m.versionMoves.acquire(ctx)
		if err != nil {
			return
		}
		defer release()
		if chat {
			if refresher, ok := m.chat.(ChatVersionRefresher); ok && policy.Chats {
				_ = m.refreshStaleChat(ctx, refresher, id, policy.MinIdle)
			}
			return
		}
		if store, ok := m.store.(staleTerminalStore); ok && policy.Terminals {
			_ = m.refreshStaleTerminal(ctx, store, id, policy.MinIdle)
		}
	})
}

func (m *Manager) versionContext() context.Context {
	if m.backgroundContext != nil {
		return m.backgroundContext
	}
	return context.Background()
}
