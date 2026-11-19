package scm

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// suppressNoInjectAOReviews clears AutoInjectReview on the observed reviews and
// inline comments that an AO --no-inject review pass posted, so the session
// that started that pass is never told to address them (#6810). It applies
// domain.SuppressNoInjectAOReviews, which ClaimPR shares, with the account the
// provider is signed in as, which is the one AO's reviewer posts with. A store
// that cannot list --no-inject passes keeps the session-wide behavior; without
// an identity resolver only recorded review ids are matched. unknownAt is the
// observation time the poll filled in for rows without a provider time.
func suppressNoInjectAOReviews(ctx context.Context, store any, identity ports.ScopedIdentityResolver, repo ports.SCMRepo, pr domain.PullRequest, unknownAt time.Time, reviews []domain.PullRequestReview, comments []domain.PullRequestComment) error {
	src, ok := store.(domain.NoInjectReviewSource)
	if !ok {
		return nil
	}
	var postingLogin func(context.Context) (string, error)
	if identity != nil {
		postingLogin = func(ctx context.Context) (string, error) {
			id, err := identity.AuthenticatedIdentityForProvider(ctx, repo.Provider, repo.Host)
			return id.Login, err
		}
	}
	return domain.SuppressNoInjectAOReviews(ctx, src, postingLogin, pr, unknownAt, reviews, comments)
}
