package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	trackergitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	trackermulti "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/multi"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func newGitHubTracker() (ports.Tracker, error) {
	return trackergithub.New(trackergithub.Options{Token: &ghTokenSource{}})
}

// githubTenantTrackerOptions configures the GitHub tracker of one GHE.com
// tenant: its API base and only that host's gh login.
func githubTenantTrackerOptions(host string) (trackergithub.Options, error) {
	base, ok := domain.GHECloudAPIBase(host)
	if !ok {
		return trackergithub.Options{}, fmt.Errorf("github tracker: %q is not a GHE.com tenant host", host)
	}
	return trackergithub.Options{Token: githubHostToken(host), BaseURL: base, Host: host}, nil
}

func newGitHubTenantTracker(host string) (ports.Tracker, error) {
	opts, err := githubTenantTrackerOptions(host)
	if err != nil {
		return nil, err
	}
	return trackergithub.New(opts)
}

// githubHostTracker serves the "github" tracker key: host "" (github.com) goes
// to def, each GHE.com tenant host to its own lazily built tracker.
type githubHostTracker struct {
	def    ports.Tracker
	build  func(host string) (ports.Tracker, error)
	logger *slog.Logger

	mu     sync.Mutex
	byHost map[string]ports.Tracker
}

func (g *githubHostTracker) forHost(host string) ports.Tracker {
	if host == "" {
		return g.def
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if t := g.byHost[host]; t != nil {
		return t
	}
	if g.byHost == nil {
		g.byHost = map[string]ports.Tracker{}
	}
	t := &lazyTracker{name: "github " + host, logger: g.logger, build: func() (ports.Tracker, error) { return g.build(host) }}
	g.byHost[host] = t
	return t
}

func (g *githubHostTracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	return g.forHost(id.Host).Get(ctx, id)
}

func (g *githubHostTracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	return g.forHost(repo.Host).List(ctx, repo, filter)
}

func (g *githubHostTracker) Preflight(ctx context.Context) error { return g.def.Preflight(ctx) }

var _ ports.Tracker = (*githubHostTracker)(nil)

// ghTokenSource mirrors the SCM credential precedence: AO_GITHUB_TOKEN →
// GITHUB_TOKEN (via EnvTokenSource) → `gh auth token` CLI fallback with
// short-lived caching. This matches the old lazyGitHubTracker's token chain
// and the GitLab tracker's DefaultTokenSource (env → glab CLI).
type ghTokenSource struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

const (
	ghTokenCacheTTL       = 5 * time.Minute
	ghTokenCommandTimeout = 5 * time.Second
)

func (s *ghTokenSource) Token(ctx context.Context) (string, error) {
	env := trackergithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}}
	if tok, err := env.Token(ctx); err == nil {
		return tok, nil
	} else if !errors.Is(err, trackergithub.ErrNoToken) {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.token != "" && now.Before(s.expiresAt) {
		return s.token, nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, ghTokenCommandTimeout)
	defer cancel()
	out, err := aoprocess.CommandContext(cmdCtx, "gh", "auth", "token").Output()
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", trackergithub.ErrNoToken
	}
	s.token = token
	s.expiresAt = now.Add(ghTokenCacheTTL)
	return token, nil
}

// newGitLabTracker constructs a host-aware GitLab tracker. AllowedHosts and
// HostTokens from GitLabConfig are passed through so the tracker can route
// self-managed GitLab issue lookups to the correct host with the correct
// token. This mirrors the SCM provider's wiring in newGitLabSCMProvider.
func newGitLabTracker(gitlabCfg config.GitLabConfig) (ports.Tracker, error) {
	hostTokens := make(map[string]scmgitlab.TokenSource, len(gitlabCfg.HostTokens))
	for host, token := range gitlabCfg.HostTokens {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			hostTokens[host] = scmgitlab.StaticTokenSource(token)
		}
	}
	return trackergitlab.New(trackergitlab.Options{
		Token:        trackergitlab.DefaultTokenSource(),
		AllowedHosts: gitlabCfg.AllowedHosts,
		HostTokens:   hostTokens,
	})
}

// newMultiTracker builds a multi-tracker dispatching to both GitHub and
// GitLab sub-trackers. The daemon builds it once (in Run) and shares the
// instance between the session service and the intake observer. A GitHub
// token from the environment is validated eagerly at boot; without one, the
// GitHub adapter stays lazy so the `gh auth token` CLI probe never blocks
// daemon readiness — no CLI token is resolved until a session spawn or an
// intake poll actually needs GitHub issue data (see lazyTracker). When one
// tracker's token is missing, the other still serves issue lookups — the
// same degrade-gracefully pattern used by newMultiSCMProvider. Callers must
// tolerate a nil ports.Tracker (the session service's nil-guard handles
// this).
func newMultiTracker(gitlabCfg config.GitLabConfig, logger *slog.Logger) ports.Tracker {
	var named []trackermulti.NamedTracker

	// Probing the environment is a cheap read — no subprocess. Only when no
	// env token exists do we defer to the gh CLI fallback, lazily.
	env := trackergithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}}
	var github ports.Tracker = &lazyTracker{name: "github", logger: logger, build: newGitHubTracker}
	if _, err := env.Token(context.Background()); err == nil {
		if t, err := newGitHubTracker(); err != nil {
			logTrackerDisabled(logger, "github", err)
		} else {
			github = t
		}
	}
	// GHE.com tenants use their own gh login, so they stay available even
	// when the github.com tracker cannot be built.
	named = append(named, trackermulti.NamedTracker{Key: "github", Tracker: &githubHostTracker{def: github, build: newGitHubTenantTracker, logger: logger}})

	if t, err := newGitLabTracker(gitlabCfg); err != nil {
		logTrackerDisabled(logger, "gitlab", err)
	} else {
		named = append(named, trackermulti.NamedTracker{Key: "gitlab", Tracker: t})
	}

	if len(named) == 0 {
		return nil
	}
	return trackermulti.New(named...)
}

// lazyTracker defers adapter construction until the first tracker call, so
// daemon readiness is not blocked by credential probing or a gh CLI call: no
// token is resolved until a session spawn or an enabled project poll actually
// needs issue data. Construction succeeds at most once; failures are not
// cached, so a user who authenticates later (e.g. `gh auth login`) is picked
// up without a daemon restart — the same retry semantics the old intake-only
// lazyGitHubTracker had.
type lazyTracker struct {
	name   string
	logger *slog.Logger
	build  func() (ports.Tracker, error)

	mu      sync.Mutex
	tracker ports.Tracker
}

func (t *lazyTracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return domain.Issue{}, err
	}
	return tracker.Get(ctx, id)
}

func (t *lazyTracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return nil, err
	}
	return tracker.List(ctx, repo, filter)
}

func (t *lazyTracker) Preflight(ctx context.Context) error {
	tracker, err := t.resolve()
	if err != nil {
		return err
	}
	return tracker.Preflight(ctx)
}

func (t *lazyTracker) resolve() (ports.Tracker, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tracker != nil {
		return t.tracker, nil
	}
	tracker, err := t.build()
	if err != nil {
		if t.logger != nil {
			logTrackerDisabled(t.logger, t.name, err)
		}
		return nil, err
	}
	t.tracker = tracker
	return tracker, nil
}

var _ ports.Tracker = (*lazyTracker)(nil)

func logTrackerDisabled(logger *slog.Logger, provider string, err error) {
	if errors.Is(err, trackergithub.ErrNoToken) || errors.Is(err, trackergitlab.ErrNoToken) {
		logger.Warn("tracker disabled: no usable token", "provider", provider, "err", err)
	} else {
		logger.Warn("tracker disabled: setup failed", "provider", provider, "err", err)
	}
}
