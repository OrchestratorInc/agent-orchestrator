package gitlab

import (
	"sync"
	"testing"
	"time"
)

func TestCache_SetGetWithinTTL(t *testing.T) {
	c := newCache()

	mr := &restMR{IID: 42, Title: "test MR"}
	c.setMRDetail("gitlab.com", "org/repo", 42, mr)
	gotMR, ok := c.getMRDetail("gitlab.com", "org/repo", 42)
	if !ok {
		t.Fatal("expected MR detail cache hit within TTL")
	}
	if gotMR != mr {
		t.Errorf("getMRDetail returned %p, want %p (same pointer)", gotMR, mr)
	}

	ap := &restApprovals{Approved: true, ApprovalsRequired: 1}
	c.setApprovals("gitlab.com", "org/repo", 42, ap)
	gotAp, ok := c.getApprovals("gitlab.com", "org/repo", 42)
	if !ok {
		t.Fatal("expected approvals cache hit within TTL")
	}
	if gotAp != ap {
		t.Errorf("getApprovals returned %p, want %p (same pointer)", gotAp, ap)
	}

	c.setForkProject("gitlab.com", 100, "fork/source-path")
	gotPath, ok := c.getForkProject("gitlab.com", 100)
	if !ok {
		t.Fatal("expected fork project cache hit within TTL")
	}
	if gotPath != "fork/source-path" {
		t.Errorf("getForkProject = %q, want %q", gotPath, "fork/source-path")
	}

	if _, ok := c.getMRDetail("gitlab.com", "org/repo", 99); ok {
		t.Error("expected miss for different IID")
	}
	if _, ok := c.getMRDetail("gitlab.com", "other/repo", 42); ok {
		t.Error("expected miss for different repo")
	}
	if _, ok := c.getMRDetail("gitlab.example.com", "org/repo", 42); ok {
		t.Error("expected miss for different host")
	}

	c.setMRDetail("GitLab.com", "Org/Repo", 7, &restMR{IID: 7})
	if got, ok := c.getMRDetail("gitlab.com", "org/repo", 7); !ok || got.IID != 7 {
		t.Errorf("case-insensitive MR detail lookup: ok=%v, got=%+v", ok, got)
	}
}

func TestCache_GetAfterTTLExpiry(t *testing.T) {
	c := newCache()
	clock := &syntheticClock{t: time.Now()}
	c.now = clock.now

	c.setMRDetail("gitlab.com", "org/repo", 42, &restMR{IID: 42})
	c.setApprovals("gitlab.com", "org/repo", 42, &restApprovals{Approved: true})
	c.setForkProject("gitlab.com", 100, "org/repo")

	if _, ok := c.getMRDetail("gitlab.com", "org/repo", 42); !ok {
		t.Fatal("expected MR detail hit before TTL expiry")
	}
	if _, ok := c.getApprovals("gitlab.com", "org/repo", 42); !ok {
		t.Fatal("expected approvals hit before TTL expiry")
	}
	if _, ok := c.getForkProject("gitlab.com", 100); !ok {
		t.Fatal("expected fork project hit before TTL expiry")
	}

	clock.advance(mrDetailCacheTTL + time.Second)
	if _, ok := c.getMRDetail("gitlab.com", "org/repo", 42); ok {
		t.Error("expected MR detail miss after TTL expiry")
	}
	if _, ok := c.getApprovals("gitlab.com", "org/repo", 42); ok {
		t.Error("expected approvals miss after TTL expiry")
	}
	if _, ok := c.getForkProject("gitlab.com", 100); !ok {
		t.Error("expected fork project hit (within 5min TTL) after short-TTL expiry")
	}

	clock.advance(forkProjectCacheTTL + time.Second)
	if _, ok := c.getForkProject("gitlab.com", 100); ok {
		t.Error("expected fork project miss after TTL expiry")
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	c := newCache()
	const goroutines = 50
	const iterations = 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				iid := n % 5
				c.setMRDetail("gitlab.com", "org/repo", iid, &restMR{IID: iid})
				c.getMRDetail("gitlab.com", "org/repo", iid)
				c.setApprovals("gitlab.com", "org/repo", iid, &restApprovals{Approved: true})
				c.getApprovals("gitlab.com", "org/repo", iid)
				c.setForkProject("gitlab.com", iid, "org/repo")
				c.getForkProject("gitlab.com", iid)
			}
		}(i)
	}
	wg.Wait()

	if mr, ok := c.getMRDetail("gitlab.com", "org/repo", 0); !ok || mr == nil || mr.IID != 0 {
		t.Errorf("getMRDetail after concurrent writes: ok=%v, mr=%+v", ok, mr)
	}
	if a, ok := c.getApprovals("gitlab.com", "org/repo", 0); !ok || a == nil || !a.Approved {
		t.Errorf("getApprovals after concurrent writes: ok=%v, a=%+v", ok, a)
	}
	if path, ok := c.getForkProject("gitlab.com", 0); !ok || path != "org/repo" {
		t.Errorf("getForkProject after concurrent writes: ok=%v, path=%q", ok, path)
	}
}

type syntheticClock struct {
	t time.Time
}

func (c *syntheticClock) now() time.Time { return c.t }

func (c *syntheticClock) advance(d time.Duration) { c.t = c.t.Add(d) }
