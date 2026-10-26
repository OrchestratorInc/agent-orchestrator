package artifactstore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore"
	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore/s3fake"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// memoryQueue hands out its versions once and records what was forgotten.
type memoryQueue struct {
	mu        sync.Mutex
	due       []domain.ArtifactObjectVersion
	forgotten []domain.ArtifactObjectVersion
}

func (q *memoryQueue) ClaimArtifactObjectVersions(_ context.Context, limit int, _ time.Duration) ([]domain.ArtifactObjectVersion, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := min(limit, len(q.due))
	claimed := q.due[:n]
	q.due = q.due[n:]
	return claimed, nil
}

func (q *memoryQueue) ForgetArtifactObjectVersions(_ context.Context, versions []domain.ArtifactObjectVersion) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.forgotten = append(q.forgotten, versions...)
	return nil
}

func TestSweepDeletesQueuedVersions(t *testing.T) {
	fake := s3fake.New(t)
	store, err := artifactstore.New(context.Background(), fake.Config())
	if err != nil {
		t.Fatal(err)
	}
	key := "orgs/o/sessions/s/report.html"
	put := func(body string) string {
		sum := sha256.Sum256([]byte(body))
		version, err := store.Put(context.Background(), key, bytes.NewReader([]byte(body)), int64(len(body)), sum[:])
		if err != nil {
			t.Fatal(err)
		}
		return version
	}
	stale, kept := put("stale"), put("kept")
	queue := &memoryQueue{due: []domain.ArtifactObjectVersion{{S3Key: key, VersionID: stale}}}

	deleted, err := artifactstore.Sweep(context.Background(), queue, store)
	if err != nil || deleted != 1 {
		t.Fatalf("Sweep = %d, %v", deleted, err)
	}
	if fake.Versions(key) != 1 {
		t.Fatalf("versions left = %d, want 1", fake.Versions(key))
	}
	if _, err := store.Head(context.Background(), key, kept); err != nil {
		t.Fatalf("the kept version is gone: %v", err)
	}
	if len(queue.forgotten) != 1 || queue.forgotten[0].VersionID != stale {
		t.Fatalf("forgotten = %+v", queue.forgotten)
	}
}
