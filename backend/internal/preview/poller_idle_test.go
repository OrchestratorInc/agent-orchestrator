package preview

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// countingPreviewSessions is fakePreviewSessions made safe to read while the
// poller's own goroutine uses it.
type countingPreviewSessions struct {
	mu sync.Mutex
	fakePreviewSessions
	lists int
}

func (c *countingPreviewSessions) ListPreviewSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists++
	return c.fakePreviewSessions.ListPreviewSessions(ctx)
}

func (c *countingPreviewSessions) SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fakePreviewSessions.SetPreview(ctx, id, previewURL)
}

func (c *countingPreviewSessions) listed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lists
}

// With no chosen preview to watch, the 250 ms poll only re-lists sessions, so
// the poller waits for the idle interval or a session change.
func TestPollerIdlesWhileWatchingNoPreview(t *testing.T) {
	src := &countingPreviewSessions{}
	src.sessions = []domain.SessionRecord{workerSession("ao-1", t.TempDir(), "")}
	wake := make(chan struct{}, 1)
	p := NewPoller(src, src, "http://127.0.0.1:3001", PollerConfig{
		Interval: 5 * time.Millisecond, IdleInterval: time.Hour, Wake: wake, Logger: discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	if n := src.listed(); n != 1 {
		t.Fatalf("listed sessions %d times with no preview to watch, want only the first poll", n)
	}

	wake <- struct{}{}
	for deadline := time.Now().Add(2 * time.Second); src.listed() < 2; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a session change did not bring a poll")
		}
	}
	cancel()
	<-done
}

// A preview is chosen through a session write, so a session change while
// nothing is watched polls at once rather than one interval later, and the
// poller sees the new preview no later than its 250 ms tick would have.
func TestPollerPollsAtOnceOnSessionChangeWhileIdle(t *testing.T) {
	src := &countingPreviewSessions{}
	src.sessions = []domain.SessionRecord{workerSession("ao-1", t.TempDir(), "")}
	wake := make(chan struct{}, 1)
	p := NewPoller(src, src, "http://127.0.0.1:3001", PollerConfig{
		Interval: 300 * time.Millisecond, IdleInterval: time.Hour, Wake: wake, Logger: discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(2 * time.Second); src.listed() < 1; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first poll did not run")
		}
	}
	time.Sleep(400 * time.Millisecond) // past one interval since the first poll

	woke := time.Now()
	wake <- struct{}{}
	for src.listed() < 2 {
		if time.Since(woke) > 200*time.Millisecond {
			t.Fatalf("the poll after a session change took over 200 ms; want at once (interval 300 ms)")
		}
		time.Sleep(time.Millisecond)
	}
}

// A chosen workspace preview keeps the fast interval, so edits refresh it as
// quickly as before.
func TestPollerStaysFastWhileWatchingAPreview(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>v1</main>")
	src := &countingPreviewSessions{}
	src.sessions = []domain.SessionRecord{workerSession("ao-1", workspace, "index.html")}
	p := NewPoller(src, src, "http://127.0.0.1:3001", PollerConfig{
		Interval: 5 * time.Millisecond, IdleInterval: time.Hour, Wake: make(chan struct{}), Logger: discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	if n := src.listed(); n < 5 {
		t.Fatalf("listed sessions %d times while watching a preview, want the fast interval", n)
	}
}
