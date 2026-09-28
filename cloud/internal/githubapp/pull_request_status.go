package githubapp

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

const (
	// mergeabilityRetryDelay is how soon to re-refresh a PR whose mergeability is
	// still unknown. The fallback scanner claims by due_at ascending, so this both
	// resolves the PR promptly and prioritizes it ahead of routine silence polls.
	mergeabilityRetryDelay = 20 * time.Second
	// mergeabilityRetryMaxAge bounds the retry: mergeability still unknown long
	// after the PR's last update is a GitHub anomaly, not a pending computation, so
	// stop rescheduling and leave it to normal silence polling rather than loop.
	mergeabilityRetryMaxAge = 6 * time.Hour
)

// RefreshPullRequestStatus refreshes a pull request's durable GitHub status.
func (s *Service) RefreshPullRequestStatus(
	ctx context.Context,
	ref domain.PullRequestRef,
	refresh domain.PullRequestRefreshContext,
) (domain.PullRequest, error) {
	if _, _, ok := strings.Cut(ref.Repository, "/"); !ok {
		return domain.PullRequest{}, postgres.ErrInvalid
	}
	snapshot, err := s.FetchPullRequestSnapshot(ctx, ref)
	if err != nil {
		return domain.PullRequest{}, err
	}
	transition, err := s.store.ApplyPullRequestSnapshot(ctx, ref.OrgID, ref.ID, snapshot, refresh)
	if err != nil {
		return domain.PullRequest{}, err
	}
	s.scheduleMergeabilityRetry(ctx, ref, transition.Current)
	return transition.Current, nil
}

// scheduleMergeabilityRetry re-arms the fallback refresh when an open PR's
// mergeability is still unknown. GitHub computes mergeability asynchronously, and
// even the REST fallback can return null on the first read right after a push, so
// one refresh is not always enough. Scheduling a near-term follow-up makes the PR
// resolve quickly and — because the scanner claims by due_at ascending — jump the
// queue. It self-terminates: once mergeability resolves, ApplyPullRequestSnapshot
// clears the fallback and this stops re-arming. Bounded by PR age so a
// permanently-unknown PR is left to normal silence polling instead of looping.
func (s *Service) scheduleMergeabilityRetry(ctx context.Context, ref domain.PullRequestRef, current domain.PullRequest) {
	if current.State != contract.PRStateOpen || current.Mergeability != contract.MergeUnknown {
		return
	}
	if current.UpdatedAtProvider != nil && time.Since(*current.UpdatedAtProvider) > mergeabilityRetryMaxAge {
		return
	}
	dueAt := time.Now().UTC().Add(mergeabilityRetryDelay)
	if err := s.store.SchedulePullRequestRefresh(
		ctx, ref.OrgID, ref.ID, domain.PullRequestRefreshWebhookSilent, dueAt, "mergeability pending",
	); err != nil {
		s.logger.Warn("schedule mergeability retry",
			"org_id", ref.OrgID, "pull_request_id", ref.ID,
			"repository", ref.Repository, "number", ref.Number, "error", err)
	}
}
