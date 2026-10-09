package githubapp

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

type AutomaticReviewStore interface {
	AutomaticReviewCandidates(context.Context) ([]domain.PullRequest, error)
}

// RunAutomaticReviews reevaluates stored PR facts when workers become idle.
// GitHub observations remain webhook driven; this loop never fetches PR status.
func (s *Service) RunAutomaticReviews(ctx context.Context, store AutomaticReviewStore) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.triggerAutomaticReviews(ctx, store); err != nil && ctx.Err() == nil {
			s.logger.Error("evaluate automatic PR reviews", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) triggerAutomaticReviews(ctx context.Context, store AutomaticReviewStore) error {
	prs, err := store.AutomaticReviewCandidates(ctx)
	if err != nil {
		return err
	}
	for _, pr := range prs {
		if _, _, err := s.TriggerAutomaticReview(ctx, pr.OrgID, pr.SessionID, "", pr); err != nil {
			s.logger.Error("start automatic PR review", "org_id", pr.OrgID, "pull_request_id", pr.ID, "error", err)
		}
	}
	return nil
}
