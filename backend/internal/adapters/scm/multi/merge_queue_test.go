package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// queueingProvider is a fakeProvider with a merge queue, as GitHub has.
type queueingProvider struct {
	fakeProvider
	enqueued []ports.SCMEnqueueRequest
	dequeued []ports.SCMDequeueRequest
}

func (q *queueingProvider) EnqueuePullRequest(_ context.Context, request ports.SCMEnqueueRequest) (domain.MergeQueueEntry, error) {
	q.enqueued = append(q.enqueued, request)
	return domain.MergeQueueEntry{State: domain.MergeQueueQueued, Position: 1}, nil
}

func (q *queueingProvider) DequeuePullRequest(_ context.Context, request ports.SCMDequeueRequest) error {
	q.dequeued = append(q.dequeued, request)
	return nil
}

// GitLab's merge trains aren't wired, so its PRs report unsupported and never
// reach GitHub's queue.
func TestMergeQueueRoutesByProviderAndReportsUnsupported(t *testing.T) {
	gh := &queueingProvider{fakeProvider: fakeProvider{key: "github"}}
	m := New(NamedProvider{Key: "github", Provider: gh}, NamedProvider{Key: "gitlab", Provider: &fakeProvider{key: "gitlab"}})
	ghRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github"}, Number: 1}
	glRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab"}, Number: 1}

	entry, err := m.EnqueuePullRequest(context.Background(), ports.SCMEnqueueRequest{PR: ghRef, ExpectedHeadSHA: "head"})
	if err != nil || entry.Position != 1 || len(gh.enqueued) != 1 {
		t.Fatalf("entry = %+v, err = %v, calls = %d", entry, err, len(gh.enqueued))
	}
	if err := m.DequeuePullRequest(context.Background(), ports.SCMDequeueRequest{PR: ghRef}); err != nil || len(gh.dequeued) != 1 {
		t.Fatalf("dequeue err = %v, calls = %d", err, len(gh.dequeued))
	}
	if _, err := m.EnqueuePullRequest(context.Background(), ports.SCMEnqueueRequest{PR: glRef}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("gitlab enqueue error = %v, want unsupported", err)
	}
	if err := m.DequeuePullRequest(context.Background(), ports.SCMDequeueRequest{PR: glRef}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("gitlab dequeue error = %v, want unsupported", err)
	}
	if len(gh.enqueued) != 1 || len(gh.dequeued) != 1 {
		t.Fatal("a GitLab request reached GitHub's queue")
	}
	var nilProvider *Provider
	if _, err := nilProvider.EnqueuePullRequest(context.Background(), ports.SCMEnqueueRequest{PR: ghRef}); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("nil provider error = %v, want unsupported", err)
	}
}
