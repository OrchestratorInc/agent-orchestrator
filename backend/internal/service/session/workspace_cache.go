package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const workspaceCacheTTL = 3 * time.Second

const workspacePathsTimeout = 30 * time.Second

type workspaceCacheKey struct {
	session domain.SessionID
	root    string
}

func (k workspaceCacheKey) String() string {
	return string(k.session) + "\x00" + k.root
}

type workspaceCacheEntry struct {
	at      time.Time
	compare workspaceCompareTarget
	changes workspaceChangeSet
}

type workspacePathsEntry struct {
	at    time.Time
	paths []string
}

type workspaceCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[workspaceCacheKey]workspaceCacheEntry
	loads   map[workspaceCacheKey]*workspaceCacheEntry
	paths   map[workspaceCacheKey]*workspacePathsEntry
}

func newWorkspaceCache(ttl time.Duration, now func() time.Time) *workspaceCache {
	if now == nil {
		now = time.Now
	}
	return &workspaceCache{
		ttl: ttl, now: now,
		entries: make(map[workspaceCacheKey]workspaceCacheEntry),
		loads:   make(map[workspaceCacheKey]*workspaceCacheEntry),
		paths:   make(map[workspaceCacheKey]*workspacePathsEntry),
	}
}

func (c *workspaceCache) get(key workspaceCacheKey) (workspaceCacheEntry, bool) {
	if c == nil {
		return workspaceCacheEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return workspaceCacheEntry{}, false
	}
	if c.now().Sub(entry.at) > c.ttl {
		delete(c.entries, key)
		return workspaceCacheEntry{}, false
	}
	return entry, true
}

func (c *workspaceCache) beginLoad(key workspaceCacheKey) *workspaceCacheEntry {
	entry := new(workspaceCacheEntry)
	if c == nil {
		return entry
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loads[key] = entry
	return entry
}

func (c *workspaceCache) finishLoad(key workspaceCacheKey, entry *workspaceCacheEntry, publish bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loads[key] != entry {
		return
	}
	delete(c.loads, key)
	if publish {
		c.entries[key] = *entry
	}
}

func (c *workspaceCache) invalidateSession(id domain.SessionID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if key.session == id {
			delete(c.entries, key)
		}
	}
	for key := range c.loads {
		if key.session == id {
			delete(c.loads, key)
		}
	}
	for key := range c.paths {
		if key.session == id {
			delete(c.paths, key)
		}
	}
}

func (s *Service) workspacePaths(ctx context.Context, id domain.SessionID, root string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := s.workspaceCache
	if c == nil {
		return gitLsFilesParts(ctx, root)
	}
	key := workspaceCacheKey{session: id, root: root}
	get := func() ([]string, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		entry := c.paths[key]
		if entry != nil && !entry.at.IsZero() && c.now().Sub(entry.at) <= c.ttl {
			return entry.paths, true
		}
		return nil, false
	}
	if paths, ok := get(); ok {
		return paths, nil
	}
	resultCh := s.workspaceGroup.DoChan(key.String()+"\x00paths", func() (any, error) {
		if paths, ok := get(); ok {
			return paths, nil
		}
		entry := new(workspacePathsEntry)
		c.mu.Lock()
		if len(c.paths) >= 128 {
			var oldest workspaceCacheKey
			for candidate, value := range c.paths {
				if oldest.root == "" || value.at.Before(c.paths[oldest].at) {
					oldest = candidate
				}
			}
			delete(c.paths, oldest)
		}
		c.paths[key] = entry
		c.mu.Unlock()
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspacePathsTimeout)
		defer cancel()
		paths, loadErr := gitLsFilesParts(loadCtx, root)
		c.mu.Lock()
		if c.paths[key] == entry {
			if loadErr != nil {
				delete(c.paths, key)
			} else {
				entry.paths, entry.at = paths, c.now()
			}
		}
		c.mu.Unlock()
		return paths, loadErr
	})
	var result singleflight.Result
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result = <-resultCh:
	}
	if result.Err != nil {
		return nil, result.Err
	}
	paths, ok := result.Val.([]string)
	if !ok {
		return nil, fmt.Errorf("workspacePaths: unexpected singleflight result type %T", result.Val)
	}
	return paths, nil
}
