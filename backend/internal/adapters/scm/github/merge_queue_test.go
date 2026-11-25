package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const queueTestHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func queueTestRef() ports.SCMPRRef {
	return ports.SCMPRRef{Repo: detailsTestRepo, Number: 42, URL: "https://github.com/octocat/hello/pull/42"}
}

// The fixtures follow GitHub's documented GraphQL shapes: PullRequest
// .isMergeQueueEnabled (Boolean!) and .mergeQueueEntry (MergeQueueEntry, null
// while the PR isn't queued) with state (MergeQueueEntryState!), position
// (Int!), estimatedTimeToMerge (Int, seconds, nullable) and enqueuedAt
// (DateTime!).
func TestFetchPullRequestsReadsTheMergeQueueInTheBatchQuery(t *testing.T) {
	queued := batchPRNode(42, "2026-11-10T10:00:00Z")
	queued["isMergeQueueEnabled"] = true
	queued["mergeQueueEntry"] = map[string]any{
		"state": "AWAITING_CHECKS", "position": 2, "estimatedTimeToMerge": 540, "enqueuedAt": "2026-11-10T09:58:00Z",
	}
	waiting := batchPRNode(43, "2026-11-10T10:00:00Z")
	waiting["isMergeQueueEnabled"] = true
	waiting["mergeQueueEntry"] = nil
	noEstimate := batchPRNode(44, "2026-11-10T10:00:00Z")
	noEstimate["isMergeQueueEnabled"] = true
	noEstimate["mergeQueueEntry"] = map[string]any{"state": "QUEUED", "position": 5, "estimatedTimeToMerge": nil, "enqueuedAt": "2026-11-10T09:59:00Z"}
	// A response from before the fields existed, or a branch with no queue.
	plain := batchPRNode(45, "2026-11-10T10:00:00Z")

	fake := newFakeGH(t)
	queries := recordGraphQL(t, fake, map[string]any{
		"pr0": map[string]any{"pullRequest": queued},
		"pr1": map[string]any{"pullRequest": waiting},
		"pr2": map[string]any{"pullRequest": noEstimate},
		"pr3": map[string]any{"pullRequest": plain},
	})
	stored := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	refs := make([]ports.SCMPRRef, 0, 4)
	for _, number := range []int{42, 43, 44, 45} {
		refs = append(refs, ports.SCMPRRef{Repo: detailsTestRepo, Number: number, DetailsUpdatedAt: stored})
	}
	obs, err := newProviderForTest(t, fake).FetchPullRequests(ctx(), refs)
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	sent := queries()
	if len(sent) != 1 {
		t.Fatalf("graphql requests = %d, want the queue read in the one batch query", len(sent))
	}
	if !strings.Contains(sent[0], "isMergeQueueEnabled mergeQueueEntry{ state position estimatedTimeToMerge enqueuedAt }") {
		t.Fatalf("batch query doesn't read the merge queue:\n%s", sent[0])
	}

	want := domain.MergeQueueEntry{
		State: domain.MergeQueueAwaitingChecks, Position: 2, EstimatedSecondsToMerge: 540,
		EnqueuedAt: time.Date(2026, 11, 10, 9, 58, 0, 0, time.UTC),
	}
	if got := obs[0].PR; !got.MergeQueueRequired || got.MergeQueue == nil || *got.MergeQueue != want {
		t.Fatalf("queued PR = required %v entry %+v, want %+v", got.MergeQueueRequired, got.MergeQueue, want)
	}
	if got := obs[1].PR; !got.MergeQueueRequired || got.MergeQueue != nil {
		t.Fatalf("PR not yet queued = required %v entry %+v", got.MergeQueueRequired, got.MergeQueue)
	}
	if got := obs[2].PR.MergeQueue; got == nil || got.State != domain.MergeQueueQueued || got.Position != 5 || got.EstimatedSecondsToMerge != 0 {
		t.Fatalf("entry with no estimate = %+v", got)
	}
	if got := obs[3].PR; got.MergeQueueRequired || got.MergeQueue != nil {
		t.Fatalf("PR on a branch with no queue = required %v entry %+v", got.MergeQueueRequired, got.MergeQueue)
	}
}

// enqueuePullRequest(input: EnqueuePullRequestInput!) takes pullRequestId and
// expectedHeadOid and answers EnqueuePullRequestPayload.mergeQueueEntry.
func TestEnqueuePullRequestPinsTheReviewedHead(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLBody(t, r)
		if !strings.Contains(body.Query, "enqueuePullRequest(input: { pullRequestId: $id, expectedHeadOid: $head })") {
			t.Fatalf("query = %s", body.Query)
		}
		if strings.Contains(body.Query, "jump") {
			t.Fatalf("an ordinary enqueue must not jump the queue: %s", body.Query)
		}
		if body.Variables["id"] != "PR_node" || body.Variables["head"] != queueTestHead {
			t.Fatalf("variables = %#v", body.Variables)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"enqueuePullRequest": map[string]any{
			"mergeQueueEntry": map[string]any{"state": "QUEUED", "position": 3, "estimatedTimeToMerge": 900, "enqueuedAt": "2026-11-10T10:00:00Z"},
		}}})
	})
	entry, err := newProviderForTest(t, f).EnqueuePullRequest(ctx(), ports.SCMEnqueueRequest{PR: queueTestRef(), NodeID: "PR_node", ExpectedHeadSHA: queueTestHead})
	if err != nil {
		t.Fatal(err)
	}
	if entry.State != domain.MergeQueueQueued || entry.Position != 3 || entry.EstimatedSecondsToMerge != 900 || entry.EnqueuedAt.IsZero() {
		t.Fatalf("entry = %+v", entry)
	}
	if n := f.callsTo(http.MethodPost, "/graphql"); n != 1 {
		t.Fatalf("graphql calls = %d, want only the mutation", n)
	}
}

func TestEnqueuePullRequestLooksUpTheNodeIDWhenUnknown(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLBody(t, r)
		switch {
		case strings.Contains(body.Query, "PullRequestNodeID"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"id": "PR_looked_up"}}}})
		case strings.Contains(body.Query, "enqueuePullRequest"):
			if body.Variables["id"] != "PR_looked_up" {
				t.Fatalf("variables = %#v", body.Variables)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"enqueuePullRequest": map[string]any{
				"mergeQueueEntry": map[string]any{"state": "QUEUED", "position": 1, "enqueuedAt": "2026-11-10T10:00:00Z"},
			}}})
		default:
			t.Fatalf("unexpected query %s", body.Query)
		}
	})
	if _, err := newProviderForTest(t, f).EnqueuePullRequest(ctx(), ports.SCMEnqueueRequest{PR: queueTestRef(), ExpectedHeadSHA: queueTestHead}); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueuePullRequestFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response map[string]any
		want     error
	}{
		{name: "refused", response: map[string]any{"errors": []any{map[string]any{"message": "Pull request is not ready to be added to the merge queue"}}}, want: ports.ErrSCMRejected},
		{name: "head moved", response: map[string]any{"errors": []any{map[string]any{"message": "Head branch was modified. Review and try the merge again."}}}, want: ports.ErrSCMHeadChanged},
		{name: "no entry", response: map[string]any{"data": map[string]any{"enqueuePullRequest": map[string]any{"mergeQueueEntry": nil}}}, want: ports.ErrSCMRejected},
		{name: "not found", response: map[string]any{"errors": []any{map[string]any{"message": "Could not resolve to a node with the global id"}}}, want: ports.ErrSCMNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.response)
			})
			_, err := newProviderForTest(t, f).EnqueuePullRequest(ctx(), ports.SCMEnqueueRequest{PR: queueTestRef(), NodeID: "PR_node", ExpectedHeadSHA: queueTestHead})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEnqueuePullRequestRejectsABadHeadWithoutCallingGitHub(t *testing.T) {
	f := newFakeGH(t)
	if _, err := newProviderForTest(t, f).EnqueuePullRequest(ctx(), ports.SCMEnqueueRequest{PR: queueTestRef(), NodeID: "PR_node", ExpectedHeadSHA: "main"}); err == nil {
		t.Fatal("want an error for a head that isn't a commit sha")
	}
	if n := f.callsTo(http.MethodPost, "/graphql"); n != 0 {
		t.Fatalf("graphql calls = %d, want none", n)
	}
}

// dequeuePullRequest(input: DequeuePullRequestInput!) takes id, the pull
// request's node id.
func TestDequeuePullRequestAddressesThePullRequest(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLBody(t, r)
		if !strings.Contains(body.Query, "dequeuePullRequest(input: { id: $id })") || body.Variables["id"] != "PR_node" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"dequeuePullRequest": map[string]any{"clientMutationId": nil}}})
	})
	p := newProviderForTest(t, f)
	if err := p.DequeuePullRequest(ctx(), ports.SCMDequeueRequest{PR: queueTestRef(), NodeID: "PR_node"}); err != nil {
		t.Fatal(err)
	}

	refused := newFakeGH(t)
	refused.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "Pull request is not in the merge queue"}}})
	})
	err := newProviderForTest(t, refused).DequeuePullRequest(ctx(), ports.SCMDequeueRequest{PR: queueTestRef(), NodeID: "PR_node"})
	if !errors.Is(err, ports.ErrSCMRejected) {
		t.Fatalf("error = %v, want a refusal", err)
	}
}
