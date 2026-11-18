package preview

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
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
	svc := &countingSessions{inner: &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}}
	changes := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Interval: time.Millisecond, Changes: changes, Logger: discardLogger()}).Start(ctx)
	defer func() { cancel(); <-done }()

	time.Sleep(50 * time.Millisecond)
	if n := svc.count(); n != 1 {
		t.Fatalf("polls with nothing watched = %d, want only the first", n)
	}

	svc.mu.Lock()
	svc.inner.sessions[0].Metadata.PreviewURL = "index.html"
	svc.mu.Unlock()
	changes <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for svc.count() < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("polls after a watched preview appeared = %d, want interval polling", svc.count())
		}
		time.Sleep(time.Millisecond)
	}
}
