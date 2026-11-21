package scm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeNoInjectStore struct {
	ids     map[string]bool
	windows []domain.NoInjectReviewWindow
}

func (s fakeNoInjectStore) NoInjectAOReviewIDs(context.Context, string) (map[string]bool, error) {
	return s.ids, nil
}

func (s fakeNoInjectStore) NoInjectAOReviewWindows(context.Context, string) ([]domain.NoInjectReviewWindow, error) {
	return s.windows, nil
}

func noInjectObservedRows(at time.Time) ([]domain.PullRequestReview, []domain.PullRequestComment) {
	reviews := []domain.PullRequestReview{
		{ID: "PRR_ao", Author: "me", URL: "https://github.com/o/r/pull/1#pullrequestreview-987", SubmittedAt: at, AutoInjectReview: true},
		{ID: "PRR_human", Author: "you", SubmittedAt: at, AutoInjectReview: true},
	}
	comments := []domain.PullRequestComment{
		{ID: "c1", ReviewID: "987", Author: "me", CreatedAt: at, AutoInjectReview: true},
		{ID: "c2", ReviewID: "555", Author: "you", CreatedAt: at, AutoInjectReview: true},
	}
	return reviews, comments
}

// #6810: the observer polls after the reviewer posted but before `ao review
// submit` recorded the review id. The pass's rows are stored non-injecting, so
// lifecycle sends no fix nudge for them.
func TestSuppressNoInjectAOReviewsPollBeforeSubmit(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	store := fakeNoInjectStore{windows: []domain.NoInjectReviewWindow{{Start: start}}}
	identity := &fakeIdentityResolver{identity: ports.SCMIdentity{Login: "me", Human: true}}
	reviews, comments := noInjectObservedRows(start.Add(time.Minute))
	repo := ports.SCMRepo{Provider: "github", Host: "github.com"}
	if err := suppressNoInjectAOReviews(context.Background(), store, identity, repo, domain.PullRequest{URL: "https://github.com/o/r/pull/1"}, time.Time{}, reviews, comments); err != nil {
		t.Fatal(err)
	}
	if reviews[0].AutoInjectReview || !reviews[1].AutoInjectReview {
		t.Fatalf("reviews = %+v, want only the running pass's review suppressed", reviews)
	}
	if comments[0].AutoInjectReview || !comments[1].AutoInjectReview {
		t.Fatalf("comments = %+v, want only the running pass's comments suppressed", comments)
	}
}

// #6810: the reviewer posted, then exited before submitting, so the pass ended
// failed with no review id. Its rows still are never delivered.
func TestSuppressNoInjectAOReviewsReviewerExitedAfterPosting(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	store := fakeNoInjectStore{windows: []domain.NoInjectReviewWindow{{Start: start, End: start.Add(5 * time.Minute)}}}
	identity := &fakeIdentityResolver{identity: ports.SCMIdentity{Login: "me", Human: true}}
	reviews, comments := noInjectObservedRows(start.Add(4 * time.Minute))
	if err := suppressNoInjectAOReviews(context.Background(), store, identity, ports.SCMRepo{Provider: "github"}, domain.PullRequest{URL: "https://github.com/o/r/pull/1"}, time.Time{}, reviews, comments); err != nil {
		t.Fatal(err)
	}
	if reviews[0].AutoInjectReview || comments[0].AutoInjectReview {
		t.Fatalf("reviews = %+v comments = %+v, want the exited pass's rows suppressed", reviews, comments)
	}
	if !reviews[1].AutoInjectReview || !comments[1].AutoInjectReview {
		t.Fatalf("reviews = %+v comments = %+v, want other accounts' rows kept", reviews, comments)
	}
}

// Without the posting account, mute every author's rows within the window
// while allowing the PR's current facts to be stored.
func TestSuppressNoInjectAOReviewsIdentityErrorMutesWindowRows(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	store := fakeNoInjectStore{windows: []domain.NoInjectReviewWindow{{Start: start}}}
	identity := &fakeIdentityResolver{err: errors.New("offline")}
	reviews, comments := noInjectObservedRows(start)
	if err := suppressNoInjectAOReviews(context.Background(), store, identity, ports.SCMRepo{Provider: "github"}, domain.PullRequest{URL: "https://github.com/o/r/pull/1"}, time.Time{}, reviews, comments); err != nil {
		t.Fatal(err)
	}
	if reviews[0].AutoInjectReview || reviews[1].AutoInjectReview || comments[0].AutoInjectReview || comments[1].AutoInjectReview {
		t.Fatalf("unattributed window rows stayed deliverable: reviews=%+v comments=%+v", reviews, comments)
	}
	// No pass: identity is not needed and not asked for.
	identity = &fakeIdentityResolver{err: errors.New("offline")}
	if err := suppressNoInjectAOReviews(context.Background(), fakeNoInjectStore{}, identity, ports.SCMRepo{Provider: "github"}, domain.PullRequest{URL: "https://github.com/o/r/pull/1"}, time.Time{}, reviews, comments); err != nil || identity.calls != 0 {
		t.Fatalf("err = %v calls = %d, want no identity lookup without an open pass", err, identity.calls)
	}
}

// An empty login uses the same conservative delivery policy as a failed lookup.
func TestSuppressNoInjectAOReviewsEmptyLoginMutesWindowRows(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	store := fakeNoInjectStore{windows: []domain.NoInjectReviewWindow{{Start: start}}}
	identity := &fakeIdentityResolver{identity: ports.SCMIdentity{Login: "  ", Human: true}}
	reviews, comments := noInjectObservedRows(start)
	if err := suppressNoInjectAOReviews(context.Background(), store, identity, ports.SCMRepo{Provider: "github"}, domain.PullRequest{URL: "https://github.com/o/r/pull/1"}, time.Time{}, reviews, comments); err != nil {
		t.Fatal(err)
	}
	if reviews[0].AutoInjectReview || reviews[1].AutoInjectReview || comments[0].AutoInjectReview || comments[1].AutoInjectReview {
		t.Fatalf("unattributed window rows stayed deliverable: reviews=%+v comments=%+v", reviews, comments)
	}
}

func (s fakeNoInjectStore) NoInjectAOReviewPRURLs(_ context.Context, pr domain.PullRequest) ([]string, error) {
	return []string{pr.URL}, nil
}
