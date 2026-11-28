package session

import (
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestSummarizePRCarriesTheMergeQueue(t *testing.T) {
	enqueuedAt := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	pr := domain.PullRequest{
		URL: "u", Number: 1, MergeQueueRequired: true,
		MergeQueue: &domain.MergeQueueEntry{State: domain.MergeQueueAwaitingChecks, Position: 2, EstimatedSecondsToMerge: 540, EnqueuedAt: enqueuedAt},
	}
	got := summarizePR(pr, nil, nil, nil, nil, true)
	if !got.MergeQueueRequired || got.MergeQueue == nil || got.MergeQueue.State != "awaiting_checks" || got.MergeQueue.Position != 2 ||
		got.MergeQueue.EstimatedSecondsToMerge != 540 || !got.MergeQueue.EnqueuedAt.Equal(enqueuedAt) {
		t.Fatalf("summary queue = required %v entry %+v", got.MergeQueueRequired, got.MergeQueue)
	}

	// A PR that left the queue by merging or closing no longer shows in it.
	for name, done := range map[string]func(*domain.PullRequest){
		"merged": func(p *domain.PullRequest) { p.Merged = true },
		"closed": func(p *domain.PullRequest) { p.Closed = true },
	} {
		ended := pr
		done(&ended)
		if got := summarizePR(ended, nil, nil, nil, nil, true); got.MergeQueue != nil {
			t.Fatalf("%s PR still shows a queue entry: %+v", name, got.MergeQueue)
		}
	}
	if got := summarizePR(domain.PullRequest{URL: "u", Number: 1}, nil, nil, nil, nil, true); got.MergeQueueRequired || got.MergeQueue != nil {
		t.Fatalf("a PR with no queue = %+v", got)
	}
}
