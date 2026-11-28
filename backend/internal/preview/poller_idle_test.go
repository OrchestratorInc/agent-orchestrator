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
	defer func() { cancel(); <-done }()
	waitForListings(t, src, 1)
	time.Sleep(100 * time.Millisecond) // 20 intervals: only extra polls could change the count
	if n := src.listed(); n != 1 {
		t.Fatalf("listed sessions %d times with no preview to watch, want only the first poll", n)
	}

	wake <- struct{}{}
	waitForListings(t, src, 2)
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
	waitForListings(t, src, 1)
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
	defer func() { cancel(); <-done }()
	// Five listings at a 5 ms interval; the idle interval is an hour.
	waitForListings(t, src, 5)
}

// After the user clears a watched preview there is nothing left to watch or
// restore, so the poller forgets the session and can rest again.
func TestPollerForgetsAPreviewTheUserCleared(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>v1</main>")
	src := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "index.html")}}
	p := NewPoller(src, src, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})
	if !p.pollAndLog(context.Background()) {
		t.Fatal("a chosen preview should be watched")
	}
	src.sessions[0].Metadata.PreviewURL = ""
	if p.pollAndLog(context.Background()) {
		t.Fatal("still watching after the user cleared the preview")
	}
}

// waitForListings waits, up to 2 s, until the poller has listed sessions at
// least n times.
func waitForListings(t *testing.T, src *countingPreviewSessions, n int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); src.listed() < n; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("listed sessions %d times, want at least %d", src.listed(), n)
		}
	}
}
