package domain

import (
	"strings"
	"time"
)

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

// NoInjectReviewSkew widens a no-inject window on both sides. The run's times
// come from this machine's clock and the observed rows' from the provider's.
const NoInjectReviewSkew = time.Minute

// NoInjectReviewWindow is the time a --no-inject AO review pass without a
// recorded provider review id was running. A zero End means it still is.
type NoInjectReviewWindow struct {
	Start time.Time
	End   time.Time
}

func (w NoInjectReviewWindow) contains(t time.Time) bool {
	if t.IsZero() || t.Before(w.Start.Add(-NoInjectReviewSkew)) {
		return false
	}
	return w.End.IsZero() || !t.After(w.End.Add(NoInjectReviewSkew))
}

// SuppressNoInjectWindowRows clears AutoInjectReview on the reviews and
// comments that login, the account AO's reviewer posts with, wrote while a
// --no-inject pass was running. It covers the pass before `ao review submit`
// records its review id, and a reviewer that exited after posting but before
// submitting. Inline comments of a suppressed review go with it.
func SuppressNoInjectWindowRows(windows []NoInjectReviewWindow, login string, reviews []PullRequestReview, comments []PullRequestComment) {
	if len(windows) == 0 || login == "" {
		return
	}
	inWindow := func(author string, at time.Time) bool {
		if !strings.EqualFold(author, login) {
			return false
		}
		for _, w := range windows {
			if w.contains(at) {
				return true
			}
		}
		return false
	}
	suppressed := map[string]bool{}
	for i := range reviews {
		if inWindow(reviews[i].Author, reviews[i].SubmittedAt) {
			reviews[i].AutoInjectReview = false
			suppressed[reviews[i].ID] = true
			if n := reviewNumberFromURL(reviews[i].URL); n != "" {
				suppressed[n] = true
			}
		}
	}
	for i := range comments {
		if (comments[i].ReviewID != "" && suppressed[comments[i].ReviewID]) || inWindow(comments[i].Author, comments[i].CreatedAt) {
			comments[i].AutoInjectReview = false
		}
	}
}
