package domain

import "testing"

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
