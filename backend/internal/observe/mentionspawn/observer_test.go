package mentionspawn

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var t0 = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

type fakeProvider struct {
	mentions  []ports.SCMRepoMention
	pulls     map[int]ports.SCMPRObservation
	pushers   map[string]bool
	permCalls int
	sinces    []time.Time
}

func (f *fakeProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	if remote != "https://github.com/o/r.git" {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}, true
}

func (f *fakeProvider) ListRepoMentions(_ context.Context, _ ports.SCMRepo, since time.Time) ([]ports.SCMRepoMention, error) {
	f.sinces = append(f.sinces, since)
	return f.mentions, nil
}

func (f *fakeProvider) FetchPullHead(_ context.Context, ref ports.SCMPRRef) (ports.SCMPRObservation, error) {
	p, ok := f.pulls[ref.Number]
	if !ok {
		return ports.SCMPRObservation{}, ports.ErrSCMNotFound
	}
	return p, nil
}

func (f *fakeProvider) CanPush(_ context.Context, _ ports.SCMRepo, login string) (bool, error) {
	f.permCalls++
	return f.pushers[login], nil
}

type fakeStore struct {
	projects []domain.ProjectRecord
	prs      map[string]domain.PullRequest
	sessions map[domain.SessionID]domain.SessionRecord
}

func (f *fakeStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return f.projects, nil
}

func (f *fakeStore) GetPR(_ context.Context, url string) (domain.PullRequest, bool, error) {
	pr, ok := f.prs[url]
	return pr, ok, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s, ok := f.sessions[id]
	return s, ok, nil
}

type fakeSessions struct {
	spawned  []ports.SpawnConfig
	claimed  map[domain.SessionID]string
	spawnErr error
	claimErr error
}

func (f *fakeSessions) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	if f.spawnErr != nil {
		return domain.Session{}, 0, 0, f.spawnErr
	}
	f.spawned = append(f.spawned, cfg)
	var s domain.Session
	s.ID = domain.SessionID("vibeli-" + string(rune('0'+len(f.spawned))))
	return s, 0, 0, nil
}

func (f *fakeSessions) ClaimPR(_ context.Context, id domain.SessionID, prURL string) error {
	if f.claimErr != nil {
		return f.claimErr
	}
	if f.claimed == nil {
		f.claimed = map[domain.SessionID]string{}
	}
	f.claimed[id] = prURL
	return nil
}

type fakeFetcher struct {
	calls []string
	err   error
}

func (f *fakeFetcher) FetchBranch(_ context.Context, repoPath, branch string) error {
	f.calls = append(f.calls, repoPath+"@"+branch)
	return f.err
}

type harness struct {
	provider *fakeProvider
	store    *fakeStore
	sessions *fakeSessions
	fetcher  *fakeFetcher
	obs      *Observer
	now      time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		provider: &fakeProvider{
			pulls:   map[int]ports.SCMPRObservation{1035: openPull(1035, "feature/login", "o/r")},
			pushers: map[string]bool{"alice": true},
		},
		store: &fakeStore{
			projects: []domain.ProjectRecord{{ID: "vibeli", Path: "/home/node/repo", RepoOriginURL: "https://github.com/o/r.git"}},
			prs:      map[string]domain.PullRequest{},
			sessions: map[domain.SessionID]domain.SessionRecord{},
		},
		sessions: &fakeSessions{},
		fetcher:  &fakeFetcher{},
		now:      t0,
	}
	h.obs = New(h.provider, h.store, h.sessions, h.fetcher, Config{Clock: func() time.Time { return h.now }})
	return h
}

func openPull(n int, branch, headRepo string) ports.SCMPRObservation {
	return ports.SCMPRObservation{Number: n, SourceBranch: branch, HeadRepo: headRepo, Title: "Fix login", State: "open"}
}

func mention(id string, pr int, author string, at time.Time) ports.SCMRepoMention {
	return ports.SCMRepoMention{
		PRNumber: pr,
		PRURL:    "https://github.com/o/r/pull/" + strconv.Itoa(pr),
		Mention:  ports.SCMMentionObservation{ID: id, Author: author, Body: "@ao раскатай на дев", CreatedAt: at},
	}
}

func (h *harness) poll(t *testing.T) {
	t.Helper()
	if err := h.obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMentionUnderUnownedPRStartsAgentOnItsBranch(t *testing.T) {
	h := newHarness(t)
	h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(time.Minute))}
	h.now = t0.Add(2 * time.Minute)

	h.poll(t)

	if len(h.sessions.spawned) != 1 {
		t.Fatalf("spawned = %+v, want one session", h.sessions.spawned)
	}
	cfg := h.sessions.spawned[0]
	if cfg.ProjectID != "vibeli" || cfg.Branch != "feature/login" || cfg.Kind != domain.KindWorker || !cfg.TerminateOnPRMerge || cfg.DisplayName != "pr-1035" {
		t.Fatalf("spawn cfg = %+v", cfg)
	}
	if !strings.Contains(cfg.Prompt, "https://github.com/o/r/pull/1035") || !strings.Contains(cfg.Prompt, "--ff-only origin/feature/login") {
		t.Fatalf("prompt = %q", cfg.Prompt)
	}
	if got := h.fetcher.calls; len(got) != 1 || got[0] != "/home/node/repo@feature/login" {
		t.Fatalf("fetches = %v, want the PR branch fetched into the project checkout first", got)
	}
	if h.sessions.claimed["vibeli-1"] != "https://github.com/o/r/pull/1035" {
		t.Fatalf("claimed = %v", h.sessions.claimed)
	}

	// The same comment on the next poll is not a second request.
	h.poll(t)
	if len(h.sessions.spawned) != 1 {
		t.Fatalf("spawned again: %+v", h.sessions.spawned)
	}
}

func TestOnlyNewestMentionPerPRCounts(t *testing.T) {
	h := newHarness(t)
	h.provider.mentions = []ports.SCMRepoMention{
		mention("1", 1035, "alice", t0.Add(time.Minute)),
		mention("2", 1035, "alice", t0.Add(3*time.Minute)),
	}
	h.now = t0.Add(4 * time.Minute)
	h.poll(t)
	if len(h.sessions.spawned) != 1 {
		t.Fatalf("spawned = %d sessions, want one per PR", len(h.sessions.spawned))
	}
}

func TestLiveOwnerKeepsThePR(t *testing.T) {
	h := newHarness(t)
	h.store.prs["https://github.com/o/r/pull/1035"] = domain.PullRequest{SessionID: "vibeli-9"}
	h.store.sessions["vibeli-9"] = domain.SessionRecord{CreatedAt: t0.Add(-time.Hour)}
	h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(time.Minute))}
	h.now = t0.Add(2 * time.Minute)
	h.poll(t)
	if len(h.sessions.spawned) != 0 {
		t.Fatalf("spawned = %+v, want mention delivery to handle a live owner", h.sessions.spawned)
	}
}

func TestTerminatedOwner(t *testing.T) {
	cases := []struct {
		name      string
		created   time.Time
		wantSpawn bool
	}{
		// The old session ended before anyone asked again: start a new one.
		{"ended before the comment", t0.Add(-time.Hour), true},
		// A session created after the comment already answered it — this is
		// how a daemon restart does not replay comments it acted on.
		{"started after the comment", t0.Add(2 * time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.store.prs["https://github.com/o/r/pull/1035"] = domain.PullRequest{SessionID: "vibeli-9"}
			h.store.sessions["vibeli-9"] = domain.SessionRecord{IsTerminated: true, CreatedAt: tc.created}
			h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(time.Minute))}
			h.now = t0.Add(3 * time.Minute)
			h.poll(t)
			if got := len(h.sessions.spawned) == 1; got != tc.wantSpawn {
				t.Fatalf("spawned = %+v, want spawn=%v", h.sessions.spawned, tc.wantSpawn)
			}
		})
	}
}

func TestAuthorWithoutPushCannotStartAgent(t *testing.T) {
	h := newHarness(t)
	h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "mallory", t0.Add(time.Minute))}
	h.now = t0.Add(2 * time.Minute)
	h.poll(t)
	h.poll(t)
	if len(h.sessions.spawned) != 0 || len(h.fetcher.calls) != 0 {
		t.Fatalf("spawned = %+v fetches = %v, want nothing for a non-pusher", h.sessions.spawned, h.fetcher.calls)
	}
	if h.provider.permCalls != 1 {
		t.Fatalf("permission calls = %d, want the rejected comment decided once", h.provider.permCalls)
	}
}

func TestUnworkablePRs(t *testing.T) {
	cases := map[string]ports.SCMPRObservation{
		"fork":   openPull(1035, "feature/login", "someone/r"),
		"draft":  func() ports.SCMPRObservation { p := openPull(1035, "feature/login", "o/r"); p.Draft = true; return p }(),
		"closed": func() ports.SCMPRObservation { p := openPull(1035, "feature/login", "o/r"); p.Closed = true; return p }(),
	}
	for name, pull := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.provider.pulls[1035] = pull
			h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(time.Minute))}
			h.now = t0.Add(2 * time.Minute)
			h.poll(t)
			if len(h.sessions.spawned) != 0 {
				t.Fatalf("spawned = %+v", h.sessions.spawned)
			}
		})
	}
}

func TestCommentsFromBeforeStartAreIgnored(t *testing.T) {
	h := newHarness(t)
	// Posted an hour before the daemon started: past the startup grace.
	h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(-time.Hour))}
	h.now = t0.Add(time.Minute)
	h.poll(t)
	if len(h.sessions.spawned) != 0 {
		t.Fatalf("spawned = %+v, want old comments ignored when the feature turns on", h.sessions.spawned)
	}
	if got := h.provider.sinces[0]; !got.Equal(t0.Add(-DefaultStartupGrace)) {
		t.Fatalf("since = %v, want start minus grace", got)
	}
}

func TestPerPollCap(t *testing.T) {
	h := newHarness(t)
	for i := 1; i <= 5; i++ {
		n := 1000 + i
		h.provider.pulls[n] = openPull(n, "b"+strconv.Itoa(i), "o/r")
		h.provider.mentions = append(h.provider.mentions, mention(strconv.Itoa(i), n, "alice", t0.Add(time.Duration(i)*time.Minute)))
	}
	h.now = t0.Add(10 * time.Minute)
	h.poll(t)
	if len(h.sessions.spawned) != DefaultMaxSpawnsPerPoll {
		t.Fatalf("spawned = %d, want the per-poll cap", len(h.sessions.spawned))
	}
	h.poll(t)
	if len(h.sessions.spawned) != 5 {
		t.Fatalf("spawned = %d after second poll, want the rest", len(h.sessions.spawned))
	}
}

func TestFailedFetchRetriesThenGivesUp(t *testing.T) {
	h := newHarness(t)
	h.fetcher.err = errors.New("couldn't find remote ref")
	h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(time.Minute))}
	h.now = t0.Add(2 * time.Minute)
	for i := 0; i < maxAttempts+2; i++ {
		h.poll(t)
	}
	if len(h.fetcher.calls) != maxAttempts {
		t.Fatalf("fetch attempts = %d, want %d", len(h.fetcher.calls), maxAttempts)
	}
	if len(h.sessions.spawned) != 0 {
		t.Fatalf("spawned = %+v, want no session without the PR branch", h.sessions.spawned)
	}
}

func TestClaimFailureKeepsSessionAndDoesNotRetry(t *testing.T) {
	h := newHarness(t)
	h.sessions.claimErr = errors.New("scm unavailable")
	h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(time.Minute))}
	h.now = t0.Add(2 * time.Minute)
	h.poll(t)
	h.poll(t)
	if len(h.sessions.spawned) != 1 {
		t.Fatalf("spawned = %d, want one session and no second agent on retry", len(h.sessions.spawned))
	}
}

func TestUnparseableAndScratchProjectsAreSkipped(t *testing.T) {
	h := newHarness(t)
	h.store.projects = []domain.ProjectRecord{
		{ID: "scratch", Kind: domain.ProjectKindScratch, Path: "/x", RepoOriginURL: "https://github.com/o/r.git"},
		{ID: "gitlab", Path: "/y", RepoOriginURL: "https://gitlab.com/o/r.git"},
	}
	h.provider.mentions = []ports.SCMRepoMention{mention("1", 1035, "alice", t0.Add(time.Minute))}
	h.now = t0.Add(2 * time.Minute)
	h.poll(t)
	if len(h.provider.sinces) != 0 || len(h.sessions.spawned) != 0 {
		t.Fatalf("scanned %d repos, spawned %d; want neither", len(h.provider.sinces), len(h.sessions.spawned))
	}
}
