package pr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type fakeQueuer struct {
	entry       domain.MergeQueueEntry
	enqueueErr  error
	dequeueErr  error
	enqueued    []ports.SCMEnqueueRequest
	dequeued    []ports.SCMDequeueRequest
	writes      []*domain.MergeQueueEntry
	writtenURLs []string
	writeErr    error
}

func (f *fakeQueuer) EnqueuePullRequest(_ context.Context, request ports.SCMEnqueueRequest) (domain.MergeQueueEntry, error) {
	f.enqueued = append(f.enqueued, request)
	return f.entry, f.enqueueErr
}

func (f *fakeQueuer) DequeuePullRequest(_ context.Context, request ports.SCMDequeueRequest) error {
	f.dequeued = append(f.dequeued, request)
	return f.dequeueErr
}

func (f *fakeQueuer) MarkPRMergeQueue(_ context.Context, prURL string, entry *domain.MergeQueueEntry, _ time.Time) error {
	f.writtenURLs = append(f.writtenURLs, prURL)
	f.writes = append(f.writes, entry)
	return f.writeErr
}

// queueFixture is a PR ready to merge on a base branch that requires a queue.
func queueFixture() (domain.PullRequest, *fakeSCMAction, *fakeQueuer) {
	pr, scm := mergeableActionFixture()
	pr.ProviderID = "PR_node"
	scm.observation.PR.MergeQueueRequired = true
	scm.observation.PR.ProviderID = "PR_node"
	return pr, scm, &fakeQueuer{entry: domain.MergeQueueEntry{State: domain.MergeQueueQueued, Position: 3, EstimatedSecondsToMerge: 600}}
}

func queueService(pr domain.PullRequest, scm *fakeSCMAction, queue *fakeQueuer, refresher *fakeRefresher) *ActionService {
	deps := ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm, QueueWriter: queue}
	if queue != nil {
		deps.Queuer = queue
	}
	if refresher != nil {
		deps.Refresher = refresher
	}
	return NewActionService(deps)
}

func TestActionServiceMerge_EnqueuesWhereTheBaseRequiresAMergeQueue(t *testing.T) {
	pr, scm, queue := queueFixture()
	refresher := &fakeRefresher{}
	result, err := queueService(pr, scm, queue, refresher).Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
	if err != nil {
		t.Fatal(err)
	}
	if scm.mergeCalls != 0 {
		t.Fatalf("direct merge calls = %d, want none on a queue-protected base", scm.mergeCalls)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0].ExpectedHeadSHA != pr.HeadSHA || queue.enqueued[0].NodeID != "PR_node" || queue.enqueued[0].PR.Number != 42 {
		t.Fatalf("enqueue requests = %#v", queue.enqueued)
	}
	if result.State != domain.PRStateOpen || result.Queue == nil || result.Queue.Position != 3 || result.PRNumber != 42 {
		t.Fatalf("result = %#v", result)
	}
	if len(queue.writes) != 1 || queue.writes[0] == nil || queue.writes[0].Position != 3 || queue.writtenURLs[0] != pr.URL {
		t.Fatalf("local queue writes = %#v", queue.writes)
	}
	if len(refresher.urls) != 1 || refresher.urls[0] != pr.URL {
		t.Fatalf("refresh requests = %v, want a sooner look at the queued PR", refresher.urls)
	}
}

// A queue-protected base gets the same guard as a direct merge: on such
// branches GitHub reports a ready PR as CLEAN, so blocked still means blocked.
func TestActionServiceMerge_QueueReadiness(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate  func(*ports.SCMObservation, *ports.SCMReviewObservation)
		wantErr error
	}{
		"blocked by the provider": {mutate: func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) {
			o.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked), Blockers: []string{"blocked_by_provider"}}
		}, wantErr: ErrPRPreconditions},
		"conflicting": {mutate: func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) {
			o.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeConflicting), Conflict: true}
		}, wantErr: ErrPRPreconditions},
		"checks still running": {mutate: func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) {
			o.CI.Summary = string(domain.CIPending)
		}, wantErr: ErrPRPreconditions},
		"head moved": {mutate: func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) {
			o.PR.HeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}, wantErr: ErrPRHeadChanged},
		"already queued": {mutate: func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) {
			o.PR.MergeQueue = &domain.MergeQueueEntry{State: domain.MergeQueueAwaitingChecks, Position: 1}
		}, wantErr: ErrPRQueued},
	} {
		t.Run(name, func(t *testing.T) {
			pr, scm, queue := queueFixture()
			tc.mutate(&scm.observation, &scm.review)
			_, err := queueService(pr, scm, queue, nil).Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if len(queue.enqueued) != 0 || scm.mergeCalls != 0 {
				t.Fatalf("enqueues = %d, direct merges = %d, want neither", len(queue.enqueued), scm.mergeCalls)
			}
		})
	}
}

// A PR on a base with no queue still merges directly, queuer or not.
func TestActionServiceMerge_WithoutAQueueMergesDirectly(t *testing.T) {
	pr, scm, queue := queueFixture()
	scm.observation.PR.MergeQueueRequired = false
	result, err := queueService(pr, scm, queue, nil).Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
	if err != nil || result.State != domain.PRStateMerged || result.Queue != nil || scm.mergeCalls != 1 || len(queue.enqueued) != 0 {
		t.Fatalf("result = %#v, err = %v, merges = %d, enqueues = %d", result, err, scm.mergeCalls, len(queue.enqueued))
	}
}

func TestActionServiceMerge_MapsEnqueueFailures(t *testing.T) {
	for name, tc := range map[string]struct{ provider, want error }{
		"head moved":  {ports.ErrSCMHeadChanged, ErrPRHeadChanged},
		"refused":     {ports.ErrSCMRejected, ErrPRRejected},
		"unsupported": {ports.ErrSCMUnsupported, ErrUnsupported},
		"not found":   {ports.ErrSCMNotFound, ErrPRNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			pr, scm, queue := queueFixture()
			queue.enqueueErr = tc.provider
			_, err := queueService(pr, scm, queue, nil).Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if len(queue.writes) != 0 {
				t.Fatalf("a failed enqueue recorded a queue entry: %#v", queue.writes)
			}
		})
	}
}

func TestActionServiceMerge_QueueRequiredWithoutAQueuerIsUnsupported(t *testing.T) {
	pr, scm, _ := queueFixture()
	_, err := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm}).
		Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
	if !errors.Is(err, ErrUnsupported) || scm.mergeCalls != 0 {
		t.Fatalf("error = %v, direct merges = %d; a direct merge can only be refused there", err, scm.mergeCalls)
	}
}

func TestActionServiceMerge_EnqueueSurvivesALocalWriteFailure(t *testing.T) {
	pr, scm, queue := queueFixture()
	queue.writeErr = errors.New("database locked")
	result, err := queueService(pr, scm, queue, nil).Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
	if err != nil || result.Queue == nil {
		t.Fatalf("result = %#v, err = %v; the provider already queued it", result, err)
	}
}

func TestActionServiceDequeue(t *testing.T) {
	pr, scm, queue := queueFixture()
	scm.observation.PR.MergeQueue = &domain.MergeQueueEntry{State: domain.MergeQueueQueued, Position: 2}
	refresher := &fakeRefresher{}
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm, Queuer: queue, QueueWriter: queue, Refresher: refresher, Clock: func() time.Time { return at }})
	result, err := svc.Dequeue(context.Background(), "42", pr.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.PRNumber != 42 || !result.ChangedAt.Equal(at) {
		t.Fatalf("result = %#v", result)
	}
	if len(queue.dequeued) != 1 || queue.dequeued[0].NodeID != "PR_node" || queue.dequeued[0].PR.Number != 42 {
		t.Fatalf("dequeue requests = %#v", queue.dequeued)
	}
	if len(queue.writes) != 1 || queue.writes[0] != nil {
		t.Fatalf("local queue writes = %#v, want the entry cleared", queue.writes)
	}
	if len(refresher.urls) != 1 {
		t.Fatalf("refresh requests = %v", refresher.urls)
	}
}

func TestActionServiceDequeue_Refusals(t *testing.T) {
	t.Run("not queued any more", func(t *testing.T) {
		pr, scm, queue := queueFixture()
		_, err := queueService(pr, scm, queue, nil).Dequeue(context.Background(), "42", pr.URL)
		if !errors.Is(err, ErrPRNotQueued) || len(queue.dequeued) != 0 {
			t.Fatalf("error = %v, dequeues = %d", err, len(queue.dequeued))
		}
		// The stale local entry is dropped so the view stops showing it.
		if len(queue.writes) != 1 || queue.writes[0] != nil {
			t.Fatalf("local queue writes = %#v", queue.writes)
		}
	})
	t.Run("branch without a queue", func(t *testing.T) {
		pr, scm, queue := queueFixture()
		scm.observation.PR.MergeQueueRequired = false
		_, err := queueService(pr, scm, queue, nil).Dequeue(context.Background(), "42", pr.URL)
		// Nothing to clear, and the branch must not be marked as merging through a queue.
		if !errors.Is(err, ErrPRNotQueued) || len(queue.writes) != 0 {
			t.Fatalf("error = %v, writes = %#v", err, queue.writes)
		}
	})
	t.Run("provider refuses", func(t *testing.T) {
		pr, scm, queue := queueFixture()
		scm.observation.PR.MergeQueue = &domain.MergeQueueEntry{State: domain.MergeQueueLocked, Position: 1}
		queue.dequeueErr = ports.ErrSCMRejected
		_, err := queueService(pr, scm, queue, nil).Dequeue(context.Background(), "42", pr.URL)
		if !errors.Is(err, ErrPRRejected) || len(queue.writes) != 0 {
			t.Fatalf("error = %v, writes = %#v", err, queue.writes)
		}
	})
	t.Run("unknown pr", func(t *testing.T) {
		pr, scm, queue := queueFixture()
		_, err := queueService(pr, scm, queue, nil).Dequeue(context.Background(), "7", pr.URL)
		if !errors.Is(err, ErrPRNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("invalid identity", func(t *testing.T) {
		pr, scm, queue := queueFixture()
		_, err := queueService(pr, scm, queue, nil).Dequeue(context.Background(), "42", " ")
		if !errors.Is(err, ErrInvalidPR) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("provider without queues", func(t *testing.T) {
		pr, scm, _ := queueFixture()
		_, err := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm}).Dequeue(context.Background(), "42", pr.URL)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("error = %v", err)
		}
	})
}
