// Package mentionspawn starts an agent session for a pull request nobody in AO
// owns yet, when a human addresses the agent in a comment under it.
//
// The SCM observer already hands "@ao" comments to the session that owns a PR.
// A PR opened by a person has no such session, so the comment went nowhere —
// the human had to `ao spawn --branch … --claim-pr …` by hand first. This loop
// does that step for them: it finds the comment, checks the author may push to
// the repo, starts a worker on the PR's branch and claims the PR for it. From
// there the existing mention delivery pastes the comment into the new session,
// exactly as it would for a PR the agent opened itself.
//
// The loop is opt-in (AO_PR_MENTION_SPAWN): starting agents on other people's
// branches is a policy decision, not a default.
package mentionspawn

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// DefaultTickInterval matches the cadence the SCM observer reads PR
	// timelines at, so a comment is answered about as fast either way.
	DefaultTickInterval = time.Minute
	// DefaultLookback bounds how old a comment may be and still start work. It
	// matches the timeline window mention delivery reads, so the comment that
	// started the session is still there for delivery to paste.
	DefaultLookback = 24 * time.Hour
	// DefaultStartupGrace is how far before daemon start a comment may have
	// been posted and still count. Without it, turning the feature on would
	// act on every "@ao" left in the past day — instructions people may well
	// have given up on. With it, a comment posted while the daemon restarted
	// is still picked up.
	DefaultStartupGrace = 15 * time.Minute
	// DefaultMaxSpawnsPerPoll caps how many sessions one poll may start, so a
	// burst of comments cannot fan out into a burst of agents.
	DefaultMaxSpawnsPerPoll = 3
	// permissionTTL is how long a push-permission answer is reused. Roles change
	// rarely; a comment thread does not need one API call per message.
	permissionTTL = 10 * time.Minute
	// maxAttempts bounds retries for a comment whose spawn keeps failing on
	// something transient (a fetch, the workspace); past it the comment is
	// dropped instead of retried every poll forever.
	maxAttempts = 3
)

// Provider is the SCM surface the loop needs.
type Provider interface {
	ParseRepository(remote string) (ports.SCMRepo, bool)
	ListRepoMentions(ctx context.Context, repo ports.SCMRepo, since time.Time) ([]ports.SCMRepoMention, error)
	FetchPullHead(ctx context.Context, ref ports.SCMPRRef) (ports.SCMPRObservation, error)
	CanPush(ctx context.Context, repo ports.SCMRepo, login string) (bool, error)
}

// Store is the durable read surface the loop needs.
type Store interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Sessions starts the worker and attaches the PR to it.
type Sessions interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	// ClaimPR attaches the PR, taking it over from a terminated owner if one
	// is still on record.
	ClaimPR(ctx context.Context, id domain.SessionID, prURL string) error
}

// BranchFetcher makes the PR's head branch known to the project checkout
// before the worktree is created. Without it a branch nobody fetched yet does
// not resolve, and the worktree would quietly start from the default branch —
// the agent would then push an unrelated history onto the human's PR.
type BranchFetcher interface {
	FetchBranch(ctx context.Context, repoPath, branch string) error
}

// Announcer receives human-facing events. Optional.
type Announcer interface {
	Announce(text string)
}

// Config holds optional knobs. Zero values use the defaults above.
type Config struct {
	Tick             time.Duration
	Lookback         time.Duration
	StartupGrace     time.Duration
	MaxSpawnsPerPoll int
	Clock            func() time.Time
	Logger           *slog.Logger
	Announcer        Announcer
}

// Observer is the polling loop.
type Observer struct {
	provider  Provider
	store     Store
	sessions  Sessions
	fetcher   BranchFetcher
	announcer Announcer
	logger    *slog.Logger
	clock     func() time.Time

	tick      time.Duration
	lookback  time.Duration
	maxSpawns int
	// notBefore is the oldest comment the loop will act on: daemon start minus
	// the startup grace.
	notBefore time.Time

	// done remembers comments already acted on or rejected, so each is
	// considered once per daemon life. Across restarts the store answers the
	// same question: a PR whose session was created after the comment has
	// already been through here.
	done     map[string]bool
	attempts map[string]int
	perms    map[string]permission
}

type permission struct {
	allowed bool
	at      time.Time
}

// New constructs the loop.
func New(provider Provider, store Store, sessions Sessions, fetcher BranchFetcher, cfg Config) *Observer {
	o := &Observer{
		provider: provider, store: store, sessions: sessions, fetcher: fetcher,
		announcer: cfg.Announcer, logger: cfg.Logger, clock: cfg.Clock,
		tick: cfg.Tick, lookback: cfg.Lookback, maxSpawns: cfg.MaxSpawnsPerPoll,
		done: map[string]bool{}, attempts: map[string]int{}, perms: map[string]permission{},
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.lookback <= 0 {
		o.lookback = DefaultLookback
	}
	if o.maxSpawns <= 0 {
		o.maxSpawns = DefaultMaxSpawnsPerPoll
	}
	grace := cfg.StartupGrace
	if grace <= 0 {
		grace = DefaultStartupGrace
	}
	o.notBefore = o.clock().Add(-grace)
	return o
}

// Start launches the loop; the first poll runs immediately.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "mention spawn")
}

// target is one repository the loop scans, with the project that owns it.
type target struct {
	repo    ports.SCMRepo
	project domain.ProjectRecord
}

// Poll runs one pass. Only a store failure is returned; provider and spawn
// failures are logged per repo or comment so one bad PR does not stall the rest.
func (o *Observer) Poll(ctx context.Context) error {
	if o.provider == nil || o.store == nil || o.sessions == nil {
		return nil
	}
	targets, err := o.targets(ctx)
	if err != nil {
		return err
	}
	now := o.clock()
	since := now.Add(-o.lookback)
	if since.Before(o.notBefore) {
		since = o.notBefore
	}
	budget := o.maxSpawns
	for _, t := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mentions, err := o.provider.ListRepoMentions(ctx, t.repo, since)
		if err != nil {
			o.logger.Warn("mention spawn: repo comments unreadable", "repo", t.repo.Repo, "err", err)
			continue
		}
		for _, m := range newestPerPR(mentions) {
			if budget == 0 {
				o.logger.Info("mention spawn: per-poll cap reached, the rest waits for the next poll", "max", o.maxSpawns)
				return nil
			}
			if o.consider(ctx, t, m) {
				budget--
			}
		}
	}
	return nil
}

// targets lists the repositories registered projects push to. Two projects on
// the same repository would make the owner of a new session ambiguous; the
// first by id wins and the clash is logged.
func (o *Observer) targets(ctx context.Context) ([]target, error) {
	projects, err := o.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	seen := map[string]string{}
	var out []target
	for _, p := range projects {
		if !p.ArchivedAt.IsZero() || p.Kind.WithDefault() == domain.ProjectKindScratch || strings.TrimSpace(p.Path) == "" {
			continue
		}
		repo, ok := o.provider.ParseRepository(p.RepoOriginURL)
		if !ok {
			continue
		}
		key := strings.ToLower(repo.Host + "/" + repo.Repo)
		if owner, dup := seen[key]; dup {
			o.logger.Warn("mention spawn: repo registered by several projects, using the first", "repo", repo.Repo, "used", owner, "ignored", p.ID)
			continue
		}
		seen[key] = p.ID
		out = append(out, target{repo: repo, project: p})
	}
	return out, nil
}

// newestPerPR keeps one comment per PR — the newest. That is the one mention
// delivery will paste, and starting one session per comment would be wrong.
func newestPerPR(mentions []ports.SCMRepoMention) []ports.SCMRepoMention {
	byPR := map[int]ports.SCMRepoMention{}
	for _, m := range mentions {
		cur, ok := byPR[m.PRNumber]
		if !ok || m.Mention.CreatedAt.After(cur.Mention.CreatedAt) ||
			(m.Mention.CreatedAt.Equal(cur.Mention.CreatedAt) && m.Mention.ID > cur.Mention.ID) {
			byPR[m.PRNumber] = m
		}
	}
	out := make([]ports.SCMRepoMention, 0, len(byPR))
	for _, m := range byPR {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mention.CreatedAt.Before(out[j].Mention.CreatedAt) })
	return out
}

// consider decides one comment and reports whether a session was started.
func (o *Observer) consider(ctx context.Context, t target, m ports.SCMRepoMention) bool {
	id := m.Mention.ID
	if o.done[id] || m.Mention.CreatedAt.Before(o.notBefore) {
		return false
	}
	log := o.logger.With("repo", t.repo.Repo, "pr", m.PRURL, "comment", id, "author", m.Mention.Author)

	owned, err := o.alreadyOwned(ctx, m)
	if err != nil {
		log.Warn("mention spawn: could not read PR ownership", "err", err)
		return false
	}
	if owned {
		// A live session gets the comment from mention delivery; a session
		// created after the comment already answered it.
		o.done[id] = true
		return false
	}

	allowed, err := o.canPush(ctx, t.repo, m.Mention.Author)
	if err != nil {
		log.Warn("mention spawn: push permission unreadable", "err", err)
		return false
	}
	if !allowed {
		log.Info("mention spawn: author cannot push to the repo, not starting an agent")
		o.done[id] = true
		return false
	}

	pull, err := o.provider.FetchPullHead(ctx, ports.SCMPRRef{Repo: t.repo, Number: m.PRNumber, URL: m.PRURL})
	if err != nil {
		log.Warn("mention spawn: PR unreadable", "err", err)
		return false
	}
	if reason := unworkable(pull, t.repo); reason != "" {
		log.Info("mention spawn: not starting an agent", "reason", reason)
		o.done[id] = true
		return false
	}

	if ok := o.start(ctx, t, m, pull, log); !ok {
		o.attempts[id]++
		if o.attempts[id] >= maxAttempts {
			log.Error("mention spawn: giving up on this comment", "attempts", o.attempts[id])
			o.done[id] = true
		}
		return false
	}
	o.done[id] = true
	return true
}

// alreadyOwned reports whether the PR has a live session, or had one started
// at or after the comment was posted.
func (o *Observer) alreadyOwned(ctx context.Context, m ports.SCMRepoMention) (bool, error) {
	pr, ok, err := o.store.GetPR(ctx, m.PRURL)
	if err != nil || !ok || pr.SessionID == "" {
		return false, err
	}
	sess, ok, err := o.store.GetSession(ctx, pr.SessionID)
	if err != nil || !ok {
		return false, err
	}
	if !sess.IsTerminated {
		return true, nil
	}
	return !sess.CreatedAt.Before(m.Mention.CreatedAt), nil
}

func (o *Observer) canPush(ctx context.Context, repo ports.SCMRepo, login string) (bool, error) {
	key := strings.ToLower(repo.Repo + "|" + login)
	if p, ok := o.perms[key]; ok && o.clock().Sub(p.at) < permissionTTL {
		return p.allowed, nil
	}
	allowed, err := o.provider.CanPush(ctx, repo, login)
	if err != nil {
		return false, err
	}
	o.perms[key] = permission{allowed: allowed, at: o.clock()}
	return allowed, nil
}

// unworkable names why the PR cannot take an agent, or returns "".
func unworkable(pull ports.SCMPRObservation, repo ports.SCMRepo) string {
	switch {
	case pull.Merged || pull.Closed:
		return "PR is not open"
	case pull.Draft:
		// Claiming refuses drafts; spawning first would leave an orphan.
		return "PR is a draft"
	case strings.TrimSpace(pull.SourceBranch) == "":
		return "PR has no head branch"
	case !strings.EqualFold(strings.TrimSpace(pull.HeadRepo), repo.Repo):
		// The branch lives in a fork AO cannot push to.
		return "PR head branch is in another repository"
	}
	return ""
}

func (o *Observer) start(ctx context.Context, t target, m ports.SCMRepoMention, pull ports.SCMPRObservation, log *slog.Logger) bool {
	branch := strings.TrimSpace(pull.SourceBranch)
	if o.fetcher != nil {
		if err := o.fetcher.FetchBranch(ctx, t.project.Path, branch); err != nil {
			log.Warn("mention spawn: could not fetch the PR branch", "branch", branch, "err", err)
			return false
		}
	}
	session, _, _, err := o.sessions.Spawn(ctx, ports.SpawnConfig{
		ProjectID:   domain.ProjectID(t.project.ID),
		Kind:        domain.KindWorker,
		Branch:      branch,
		Prompt:      BuildPrompt(m, pull, branch),
		DisplayName: displayName(m.PRNumber),
		// The work ends with the PR; leaving the worktree behind would only
		// pile up checkouts of branches people merged long ago.
		TerminateOnPRMerge: true,
	})
	if err != nil {
		log.Error("mention spawn: spawn failed", "branch", branch, "err", err)
		return false
	}
	if err := o.sessions.ClaimPR(ctx, session.ID, m.PRURL); err != nil {
		// The session exists and sits on the right branch, so it is kept: a
		// human can claim by hand, and retrying would start a second agent.
		log.Error("mention spawn: session started but PR claim failed", "session", session.ID, "err", err)
		o.announce(fmt.Sprintf("⚠️ @%s позвал агента в %s, сессия %s запущена, но PR к ней не привязался: %v", m.Mention.Author, m.PRURL, session.ID, err))
		return true
	}
	log.Info("mention spawn: agent started for PR", "session", session.ID, "branch", branch)
	o.announce(fmt.Sprintf("🤖 @%s позвал агента в %s\nсессия: %s · проект: %s · ветка: %s", m.Mention.Author, m.PRURL, session.ID, t.project.ID, branch))
	return true
}

func (o *Observer) announce(text string) {
	if o.announcer != nil {
		o.announcer.Announce(text)
	}
}

// displayName fits the sidebar's 20-character limit.
func displayName(number int) string {
	return fmt.Sprintf("pr-%d", number)
}

// BuildPrompt is the session's first message. It does not repeat the comment:
// mention delivery pastes that as the next message, the same way it would for
// a PR the agent opened, so the instruction arrives exactly once.
func BuildPrompt(m ports.SCMRepoMention, pull ports.SCMPRObservation, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You were called into pull request %s", m.PRURL)
	if title := strings.TrimSpace(pull.Title); title != "" {
		fmt.Fprintf(&b, " (%q)", title)
	}
	fmt.Fprintf(&b, " by @%s. A person opened this PR, not AO; its branch is `%s`.\n\n", m.Mention.Author, branch)
	b.WriteString("Before changing anything:\n")
	fmt.Fprintf(&b, "1. Sync with the PR: `git fetch origin %s && git merge --ff-only origin/%s`. If that is not a fast-forward, stop and report — do not rewrite the branch.\n", branch, branch)
	b.WriteString("2. Read the PR description and the discussion under it (`gh pr view --comments`).\n")
	b.WriteString("3. Commit on this branch and push to it; do not open a new PR. Other people may push here too, so keep commits small and never force-push.\n\n")
	b.WriteString("The comment that called you arrives as the next message. Do what it asks, then reply under the PR with what you did.")
	return b.String()
}
