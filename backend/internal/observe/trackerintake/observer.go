package trackerintake

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

const (
	DefaultTickInterval   = time.Minute
	DefaultFailureBackoff = 5 * time.Minute
	maxIntakePromptLen    = 16 << 10

	intakePromptTruncationNotice = "\n\n[Issue content truncated to fit the session prompt limit. Open the linked issue for the full details.]\n"
	intakePromptFooter           = "\nImplement the requested change in this repository, run the relevant checks, and open or update a pull request when ready."
)

type Store interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type Spawner interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
}

type TrackerResolver interface {
	Resolve(provider domain.TrackerProvider) (ports.Tracker, error)
}

type SingleTrackerResolver struct {
	Provider domain.TrackerProvider
	Adapter  ports.Tracker
}

func (s SingleTrackerResolver) Resolve(provider domain.TrackerProvider) (ports.Tracker, error) {
	if s.Adapter == nil {
		return nil, fmt.Errorf("tracker intake: no adapter for provider %q", provider)
	}
	if s.Provider == "" || provider == "" || provider == s.Provider {
		return s.Adapter, nil
	}
	return nil, fmt.Errorf("tracker intake: no adapter for provider %q", provider)
}

type Config struct {
	Tick           time.Duration
	FailureBackoff time.Duration
	Clock          func() time.Time
	Logger         *slog.Logger
}

type Observer struct {
	resolver       TrackerResolver
	store          Store
	spawner        Spawner
	tick           time.Duration
	failureBackoff time.Duration
	clock          func() time.Time
	logger         *slog.Logger
	backoffUntil   map[string]time.Time
}

func New(resolver TrackerResolver, store Store, spawner Spawner, cfg Config) *Observer {
	o := &Observer{resolver: resolver, store: store, spawner: spawner, tick: cfg.Tick, failureBackoff: cfg.FailureBackoff, clock: cfg.Clock, logger: cfg.Logger, backoffUntil: map[string]time.Time{}}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.failureBackoff <= 0 {
		o.failureBackoff = DefaultFailureBackoff
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "tracker intake")
}

func (o *Observer) Poll(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.resolver == nil || o.store == nil || o.spawner == nil {
		return nil
	}
	now := o.clock().UTC()
	projects, err := o.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	enabledProjects := make([]domain.ProjectRecord, 0, len(projects))
	for _, project := range projects {
		if project.Config.TrackerIntake.Enabled {
			enabledProjects = append(enabledProjects, project)
		}
	}
	if len(enabledProjects) == 0 {
		return nil
	}
	sessions, err := o.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	seen := seenIssueIDs(sessions)
	for _, project := range enabledProjects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if until, ok := o.backoffUntil[project.ID]; ok && now.Before(until) {
			o.logger.Debug("tracker intake: project in failure backoff", "project", project.ID, "until", until)
			continue
		}
		if failed := o.pollProject(ctx, project, seen); failed {
			o.backoffUntil[project.ID] = now.Add(o.failureBackoff)
		} else {
			delete(o.backoffUntil, project.ID)
		}
	}
	return nil
}

func (o *Observer) pollProject(ctx context.Context, project domain.ProjectRecord, seen map[domain.IssueID]bool) (failed bool) {
	cfg := project.Config.TrackerIntake.WithDefaults()
	if !cfg.Enabled {
		return false
	}
	if err := cfg.Validate(); err != nil {
		o.logger.Warn("tracker intake: skipping project with invalid config", "project", project.ID, "err", err)
		return true
	}
	repo, ok := trackerRepo(project, cfg)
	if !ok {
		o.logger.Warn("tracker intake: skipping project without tracker scope", "project", project.ID, "provider", cfg.Provider, "origin", project.RepoOriginURL)
		return true
	}
	tracker, err := o.resolver.Resolve(cfg.Provider)
	if err != nil {
		o.logger.Warn("tracker intake: no adapter for provider", "project", project.ID, "provider", cfg.Provider, "err", err)
		return true
	}
	issues, err := tracker.List(ctx, repo, domain.ListFilter{
		State:    domain.ListOpen,
		Assignee: cfg.Assignee,
	})
	if err != nil {
		o.logger.Error("tracker intake: list issues failed", "project", project.ID, "repo", repo.Native, "err", err)
		return true
	}
	var spawnFailed bool
	for _, issue := range issues {
		if ctx.Err() != nil {
			return true
		}
		if issue.State != domain.IssueOpen {
			continue
		}
		if !issueMatchesConfig(issue, cfg) {
			continue
		}
		issueID := CanonicalIssueID(issue.ID)
		if issueID == "" || seen[issueID] {
			continue
		}
		if _, _, _, err := o.spawner.Spawn(ctx, ports.SpawnConfig{
			ProjectID: domain.ProjectID(project.ID),
			IssueID:   issueID,
			Kind:      domain.KindWorker,
			Prompt:    BuildIssuePrompt(issue),
			CreatedBy: domain.SessionCreatedByAutomation,
		}); err != nil {
			o.logger.Error("tracker intake: spawn issue session failed", "project", project.ID, "issue", issueID, "err", err)
			if kind, code := telemetrymeta.ErrorKindAndCode(err); kind == "internal" || sentryobs.IsFaultCode(code) {
				sentryobs.CaptureBackground(ctx, err, "trackerintake", "tracker_spawn", nil)
			}
			spawnFailed = true
			continue
		}
		seen[issueID] = true
	}
	return spawnFailed
}

func issueMatchesConfig(issue domain.Issue, cfg domain.TrackerIntakeConfig) bool {
	assignee := strings.TrimSpace(cfg.Assignee)
	switch {
	case assignee == "":
		return true
	case assignee == "*":
		return len(issue.Assignees) > 0
	case strings.EqualFold(assignee, "none"):
		return len(issue.Assignees) == 0
	default:
		return containsFold(issue.Assignees, assignee)
	}
}

func containsFold(values []string, needle string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}

func seenIssueIDs(sessions []domain.SessionRecord) map[domain.IssueID]bool {
	seen := make(map[domain.IssueID]bool, len(sessions))
	for _, sess := range sessions {
		if sess.IssueID != "" && !sess.IsTerminated {
			seen[sess.IssueID] = true
		}
	}
	return seen
}

func CanonicalIssueID(id domain.TrackerID) domain.IssueID {
	provider := id.Provider
	if provider == "" {
		provider = domain.TrackerProviderGitHub
	}
	native := strings.TrimSpace(id.Native)
	if native == "" {
		return ""
	}
	host := strings.TrimSpace(id.Host)
	if host != "" {
		return domain.IssueID(string(provider) + ":" + native + "@" + host)
	}
	return domain.IssueID(string(provider) + ":" + native)
}

func BuildIssuePrompt(issue domain.Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Work on tracker issue %s.\n\n", CanonicalIssueID(issue.ID))
	if issue.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", issue.Title)
	}
	if issue.URL != "" {
		fmt.Fprintf(&b, "URL: %s\n", issue.URL)
	}
	if len(issue.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(issue.Labels, ", "))
	}
	if len(issue.Assignees) > 0 {
		fmt.Fprintf(&b, "Assignees: %s\n", strings.Join(issue.Assignees, ", "))
	}
	body := strings.TrimSpace(issue.Body)
	if body != "" {
		fmt.Fprintf(&b, "\nBody:\n%s\n", body)
	}
	b.WriteString(intakePromptFooter)
	return capIntakePrompt(b.String())
}

func capIntakePrompt(prompt string) string {
	if len(prompt) <= maxIntakePromptLen {
		return prompt
	}
	prefix := strings.TrimSuffix(prompt, intakePromptFooter)
	prefixBudget := maxIntakePromptLen - len(intakePromptTruncationNotice) - len(intakePromptFooter)
	if prefixBudget <= 0 {
		return truncateUTF8(prompt, maxIntakePromptLen)
	}
	return truncateUTF8(prefix, prefixBudget) + intakePromptTruncationNotice + intakePromptFooter
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		cut = i
	}
	return s[:cut]
}

func trackerRepo(project domain.ProjectRecord, cfg domain.TrackerIntakeConfig) (domain.TrackerRepo, bool) {
	provider := cfg.Provider
	if provider == "" {
		provider = domain.InferTrackerProvider(project.RepoOriginURL)
	}
	native := strings.TrimSpace(cfg.Repo)
	if native == "" {
		native, _ = parseRepoNative(project.RepoOriginURL, provider)
	}
	if native == "" {
		return domain.TrackerRepo{}, false
	}
	host := repoHostFromOrigin(project.RepoOriginURL, provider)
	return domain.TrackerRepo{Provider: provider, Native: native, Host: host}, true
}

func parseRepoNative(remote string, provider domain.TrackerProvider) (string, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", false
	}
	if strings.HasPrefix(remote, "git@") {
		if _, rest, ok := strings.Cut(remote, ":"); ok {
			return cleanRepoPath(rest, provider), true
		}
		return "", false
	}
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		if provider == domain.TrackerProviderGitHub {
			host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
			if host == "github.com" || strings.HasSuffix(host, ".github.com") || strings.HasSuffix(host, ".ghe.io") {
				return cleanRepoPath(u.Path, provider), true
			}
			return "", false
		}
		return cleanRepoPath(u.Path, provider), true
	}
	return cleanRepoPath(remote, provider), true
}

func repoHostFromOrigin(remote string, provider domain.TrackerProvider) string {
	if provider != domain.TrackerProviderGitLab {
		return ""
	}
	host := hostFromRemote(remote)
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "gitlab.com" || host == "www.gitlab.com" {
		return ""
	}
	return host
}

func hostFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if strings.HasPrefix(remote, "git@") {
		rest := strings.TrimPrefix(remote, "git@")
		if colonIdx := strings.Index(rest, ":"); colonIdx > 0 {
			return rest[:colonIdx]
		}
		return ""
	}
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}

func cleanRepoPath(path string, provider domain.TrackerProvider) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	if provider == domain.TrackerProviderGitLab {
		for _, p := range parts {
			if strings.TrimSpace(p) == "" {
				return ""
			}
		}
		return strings.Join(parts, "/")
	}
	owner := strings.TrimSpace(parts[len(parts)-2])
	repo := strings.TrimSpace(parts[len(parts)-1])
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}
