package chat

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type versionSwitchStore interface {
	SetSessionVersionSwitchFailed(ctx context.Context, id domain.SessionID, version string, at time.Time) error
}

func (s *Service) SwitchToInstalledRelease(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error) {
	stopped, err := s.HibernateChatForVersionRefresh(ctx, id, minIdle)
	if err != nil || !stopped {
		return false, err
	}
	s.startBackgroundWake(id)
	return true, nil
}

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
