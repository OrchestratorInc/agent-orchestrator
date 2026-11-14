package gitlab

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	mrDetailCacheTTL = 60 * time.Second

	approvalsCacheTTL = 60 * time.Second

	forkProjectCacheTTL = 5 * time.Minute
)

type cacheEntry[T any] struct {
	value     T
	fetchedAt time.Time
}

type cache struct {
	mu sync.Mutex

	now func() time.Time

	mrDetail map[string]cacheEntry[*restMR]

	approvals map[string]cacheEntry[*restApprovals]

	forkProject map[string]cacheEntry[string]
}

func newCache() *cache {
	return &cache{
		now:         time.Now,
		mrDetail:    map[string]cacheEntry[*restMR]{},
		approvals:   map[string]cacheEntry[*restApprovals]{},
		forkProject: map[string]cacheEntry[string]{},
	}
}

func (c *cache) getMRDetail(host, repo string, iid int) (*restMR, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return getCacheEntry(c.mrDetail, mrKey(host, repo, iid), mrDetailCacheTTL, c.now())
}

func (c *cache) setMRDetail(host, repo string, iid int, mr *restMR) {
	c.mu.Lock()
	defer c.mu.Unlock()
	setCacheEntry(c.mrDetail, mrKey(host, repo, iid), mr, c.now())
}

func (c *cache) getApprovals(host, repo string, iid int) (*restApprovals, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return getCacheEntry(c.approvals, mrKey(host, repo, iid), approvalsCacheTTL, c.now())
}

func (c *cache) setApprovals(host, repo string, iid int, a *restApprovals) {
	c.mu.Lock()
	defer c.mu.Unlock()
	setCacheEntry(c.approvals, mrKey(host, repo, iid), a, c.now())
}

func (c *cache) getForkProject(host string, sourceProjectID int) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return getCacheEntry(c.forkProject, forkProjectKey(host, sourceProjectID), forkProjectCacheTTL, c.now())
}

func (c *cache) setForkProject(host string, sourceProjectID int, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	setCacheEntry(c.forkProject, forkProjectKey(host, sourceProjectID), path, c.now())
}

func mrKey(host, repo string, iid int) string {
	return strings.ToLower(host) + "|" + strings.ToLower(repo) + "|" + fmt.Sprint(iid)
}

func forkProjectKey(host string, sourceProjectID int) string {
	return strings.ToLower(host) + "|" + fmt.Sprint(sourceProjectID)
}

func getCacheEntry[T any](m map[string]cacheEntry[T], key string, ttl time.Duration, now time.Time) (T, bool) {
	e, ok := m[key]
	if !ok {
		var zero T
		return zero, false
	}
	if now.Sub(e.fetchedAt) > ttl {
		var zero T
		return zero, false
	}
	return e.value, true
}

func setCacheEntry[T any](m map[string]cacheEntry[T], key string, value T, now time.Time) {
	m[key] = cacheEntry[T]{value: value, fetchedAt: now}
}
