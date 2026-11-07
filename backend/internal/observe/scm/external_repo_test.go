package scm

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A PR claimed in a repository outside the session's project has no repo
// PR-list guard or sync cursor. It must still be polled, including merges, but
// at the max-age cadence rather than on every tick.
func TestPoll_ExternalRepoPRRefreshesAtMaxAgeCadence(t *testing.T) {
	external := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "other", Name: "releases", Repo: "other/releases"}
	obsOpen := testObs(50)
	obsOpen.Repo = external.Repo
	obsOpen.PR.URL = "https://github.com/other/releases/pull/50"
	obsOpen.PR.SourceBranch = "unrelated-branch"
	local, _, _, _, _ := domainFromObservation("p-1", domain.SessionRecord{}, obsOpen, domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	local.ReviewObservedAt = time.Unix(2, 0).UTC()
	merged := obsOpen
	merged.PR.Merged = true
	merged.PR.State = "merged"

	store := testStoreWithSession()
	store.prs["p-1"] = []domain.PullRequest{local}
	projectKey := prKey(testRepo, 0)
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{projectKey: {ETag: "v1", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(external, "sha50"): {ETag: "ci1", NotModified: true}},
		observations: map[string]ports.SCMObservation{prKey(external, 50): merged},
	}
	now := time.Unix(10_000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	obs.Cache.RepoPRListETag[projectKey] = "v1"
	obs.Cache.CommitChecksETag[commitKey(external, "sha50")] = "ci1"
	key := keyForTrackedPR(local, external)
	poll := func() []ports.SCMPRRef {
		t.Helper()
		store.writes, provider.fetchBatches = nil, nil
		store.prs["p-1"] = []domain.PullRequest{local}
		if err := obs.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		var refs []ports.SCMPRRef
		for _, batch := range provider.fetchBatches {
			refs = append(refs, batch...)
		}
		return refs
	}

	if refs := poll(); len(refs) != 1 || refs[0].Repo.Repo != "other/releases" || refs[0].Number != 50 {
		t.Fatalf("first poll fetched %+v, want other/releases#50", refs)
	}
	mergedWritten := false
	for _, w := range store.writes {
		mergedWritten = mergedWritten || (w.pr.Merged && w.pr.Repo == "other/releases" && w.pr.SessionID == "p-1")
	}
	if !mergedWritten {
		t.Fatalf("writes = %+v, want the external PR's merge", store.writes)
	}

	obs.Cache.LastPRFetchAt[key] = now
	if refs := poll(); len(refs) != 0 {
		t.Fatalf("recently fetched external PR refetched: %+v", refs)
	}

	// Activity in the project repository lists its PRs and runs terminal
	// reconciliation; neither may refetch the external PR early.
	provider.repoGuards[projectKey] = ports.SCMGuardResult{ETag: "v2"}
	obs.Cache.LastSyncCursor[projectKey] = now.Add(-time.Minute)
	if refs := poll(); len(refs) != 0 {
		t.Fatalf("project repository activity refetched the external PR: %+v", refs)
	}

	obs.Cache.LastPRFetchAt[key] = now.Add(-(DefaultPRMaxAge + time.Minute))
	if refs := poll(); len(refs) != 1 || refs[0].Repo.Repo != "other/releases" {
		t.Fatalf("stale external PR fetched %+v, want one refresh", refs)
	}
}
