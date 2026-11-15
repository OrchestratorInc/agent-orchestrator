package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/multi"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/githubpat"
)

const (
	githubLoginTimeout = 2 * time.Second
	githubLoginMissTTL = 30 * time.Second
)

type githubStatusService struct {
	*githubpat.Service
	identity ports.ScopedIdentityResolver
	now      func() time.Time

	mu        sync.Mutex
	missUntil time.Time
}

func newGitHubStatusService(svc *githubpat.Service, provider *multi.Provider) *githubStatusService {
	s := &githubStatusService{Service: svc, now: time.Now}
	if provider != nil {
		s.identity = provider
	}
	return s
}

func (s *githubStatusService) Login(ctx context.Context) string {
	if s.identity == nil {
		return ""
	}
	s.mu.Lock()
	missed := s.now().Before(s.missUntil)
	s.mu.Unlock()
	if missed {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, githubLoginTimeout)
	defer cancel()
	identity, err := s.identity.AuthenticatedIdentityForProvider(ctx, "github", "")
	if err != nil || !identity.Human || identity.Login == "" {
		s.mu.Lock()
		s.missUntil = s.now().Add(githubLoginMissTTL)
		s.mu.Unlock()
		return ""
	}
	return identity.Login
}
