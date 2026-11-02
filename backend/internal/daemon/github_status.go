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
	// githubLoginTimeout bounds the username lookup, so a slow GitHub never
	// holds up the status check that asks for it.
	githubLoginTimeout = 2 * time.Second
	// githubLoginMissTTL is how long a failed lookup is remembered. The SCM
	// provider caches only a username it found, so without this a machine with
	// no gh sign-in would run `gh auth token` on every status request.
	githubLoginMissTTL = 30 * time.Second
)

// githubStatusService is the GitHub token service plus the username AO is
// signed in as. The username comes from the SCM provider, which already
// resolves it from the same sources the observer uses (AO_GITHUB_TOKEN, then
// the gh CLI), so a user signed in through gh has one without storing a token.
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

// Login is best effort: a missing provider or a failed lookup is no username.
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
