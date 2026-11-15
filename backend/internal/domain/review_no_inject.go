package domain

import (
	"context"
	"fmt"
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

// NoInjectReviewWindow is the time a --no-inject AO review pass was running.
// A zero End means it still is.
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
//
// A row with no provider time cannot be placed outside a pass, so the
// account's rows without one match whenever a pass exists. Writers fill a
// missing time with their observation time; unknownAt is that value, and a
// row carrying it (or a zero time) has no provider time. Provider times are
// whole seconds and the observation clock is not, so they do not collide.
func SuppressNoInjectWindowRows(windows []NoInjectReviewWindow, login string, unknownAt time.Time, reviews []PullRequestReview, comments []PullRequestComment) {
	if len(windows) == 0 || login == "" {
		return
	}
	inWindow := func(author string, at time.Time) bool {
		if !strings.EqualFold(author, login) {
			return false
		}
		if at.IsZero() || (!unknownAt.IsZero() && at.Equal(unknownAt)) {
			return true
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

// NoInjectReviewSource lists the --no-inject AO review passes on a PR: the
// provider review ids they recorded and when each ran.
type NoInjectReviewSource interface {
	NoInjectAOReviewIDs(ctx context.Context, prURL string) (map[string]bool, error)
	NoInjectAOReviewWindows(ctx context.Context, prURL string) ([]NoInjectReviewWindow, error)
}

// SuppressNoInjectAOReviews clears AutoInjectReview on the reviews and comments
// that a --no-inject AO review pass on prURL posted, before they are stored:
// stored rows keep the value they were written with, so every writer of
// observed review rows must call it (#6810). Rows are matched by the review id
// a pass recorded, and by what postingLogin's account wrote while a pass ran,
// which covers a pass that has not recorded its id yet and a reviewer that
// exited without submitting. postingLogin is asked only when a pass exists; an
// error or empty login fails the call so the writer retries rather than store
// rows that might be delivered. A nil postingLogin matches by id only.
// unknownAt is the time the writer filled in for rows the provider sent
// without one (see SuppressNoInjectWindowRows).
func SuppressNoInjectAOReviews(ctx context.Context, src NoInjectReviewSource, postingLogin func(context.Context) (string, error), prURL string, unknownAt time.Time, reviews []PullRequestReview, comments []PullRequestComment) error {
	if src == nil || prURL == "" || (len(reviews) == 0 && len(comments) == 0) {
		return nil
	}
	ids, err := src.NoInjectAOReviewIDs(ctx, prURL)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		SuppressNoInjectReviewRows(ids, reviews, comments)
	}
	if postingLogin == nil {
		return nil
	}
	windows, err := src.NoInjectAOReviewWindows(ctx, prURL)
	if err != nil || len(windows) == 0 {
		return err
	}
	login, err := postingLogin(ctx)
	if err != nil {
		return fmt.Errorf("resolve the account AO reviews post with: %w", err)
	}
	login = strings.TrimSpace(login)
	if login == "" {
		return fmt.Errorf("resolve the account AO reviews post with: no login")
	}
	SuppressNoInjectWindowRows(windows, login, unknownAt, reviews, comments)
	return nil
}
