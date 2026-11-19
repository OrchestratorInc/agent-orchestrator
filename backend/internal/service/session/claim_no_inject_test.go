package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// noInjectClaimer records the review rows a claim stores and lists one
// --no-inject pass on the PR.
type noInjectClaimer struct {
	fakePRClaimer
	windows     []domain.NoInjectReviewWindow
	gotReviews  []domain.PullRequestReview
	gotComments []domain.PullRequestComment
}

func (c *noInjectClaimer) ClaimPR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, mode ports.ReviewWriteMode, takeover bool) (ports.ClaimOutcome, error) {
	c.gotReviews, c.gotComments = reviews, comments
	return c.fakePRClaimer.ClaimPR(ctx, pr, checks, reviews, threads, comments, mode, takeover)
}

func (c *noInjectClaimer) NoInjectAOReviewIDs(context.Context, string) (map[string]bool, error) {
	return nil, nil
}

func (c *noInjectClaimer) NoInjectAOReviewWindows(context.Context, string) ([]domain.NoInjectReviewWindow, error) {
	return c.windows, nil
}

type signedInSCM struct {
	fakeSCM
	*signedIn
}

func TestClaimPRNoInjectIdentityFailureStillWrites(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, AutoInjectReview: true, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	url := "https://github.com/acme/repo/pull/7"
	scm := signedInSCM{
		fakeSCM: fakeSCM{
			obs: ports.SCMObservation{Fetched: true, Provider: "github", PR: ports.SCMPRObservation{URL: url, Number: 7, Title: "Current PR", HeadSHA: "new-head"}, CI: ports.SCMCIObservation{Summary: string(domain.CIPassing)}},
			review: ports.SCMReviewObservation{
				Reviews: []ports.SCMReviewSummaryObservation{{ID: "human", Author: "bob", State: string(domain.ReviewChangesRequest), SubmittedAt: start.Add(time.Minute)}},
				Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "bob", Body: "fix", CreatedAt: start.Add(time.Minute)}}}},
			},
		},
		signedIn: &signedIn{err: errors.New("offline")},
	}
	claimer := &noInjectClaimer{windows: []domain.NoInjectReviewWindow{{Start: start, End: start.Add(5 * time.Minute)}}}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: scm})
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err != nil {
		t.Fatalf("identity failure blocked claim: %v", err)
	}
	if !claimer.called || claimer.gotPR.HeadSHA != "new-head" || claimer.gotPR.CI != domain.CIPassing {
		t.Fatalf("claim lost current PR facts: %+v", claimer.gotPR)
	}
	if len(claimer.gotReviews) != 1 || claimer.gotReviews[0].AutoInjectReview || len(claimer.gotComments) != 1 || claimer.gotComments[0].AutoInjectReview {
		t.Fatalf("unattributed window rows stayed deliverable: reviews=%+v comments=%+v", claimer.gotReviews, claimer.gotComments)
	}
}

// #6810: a claim, which every review trigger runs to refresh its PRs, while a
// --no-inject pass is running stores that pass's review and comments as never
// delivered, like the SCM observer; another account's rows stay deliverable.
func TestClaimPRMidNoInjectPassStoresRowsNonInjecting(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	posted := start.Add(2 * time.Minute)
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, AutoInjectReview: true, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	url := "https://github.com/acme/repo/pull/7"
	scm := signedInSCM{
		fakeSCM: fakeSCM{
			obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: url, Number: 7}},
			review: ports.SCMReviewObservation{
				Reviews: []ports.SCMReviewSummaryObservation{
					{ID: "PRR_ao", Author: "alice", State: string(domain.ReviewChangesRequest), URL: url + "#pullrequestreview-987", SubmittedAt: posted},
					{ID: "PRR_bob", Author: "bob", State: string(domain.ReviewChangesRequest), SubmittedAt: posted},
				},
				Threads: []ports.SCMReviewThreadObservation{
					{ID: "t1", Path: "a.go", Line: 1, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", ReviewID: "987", Author: "alice", Body: "fix", CreatedAt: posted}}},
					{ID: "t2", Path: "b.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c2", Author: "bob", Body: "fix", CreatedAt: posted}}},
				},
			},
		},
		signedIn: alice(),
	}
	claimer := &noInjectClaimer{windows: []domain.NoInjectReviewWindow{{Start: start}}}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: scm})
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err != nil {
		t.Fatal(err)
	}
	for _, rv := range claimer.gotReviews {
		if want := rv.Author != "alice"; rv.AutoInjectReview != want {
			t.Fatalf("review %s AutoInjectReview = %v, want %v", rv.ID, rv.AutoInjectReview, want)
		}
	}
	for _, c := range claimer.gotComments {
		if want := c.Author != "alice"; c.AutoInjectReview != want {
			t.Fatalf("comment %s AutoInjectReview = %v, want %v", c.ID, c.AutoInjectReview, want)
		}
	}
	if len(claimer.gotReviews) != 2 || len(claimer.gotComments) != 2 {
		t.Fatalf("stored reviews=%d comments=%d, want 2 each", len(claimer.gotReviews), len(claimer.gotComments))
	}

	// Without the signed-in account, store the claim with every author's
	// window rows muted.
	claimer = &noInjectClaimer{windows: []domain.NoInjectReviewWindow{{Start: start}}}
	svc = NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: scm.fakeSCM})
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err != nil {
		t.Fatal(err)
	}
	if !claimer.called || len(claimer.gotReviews) != 2 || len(claimer.gotComments) != 2 {
		t.Fatal("claim lost rows without the posting account")
	}
	if claimer.gotReviews[0].AutoInjectReview || claimer.gotReviews[1].AutoInjectReview || claimer.gotComments[0].AutoInjectReview || claimer.gotComments[1].AutoInjectReview {
		t.Fatal("unattributed window rows stayed deliverable")
	}
}

// #6810: the claim fills its own time into rows the provider sent without one;
// that time is not when they were posted, so the posting account's such rows
// are held back even when the claim comes long after the pass ended.
func TestClaimPRUnknownPostingTimeStaysNonInjecting(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, AutoInjectReview: true, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	url := "https://github.com/acme/repo/pull/7"
	scm := signedInSCM{
		fakeSCM: fakeSCM{
			obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: url, Number: 7}},
			review: ports.SCMReviewObservation{
				Reviews: []ports.SCMReviewSummaryObservation{{ID: "PRR_ao", Author: "alice", State: string(domain.ReviewChangesRequest)}},
				Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Path: "a.go", Line: 1, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "alice", Body: "fix"}}}},
			},
		},
		signedIn: alice(),
	}
	claimer := &noInjectClaimer{windows: []domain.NoInjectReviewWindow{{Start: start, End: start.Add(5 * time.Minute)}}}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: scm})
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err != nil {
		t.Fatal(err)
	}
	if len(claimer.gotReviews) != 1 || claimer.gotReviews[0].AutoInjectReview {
		t.Fatalf("reviews = %+v, want the review without a provider time stored non-injecting", claimer.gotReviews)
	}
	if len(claimer.gotComments) != 1 || claimer.gotComments[0].AutoInjectReview {
		t.Fatalf("comments = %+v, want the comment without a provider time stored non-injecting", claimer.gotComments)
	}
}

func (c *noInjectClaimer) NoInjectAOReviewPRURLs(_ context.Context, pr domain.PullRequest) ([]string, error) {
	return []string{pr.URL}, nil
}
