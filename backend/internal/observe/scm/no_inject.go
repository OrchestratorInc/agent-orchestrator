package scm

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// NoInjectReviewLister lists the provider review ids of AO review passes on a
// PR that were started with --no-inject.
type NoInjectReviewLister interface {
	NoInjectAOReviewIDs(ctx context.Context, prURL string) (map[string]bool, error)
}

// NoInjectReviewWindowLister lists when the --no-inject AO review passes on a
// PR that have no recorded provider review id were running.
type NoInjectReviewWindowLister interface {
	NoInjectAOReviewWindows(ctx context.Context, prURL string) ([]domain.NoInjectReviewWindow, error)
}

// suppressNoInjectAOReviews clears AutoInjectReview on the observed reviews and
// inline comments that an AO --no-inject review pass posted, so the session
// that started that pass is never told to address them (#6810). Rows otherwise
// snapshot the session's live auto-inject setting, which a --no-inject trigger
// leaves on, and keep it once stored. A store without the listers keeps the
// session-wide behavior.
//
// A pass is matched by its recorded review id once `ao review submit` ran.
// Before that, or when the reviewer exited after posting without submitting,
// whatever the account AO's reviewer posts with wrote while the pass ran is
// matched instead; that needs identity, and an unresolvable account is an
// error so the poll retries rather than store deliverable rows.
func suppressNoInjectAOReviews(ctx context.Context, store any, identity ports.ScopedIdentityResolver, repo ports.SCMRepo, prURL string, reviews []domain.PullRequestReview, comments []domain.PullRequestComment) error {
	if prURL == "" || (len(reviews) == 0 && len(comments) == 0) {
		return nil
	}
	if lister, ok := store.(NoInjectReviewLister); ok {
		ids, err := lister.NoInjectAOReviewIDs(ctx, prURL)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			domain.SuppressNoInjectReviewRows(ids, reviews, comments)
		}
	}
	windowLister, ok := store.(NoInjectReviewWindowLister)
	if !ok || identity == nil {
		return nil
	}
	windows, err := windowLister.NoInjectAOReviewWindows(ctx, prURL)
	if err != nil || len(windows) == 0 {
		return err
	}
	id, err := identity.AuthenticatedIdentityForProvider(ctx, repo.Provider, repo.Host)
	if err != nil {
		return err
	}
	domain.SuppressNoInjectWindowRows(windows, strings.TrimSpace(id.Login), reviews, comments)
	return nil
}
