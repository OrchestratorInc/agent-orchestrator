// Package prpreview reads the state and summary of any GitHub pull request a
// chat links to, so the renderer can show it as a chip with a hover card. It
// reuses the SCM provider the daemon already reads session PRs with, and
// stores nothing: results live in a small in-memory cache.
package prpreview

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// fetchTimeout bounds one provider read; a hover must not hang on GitHub.
	fetchTimeout = 10 * time.Second
	// successTTL is short: a PR's state, checks and diff move while people
	// work on it, and a chat reread minutes later should show the new state.
	successTTL = 2 * time.Minute
	// notFoundTTL covers a PR that does not exist or this account cannot see.
	notFoundTTL = 5 * time.Minute
	// fetchFailureTTL keeps a failing provider from being asked once per hover.
	fetchFailureTTL = 20 * time.Second
	// maxCacheEntries bounds the cache; links are user-driven.
	maxCacheEntries = 256
)

// ErrFetchFailed marks a provider failure other than the PR not existing. The
// controller renders it as 502, which apierr has no kind for.
var ErrFetchFailed = errors.New("prpreview: pull request read failed")

// Reader reads pull requests from their provider.
type Reader interface {
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
}

// Preview is what a chat shows for one pull request.
type Preview struct {
	// URL is the PR's canonical browser URL.
	URL    string
	Repo   string
	Number int
	Title  string
	// State is open, draft, merged or closed.
	State string
	// Checks is passing, failing or pending; empty when the PR has none.
	Checks       string
	Author       string
	SourceBranch string
	TargetBranch string
	Additions    int
	Deletions    int
	ChangedFiles int
	// StateChangedAt is when it merged or closed, else when it was opened.
	// Zero when the provider did not say.
	StateChangedAt time.Time
}

type failureKind int

const (
	failureNone failureKind = iota
	failureFetch
	failureNotFound
)

type cacheEntry struct {
	preview   Preview
	failure   failureKind
	fetchedAt time.Time
}

// Service reads and caches PR previews. It is safe for concurrent use.
type Service struct {
	reader       Reader
	fetchTimeout time.Duration
	now          func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
	flight  singleflight.Group
}

// New returns a Service that reads through reader.
func New(reader Reader) *Service {
	return &Service{reader: reader, fetchTimeout: fetchTimeout, now: time.Now, entries: map[string]cacheEntry{}}
}

// Preview returns the cached or freshly read preview of the GitHub pull
// request rawURL links to. A URL that is not a github.com pull request is a
// 400-class error; a PR that does not exist or is not visible is a 404.
func (s *Service) Preview(ctx context.Context, rawURL string) (Preview, error) {
	repo, number, err := ParseURL(rawURL)
	if err != nil {
		return Preview{}, err
	}
	key := fmt.Sprintf("%s#%d", strings.ToLower(repo.Repo), number)
	if entry, ok := s.lookup(key); ok {
		return entry.result()
	}
	// The read is detached from the caller: leaving a link cancels only that
	// hover's wait, and the result still lands in the cache for the next one.
	ch := s.flight.DoChan(key, func() (any, error) {
		defer sentryobs.RecoverBackground()
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.fetchTimeout)
		defer cancel()
		entry := s.read(readCtx, repo, number)
		s.store(key, entry)
		return entry, nil
	})
	select {
	case <-ctx.Done():
		return Preview{}, ctx.Err()
	case res := <-ch:
		entry, _ := res.Val.(cacheEntry)
		return entry.result()
	}
}

func (e cacheEntry) result() (Preview, error) {
	switch e.failure {
	case failureNotFound:
		return Preview{}, apierr.NotFound("PR_NOT_FOUND", "The pull request was not found or is not visible to this account")
	case failureFetch:
		return Preview{}, ErrFetchFailed
	default:
		return e.preview, nil
	}
}

func (s *Service) read(ctx context.Context, repo ports.SCMRepo, number int) cacheEntry {
	now := s.now()
	observations, err := s.reader.FetchPullRequests(ctx, []ports.SCMPRRef{{Repo: repo, Number: number}})
	if err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return cacheEntry{failure: failureNotFound, fetchedAt: now}
		}
		return cacheEntry{failure: failureFetch, fetchedAt: now}
	}
	if len(observations) != 1 {
		return cacheEntry{failure: failureFetch, fetchedAt: now}
	}
	o := observations[0]
	if o.Error != nil || !o.Fetched {
		if o.Error == nil || errors.Is(o.Error, ports.ErrSCMNotFound) {
			return cacheEntry{failure: failureNotFound, fetchedAt: now}
		}
		return cacheEntry{failure: failureFetch, fetchedAt: now}
	}
	return cacheEntry{preview: previewOf(o, repo, number), fetchedAt: now}
}

func previewOf(o ports.SCMObservation, repo ports.SCMRepo, number int) Preview {
	pr := o.PR
	p := Preview{
		URL:          firstNonEmpty(pr.HTMLURL, pr.URL, fmt.Sprintf("https://%s/%s/pull/%d", repo.Host, repo.Repo, number)),
		Repo:         firstNonEmpty(pr.BaseRepo, o.Repo, repo.Repo),
		Number:       number,
		Title:        pr.Title,
		State:        stateOf(pr),
		Author:       pr.Author,
		SourceBranch: pr.SourceBranch,
		TargetBranch: pr.TargetBranch,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		ChangedFiles: pr.ChangedFiles,
	}
	switch o.CI.Summary {
	case "passing", "failing", "pending":
		p.Checks = o.CI.Summary
	}
	switch p.State {
	case "merged":
		p.StateChangedAt = pr.MergedAtProvider
	case "closed":
		p.StateChangedAt = pr.ClosedAtProvider
	default:
		p.StateChangedAt = pr.CreatedAtProvider
	}
	return p
}

func stateOf(pr ports.SCMPRObservation) string {
	switch {
	case pr.Merged:
		return "merged"
	case pr.Closed:
		return "closed"
	case pr.Draft:
		return "draft"
	}
	switch pr.State {
	case "merged", "closed", "draft":
		return pr.State
	}
	return "open"
}

// ParseURL reads the repository and number from a github.com pull request
// URL, ignoring anything after the number (/files, #discussion…).
func ParseURL(rawURL string) (ports.SCMRepo, int, error) {
	invalid := apierr.Invalid("PR_URL_INVALID", "url must be a github.com pull request URL", nil)
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ports.SCMRepo{}, 0, invalid
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return ports.SCMRepo{}, 0, invalid
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] == "" || parts[2] != "pull" {
		return ports.SCMRepo{}, 0, invalid
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number <= 0 {
		return ports.SCMRepo{}, 0, invalid
	}
	return ports.SCMRepo{
		Provider: "github",
		Host:     "github.com",
		Owner:    parts[0],
		Name:     parts[1],
		Repo:     parts[0] + "/" + parts[1],
	}, number, nil
}

func (s *Service) lookup(key string) (cacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	ttl := successTTL
	switch entry.failure {
	case failureNotFound:
		ttl = notFoundTTL
	case failureFetch:
		ttl = fetchFailureTTL
	}
	if s.now().Sub(entry.fetchedAt) >= ttl {
		delete(s.entries, key)
		return cacheEntry{}, false
	}
	return entry, true
}

func (s *Service) store(key string, entry cacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[key]; !ok && len(s.entries) >= maxCacheEntries {
		// Evict the oldest entry; the cache is small, so a scan is fine.
		var oldest string
		var oldestAt time.Time
		for k, e := range s.entries {
			if oldest == "" || e.fetchedAt.Before(oldestAt) {
				oldest, oldestAt = k, e.fetchedAt
			}
		}
		delete(s.entries, oldest)
	}
	s.entries[key] = entry
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
