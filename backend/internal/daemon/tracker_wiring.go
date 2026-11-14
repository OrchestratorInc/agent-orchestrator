package daemon

import (
	"context"
	"errors"
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

func newMultiTracker(gitlabCfg config.GitLabConfig, logger *slog.Logger) ports.Tracker {
	var named []trackermulti.NamedTracker

	env := trackergithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}}
	if _, err := env.Token(context.Background()); err == nil {
		if t, err := newGitHubTracker(); err != nil {
			logTrackerDisabled(logger, "github", err)
		} else {
			named = append(named, trackermulti.NamedTracker{Key: "github", Tracker: t})
		}
	} else {
		named = append(named, trackermulti.NamedTracker{Key: "github", Tracker: &lazyTracker{name: "github", logger: logger, build: newGitHubTracker}})
	}

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
