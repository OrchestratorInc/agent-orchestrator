package domain

import "strings"

// SuppressNoInjectReviewRows clears AutoInjectReview on every review and
// comment belonging to one of the given provider review ids.
func SuppressNoInjectReviewRows(ids map[string]bool, reviews []PullRequestReview, comments []PullRequestComment) {
	for i := range reviews {
		if ids[reviews[i].ID] || ids[reviewNumberFromURL(reviews[i].URL)] {
			reviews[i].AutoInjectReview = false
		}
	}
	for i := range comments {
		if comments[i].ReviewID != "" && ids[comments[i].ReviewID] {
			comments[i].AutoInjectReview = false
		}
	}
}

// reviewNumberFromURL returns the numeric review id GitHub puts in a submitted
// review's URL fragment (#pullrequestreview-<id>). AO records that id for its
// own reviews, while the observed review row is keyed by GraphQL node id.
func reviewNumberFromURL(url string) string {
	const marker = "#pullrequestreview-"
	i := strings.LastIndex(url, marker)
	if i < 0 {
		return ""
	}
	return url[i+len(marker):]
}
