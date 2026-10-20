package chat

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type launchRefreshStore interface {
	BeginSessionLaunchRefresh(context.Context, domain.SessionID, int64, time.Time) (bool, error)
	SessionLaunchRefreshPending(context.Context, domain.SessionID) (bool, error)
	ClearSessionLaunchRefresh(context.Context, domain.SessionID) error
}

func (s *Service) launchRefreshPending(ctx context.Context, id domain.SessionID) (bool, error) {
	marker, ok := s.sessions.(launchRefreshStore)
	if !ok {
		return false, nil
	}
	return marker.SessionLaunchRefreshPending(ctx, id)
}

func providerNeedsLaunchRefresh(controller *Controller) bool {
	refresher, ok := controller.conv.(ports.ChatProviderLaunchRefresher)
	return ok && refresher.NeedsLaunchRefresh()
}

// refreshProviderLaunch adopts old hosts first, including their in-flight work,
// then replaces only a quiescent provider. An idle sweep respects open views;
// a new message may refresh under its view before entering the durable queue.
func (s *Service) refreshProviderLaunch(ctx context.Context, id domain.SessionID, ignoreViews bool) (bool, error) {
	controller, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !providerNeedsLaunchRefresh(controller) {
		return false, nil
	}
	stopped, err := s.hibernateChat(ctx, id, hibernateOptions{launchRefresh: true, ignoreViews: ignoreViews})
	if err != nil || !stopped {
		return false, err
	}
	return true, nil
}
