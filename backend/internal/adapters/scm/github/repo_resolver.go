package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	repoResolveTTL        = 12 * time.Hour
	repoResolveFailureTTL = 10 * time.Minute
	repoResolveTimeout    = 15 * time.Second
)

// RepoResolver maps a github.com owner/name to the repository's current
// identity. GET /repos/{owner}/{repo} follows renames and transfers (GitHub
// answers the old name with a redirect to /repositories/{id}), so an origin
// that still uses an old org name resolves to the renamed repository.
// Results are cached per owner/name; failures are cached briefly so a
// missing token or an outage does not cost a request on every read.
type RepoResolver struct {
	client *Client
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]repoResolution
	group singleflight.Group
}

type repoResolution struct {
	repo    ports.GitHubRepository
	err     error
	expires time.Time
}

var _ ports.GitHubRepositoryResolver = (*RepoResolver)(nil)

// NewRepoResolver returns a resolver that shares the provider's client and auth.
func NewRepoResolver(p *Provider) *RepoResolver {
	return &RepoResolver{client: p.client, now: time.Now, cache: map[string]repoResolution{}}
}

// ResolveGitHubRepository returns the repository's current id and full name.
// The lookup itself runs detached from ctx: a caller that stops waiting (for
// example a read with a short deadline) still lets it finish and fill the
// cache for the next read.
func (r *RepoResolver) ResolveGitHubRepository(ctx context.Context, owner, name string) (ports.GitHubRepository, error) {
	owner, name = strings.TrimSpace(owner), strings.TrimSuffix(strings.TrimSpace(name), ".git")
	if owner == "" || name == "" {
		return ports.GitHubRepository{}, errors.New("github scm: repository owner and name are required")
	}
	key := strings.ToLower(owner + "/" + name)
	r.mu.Lock()
	cached, ok := r.cache[key]
	r.mu.Unlock()
	if ok && r.now().Before(cached.expires) {
		return cached.repo, cached.err
	}
	result := r.group.DoChan(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), repoResolveTimeout)
		defer cancel()
		repo, err := r.fetch(fetchCtx, owner, name)
		ttl := repoResolveTTL
		if err != nil {
			ttl = repoResolveFailureTTL
			// A transient failure (offline, 5xx, rate limit) keeps the last
			// good answer, so an expired entry does not flip the project back
			// to its stale owner. A 404 means the repository is gone or no
			// longer visible, so it replaces the old answer.
			if ok && cached.err == nil && !errors.Is(err, ErrNotFound) {
				repo, err = cached.repo, nil
			}
		}
		r.mu.Lock()
		r.cache[key] = repoResolution{repo: repo, err: err, expires: r.now().Add(ttl)}
		r.mu.Unlock()
		return repo, err
	})
	select {
	case res := <-result:
		repo, _ := res.Val.(ports.GitHubRepository)
		return repo, res.Err
	case <-ctx.Done():
		return ports.GitHubRepository{}, ctx.Err()
	}
}

func (r *RepoResolver) fetch(ctx context.Context, owner, name string) (ports.GitHubRepository, error) {
	resp, err := r.client.doREST(ctx, http.MethodGet, repoPath(owner, name), nil, nil)
	if err != nil {
		return ports.GitHubRepository{}, err
	}
	var body struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Owner    struct {
			AvatarURL string `json:"avatar_url"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return ports.GitHubRepository{}, fmt.Errorf("github scm: decode repository: %w", err)
	}
	fullName := strings.TrimSpace(body.FullName)
	if body.ID == 0 || strings.Count(fullName, "/") != 1 || strings.HasPrefix(fullName, "/") || strings.HasSuffix(fullName, "/") {
		return ports.GitHubRepository{}, errors.New("github scm: repository response missing id or full_name")
	}
	return ports.GitHubRepository{ID: body.ID, FullName: fullName, OwnerAvatarURL: ownerAvatarURL(body.Owner.AvatarURL)}, nil
}

// ownerAvatarURL keeps only an HTTPS avatars.githubusercontent.com URL, the
// one host the renderer loads GitHub avatars from.
func ownerAvatarURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != "avatars.githubusercontent.com" || u.User != nil {
		return ""
	}
	return u.String()
}
