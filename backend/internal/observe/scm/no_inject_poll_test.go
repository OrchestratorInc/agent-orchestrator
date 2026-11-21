package scm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type noInjectPollStore struct {
	*fakeStore
	fakeNoInjectStore
}

func TestPollNoInjectIdentityFailureStillPersistsAndReacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		merged bool
		login  string
		err    error
	}{
		{name: "merged with identity error", merged: true, login: "me", err: errors.New("offline")},
		{name: "CI update with empty login", login: "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
			now := start.Add(time.Hour)
			base := testStoreWithSession()
			base.sessions[0].AutoInjectReview = true
			local := knownPR(1)
			local.CI, local.CIHash = domain.CIFailing, "old-ci"
			base.prs["p-1"] = []domain.PullRequest{local}
			store := &noInjectPollStore{fakeStore: base, fakeNoInjectStore: fakeNoInjectStore{
				windows: []domain.NoInjectReviewWindow{{Start: start, End: start.Add(5 * time.Minute)}},
			}}
			observation := testObs(1)
			observation.PR.Merged = tc.merged
			observation.Review.Decision = string(domain.ReviewChangesRequest)
			review := ports.SCMReviewObservation{
				Decision: string(domain.ReviewChangesRequest),
				Reviews: []ports.SCMReviewSummaryObservation{
					{ID: "ao", Author: "me", State: string(domain.ReviewChangesRequest), SubmittedAt: start.Add(time.Minute)},
					{ID: "human", Author: "you", State: string(domain.ReviewChangesRequest), SubmittedAt: start.Add(time.Minute)},
					{ID: "later", Author: "you", State: string(domain.ReviewChangesRequest), SubmittedAt: start.Add(30 * time.Minute)},
				},
				Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Comments: []ports.SCMReviewCommentObservation{
					{ID: "inside", Author: "you", Body: "fix", CreatedAt: start.Add(time.Minute)},
					{ID: "outside", Author: "you", Body: "fix", CreatedAt: start.Add(30 * time.Minute)},
				}}},
			}
			provider := &fakeProvider{
				repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
				observations: map[string]ports.SCMObservation{prKey(testRepo, 1): observation},
				reviews:      map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
			}
			identity := &fakeIdentityResolver{identity: ports.SCMIdentity{Login: tc.login}, err: tc.err}
			lc := &fakeLifecycle{}
			observer := New(provider, store, lc, Config{Clock: func() time.Time { return now }, Logger: quietSlog(), ScopedIdentityResolver: identity})
			observer.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
			if err := observer.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(store.writes) != 2 || len(lc.observed) != 1 {
				t.Fatalf("writes=%d lifecycle=%d, want facts, lifecycle and acknowledgement despite identity failure", len(store.writes), len(lc.observed))
			}
			write := store.writes[0]
			if write.pr.Merged != tc.merged || write.pr.CI != domain.CIPassing || len(write.checks) != 1 {
				t.Fatalf("PR facts were lost: PR=%+v checks=%+v", write.pr, write.checks)
			}
			if len(write.reviews) != 3 || write.reviews[0].AutoInjectReview || write.reviews[1].AutoInjectReview || !write.reviews[2].AutoInjectReview {
				t.Fatalf("reviews=%+v, want every author muted only inside the window", write.reviews)
			}
			if len(write.comments) != 2 || write.comments[0].AutoInjectReview || !write.comments[1].AutoInjectReview {
				t.Fatalf("comments=%+v, want only the window's comments muted", write.comments)
			}
		})
	}
}
