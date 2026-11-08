package scm

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sessionRefreshFixture tracks PR 1 for p-1 and PR 2 for p-2 behind an
// unchanged repo guard, so only a forced refresh fetches either.
func sessionRefreshFixture(pr1 ports.SCMObservation) (*Observer, *fakeProvider, *fakeStore) {
	store := testStoreWithSession()
	store.sessions = append(store.sessions, domain.SessionRecord{ID: "p-2", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}})
	other := knownPR(2)
	other.SessionID = "p-2"
	store.prs["p-1"] = []domain.PullRequest{knownPR(1)}
	store.prs["p-2"] = []domain.PullRequest{other}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): pr1, prKey(testRepo, 2): testObs(2)},
		reviews:      map[string]ports.SCMReviewObservation{prKey(testRepo, 1): {Decision: string(domain.ReviewNone)}, prKey(testRepo, 2): {Decision: string(domain.ReviewNone)}},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(200, 0).UTC())
	obs.sessionRefreshDebounce = time.Millisecond
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = time.Unix(190, 0).UTC()
	for _, pr := range []domain.PullRequest{knownPR(1), other} {
		obs.Cache.LastPRFetchAt[keyForTrackedPR(pr, testRepo)] = time.Unix(199, 0).UTC()
	}
	return obs, provider, store
}

func TestRequestSessionRefresh_CoalescesIntoOneTrigger(t *testing.T) {
	obs, _, _ := sessionRefreshFixture(testObs(1))
	obs.sessionRefreshDebounce = 50 * time.Millisecond

	obs.RequestSessionRefresh("p-1")
	obs.RequestSessionRefresh("p-2")
	select {
	case <-obs.trigger:
		t.Fatal("refresh woke the poll loop before the debounce elapsed")
	default:
	}
	select {
	case <-obs.trigger:
	case <-time.After(5 * time.Second):
		t.Fatal("debounce did not wake the poll loop")
	}
	if forced := obs.takeForcedSessions(); !forced["p-1"] || !forced["p-2"] {
		t.Fatalf("forced = %v, want both sessions", forced)
	}
}

// A signal fetches that session's PR past the 304 repo guard, once, and leaves
// the other session's PR and its CI guard to the regular poll.
func TestPoll_SessionRefreshFetchesOnlyThatSessionOnce(t *testing.T) {
	merged := testObs(1)
	merged.PR.State = "merged"
	merged.PR.Merged = true
	obs, provider, store := sessionRefreshFixture(merged)
	provider.checkGuards = map[string]ports.SCMGuardResult{commitKey(testRepo, knownPR(2).HeadSHA): {ETag: "ci-changed"}}

	obs.RequestSessionRefresh("p-1")
	<-obs.trigger
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 || provider.fetchBatches[0][0].Number != 1 {
		t.Fatalf("forced poll batches = %v, want only PR 1", provider.fetchBatches)
	}
	if len(store.writes) == 0 || !store.writes[0].pr.Merged {
		t.Fatalf("merged state not persisted: %#v", store.writes)
	}

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 2 || provider.fetchBatches[1][0].Number != 2 {
		t.Fatalf("regular poll batches = %v, want PR 2's CI change and no repeat of PR 1", provider.fetchBatches)
	}
}
