package chat

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// versionSwitchStore records a failed move to a new harness release.
type versionSwitchStore interface {
	SetSessionVersionSwitchFailed(ctx context.Context, id domain.SessionID, version string, at time.Time) error
}

// SwitchToInstalledRelease stops an idle provider that still runs a replaced
// harness release and starts it again on the installed one right away, so the
// user finds the session ready and never restarts it themselves. It reports
// whether the provider was stopped.
func (s *Service) SwitchToInstalledRelease(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error) {
	stopped, err := s.HibernateChatForVersionRefresh(ctx, id, minIdle)
	if err != nil || !stopped {
		return false, err
	}
	s.startBackgroundWake(id)
	return true, nil
}

// WaitForWake returns once no wake of id is in flight: the provider has
// started on the installed release and taken what queued meanwhile, or failed
// to. A move to a new release waits for it so it holds its concurrency permit
// through the provider's start, not only its stop.
func (s *Service) WaitForWake(ctx context.Context, id domain.SessionID) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.wakeMu.Lock()
		waking := s.backgroundWakes[id] || s.wakeRuns[id] != nil
		s.wakeMu.Unlock()
		if !waking {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// switchBeforeSend moves a session still on a replaced release to the
// installed one before it takes the user's next message, when it has been
// quiet long enough that its prompt cache is gone. Any work in flight, queued
// message or pending approval keeps the old release for this message.
func (s *Service) switchBeforeSend(ctx context.Context, record domain.SessionRecord) bool {
	if s.versionRefresh == nil || record.StaleBinary == nil || record.StaleBinary.SwitchFailedAt != nil || record.HibernatedAt != nil {
		return false
	}
	idle, enabled := s.versionRefresh()
	if !enabled {
		return false
	}
	stopped, err := s.hibernateChat(ctx, record.ID, hibernateOptions{versionRefresh: true, minIdle: max(idle, chatHibernateGrace), ignoreViews: true})
	if err != nil {
		s.log.Warn("could not stop chat to move to a new harness release", "session", record.ID, "err", err)
		return false
	}
	return stopped
}

func (s *Service) noteVersionSwitch(id domain.SessionID, version string) {
	s.versionSwitchMu.Lock()
	defer s.versionSwitchMu.Unlock()
	if s.versionSwitches == nil {
		s.versionSwitches = make(map[domain.SessionID]string)
	}
	s.versionSwitches[id] = version
}

// SetVersionSwitchObserver late-binds a callback told how each move of a
// Chat session to a new release ended ("succeeded" or "failed").
func (s *Service) SetVersionSwitchObserver(observer func(id domain.SessionID, version, result string)) {
	s.versionSwitchMu.Lock()
	defer s.versionSwitchMu.Unlock()
	s.versionSwitchObserver = observer
}

func (s *Service) observeVersionSwitch(id domain.SessionID, version, result string) {
	s.versionSwitchMu.Lock()
	observer := s.versionSwitchObserver
	s.versionSwitchMu.Unlock()
	if observer != nil {
		observer(id, version, result)
	}
}

func (s *Service) takeVersionSwitch(id domain.SessionID) (string, bool) {
	s.versionSwitchMu.Lock()
	defer s.versionSwitchMu.Unlock()
	version, ok := s.versionSwitches[id]
	delete(s.versionSwitches, id)
	return version, ok
}

// versionSwitchFailed handles a failed wake of a session stopped to move it to
// a new release. Its queued messages stay queued for the user's resume, and
// the session records the failure so the client can say what happened. It
// reports false for any other wake, which keeps the usual failure handling.
func (s *Service) versionSwitchFailed(ctx context.Context, id domain.SessionID, wakeErr error) bool {
	version, ok := s.takeVersionSwitch(id)
	if !ok {
		return false
	}
	s.observeVersionSwitch(id, version, "failed")
	s.log.Warn("version_switch_failed: chat session could not start on the new harness release; queued messages kept",
		"session", id, "version", version, "err", wakeErr)
	if store, ok := s.sessions.(versionSwitchStore); ok {
		if err := store.SetSessionVersionSwitchFailed(ctx, id, version, s.now()); err != nil {
			s.log.Warn("could not record version switch failure", "session", id, "err", err)
		}
	}
	return true
}
