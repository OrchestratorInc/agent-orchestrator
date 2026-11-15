package scm

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// NoInjectReviewLister lists the provider review ids of AO review passes on a
// PR that were started with --no-inject.
type NoInjectReviewLister interface {
	NoInjectAOReviewIDs(ctx context.Context, prURL string) (map[string]bool, error)
}

// suppressNoInjectAOReviews clears AutoInjectReview on the observed reviews and
// inline comments that an AO --no-inject review pass posted, so the session
// that started that pass is never told to address them (#6810). Rows otherwise
// snapshot the session's live auto-inject setting, which a --no-inject trigger
// leaves on. A store without the lister keeps the session-wide behavior.
func suppressNoInjectAOReviews(ctx context.Context, store any, prURL string, reviews []domain.PullRequestReview, comments []domain.PullRequestComment) error {
	lister, ok := store.(NoInjectReviewLister)
	if !ok || prURL == "" || (len(reviews) == 0 && len(comments) == 0) {
		return nil
	}
	ids, err := lister.NoInjectAOReviewIDs(ctx, prURL)
	if err != nil || len(ids) == 0 {
		return err
	}
	domain.SuppressNoInjectReviewRows(ids, reviews, comments)
	return nil
}
