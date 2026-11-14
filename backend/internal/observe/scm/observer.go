package scm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	DefaultTickInterval     = 30 * time.Second
	DefaultReviewInterval   = 2 * time.Minute
	DefaultCacheMax         = 512
	DefaultPRMaxAge         = 5 * time.Minute
	BatchSize               = 25
	DefaultLifecycleTimeout = 30 * time.Second
)

func identityKey(provider, host string) string {
	if provider == "github" {
		return provider
	}
	return provider + "|" + host
}

type Provider interface {
	ParseRepository(remote string) (ports.SCMRepo, bool)
	RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error)
	ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error)
	CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error)
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
	FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error)
	FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)
}

type Store interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	UpsertProject(ctx context.Context, row domain.ProjectRecord) error
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
	ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error)
	ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error)
	PRDetailsUpdatedAt(ctx context.Context, prURL string) (time.Time, bool, error)
	WriteSCMObservation(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode) error
	RecordRepositoryRename(ctx context.Context, r domain.RepositoryRename) error
}

type Lifecycle interface {
	ApplySCMObservation(ctx context.Context, sessionID domain.SessionID, obs ports.SCMObservation) error
}

type credentialChecker interface {
	SCMCredentialsAvailable(ctx context.Context) (bool, error)
}

type rateLimitedError interface {
	GetRetryAfter() time.Duration
	GetResetAt() time.Time
}

func rateLimitCooldown(now time.Time, err error) (time.Duration, bool) {
	var rl rateLimitedError
	if !errors.As(err, &rl) {
		return 0, false
	}
	if ra := rl.GetRetryAfter(); ra > 0 {
		return boundedCooldown(ra), true
	}
	if reset := rl.GetResetAt(); !reset.IsZero() && reset.After(now) {
		return boundedCooldown(reset.Sub(now)), true
	}
	return defaultRateLimitCooldown(now), true
}

const (
	minRateLimitCooldown        = 30 * time.Second
	maxRateLimitCooldown        = 10 * time.Minute
	incrementalDiscoveryOverlap = 5 * time.Minute
)

func boundedCooldown(d time.Duration) time.Duration {
	if d < minRateLimitCooldown {
		d = minRateLimitCooldown
	}
	if d > maxRateLimitCooldown {
		d = maxRateLimitCooldown
	}
	return d
}

func defaultRateLimitCooldown(now time.Time) time.Duration {
	base := 60 * time.Second
	jitter := time.Duration(now.UnixNano()%int64(15*time.Second)) - 7*time.Second
	return boundedCooldown(base + jitter)
}

type Config struct {
	Tick                   time.Duration
	ReviewInterval         time.Duration
	Clock                  func() time.Time
	Logger                 *slog.Logger
	CacheMax               int
	ScopedIdentityResolver ports.ScopedIdentityResolver
	BranchStates           BranchStateReconciler
}

type BranchStateReconciler interface {
	ReconcileBranchStates(ctx context.Context) error
}

type ObserverCache struct {
	RepoPRListETag      map[string]string
	CommitChecksETag    map[string]string
	LastReviewPollAt    map[string]time.Time
	ReviewRefreshFailed map[string]bool
	LastPRFetchAt       map[string]time.Time
	lastPRFetchOrder    []string
	LastSyncCursor      map[string]time.Time
	repoOrder           []string
	commitOrder         []string
	lastReviewPollOrder []string
	reviewFailedOrder   []string
	max                 int
}

func newCache(maxEntries int) ObserverCache {
	if maxEntries <= 0 {
		maxEntries = DefaultCacheMax
	}
	return ObserverCache{
		RepoPRListETag:      map[string]string{},
		CommitChecksETag:    map[string]string{},
		LastReviewPollAt:    map[string]time.Time{},
		ReviewRefreshFailed: map[string]bool{},
		LastPRFetchAt:       map[string]time.Time{},
		LastSyncCursor:      map[string]time.Time{},
		max:                 maxEntries,
	}
}

type Observer struct {
	provider               Provider
	store                  Store
	lifecycle              Lifecycle
	tick                   time.Duration
	reviewInterval         time.Duration
	clock                  func() time.Time
	logger                 *slog.Logger
	credentialsChecked     bool
	disabled               bool
	scopedIdentityResolver ports.ScopedIdentityResolver
	rateLimitUntil         map[string]time.Time
	Cache                  ObserverCache
	renames                map[string]string
	branchStates           BranchStateReconciler
	forceMu                sync.Mutex
	forcedReviews          map[string]bool
	forcedSessions         map[domain.SessionID]bool
	sessionRefreshArmed    bool
	sessionRefreshDebounce time.Duration
	trigger                chan struct{}
	lifecycleTimeout       time.Duration
}

func New(provider Provider, store Store, lifecycle Lifecycle, cfg Config) *Observer {
	o := &Observer{provider: provider, store: store, lifecycle: lifecycle, tick: cfg.Tick, reviewInterval: cfg.ReviewInterval, clock: cfg.Clock, logger: cfg.Logger, scopedIdentityResolver: cfg.ScopedIdentityResolver, Cache: newCache(cfg.CacheMax), rateLimitUntil: map[string]time.Time{}, branchStates: cfg.BranchStates, lifecycleTimeout: DefaultLifecycleTimeout, forcedReviews: map[string]bool{}, forcedSessions: map[domain.SessionID]bool{}, sessionRefreshDebounce: 500 * time.Millisecond, trigger: make(chan struct{}, 1)}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.reviewInterval <= 0 {
		o.reviewInterval = DefaultReviewInterval
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
	var credentialGate sync.Once
	poll := func(ctx context.Context) error {
		credentialGate.Do(func() {
			if _, err := o.checkCredentials(ctx); err != nil && !errors.Is(err, context.Canceled) {
				o.logger.Error("scm observer: initial credential check failed", "err", err)
			}
		})
		return o.Poll(ctx)
	}
	return observe.StartTriggeredPollLoop(ctx, o.tick, o.trigger, poll, o.logger, "scm observer")
}

type subject struct {
	session domain.SessionRecord
	repo    ports.SCMRepo
	branch  string
	known   domain.PullRequest
	hasPR   bool
}

type sessionRepo struct {
	session   domain.SessionRecord
	repo      ports.SCMRepo
	headNames map[string]struct{}
	branch    string
	workspace bool
}

type repoGuardState struct {
	result  ports.SCMGuardResult
	hadETag bool
	err     error
}

type pendingCacheString struct {
	key   string
	value string
}

type refreshSelection struct {
	refs          []ports.SCMPRRef
	refKeys       []string
	subjectsByPR  map[string]*subject
	commitETags   map[string]pendingCacheString
	candidateKeys map[string]bool
}

type persistenceOptions struct {
	reviewFetched               bool
	preserveLocalMetadataHash   bool
	preserveLocalCIHash         bool
	preserveLocalReviewHash     bool
	preserveLocalReviewDecision bool
}

func (o *Observer) RequestReviewRefresh(prURL string) {
	prURL = strings.TrimSpace(prURL)
	if prURL == "" {
		return
	}
	o.forceMu.Lock()
	o.forcedReviews[prURL] = true
	o.forceMu.Unlock()
	select {
	case o.trigger <- struct{}{}:
	default:
	}
}

func (o *Observer) RequestSessionRefresh(sessionID domain.SessionID) {
	if sessionID == "" {
		return
	}
	o.forceMu.Lock()
	defer o.forceMu.Unlock()
	o.forcedSessions[sessionID] = true
	if o.sessionRefreshArmed {
		return
	}
	o.sessionRefreshArmed = true
	time.AfterFunc(o.sessionRefreshDebounce, func() {
		o.forceMu.Lock()
		o.sessionRefreshArmed = false
		o.forceMu.Unlock()
		select {
		case o.trigger <- struct{}{}:
		default:
		}
	})
}

func (o *Observer) takeForcedSessions() map[domain.SessionID]bool {
	o.forceMu.Lock()
	defer o.forceMu.Unlock()
	forced := o.forcedSessions
	o.forcedSessions = map[domain.SessionID]bool{}
	return forced
}

func (o *Observer) takeForcedReviews() map[string]bool {
	o.forceMu.Lock()
	defer o.forceMu.Unlock()
	forced := o.forcedReviews
	o.forcedReviews = map[string]bool{}
	return forced
}

func forcedFor(forced map[string]bool, pr domain.PullRequest) bool {
	return forced[pr.URL] || (pr.HTMLURL != "" && forced[pr.HTMLURL])
}

func (o *Observer) Poll(ctx context.Context) (pollErr error) {
	pollStarted := time.Now()
	o.logger.Debug("scm observer: poll started", "started_at", pollStarted.UTC())
	defer func() {
		completedAt := time.Now()
		duration := completedAt.Sub(pollStarted)
		attrs := []any{
			"stage", "poll",
			"started_at", pollStarted.UTC(),
			"completed_at", completedAt.UTC(),
			"duration_ms", duration.Milliseconds(),
		}
		if pollErr != nil {
			attrs = append(attrs, "err", pollErr)
		}
		if duration >= o.tick {
			o.logger.Warn("scm observer: slow poll completed", attrs...)
			return
		}
		o.logger.Debug("scm observer: poll completed", attrs...)
	}()
	now := o.clock().UTC()
	forcedSessions := o.takeForcedSessions()
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.branchStates != nil {
		if err := o.branchStates.ReconcileBranchStates(ctx); err != nil && !errors.Is(err, context.Canceled) {
			o.logger.Warn("scm observer: branch state reconcile failed", "err", err)
		}
	}
	subjects, sessionRepos, err := o.discoverSubjects(ctx)
	if err != nil {
		return err
	}
	if len(sessionRepos) == 0 {
		return nil
	}
	proceed, err := o.checkCredentials(ctx)
	if err != nil {
		return err
	}
	if !proceed || o.disabled {
		return nil
	}

	repoGuards := o.guardRepos(ctx, sessionRepos)
	repoRefreshOK := pendingRepoRefreshes(repoGuards)
	repoListFailed := map[string]bool{}
	markRepoRefreshFailed := func(repo ports.SCMRepo) {
		key := prKey(repo, 0)
		if _, ok := repoRefreshOK[key]; ok {
			repoRefreshOK[key] = false
		}
	}
	markRepoListFailed := func(repo ports.SCMRepo) {
		repoListFailed[prKey(repo, 0)] = true
		markRepoRefreshFailed(repo)
	}
	markRepoRefreshOK := func(repo ports.SCMRepo) {
		key := prKey(repo, 0)
		if repoListFailed[key] {
			return
		}
		if g, ok := repoGuards[key]; ok && g.err == nil && g.result.ETag != "" {
			repoRefreshOK[key] = true
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listedPRs, listedRepos := o.discoverNewPRs(ctx, sessionRepos, subjects, repoGuards, now, markRepoListFailed)
	if err := ctx.Err(); err != nil {
		return err
	}
	forced := o.takeForcedReviews()
	selection := o.selectRefreshCandidates(ctx, subjects, repoGuards, listedPRs, forced, forcedSessions, markRepoRefreshFailed, now)
	reconciledObs := o.reconcileTerminalGitHubPRs(ctx, subjects, repoGuards, listedPRs, &selection, now, markRepoRefreshFailed)
	observations := map[string]ports.SCMObservation{}
	for key, obs := range reconciledObs {
		observations[key] = obs
	}
	prRefreshOK := map[string]bool{}
	for key := range selection.candidateKeys {
		prRefreshOK[key] = false
	}
	for start := 0; start < len(selection.refs); start += BatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+BatchSize, len(selection.refs))
		active := make([]ports.SCMPRRef, 0, end-start)
		activeKeys := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			ref := selection.refs[i]
			if o.inRateLimitCooldown(now, ref.Repo.Provider) {
				markRepoRefreshFailed(ref.Repo)
				continue
			}
			active = append(active, ref)
			activeKeys = append(activeKeys, selection.refKeys[i])
		}
		if len(active) == 0 {
			continue
		}
		started := time.Now()
		batch, err := o.provider.FetchPullRequests(ctx, active)
		o.logProviderRequest("fetch_pull_requests", repoNames(active), started, err)
		if err != nil {
			if cooldown, ok := rateLimitCooldown(now, err); ok {
				for _, ref := range active {
					o.setRateLimitCooldown(now, ref.Repo.Provider, cooldown)
				}
				o.logger.Warn("scm observer: provider rate-limited; entering cooldown", "cooldown", cooldown, "err", err)
				for _, ref := range active {
					markRepoRefreshFailed(ref.Repo)
				}
				continue
			}
			o.logger.Error("scm observer: GraphQL PR batch failed", "err", err)
			for _, ref := range active {
				markRepoRefreshFailed(ref.Repo)
			}
			continue
		}
		for i, ref := range active {
			if i >= len(batch) || !batch[i].Fetched {
				if i < len(batch) && batch[i].Error != nil {
					fetchErr := batch[i].Error
					providerKey := firstNonEmpty(batch[i].Provider, ref.Repo.Provider)
					if errors.Is(fetchErr, ports.ErrSCMNotFound) {
						o.logger.Debug("scm observer: tracked PR not found at provider; marking refresh-incomplete", "provider", providerKey, "pr", ref.URL, "err", fetchErr)
					} else if cooldown, ok := rateLimitCooldown(now, fetchErr); ok {
						o.setRateLimitCooldown(now, providerKey, cooldown)
						o.logger.Warn("scm observer: provider rate-limited (per-observation); entering cooldown", "provider", providerKey, "cooldown", cooldown, "err", fetchErr)
					} else {
						o.logger.Warn("scm observer: provider fetch failed (per-observation); marking refresh-incomplete", "provider", providerKey, "err", fetchErr)
					}
				}
				markRepoRefreshFailed(ref.Repo)
				continue
			}
			obs := batch[i]
			obs.ObservedAt = now
			observations[activeKeys[i]] = obs
		}
	}

	for key, subj := range selection.subjectsByPR {
		if err := ctx.Err(); err != nil {
			return err
		}
		obs, ok := observations[key]
		if !ok {
			continue
		}
		local := subj.known
		o.enrichFailureLogs(ctx, &obs, local)
		observations[key] = obs
	}

	reviewModes := map[string]ports.ReviewWriteMode{}
	localOnlyObservations := map[string]bool{}
	reviewStale := map[string]bool{}
	o.refreshReviews(ctx, subjects, observations, selection.subjectsByPR, forced, reviewModes, localOnlyObservations, reviewStale, now)
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, key := range dispatchOrder(observations, selection.subjectsByPR) {
		if err := ctx.Err(); err != nil {
			return err
		}
		obs := observations[key]
		obs.Error = nil
		observations[key] = obs
		subj, ok := selection.subjectsByPR[key]
		if !ok {
			continue
		}
		local := subj.known
		reviewMode := reviewModes[key]
		opts := persistenceOptions{
			reviewFetched:               reviewMode != ports.ReviewWritePreserve,
			preserveLocalMetadataHash:   localOnlyObservations[key],
			preserveLocalCIHash:         localOnlyObservations[key] || obs.CI.Partial,
			preserveLocalReviewHash:     reviewStale[key],
			preserveLocalReviewDecision: reviewStale[key],
		}
		prepared := o.prepareForPersistence(obs, local, opts, now)
		if !prepared.Changed.Metadata && !prepared.Changed.CI && !prepared.Changed.Review {
			refreshed := !prepared.PR.DetailsPending
			if prepared.PR.Details != nil {
				pr, _, _, _, _ := domainFromObservation(subj.session.ID, subj.session, prepared, local, opts, now)
				if err := o.store.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
					o.logger.Warn("scm observer: PR details write failed", "session", subj.session.ID, "pr", pr.URL, "err", err)
					refreshed = false
				}
			}
			prRefreshOK[key] = refreshed
			continue
		}
		finalPR, finalChecks, finalReviews, finalThreads, finalComments := domainFromObservation(subj.session.ID, subj.session, prepared, local, opts, now)
		pr, checks, reviews, threads, comments := finalPR, finalChecks, finalReviews, finalThreads, finalComments
		if o.lifecycle != nil {
			pendingOpts := opts
			if prepared.Changed.Metadata {
				pendingOpts.preserveLocalMetadataHash = true
			}
			if prepared.Changed.CI {
				pendingOpts.preserveLocalCIHash = true
			}
			if prepared.Changed.Review {
				pendingOpts.preserveLocalReviewHash = true
			}
			pr, checks, reviews, threads, comments = domainFromObservation(subj.session.ID, subj.session, prepared, local, pendingOpts, now)
		}
		if err := o.store.WriteSCMObservation(ctx, pr, checks, reviews, threads, comments, reviewMode); err != nil {
			o.logger.Error("scm observer: DB write failed", "session", subj.session.ID, "pr", pr.URL, "err", err)
			sentryobs.CaptureBackground(ctx, err, "scm", "scm_persist", map[string]string{"reason": "db_write"})
			markRepoRefreshFailed(subj.repo)
			continue
		}
		if o.lifecycle != nil {
			lifecycleCtx, cancel := ctx, context.CancelFunc(func() {})
			if !prepared.PR.Merged && !prepared.PR.Closed {
				lifecycleCtx, cancel = context.WithTimeout(ctx, o.lifecycleTimeout)
			}
			err := o.lifecycle.ApplySCMObservation(lifecycleCtx, subj.session.ID, prepared)
			cancel()
			if err != nil {
				o.logger.Error("scm observer: lifecycle notification failed", "session", subj.session.ID, "pr", firstNonEmpty(prepared.PR.URL, prepared.PR.HTMLURL, local.URL), "err", err)
				sentryobs.CaptureBackground(ctx, err, "scm", "scm_persist", map[string]string{"reason": "notify"})
				markRepoRefreshFailed(subj.repo)
				continue
			}
			if err := o.store.WriteSCMObservation(ctx, finalPR, finalChecks, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
				o.logger.Error("scm observer: DB lifecycle acknowledgement failed", "session", subj.session.ID, "pr", finalPR.URL, "err", err)
				sentryobs.CaptureBackground(ctx, err, "scm", "scm_persist", map[string]string{"reason": "db_write"})
				markRepoRefreshFailed(subj.repo)
				continue
			}
		}
		markRepoRefreshOK(subj.repo)
		prRefreshOK[key] = !prepared.PR.DetailsPending
	}
	for key, ok := range prRefreshOK {
		if !ok {
			continue
		}
		if pending, found := selection.commitETags[key]; found {
			o.cacheSetString(o.Cache.CommitChecksETag, &o.Cache.commitOrder, pending.key, pending.value)
		}
		if reviewModes[key] != ports.ReviewWritePreserve {
			o.cacheSetTime(o.Cache.LastReviewPollAt, &o.Cache.lastReviewPollOrder, key, now)
		}
		o.cacheSetTime(o.Cache.LastPRFetchAt, &o.Cache.lastPRFetchOrder, key, now)
	}
	repoCandidateKeys := map[string][]string{}
	for pk := range selection.candidateKeys {
		subj, ok := selection.subjectsByPR[pk]
		if !ok {
			continue
		}
		rk := prKey(subj.repo, 0)
		repoCandidateKeys[rk] = append(repoCandidateKeys[rk], pk)
	}
	for key, ok := range repoRefreshOK {
		if !ok {
			continue
		}
		allRefsOK := true
		for _, pk := range repoCandidateKeys[key] {
			if !prRefreshOK[pk] {
				allRefsOK = false
				break
			}
		}
		if !allRefsOK {
			continue
		}
		if etag := repoGuards[key].result.ETag; etag != "" {
			o.cacheSetString(o.Cache.RepoPRListETag, &o.Cache.repoOrder, key, etag)
		}
		if listedRepos[key] {
			o.Cache.LastSyncCursor[key] = now
		}
	}
	return nil
}

func dispatchOrder(observations map[string]ports.SCMObservation, subjectsByPR map[string]*subject) []string {
	keys := make([]string, 0, len(observations))
	for key := range observations {
		keys = append(keys, key)
	}
	sessionOf := func(key string) string {
		if s := subjectsByPR[key]; s != nil {
			return string(s.session.ID)
		}
		return ""
	}
	sort.Slice(keys, func(i, j int) bool {
		if si, sj := sessionOf(keys[i]), sessionOf(keys[j]); si != sj {
			return si < sj
		}
		if ni, nj := observations[keys[i]].PR.Number, observations[keys[j]].PR.Number; ni != nj {
			return ni < nj
		}
		return keys[i] < keys[j]
	})
	return keys
}
func (o *Observer) checkCredentials(ctx context.Context) (bool, error) {
	var probe observe.CredentialProbe
	if checker, ok := o.provider.(credentialChecker); ok {
		probe = checker.SCMCredentialsAvailable
	}
	return observe.CheckCredentialsOnce(ctx, probe, &o.credentialsChecked, &o.disabled, o.logger, "scm observer")
}

func (o *Observer) discoverSubjects(ctx context.Context) (map[string]*subject, []sessionRepo, error) {
	sessions, err := o.store.ListAllSessions(ctx)
	if err != nil {
		return nil, nil, err
	}
	projects := map[domain.ProjectID]domain.ProjectRecord{}
	scanRepos := map[domain.ProjectID][]ports.SCMRepo{}
	out := map[string]*subject{}
	var sessionRepos []sessionRepo
	for _, sess := range sessions {
		if sess.IsTerminated {
			continue
		}
		branch := strings.TrimSpace(sess.Metadata.Branch)
		if branch == "" {
			continue
		}
		proj, ok := projects[sess.ProjectID]
		if !ok {
			p, found, err := o.store.GetProject(ctx, string(sess.ProjectID))
			if err != nil {
				return nil, nil, err
			}
			if !found || !p.ArchivedAt.IsZero() {
				continue
			}
			if p.RepoOriginURL == "" && p.Path != "" {
				if url := resolveGitOriginURL(p.Path); url != "" {
					p.RepoOriginURL = url
					if err := o.store.UpsertProject(ctx, p); err != nil {
						o.logger.Warn("scm observer: backfill origin URL persist failed", "project", p.ID, "err", err)
					}
				}
			}
			projects[sess.ProjectID] = p
			proj = p
			if origin, ok := o.provider.ParseRepository(p.RepoOriginURL); ok {
				scanRepos[sess.ProjectID] = o.resolveScanRepos(ctx, p, origin)
			}
		}
		repos := append([]ports.SCMRepo(nil), scanRepos[sess.ProjectID]...)
		sessionRepos = append(sessionRepos, checkoutSessionRepos(sess, branch, proj.Kind.WithDefault() == domain.ProjectKindWorkspace, repos)...)
		childRepos, err := o.workspaceSCMSessionRepos(ctx, proj, sess, branch)
		if err != nil {
			return nil, nil, err
		}
		for _, child := range childRepos {
			sessionRepos = append(sessionRepos, child)
			repos = append(repos, child.repo)
		}
		if len(repos) == 0 {
			o.logger.Debug("scm observer: project has no supported SCM origins", "project", proj.ID)
			continue
		}
		prs, err := o.store.ListPRsBySession(ctx, sess.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, pr := range trackedPRsForSession(sess, prs) {
			prRepo, ok := repoForTrackedPR(pr, repos)
			if !ok {
				o.logger.Warn("scm observer: tracked PR repo no longer belongs to project", "session", sess.ID, "pr", pr.URL, "repo", pr.Repo)
				continue
			}
			key := keyForTrackedPR(pr, prRepo)
			if existing, ok := out[key]; ok {
				o.logger.Warn("scm observer: duplicate tracked PR subject skipped", "pr", key, "kept_session", existing.session.ID, "skipped_session", sess.ID)
				continue
			}
			out[key] = &subject{session: sess, repo: prRepo, branch: branch, known: pr, hasPR: true}
		}
	}
	return out, sessionRepos, nil
}

func (o *Observer) resolveScanRepos(ctx context.Context, proj domain.ProjectRecord, origin ports.SCMRepo) []ports.SCMRepo {
	repos := []ports.SCMRepo{origin}
	if strings.TrimSpace(proj.Path) == "" {
		return repos
	}
	seen := map[string]bool{strings.ToLower(prKey(origin, 0)): true}
	for _, url := range gitRemoteURLsFunc(ctx, proj.Path) {
		repo, ok := o.provider.ParseRepository(url)
		if !ok {
			continue
		}
		key := strings.ToLower(prKey(repo, 0))
		if seen[key] {
			continue
		}
		seen[key] = true
		repos = append(repos, repo)
	}
	return repos
}

func checkoutSessionRepos(sess domain.SessionRecord, branch string, workspace bool, checkouts ...[]ports.SCMRepo) []sessionRepo {
	var result []sessionRepo
	byBase := make(map[string]int)
	for _, checkout := range checkouts {
		for _, base := range checkout {
			key := strings.ToLower(prKey(base, 0))
			position, exists := byBase[key]
			if !exists {
				position = len(result)
				byBase[key] = position
				result = append(result, sessionRepo{
					session: sess, repo: base, branch: branch, workspace: workspace,
					headNames: make(map[string]struct{}),
				})
			}
			for _, destination := range checkout {
				if !strings.EqualFold(base.Provider, destination.Provider) || !strings.EqualFold(base.Host, destination.Host) {
					continue
				}
				result[position].headNames[strings.ToLower(repoFullName(destination))] = struct{}{}
			}
		}
	}
	return result
}

func (o *Observer) workspaceSCMSessionRepos(ctx context.Context, proj domain.ProjectRecord, sess domain.SessionRecord, branch string) ([]sessionRepo, error) {
	if proj.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return nil, nil
	}
	childRepos, err := o.store.ListWorkspaceRepos(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	var checkouts [][]ports.SCMRepo
	for _, child := range childRepos {
		if strings.TrimSpace(child.RepoOriginURL) == "" {
			continue
		}
		repo, ok := o.provider.ParseRepository(child.RepoOriginURL)
		if !ok {
			o.logger.Debug("scm observer: unsupported SCM origin", "project", proj.ID, "repo", child.Name, "origin", child.RepoOriginURL)
			continue
		}
		childPath := filepath.Join(proj.Path, filepath.FromSlash(child.RelativePath))
		checkouts = append(checkouts, o.resolveScanRepos(ctx, domain.ProjectRecord{Path: childPath}, repo))
	}
	return checkoutSessionRepos(sess, branch, true, checkouts...), nil
}

func repoForTrackedPR(pr domain.PullRequest, repos []ports.SCMRepo) (ports.SCMRepo, bool) {
	if pr.Provider != "" && pr.Host != "" && pr.Repo != "" {
		owner, name, ok := strings.Cut(pr.Repo, "/")
		if !ok || owner == "" || name == "" {
			return ports.SCMRepo{}, false
		}
		return ports.SCMRepo{Provider: pr.Provider, Host: pr.Host, Owner: owner, Name: name, Repo: pr.Repo}, true
	}
	if pr.Repo != "" {
		for _, repo := range repos {
			if matchesTrackedPRRepo(pr, repo) {
				return repo, true
			}
		}
		return ports.SCMRepo{}, false
	}
	if len(repos) == 1 {
		return repos[0], true
	}
	for _, repo := range repos {
		if strings.EqualFold(repo.Repo, repos[0].Repo) {
			return repo, true
		}
	}
	return repos[0], len(repos) > 0
}

func matchesTrackedPRRepo(pr domain.PullRequest, repo ports.SCMRepo) bool {
	if pr.Provider != "" && !strings.EqualFold(pr.Provider, repo.Provider) {
		return false
	}
	if pr.Host != "" && !strings.EqualFold(pr.Host, repo.Host) {
		return false
	}
	if pr.Repo != "" && !strings.EqualFold(pr.Repo, repoFullName(repo)) {
		return false
	}
	return true
}

func openTrackedPRs(prs []domain.PullRequest) []domain.PullRequest {
	out := make([]domain.PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.Number > 0 && !pr.Merged && !pr.Closed {
			out = append(out, pr)
		}
	}
	return out
}

func trackedPRsForSession(sess domain.SessionRecord, prs []domain.PullRequest) []domain.PullRequest {
	if !sess.TerminateOnPRMerge {
		return openTrackedPRs(prs)
	}
	out := make([]domain.PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.Number > 0 {
			out = append(out, pr)
		}
	}
	return out
}

func (o *Observer) logProviderRequest(stage, repository string, startedAt time.Time, err error) {
	completedAt := time.Now()
	duration := completedAt.Sub(startedAt)
	attrs := []any{
		"stage", stage,
		"repository", repository,
		"started_at", startedAt.UTC(),
		"completed_at", completedAt.UTC(),
		"duration_ms", duration.Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	if duration >= o.tick {
		o.logger.Warn("scm observer: slow provider request completed", attrs...)
		return
	}
	o.logger.Debug("scm observer: provider request completed", attrs...)
}

func repoNames(refs []ports.SCMPRRef) string {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		seen[repoFullName(ref.Repo)] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (o *Observer) guardRepos(ctx context.Context, sessionRepos []sessionRepo) map[string]repoGuardState {
	repos := map[string]ports.SCMRepo{}
	for _, sr := range sessionRepos {
		repos[prKey(sr.repo, 0)] = sr.repo
	}
	out := map[string]repoGuardState{}
	for key, repo := range repos {
		prev, had := o.Cache.RepoPRListETag[key]
		started := time.Now()
		res, err := o.provider.RepoPRListGuard(ctx, repo, prev)
		o.logProviderRequest("repo_pr_list_guard", repoFullName(repo), started, err)
		if err != nil {
			o.logger.Error("scm observer: repo PR-list guard failed", "repo", repoFullName(repo), "err", err)
			out[key] = repoGuardState{hadETag: had, err: err}
			continue
		}
		out[key] = repoGuardState{result: res, hadETag: had}
	}
	return out
}

func pendingRepoRefreshes(guards map[string]repoGuardState) map[string]bool {
	out := map[string]bool{}
	for key, g := range guards {
		if g.err == nil && g.result.ETag != "" {
			out[key] = true
		}
	}
	return out
}

func (o *Observer) discoverNewPRs(ctx context.Context, sessionRepos []sessionRepo, subjects map[string]*subject, guards map[string]repoGuardState, now time.Time, markRepoFailed func(ports.SCMRepo)) (listedPRs, listedRepos map[string]bool) {
	identities := o.resolveIdentities(ctx, sessionRepos)
	byRepo := map[string][]sessionRepo{}
	repos := map[string]ports.SCMRepo{}
	for _, sr := range sessionRepos {
		key := prKey(sr.repo, 0)
		byRepo[key] = append(byRepo[key], sr)
		repos[key] = sr.repo
	}
	listedPRs = map[string]bool{}
	listedRepos = map[string]bool{}
	pullsByRepo := map[string][]ports.SCMPRObservation{}
	for repoKey, repo := range repos {
		if _, ok := identities[identityKey(repo.Provider, repo.Host)]; !ok {
			cacheDelete(o.Cache.RepoPRListETag, &o.Cache.repoOrder, repoKey)
			delete(o.Cache.LastSyncCursor, repoKey)
			if markRepoFailed != nil {
				markRepoFailed(repo)
			}
			continue
		}
		g := guards[repoKey]
		if g.err != nil {
			continue
		}
		if g.result.NotModified && g.hadETag {
			continue
		}
		repoKey := prKey(repo, 0)
		cursor := o.Cache.LastSyncCursor[repoKey]
		updatedAfter := time.Time{}
		if !cursor.IsZero() {
			updatedAfter = cursor.Add(-incrementalDiscoveryOverlap)
		}
		started := time.Now()
		pulls, err := o.provider.ListPRsByRepo(ctx, repo, updatedAfter)
		o.logProviderRequest("list_open_prs", repoFullName(repo), started, err)
		if err != nil {
			o.logger.Debug("scm observer: open PR list failed", "repo", repoFullName(repo), "err", err)
			if markRepoFailed != nil && !errors.Is(err, ports.ErrSCMNotFound) {
				markRepoFailed(repo)
			}
			continue
		}
		pullsByRepo[repoKey] = pulls
		o.recordRename(ctx, repo, pulls)
		listedRepos[repoKey] = true
		for _, pr := range pulls {
			listedPRs[prKey(repo, pr.Number)] = true
			if pr.ProviderID != "" {
				if idKey := identityPRKey(repo.Provider, repo.Host, pr.ProviderID); subjects[idKey] != nil {
					listedPRs[idKey] = true
				}
			}
		}
	}

	trackedProviderIdentities := map[string]bool{}
	for repoKey, pulls := range pullsByRepo {
		repo := repos[repoKey]
		for _, pr := range pulls {
			if pr.ProviderID == "" {
				continue
			}
			idKey := identityPRKey(repo.Provider, repo.Host, pr.ProviderID)
			if subjects[idKey] != nil || subjects[prKey(repo, pr.Number)] != nil {
				trackedProviderIdentities[idKey] = true
			}
		}
	}

	for repoKey, pulls := range pullsByRepo {
		repo := repos[repoKey]
		for _, pr := range pulls {
			if pr.Number <= 0 || pr.SourceBranch == "" {
				continue
			}
			id := identities[identityKey(repo.Provider, repo.Host)]
			if !id.Authored(pr.Author) {
				continue
			}
			if _, ok := subjects[prKey(repo, pr.Number)]; ok {
				continue
			}
			if pr.ProviderID != "" {
				idKey := identityPRKey(repo.Provider, repo.Host, pr.ProviderID)
				if trackedProviderIdentities[idKey] || subjects[idKey] != nil {
					continue
				}
			}
			headRepo := pr.HeadRepo
			if pr.BaseRepo != "" && strings.EqualFold(pr.HeadRepo, pr.BaseRepo) {
				headRepo = repoFullName(repo)
			}
			eligible := candidatesForHeadRepo(byRepo[repoKey], headRepo)
			sr, ok := matchSession(eligible, pr.SourceBranch)
			if !ok {
				continue
			}
			known := domain.PullRequest{
				URL:          firstNonEmpty(pr.URL, pr.HTMLURL),
				SessionID:    sr.session.ID,
				Number:       pr.Number,
				Draft:        pr.Draft,
				SourceBranch: pr.SourceBranch,
				TargetBranch: pr.TargetBranch,
				HeadSHA:      pr.HeadSHA,
				Author:       strings.TrimSpace(pr.Author),
				Provider:     repo.Provider,
				Host:         repo.Host,
				Repo:         repoFullName(repo),
				ProviderID:   pr.ProviderID,
				UpdatedAt:    now,
			}
			if err := o.store.WriteSCMObservation(ctx, known, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
				o.logger.Error("scm observer: persist discovered PR failed", "session", sr.session.ID, "pr", known.URL, "err", err)
				sentryobs.CaptureBackground(ctx, err, "scm", "scm_persist", map[string]string{"reason": "persist"})
				if markRepoFailed != nil {
					markRepoFailed(repo)
				}
				continue
			}
			subjects[keyForTrackedPR(known, repo)] = &subject{
				session: sr.session,
				repo:    repo,
				branch:  sr.branch,
				known:   known,
				hasPR:   true,
			}
		}
	}
	return listedPRs, listedRepos
}

func (o *Observer) resolveIdentities(ctx context.Context, sessionRepos []sessionRepo) map[string]ports.SCMIdentity {
	if o.scopedIdentityResolver == nil {
		return nil
	}
	seen := map[string]bool{}
	identities := map[string]ports.SCMIdentity{}
	for _, sr := range sessionRepos {
		ik := identityKey(sr.repo.Provider, sr.repo.Host)
		if seen[ik] {
			continue
		}
		seen[ik] = true
		id, err := o.scopedIdentityResolver.AuthenticatedIdentityForProvider(ctx, sr.repo.Provider, sr.repo.Host)
		if err != nil {
			o.logger.Debug("scm observer: identity unavailable; automatic discovery disabled for scope", "provider", sr.repo.Provider, "host", sr.repo.Host, "err", err)
			continue
		}
		id.Login = strings.TrimSpace(id.Login)
		if !id.Human || id.Login == "" {
			continue
		}
		identities[ik] = id
	}
	return identities
}

func candidatesForHeadRepo(candidates []sessionRepo, headRepo string) []sessionRepo {
	name := strings.ToLower(strings.TrimSpace(headRepo))
	if name == "" {
		return nil
	}
	var out []sessionRepo
	for _, sr := range candidates {
		if _, configured := sr.headNames[name]; configured {
			out = append(out, sr)
		}
	}
	return out
}

func matchSession(candidates []sessionRepo, sourceBranch string) (sessionRepo, bool) {
	var best sessionRepo
	bestLen := -1
	ambiguous := false
	consider := func(sr sessionRepo, length int) {
		if length > bestLen {
			best, bestLen, ambiguous = sr, length, false
		} else if length == bestLen && best.session.ID != sr.session.ID {
			ambiguous = true
		}
	}
	for _, sr := range candidates {
		if sr.branch == "" {
			continue
		}
		for temporary, owned := range []string{sr.branch, sr.session.TemporaryBranch} {
			if owned == "" {
				continue
			}
			rank := func(length int) int { return 2*length + 1 - temporary }
			if owned == sourceBranch {
				consider(sr, rank(len(sourceBranch)+1))
			}
			for _, prefix := range sessionBranchPrefixes(owned) {
				if prefix == sourceBranch || strings.HasPrefix(sourceBranch, prefix+"/") {
					consider(sr, rank(len(prefix)))
				}
			}
			if workspaceHyphenBranch(sr, owned) && strings.HasPrefix(sourceBranch, owned+"-") && len(sourceBranch) > len(owned)+1 {
				consider(sr, rank(len(owned)))
			}
		}
	}
	return best, bestLen >= 0 && !ambiguous
}

func workspaceHyphenBranch(sr sessionRepo, branch string) bool {
	if !sr.workspace || sr.session.ID == "" {
		return false
	}
	base := "ao/" + string(sr.session.ID)
	if branch == base {
		return true
	}
	suffix, ok := strings.CutPrefix(branch, base+"-")
	if !ok {
		return false
	}
	n, err := strconv.Atoi(suffix)
	return err == nil && n >= 2 && strconv.Itoa(n) == suffix
}

func sessionBranchPrefixes(branch string) []string {
	prefixes := []string{branch}
	if namespace, ok := strings.CutSuffix(branch, "/root"); ok && namespace != "" {
		prefixes = append(prefixes, namespace)
	}
	return prefixes
}

func (o *Observer) trackedPRRef(ctx context.Context, s *subject) ports.SCMPRRef {
	ref := ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL}
	if s.known.URL == "" {
		return ref
	}
	updatedAt, ok, err := o.store.PRDetailsUpdatedAt(ctx, s.known.URL)
	if err != nil {
		o.logger.Warn("scm observer: read stored PR details updatedAt failed; reading details again", "pr", s.known.URL, "err", err)
		return ref
	}
	if ok {
		ref.DetailsUpdatedAt = updatedAt
	}
	return ref
}

func (o *Observer) selectRefreshCandidates(ctx context.Context, subjects map[string]*subject, guards map[string]repoGuardState, listedPRs, forced map[string]bool, forcedSessions map[domain.SessionID]bool, markRepoFailed func(ports.SCMRepo), now time.Time) refreshSelection {
	selection := refreshSelection{
		subjectsByPR:  map[string]*subject{},
		commitETags:   map[string]pendingCacheString{},
		candidateKeys: map[string]bool{},
	}
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		key := keyForTrackedPR(s.known, s.repo)
		selection.subjectsByPR[key] = s
		candidate := missingLocalState(s.known) || forcedFor(forced, s.known) ||
			(forcedSessions[s.session.ID] && !s.known.Merged && !s.known.Closed)
		repoCursor := o.Cache.LastSyncCursor[prKey(s.repo, 0)]
		hasCursor := !repoCursor.IsZero()
		g := guards[prKey(s.repo, 0)]
		if g.err == nil && !g.result.NotModified {
			if hasCursor && listedPRs != nil {
				if listedPRs[key] {
					candidate = true
				}
			} else {
				candidate = true
			}
		}
		deferChecksGuard := len(forcedSessions) > 0 && !forcedSessions[s.session.ID]
		if s.known.HeadSHA != "" && !deferChecksGuard {
			commitKey := commitKey(s.repo, s.known.HeadSHA)
			prev := o.Cache.CommitChecksETag[commitKey]
			started := time.Now()
			res, err := o.provider.CommitChecksGuard(ctx, s.repo, s.known.HeadSHA, prev)
			o.logProviderRequest("commit_checks_guard", repoFullName(s.repo), started, err)
			if err != nil {
				o.logger.Error("scm observer: commit check-runs guard failed", "pr", s.known.URL, "sha", s.known.HeadSHA, "err", err)
				if markRepoFailed != nil {
					markRepoFailed(s.repo)
				}
			} else if !res.NotModified && res.ETag != "" && res.ETag != prev {
				candidate = true
				selection.commitETags[key] = pendingCacheString{key: commitKey, value: res.ETag}
			}
		}
		if !candidate {
			if !hasCursor || listedPRs == nil || listedPRs[key] {
				last := o.Cache.LastPRFetchAt[key]
				if last.IsZero() || now.Sub(last) > DefaultPRMaxAge {
					candidate = true
				}
			}
		}
		if candidate {
			selection.refs = append(selection.refs, o.trackedPRRef(ctx, s))
			selection.refKeys = append(selection.refKeys, key)
			selection.candidateKeys[key] = true
		}
	}
	return selection
}

func (o *Observer) reconcileTerminalGitHubPRs(ctx context.Context, subjects map[string]*subject, guards map[string]repoGuardState, listedPRs map[string]bool, selection *refreshSelection, now time.Time, markRepoFailed func(ports.SCMRepo)) map[string]ports.SCMObservation {
	out := map[string]ports.SCMObservation{}
	if listedPRs == nil {
		return out
	}
	type pendingReconcile struct {
		ref ports.SCMPRRef
		s   *subject
	}
	var refs []pendingReconcile
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		if s.repo.Provider != "github" {
			continue
		}
		if s.known.Merged || s.known.Closed {
			continue
		}
		repoKey := prKey(s.repo, 0)
		g := guards[repoKey]
		if g.err != nil || g.result.NotModified {
			continue
		}
		key := keyForTrackedPR(s.known, s.repo)
		if listedPRs[key] || listedPRs[prKey(s.repo, s.known.Number)] {
			continue
		}
		if selection.candidateKeys[key] {
			continue
		}
		refs = append(refs, pendingReconcile{
			ref: o.trackedPRRef(ctx, s),
			s:   s,
		})
	}
	if len(refs) == 0 {
		return out
	}
	for _, chunk := range chunks(refs, BatchSize) {
		if err := ctx.Err(); err != nil {
			return out
		}
		activePending := make([]pendingReconcile, 0, len(chunk))
		for _, r := range chunk {
			if o.inRateLimitCooldown(now, r.ref.Repo.Provider) {
				markRepoFailed(r.ref.Repo)
				continue
			}
			activePending = append(activePending, r)
		}
		if len(activePending) == 0 {
			continue
		}
		active := make([]ports.SCMPRRef, len(activePending))
		for i, r := range activePending {
			active[i] = r.ref
		}
		started := time.Now()
		batch, err := o.provider.FetchPullRequests(ctx, active)
		o.logProviderRequest("reconcile_terminal_pull_requests", repoNames(active), started, err)
		if err != nil {
			if cooldown, ok := rateLimitCooldown(now, err); ok {
				for _, ref := range active {
					o.setRateLimitCooldown(now, ref.Repo.Provider, cooldown)
				}
				o.logger.Warn("scm observer: reconciliation rate-limited; entering cooldown", "cooldown", cooldown, "err", err)
			} else {
				o.logger.Error("scm observer: reconciliation detail fetch failed", "err", err)
			}
			for _, ref := range active {
				markRepoFailed(ref.Repo)
			}
			continue
		}
		for i, r := range activePending {
			if i >= len(batch) || !batch[i].Fetched {
				if i < len(batch) && batch[i].Error != nil {
					fetchErr := batch[i].Error
					providerKey := firstNonEmpty(batch[i].Provider, r.ref.Repo.Provider)
					if errors.Is(fetchErr, ports.ErrSCMNotFound) {
						o.logger.Debug("scm observer: reconciled PR not found at provider; marking refresh-incomplete", "provider", providerKey, "pr", r.ref.URL, "err", fetchErr)
					} else if cooldown, ok := rateLimitCooldown(now, fetchErr); ok {
						o.setRateLimitCooldown(now, providerKey, cooldown)
						o.logger.Warn("scm observer: reconciliation rate-limited (per-observation); entering cooldown", "provider", providerKey, "cooldown", cooldown, "err", fetchErr)
					} else {
						o.logger.Warn("scm observer: reconciliation fetch failed (per-observation); marking refresh-incomplete", "provider", providerKey, "err", fetchErr)
					}
				}
				markRepoFailed(r.ref.Repo)
				continue
			}
			obs := batch[i]
			obs.ObservedAt = now
			key := keyForTrackedPR(r.s.known, r.s.repo)
			out[key] = obs
			selection.subjectsByPR[key] = r.s
			selection.candidateKeys[key] = true
			markRepoFailed(r.ref.Repo)
		}
	}
	return out
}

func missingLocalState(pr domain.PullRequest) bool {
	return pr.URL == "" || pr.HeadSHA == "" || pr.MetadataHash == "" || pr.CIHash == ""
}

func (o *Observer) enrichFailureLogs(ctx context.Context, obs *ports.SCMObservation, local domain.PullRequest) {
	if obs.CI.Summary != string(domain.CIFailing) || obs.CI.FailedFingerprint == "" {
		return
	}
	if strings.HasPrefix(local.CIHash, obs.CI.FailedFingerprint+":") {
		checks, err := o.store.ListChecks(ctx, local.URL)
		if err == nil && obs.CI.ApplyStoredLogTails(checks) {
			return
		}
	}
	tails := make([]string, 0, len(obs.CI.FailedChecks))
	checksByProviderID := make(map[string][]int, len(obs.CI.Checks))
	for i := range obs.CI.Checks {
		key := checkProviderKey(obs.CI.Checks[i])
		checksByProviderID[key] = append(checksByProviderID[key], i)
	}
	for i := range obs.CI.FailedChecks {
		tail := obs.CI.FailedChecks[i].LogTail
		if tail == "" && obs.CI.FailedChecks[i].ProviderID != "" {
			var err error
			repo := ports.SCMRepo{Provider: obs.Provider, Host: obs.Host, Repo: obs.Repo, Owner: ownerOf(obs.Repo), Name: nameOf(obs.Repo)}
			started := time.Now()
			tail, err = o.provider.FetchFailedCheckLogTail(ctx, repo, obs.CI.FailedChecks[i])
			o.logProviderRequest("fetch_failed_check_log", repoFullName(repo), started, err)
			if err != nil {
				tail = "<log fetch failed: " + scrubLine(err.Error()) + ">"
			}
		}
		obs.CI.FailedChecks[i].LogTail = tail
		if tail != "" {
			tails = append(tails, tail)
		}
		for _, j := range checksByProviderID[checkProviderKey(obs.CI.FailedChecks[i])] {
			obs.CI.Checks[j].LogTail = tail
		}
	}
	obs.CI.FailureLogTail = strings.Join(tails, "\n---\n")
}

func checkProviderKey(ch ports.SCMCheckObservation) string {
	return ch.Name + "\x00" + ch.ProviderID
}

func (o *Observer) refreshReviews(ctx context.Context, subjects map[string]*subject, observations map[string]ports.SCMObservation, subjectsByPR map[string]*subject, forced map[string]bool, reviewModes map[string]ports.ReviewWriteMode, localOnlyObservations, reviewStale map[string]bool, now time.Time) {
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		if s.known.Merged || s.known.Closed {
			continue
		}
		pkey := keyForTrackedPR(s.known, s.repo)
		obs, hasObs := observations[pkey]
		decision := string(s.known.Review)
		if hasObs && obs.Review.Decision != "" {
			decision = obs.Review.Decision
		}
		updatedAtProvider := obs.PR.UpdatedAtProvider
		if !forcedFor(forced, s.known) && !o.needsReviewRefresh(pkey, s.known, decision, hasObs, updatedAtProvider, now) {
			continue
		}
		if o.inRateLimitCooldown(now, s.repo.Provider) {
			continue
		}
		started := time.Now()
		review, err := o.provider.FetchReviewThreads(ctx, ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL})
		o.logProviderRequest("fetch_review_threads", repoFullName(s.repo), started, err)
		if err != nil {
			o.logger.Error("scm observer: review refresh failed", "pr", s.known.URL, "err", err)
			o.cacheSetBool(o.Cache.ReviewRefreshFailed, &o.Cache.reviewFailedOrder, pkey, true)
			if hasObs {
				obs.Review.Decision = string(s.known.Review)
				obs.Review.Threads = nil
				observations[pkey] = obs
				subjectsByPR[pkey] = s
				reviewStale[pkey] = true
			}
			continue
		}
		if !hasObs {
			checks, err := o.store.ListChecks(ctx, s.known.URL)
			if err != nil {
				o.logger.Error("scm observer: list local checks for review-only refresh failed", "pr", s.known.URL, "err", err)
			}
			obs = observationFromLocal(s.repo, s.known, checks)
			localOnlyObservations[pkey] = true
		}
		if review.Decision != "" && reviewDecisionPriority(domain.ReviewDecision(review.Decision)) >= reviewDecisionPriority(domain.ReviewDecision(obs.Review.Decision)) {
			obs.Review.Decision = review.Decision
		}
		obs.Review.Reviews = review.Reviews
		obs.Review.Threads = review.Threads
		obs.Review.Partial = review.Partial
		obs.ObservedAt = now
		observations[pkey] = obs
		subjectsByPR[pkey] = s
		if review.Partial {
			reviewModes[pkey] = ports.ReviewWriteMerge
		} else {
			reviewModes[pkey] = ports.ReviewWriteReplace
		}
		cacheDelete(o.Cache.ReviewRefreshFailed, &o.Cache.reviewFailedOrder, pkey)
	}
}

func reviewDecisionPriority(d domain.ReviewDecision) int {
	switch d {
	case domain.ReviewChangesRequest:
		return 3
	case domain.ReviewRequired:
		return 2
	case domain.ReviewApproved:
		return 1
	default:
		return 0
	}
}

func (o *Observer) needsReviewRefresh(key string, local domain.PullRequest, decision string, hasObs bool, updatedAtProvider, now time.Time) bool {
	if o.Cache.ReviewRefreshFailed[key] {
		return true
	}
	if local.ReviewHash == "" {
		return true
	}
	if local.ReviewObservedAt.IsZero() {
		return true
	}
	if decision == string(domain.ReviewChangesRequest) {
		last := o.Cache.LastReviewPollAt[key]
		return last.IsZero() || now.Sub(last) >= o.reviewInterval
	}
	if hasObs && decision != string(local.Review) {
		return true
	}
	if local.ReviewHash != "" && string(local.Review) == string(domain.ReviewChangesRequest) && decision != string(domain.ReviewChangesRequest) {
		return true
	}
	if hasObs && !updatedAtProvider.IsZero() && updatedAtProvider.After(local.ReviewObservedAt) {
		return true
	}
	return false
}

func (o *Observer) prepareForPersistence(obs ports.SCMObservation, local domain.PullRequest, opts persistenceOptions, now time.Time) ports.SCMObservation {
	metadataHash := metadataSemanticHash(obs)
	if opts.preserveLocalMetadataHash {
		metadataHash = local.MetadataHash
	}
	ciHash := ciSemanticHash(obs.CI)
	if opts.preserveLocalCIHash {
		ciHash = local.CIHash
	}
	reviewHash := local.ReviewHash
	if !opts.preserveLocalReviewHash && (opts.reviewFetched || local.ReviewHash == "" || obs.Review.Decision != string(local.Review)) {
		reviewHash = reviewSemanticHash(obs.Review)
	}
	obs.Changed = ports.SCMChanged{
		Metadata: metadataHash != local.MetadataHash,
		CI:       ciHash != local.CIHash,
		Review:   reviewHash != local.ReviewHash,
	}
	if opts.reviewFetched && obs.Review.Partial != local.ReviewPartial {
		obs.Changed.Review = true
	}
	obs.PR.State = firstNonEmpty(obs.PR.State, normalizePRState(obs.PR.Draft, obs.PR.Merged, obs.PR.Closed))
	obs.ObservedAt = firstTime(obs.ObservedAt, now)
	return obs
}

func domainFromObservation(sessionID domain.SessionID, sessionRecord domain.SessionRecord, obs ports.SCMObservation, local domain.PullRequest, opts persistenceOptions, now time.Time) (domain.PullRequest, []domain.PullRequestCheck, []domain.PullRequestReview, []domain.PullRequestReviewThread, []domain.PullRequestComment) {
	metadataHash := metadataSemanticHash(obs)
	if opts.preserveLocalMetadataHash {
		metadataHash = local.MetadataHash
	}
	ciHash := ciSemanticHash(obs.CI)
	if opts.preserveLocalCIHash {
		ciHash = local.CIHash
	}
	reviewHash := reviewSemanticHash(obs.Review)
	reviewDecision := domain.ReviewDecision(firstNonEmpty(obs.Review.Decision, string(domain.ReviewNone)))
	if opts.preserveLocalReviewDecision {
		reviewDecision = local.Review
	}
	if opts.preserveLocalReviewHash {
		reviewHash = local.ReviewHash
	} else if !opts.reviewFetched && local.ReviewHash != "" && reviewDecision == local.Review {
		reviewHash = local.ReviewHash
	}
	observedAt := obs.ObservedAt
	if !obs.Changed.Metadata && !obs.Changed.CI && !local.ObservedAt.IsZero() {
		observedAt = local.ObservedAt
	}
	ciObservedAt := local.CIObservedAt
	if obs.Changed.CI || ciObservedAt.IsZero() {
		ciObservedAt = obs.ObservedAt
	}
	reviewObservedAt := local.ReviewObservedAt
	if opts.reviewFetched {
		reviewObservedAt = obs.ObservedAt
	}
	reviewPartial := local.ReviewPartial
	if opts.reviewFetched {
		reviewPartial = obs.Review.Partial
	}
	pr := domain.PullRequest{
		URL:                      firstNonEmpty(obs.PR.URL, obs.PR.HTMLURL),
		URLAlias:                 obs.PR.URLAlias,
		SessionID:                sessionID,
		Number:                   obs.PR.Number,
		Draft:                    obs.PR.Draft,
		Merged:                   obs.PR.Merged,
		Closed:                   obs.PR.Closed,
		CI:                       domain.CIState(firstNonEmpty(obs.CI.Summary, string(domain.CIUnknown))),
		Review:                   reviewDecision,
		Mergeability:             domain.Mergeability(firstNonEmpty(obs.Mergeability.State, string(domain.MergeUnknown))),
		UpdatedAt:                now,
		Provider:                 obs.Provider,
		Host:                     obs.Host,
		Repo:                     obs.Repo,
		ProviderID:               obs.PR.ProviderID,
		SourceBranch:             obs.PR.SourceBranch,
		TargetBranch:             obs.PR.TargetBranch,
		HeadSHA:                  obs.PR.HeadSHA,
		Title:                    obs.PR.Title,
		Additions:                obs.PR.Additions,
		Deletions:                obs.PR.Deletions,
		ChangedFiles:             obs.PR.ChangedFiles,
		Author:                   obs.PR.Author,
		AuthorAvatarURL:          obs.PR.AuthorAvatarURL,
		BaseSHA:                  obs.PR.BaseSHA,
		MergeCommitSHA:           obs.PR.MergeCommitSHA,
		ProviderState:            obs.PR.ProviderState,
		ProviderMergeable:        obs.PR.ProviderMergeable,
		ProviderMergeStateStatus: obs.PR.ProviderMergeStateStatus,
		HTMLURL:                  obs.PR.HTMLURL,
		CreatedAtProvider:        obs.PR.CreatedAtProvider,
		UpdatedAtProvider:        obs.PR.UpdatedAtProvider,
		MergedAtProvider:         obs.PR.MergedAtProvider,
		ClosedAtProvider:         obs.PR.ClosedAtProvider,
		MetadataHash:             metadataHash,
		CIHash:                   ciHash,
		ReviewHash:               reviewHash,
		ObservedAt:               observedAt,
		CIObservedAt:             ciObservedAt,
		ReviewObservedAt:         reviewObservedAt,
		ReviewPartial:            reviewPartial,
	}
	if obs.PR.Details != nil {
		details := *obs.PR.Details
		details.ObservedAt = obs.ObservedAt
		pr.Details = &details
	}
	checks := make([]domain.PullRequestCheck, 0, len(obs.CI.Checks))
	for _, ch := range obs.CI.Checks {
		checks = append(checks, domain.PullRequestCheck{Name: ch.Name, CommitHash: obs.CI.HeadSHA, Status: domain.PRCheckStatus(ch.Status), Conclusion: ch.Conclusion, URL: ch.URL, Details: ch.ProviderID, LogTail: ch.LogTail, CreatedAt: now})
	}
	reviews := make([]domain.PullRequestReview, 0, len(obs.Review.Reviews))
	for _, review := range obs.Review.Reviews {
		reviews = append(reviews, domain.PullRequestReview{
			ID:               review.ID,
			Author:           review.Author,
			State:            domain.ReviewDecision(firstNonEmpty(review.State, string(domain.ReviewNone))),
			URL:              review.URL,
			Body:             review.Body,
			IsBot:            review.IsBot,
			TargetSHA:        review.TargetSHA,
			SubmittedAt:      firstTime(review.SubmittedAt, now),
			AutoInjectReview: sessionRecord.AutoInjectReview,
		})
	}
	threads := make([]domain.PullRequestReviewThread, 0, len(obs.Review.Threads))
	commentCount := 0
	for _, th := range obs.Review.Threads {
		commentCount += len(th.Comments)
	}
	comments := make([]domain.PullRequestComment, 0, commentCount)
	for _, th := range obs.Review.Threads {
		threads = append(threads, domain.PullRequestReviewThread{ThreadID: th.ID, Path: th.Path, Line: th.Line, Resolved: th.Resolved, IsBot: th.IsBot, SemanticHash: threadSemanticHash(th), UpdatedAt: now})
		for _, c := range th.Comments {
			comments = append(comments, domain.PullRequestComment{ThreadID: th.ID, ReviewID: c.ReviewID, ID: c.ID, Author: c.Author, File: th.Path, Line: th.Line, Body: c.Body, URL: c.URL, Resolved: th.Resolved, IsBot: c.IsBot, CreatedAt: firstTime(c.CreatedAt, now), AutoInjectReview: sessionRecord.AutoInjectReview})
		}
	}
	return pr, checks, reviews, threads, comments
}

func observationFromLocal(repo ports.SCMRepo, pr domain.PullRequest, checks []domain.PullRequestCheck) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:      true,
		Provider:     firstNonEmpty(pr.Provider, repo.Provider),
		Host:         firstNonEmpty(pr.Host, repo.Host),
		Repo:         firstNonEmpty(pr.Repo, repoFullName(repo)),
		PR:           ports.SCMPRObservation{URL: pr.URL, Number: pr.Number, State: normalizePRState(pr.Draft, pr.Merged, pr.Closed), Draft: pr.Draft, Merged: pr.Merged, Closed: pr.Closed, SourceBranch: pr.SourceBranch, TargetBranch: pr.TargetBranch, HeadSHA: pr.HeadSHA, Title: pr.Title, Additions: pr.Additions, Deletions: pr.Deletions, ChangedFiles: pr.ChangedFiles, Author: pr.Author, AuthorAvatarURL: pr.AuthorAvatarURL, BaseSHA: pr.BaseSHA, MergeCommitSHA: pr.MergeCommitSHA, ProviderState: pr.ProviderState, ProviderMergeable: pr.ProviderMergeable, ProviderMergeStateStatus: pr.ProviderMergeStateStatus, HTMLURL: pr.HTMLURL, CreatedAtProvider: pr.CreatedAtProvider, UpdatedAtProvider: pr.UpdatedAtProvider, MergedAtProvider: pr.MergedAtProvider, ClosedAtProvider: pr.ClosedAtProvider},
		CI:           ciObservationFromLocal(pr, checks),
		Review:       ports.SCMReviewObservation{Decision: string(pr.Review)},
		Mergeability: mergeabilityObservationFromLocal(pr),
	}
}

func ciObservationFromLocal(pr domain.PullRequest, checks []domain.PullRequestCheck) ports.SCMCIObservation {
	ci := ports.SCMCIObservation{
		Summary:           firstNonEmpty(string(pr.CI), string(domain.CIUnknown)),
		HeadSHA:           pr.HeadSHA,
		FailedFingerprint: failedFingerprintFromCIHash(pr.CIHash),
	}
	tails := []string{}
	for _, ch := range checks {
		if pr.HeadSHA != "" && ch.CommitHash != "" && ch.CommitHash != pr.HeadSHA {
			continue
		}
		if ci.HeadSHA == "" {
			ci.HeadSHA = ch.CommitHash
		}
		check := ports.SCMCheckObservation{
			Name:       ch.Name,
			Status:     string(ch.Status),
			Conclusion: ch.Conclusion,
			URL:        ch.URL,
			LogTail:    ch.LogTail,
			ProviderID: ch.Details,
		}
		ci.Checks = append(ci.Checks, check)
		if ch.Status == domain.PRCheckFailed {
			ci.FailedChecks = append(ci.FailedChecks, check)
			if ch.LogTail != "" {
				tails = append(tails, ch.LogTail)
			}
		}
	}
	ci.FailureLogTail = strings.Join(tails, "\n---\n")
	return ci
}

func failedFingerprintFromCIHash(hash string) string {
	before, _, ok := strings.Cut(hash, ":")
	if !ok {
		return ""
	}
	return before
}

func mergeabilityObservationFromLocal(pr domain.PullRequest) ports.SCMMergeabilityObservation {
	out := mergeabilityFromProviderFacts(pr.ProviderMergeable, pr.ProviderMergeStateStatus, string(pr.CI), string(pr.Review), pr.Draft)
	if pr.Mergeability != "" && out.State != string(pr.Mergeability) {
		out = ports.SCMMergeabilityObservation{State: string(pr.Mergeability)}
	} else if pr.Mergeability != "" {
		out.State = string(pr.Mergeability)
	}
	switch domain.Mergeability(out.State) {
	case domain.MergeMergeable:
		out.Mergeable = true
	case domain.MergeConflicting:
		out.Conflict = true
		if len(out.Blockers) == 0 {
			out.Blockers = append(out.Blockers, "conflicts")
		}
	case domain.MergeBlocked:
		if len(out.Blockers) == 0 {
			out.Blockers = mergeBlockersFromLocal(pr)
		}
	}
	return out
}

func mergeBlockersFromLocal(pr domain.PullRequest) []string {
	blockers := []string{}
	if pr.Draft {
		blockers = append(blockers, "draft")
	}
	if pr.CI == domain.CIFailing {
		blockers = append(blockers, "ci_failing")
	}
	switch pr.Review {
	case domain.ReviewChangesRequest:
		blockers = append(blockers, "changes_requested")
	case domain.ReviewRequired:
		blockers = append(blockers, "review_required")
	}
	if len(blockers) == 0 {
		blockers = append(blockers, "blocked_by_provider")
	}
	return blockers
}

func mergeabilityFromProviderFacts(providerMergeable, providerMergeState, ci, review string, draft bool) ports.SCMMergeabilityObservation {
	state := strings.ToUpper(strings.TrimSpace(providerMergeState))
	mergeable := strings.ToUpper(strings.TrimSpace(providerMergeable))
	out := ports.SCMMergeabilityObservation{State: string(domain.MergeUnknown)}
	addBlocker := func(b string) { out.Blockers = append(out.Blockers, b) }
	if state == "DIRTY" || mergeable == "CONFLICTING" {
		out.State = string(domain.MergeConflicting)
		out.Conflict = true
		addBlocker("conflicts")
		return out
	}
	if state == "BEHIND" || state == "BEHIND_BASE" {
		out.BehindBase = true
		addBlocker("behind_base")
	}
	if state == "BLOCKED" {
		out.State = string(domain.MergeBlocked)
		addBlocker("blocked_by_provider")
	}
	if state == "UNSTABLE" {
		out.State = string(domain.MergeUnstable)
		return out
	}
	if draft {
		out.State = string(domain.MergeBlocked)
		addBlocker("draft")
	}
	if ci == string(domain.CIFailing) {
		out.State = string(domain.MergeBlocked)
		addBlocker("ci_failing")
	}
	switch review {
	case string(domain.ReviewChangesRequest):
		out.State = string(domain.MergeBlocked)
		addBlocker("changes_requested")
	case string(domain.ReviewRequired):
		out.State = string(domain.MergeBlocked)
		addBlocker("review_required")
	}
	if out.State == string(domain.MergeBlocked) {
		return out
	}
	if mergeable == "MERGEABLE" && (state == "CLEAN" || state == "HAS_HOOKS" || state == "") &&
		(review == "" || review == string(domain.ReviewApproved) || review == string(domain.ReviewNone)) && !draft {
		out.State = string(domain.MergeMergeable)
		out.Mergeable = true
		return out
	}
	return out
}

func chunks[T any](in []T, n int) [][]T {
	if n <= 0 || len(in) == 0 {
		return nil
	}
	out := make([][]T, 0, (len(in)+n-1)/n)
	for len(in) > 0 {
		end := n
		if len(in) < end {
			end = len(in)
		}
		out = append(out, in[:end])
		in = in[end:]
	}
	return out
}

func metadataSemanticHash(obs ports.SCMObservation) string {
	return stableHash(map[string]any{"provider": obs.Provider, "host": obs.Host, "repo": obs.Repo, "pr": obs.PR, "mergeability": obs.Mergeability})
}

func ciSemanticHash(ci ports.SCMCIObservation) string {
	h := stableHash(map[string]any{"summary": ci.Summary, "head": ci.HeadSHA, "checks": ci.Checks, "failed": ci.FailedChecks, "tail": ci.FailureLogTail})
	if ci.FailedFingerprint != "" {
		return ci.FailedFingerprint + ":" + h
	}
	return h
}

func reviewSemanticHash(review ports.SCMReviewObservation) string {
	type reviewHashPayload struct {
		Decision string
		Reviews  []ports.SCMReviewSummaryObservation
		Threads  []ports.SCMReviewThreadObservation
		Partial  bool `json:",omitempty"`
	}
	return stableHash(reviewHashPayload{Decision: review.Decision, Reviews: review.Reviews, Threads: review.Threads, Partial: review.Partial})
}

func threadSemanticHash(th ports.SCMReviewThreadObservation) string {
	return stableHash(th)
}

func stableHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf("%#v", v))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func identityPRKey(provider, host, providerID string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + ":" +
		strings.ToLower(strings.TrimSpace(host)) + "@" + strings.TrimSpace(providerID)
}

func keyForTrackedPR(pr domain.PullRequest, repo ports.SCMRepo) string {
	if pr.Provider != "" && pr.Host != "" && pr.ProviderID != "" {
		return identityPRKey(pr.Provider, pr.Host, pr.ProviderID)
	}
	return prKey(repo, pr.Number)
}

func prKey(repo ports.SCMRepo, number int) string {
	base := repo.Provider + ":" + repo.Host + ":" + repoFullName(repo)
	if number <= 0 {
		return base
	}
	return base + "#" + fmt.Sprint(number)
}

func commitKey(repo ports.SCMRepo, sha string) string { return prKey(repo, 0) + "@" + sha }

func repoFullName(repo ports.SCMRepo) string {
	if repo.Repo != "" {
		return repo.Repo
	}
	return repo.Owner + "/" + repo.Name
}

func (o *Observer) recordRename(ctx context.Context, repo ports.SCMRepo, pulls []ports.SCMPRObservation) {
	if len(pulls) == 0 || pulls[0].BaseRepo == "" {
		return
	}
	r := domain.RepositoryRename{
		Host:    strings.ToLower(repo.Host),
		OldName: strings.ToLower(repoFullName(repo)),
		Name:    strings.ToLower(pulls[0].BaseRepo),
	}
	key := r.Host + "/" + r.OldName
	if r.Name == r.OldName || o.renames[key] == r.Name {
		return
	}
	if err := o.store.RecordRepositoryRename(ctx, r); err != nil {
		o.logger.Warn("scm observer: record repository rename failed", "repo", key, "err", err)
		return
	}
	if o.renames == nil {
		o.renames = map[string]string{}
	}
	o.renames[key] = r.Name
}

func ownerOf(full string) string {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

func nameOf(full string) string {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return full
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstTime(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}

func normalizePRState(draft, merged, closed bool) string {
	switch {
	case merged:
		return string(domain.PRStateMerged)
	case closed:
		return string(domain.PRStateClosed)
	case draft:
		return string(domain.PRStateDraft)
	default:
		return string(domain.PRStateOpen)
	}
}

func resolveGitOriginURL(path string) string {
	out, err := aoprocess.Command("git", "-C", path, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitRemoteURLs(ctx context.Context, path string) []string {
	out, err := aoprocess.CommandContext(ctx, "git", "-C", path, "remote").Output()
	if err != nil {
		return nil
	}
	var urls []string
	seen := make(map[string]bool)
	for _, name := range strings.Fields(string(out)) {
		for _, options := range [][]string{{"--all"}, {"--push", "--all"}} {
			args := append([]string{"-C", path, "remote", "get-url"}, options...)
			args = append(args, name)
			output, err := aoprocess.CommandContext(ctx, "git", args...).Output()
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(output), "\n") {
				address := strings.TrimSpace(line)
				if address != "" && !seen[address] {
					seen[address] = true
					urls = append(urls, address)
				}
			}
		}
	}
	return urls
}

var gitRemoteURLsFunc = gitRemoteURLs

func scrubLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func (o *Observer) cacheSetString(m map[string]string, order *[]string, key, value string) {
	observe.CacheSet(m, order, o.Cache.max, key, value)
}

func (o *Observer) cacheSetTime(m map[string]time.Time, order *[]string, key string, value time.Time) {
	observe.CacheSet(m, order, o.Cache.max, key, value)
}

func (o *Observer) cacheSetBool(m map[string]bool, order *[]string, key string, value bool) {
	observe.CacheSet(m, order, o.Cache.max, key, value)
}

func cacheDelete[V any](m map[string]V, order *[]string, key string) {
	observe.CacheDelete(m, order, key)
}

func (o *Observer) inRateLimitCooldown(now time.Time, providerKey string) bool {
	if o.rateLimitUntil == nil {
		return false
	}
	until, ok := o.rateLimitUntil[providerKey]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(o.rateLimitUntil, providerKey)
	return false
}

func (o *Observer) setRateLimitCooldown(now time.Time, providerKey string, d time.Duration) {
	if o.rateLimitUntil == nil {
		o.rateLimitUntil = map[string]time.Time{}
	}
	o.rateLimitUntil[providerKey] = now.Add(d)
}
