package domain

import (
	"testing"
	"time"
)

func TestSuppressNoInjectReviewRows(t *testing.T) {
	reviews := []PullRequestReview{
		{ID: "PRR_node", URL: "https://github.com/o/r/pull/1#pullrequestreview-987", AutoInjectReview: true},
		{ID: "PRR_human", URL: "https://github.com/o/r/pull/1#pullrequestreview-555", AutoInjectReview: true},
		{ID: "987", AutoInjectReview: true},
	}
	comments := []PullRequestComment{
		{ID: "c1", ReviewID: "987", AutoInjectReview: true},
		{ID: "c2", ReviewID: "555", AutoInjectReview: true},
		{ID: "c3", ReviewID: "", AutoInjectReview: true},
	}
	SuppressNoInjectReviewRows(map[string]bool{"987": true}, reviews, comments)
	if reviews[0].AutoInjectReview || !reviews[1].AutoInjectReview || reviews[2].AutoInjectReview {
		t.Fatalf("reviews = %+v, want only AO review 987 (by URL or id) suppressed", reviews)
	}
	if comments[0].AutoInjectReview || !comments[1].AutoInjectReview || !comments[2].AutoInjectReview {
		t.Fatalf("comments = %+v, want only comments of review 987 suppressed", comments)
	}
}

// #6810: before `ao review submit` records a --no-inject pass's review id,
// what the reviewer's account posted while the pass runs is matched by author
// and time.
func TestSuppressNoInjectWindowRowsBeforeSubmit(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	running := []NoInjectReviewWindow{{Start: start}}
	reviews := []PullRequestReview{
		{ID: "PRR_ao", Author: "Me", URL: "https://github.com/o/r/pull/1#pullrequestreview-987", SubmittedAt: start.Add(2 * time.Minute), AutoInjectReview: true},
		{ID: "PRR_human", Author: "you", SubmittedAt: start.Add(2 * time.Minute), AutoInjectReview: true},
		{ID: "PRR_old", Author: "me", SubmittedAt: start.Add(-time.Hour), AutoInjectReview: true},
	}
	comments := []PullRequestComment{
		{ID: "c1", ReviewID: "987", Author: "me", CreatedAt: start.Add(2 * time.Minute), AutoInjectReview: true},
		{ID: "c2", ReviewID: "PRR_ao", Author: "me", AutoInjectReview: true},
		{ID: "c3", Author: "you", CreatedAt: start.Add(2 * time.Minute), AutoInjectReview: true},
		{ID: "c4", Author: "me", CreatedAt: start.Add(-time.Hour), AutoInjectReview: true},
	}
	SuppressNoInjectWindowRows(running, "me", reviews, comments)
	if reviews[0].AutoInjectReview || !reviews[1].AutoInjectReview || !reviews[2].AutoInjectReview {
		t.Fatalf("reviews = %+v, want only the AO account's review during the pass suppressed", reviews)
	}
	if comments[0].AutoInjectReview || comments[1].AutoInjectReview || !comments[2].AutoInjectReview || !comments[3].AutoInjectReview {
		t.Fatalf("comments = %+v, want only the pass's comments suppressed", comments)
	}
}

// #6810: a reviewer that exited after posting but before submitting leaves a
// failed pass without a review id; what it posted while running stays quiet,
// and the account's later comments are delivered again.
func TestSuppressNoInjectWindowRowsReviewerExitedAfterPosting(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	ended := []NoInjectReviewWindow{{Start: start, End: start.Add(10 * time.Minute)}}
	comments := []PullRequestComment{
		{ID: "posted", Author: "me", CreatedAt: start.Add(5 * time.Minute), AutoInjectReview: true},
		{ID: "skewed", Author: "me", CreatedAt: start.Add(10*time.Minute + 30*time.Second), AutoInjectReview: true},
		{ID: "later", Author: "me", CreatedAt: start.Add(time.Hour), AutoInjectReview: true},
	}
	SuppressNoInjectWindowRows(ended, "me", nil, comments)
	if comments[0].AutoInjectReview || comments[1].AutoInjectReview || !comments[2].AutoInjectReview {
		t.Fatalf("comments = %+v, want the pass's comments suppressed and later ones kept", comments)
	}
	// Without the account the rows cannot be attributed, so nothing changes.
	later := []PullRequestComment{{ID: "posted", Author: "me", CreatedAt: start.Add(5 * time.Minute), AutoInjectReview: true}}
	SuppressNoInjectWindowRows(ended, "", nil, later)
	if !later[0].AutoInjectReview {
		t.Fatal("an unknown account suppressed a comment")
	}
}
