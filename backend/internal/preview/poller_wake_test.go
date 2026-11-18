package preview

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type countingSessions struct {
	mu    sync.Mutex
	inner *fakePreviewSessions
	lists int
}

func (c *countingSessions) ListPreviewSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists++
	return c.inner.ListPreviewSessions(ctx)
}

func (c *countingSessions) SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inner.SetPreview(ctx, id, previewURL)
}

func (c *countingSessions) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lists
}

func TestPollerWaitsForChangeWhileNothingIsWatched(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	synctest.Test(t, func(t *testing.T) {
		svc := &countingSessions{inner: &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}}
		changes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Interval: 10 * time.Millisecond, Changes: changes, Logger: discardLogger()}).Start(ctx)
		defer func() { cancel(); <-done }()
		setPreview := func(url string) {
			svc.mu.Lock()
			svc.inner.sessions[0].Metadata.PreviewURL = url
			svc.mu.Unlock()
		}

		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if n := svc.count(); n != 1 {
			t.Fatalf("polls with nothing watched = %d, want only the first", n)
		}

		setPreview("index.html")
		changes <- struct{}{}
		synctest.Wait()
		watching := svc.count()
		if watching < 2 {
			t.Fatalf("polls after a session change = %d, want a poll for the change", watching)
		}
		time.Sleep(25 * time.Millisecond)
		synctest.Wait()
		if n := svc.count(); n != watching+2 {
			t.Fatalf("polls in 25ms while watching = %d, want 2 at the 10ms interval", n-watching)
		}

		// While watching, change events wait for the next tick.
		watching = svc.count()
		changes <- struct{}{}
		synctest.Wait()
		if n := svc.count(); n != watching {
			t.Fatalf("polls after a change event while watching = %d, want none until the next tick", n-watching)
		}

		// An explicit clear leaves nothing to watch or restore.
		setPreview("")
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		idleFrom := svc.count()
		time.Sleep(time.Second)
		synctest.Wait()
		if n := svc.count(); n > idleFrom+1 {
			t.Fatalf("polls after an explicit clear = %d more, want at most one for the queued change", n-idleFrom)
		}
	})
}

func TestPollerFallsBackToTheIntervalWhenChangesCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &countingSessions{inner: &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", t.TempDir(), "")}}}
		changes := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Interval: 10 * time.Millisecond, Changes: changes, Logger: discardLogger()}).Start(ctx)
		defer func() { cancel(); <-done }()

		synctest.Wait()
		close(changes)
		time.Sleep(25 * time.Millisecond)
		synctest.Wait()
		if n := svc.count(); n < 2 || n > 4 {
			t.Fatalf("polls 25ms after Changes closed = %d, want interval polling (2 to 4)", n)
		}
	})
}
