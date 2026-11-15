package session

import (
	"context"
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

	// Without the signed-in account the rows cannot be attributed, so the
	// claim fails rather than store rows that might be delivered.
	claimer = &noInjectClaimer{windows: []domain.NoInjectReviewWindow{{Start: start}}}
	svc = NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: scm.fakeSCM})
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err == nil {
		t.Fatal("want the claim to fail without the posting account")
	}
	if claimer.called {
		t.Fatal("stored rows without the posting account")
	}
}
