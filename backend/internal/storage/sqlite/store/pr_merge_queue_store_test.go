package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// A PR's merge queue facts round-trip through an observation, change with a
// confirmed enqueue or dequeue, tell listeners each time, and end with a merge.
func TestMergeQueueFactsAreStoredAndEmitCDC(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mq")
	r, _ := s.CreateSession(ctx, sampleRecord("mq"))
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{
		URL: "https://github.com/acme/repo/pull/9", SessionID: r.ID, Number: 9,
		CI: domain.CIPassing, Review: domain.ReviewApproved, Mergeability: domain.MergeMergeable,
		Provider: "github", Host: "github.com", Repo: "acme/repo", HeadSHA: "head", Title: "keep",
		MetadataHash: "metadata-v1", UpdatedAt: now, StateChangedAt: now, ObservedAt: now,
	}
	write := func(p domain.PullRequest) {
		t.Helper()
		if err := s.WriteSCMObservation(ctx, p, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatal(err)
		}
	}
	read := func() domain.PullRequest {
		t.Helper()
		got, ok, err := s.GetPR(ctx, pr.URL)
		if err != nil || !ok {
			t.Fatalf("get pr: ok=%v err=%v", ok, err)
		}
		return got
	}
	events := func(run func()) int {
		t.Helper()
		base, err := s.LatestSeq(ctx)
		if err != nil {
			t.Fatal(err)
		}
		run()
		got, err := s.EventsAfter(ctx, base, 10)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, event := range got {
			if string(event.Type) == "pr_updated" {
				n++
			}
		}
		return n
	}

	write(pr)
	if got := read(); got.MergeQueueRequired || got.MergeQueue != nil {
		t.Fatalf("a PR with no queue = required %v entry %+v", got.MergeQueueRequired, got.MergeQueue)
	}

	// The observer sees the base requires a queue, then the PR in it.
	pr.MergeQueueRequired = true
	if n := events(func() { write(pr) }); n != 1 {
		t.Fatalf("pr_updated events for a base that gained a queue = %d, want 1", n)
	}
	enqueuedAt := now.Add(-time.Minute)
	pr.MergeQueue = &domain.MergeQueueEntry{State: domain.MergeQueueAwaitingChecks, Position: 2, EstimatedSecondsToMerge: 540, EnqueuedAt: enqueuedAt}
	if n := events(func() { write(pr) }); n != 1 {
		t.Fatalf("pr_updated events for a PR joining the queue = %d, want 1", n)
	}
	got := read()
	if !got.MergeQueueRequired || got.MergeQueue == nil || got.MergeQueue.State != domain.MergeQueueAwaitingChecks ||
		got.MergeQueue.Position != 2 || got.MergeQueue.EstimatedSecondsToMerge != 540 || !got.MergeQueue.EnqueuedAt.Equal(enqueuedAt) {
		t.Fatalf("stored entry = %+v", got.MergeQueue)
	}
	// An unchanged poll says nothing; moving up the queue does.
	if n := events(func() { write(pr) }); n != 0 {
		t.Fatalf("pr_updated events for an unchanged queue entry = %d, want 0", n)
	}
	pr.MergeQueue = &domain.MergeQueueEntry{State: domain.MergeQueueAwaitingChecks, Position: 1, EnqueuedAt: enqueuedAt}
	if n := events(func() { write(pr) }); n != 1 {
		t.Fatalf("pr_updated events for moving up the queue = %d, want 1", n)
	}

	// A confirmed dequeue and enqueue from AO change only the queue facts.
	if n := events(func() {
		if err := s.MarkPRMergeQueue(ctx, pr.URL, nil, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}); n != 1 {
		t.Fatalf("pr_updated events for a dequeue = %d, want 1", n)
	}
	if got := read(); got.MergeQueue != nil || !got.MergeQueueRequired || got.Title != "keep" || got.MetadataHash != "metadata-v1" {
		t.Fatalf("after dequeue = %+v", got)
	}
	entry := &domain.MergeQueueEntry{State: domain.MergeQueueQueued, Position: 4}
	if err := s.MarkPRMergeQueue(ctx, pr.URL, entry, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.MergeQueue == nil || got.MergeQueue.Position != 4 || got.MergeQueue.State != domain.MergeQueueQueued {
		t.Fatalf("after enqueue = %+v", got.MergeQueue)
	}
	if err := s.MarkPRMergeQueue(ctx, "https://github.com/acme/repo/pull/404", entry, now); err == nil {
		t.Fatal("mark merge queue succeeded for an unknown PR")
	}

	// Merging takes it out of the queue, and nothing puts it back.
	if err := s.MarkPRMerged(ctx, pr.URL, "merge-sha", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.MergeQueue != nil {
		t.Fatalf("a merged PR is still queued: %+v", got.MergeQueue)
	}
	if err := s.MarkPRMergeQueue(ctx, pr.URL, entry, now.Add(2*time.Hour)); err == nil {
		t.Fatal("mark merge queue rewrote a merged PR")
	}
	// A poll that read the open, queued PR just before the merge lands late.
	write(pr)
	if got := read(); !got.Merged || got.MergeQueue != nil {
		t.Fatalf("a late poll re-queued a merged PR: merged=%v entry=%+v", got.Merged, got.MergeQueue)
	}
}
