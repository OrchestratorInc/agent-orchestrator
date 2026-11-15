package review

import (
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

// StateStatus is the per-PR review planning state.
type StateStatus = contract.AOReviewState

const (
	// ReviewStateNeedsReview means an eligible PR has no current AO approval or running pass.
	ReviewStateNeedsReview = contract.AOReviewNeedsReview
	// ReviewStateRunning means a review run is already active for the PR's current head.
	ReviewStateRunning = contract.AOReviewRunning
	// ReviewStateUpToDate means AO approved the PR's current head.
	ReviewStateUpToDate = contract.AOReviewUpToDate
	// ReviewStateChangesRequested means AO requested changes on the PR's current head.
	ReviewStateChangesRequested = contract.AOReviewChangesRequested
	// ReviewStateIneligible means the PR is closed, merged, or missing required facts.
	ReviewStateIneligible = contract.AOReviewIneligible
)

// PRReviewState is one PR-scoped review decision for a worker session.
type PRReviewState struct {
	PRURL       string            `json:"prUrl"`
	PRNumber    int               `json:"prNumber"`
	Title       string            `json:"title"`
	TargetSHA   string            `json:"targetSha"`
	Status      StateStatus       `json:"status" enum:"needs_review,running,up_to_date,changes_requested,ineligible"`
	LatestRun   *domain.ReviewRun `json:"latestRun,omitempty"`
	PreviousRun *domain.ReviewRun `json:"previousRun,omitempty"`
	// Reviewers is set only when several reviewers are configured. Each entry
	// is one reviewer's own state for TargetSHA; Status above is then the
	// combined verdict across all of them.
	Reviewers []ReviewerState `json:"reviewers,omitempty"`
}

// ReviewerState is one configured reviewer's state for a PR's current head.
type ReviewerState struct {
	Harness   domain.ReviewerHarness `json:"harness"`
	Status    StateStatus            `json:"status" enum:"needs_review,running,up_to_date,changes_requested,ineligible"`
	LatestRun *domain.ReviewRun      `json:"latestRun,omitempty"`
}

// Plan computes per-PR review work from the currently observed PRs and existing
// review runs. It is pure so the trigger path and API list path share exactly
// the same eligibility/status rules.
func Plan(prs []domain.PullRequest, runs []domain.ReviewRun) []PRReviewState {
	latest := latestRunsByPRAndSHA(runs)
	reviews := make([]PRReviewState, 0, len(prs))
	for _, pr := range prs {
		review := PRReviewState{
			PRURL:     pr.URL,
			PRNumber:  pr.Number,
			Title:     pr.Title,
			TargetSHA: pr.HeadSHA,
			Status:    ReviewStateNeedsReview,
		}
		if run, ok := latestCompletedRunForOtherSHA(runs, review.PRURL, review.TargetSHA); ok {
			review.PreviousRun = &run
		}
		if pr.URL == "" || pr.HeadSHA == "" || pr.Merged || pr.Closed {
			review.Status = ReviewStateIneligible
			if run, ok := latest[review.PRURL+"\x00"+review.TargetSHA]; ok {
				review.LatestRun = &run
			}
			reviews = append(reviews, review)
			continue
		}
		if run, ok := latest[review.PRURL+"\x00"+review.TargetSHA]; ok {
			review.LatestRun = &run
			switch {
			case run.Status == domain.ReviewRunRunning:
				review.Status = ReviewStateRunning
			case run.Verdict == domain.VerdictApproved:
				review.Status = ReviewStateUpToDate
			case run.Verdict == domain.VerdictChangesRequested:
				review.Status = ReviewStateChangesRequested
			case run.Status == domain.ReviewRunFailed || run.Status == domain.ReviewRunCancelled:
				review.Status = ReviewStateNeedsReview
			default:
				review.Status = ReviewStateNeedsReview
			}
		}
		reviews = append(reviews, review)
	}
	sort.SliceStable(reviews, func(i, j int) bool {
		if reviews[i].PRNumber != reviews[j].PRNumber {
			return reviews[i].PRNumber < reviews[j].PRNumber
		}
		return reviews[i].PRURL < reviews[j].PRURL
	})
	return reviews
}

func latestCompletedRunForOtherSHA(runs []domain.ReviewRun, prURL, targetSHA string) (domain.ReviewRun, bool) {
	if prURL == "" || targetSHA == "" {
		return domain.ReviewRun{}, false
	}
	var latest domain.ReviewRun
	found := false
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA == "" || run.TargetSHA == targetSHA {
			continue
		}
		if run.Status != domain.ReviewRunComplete && run.Status != domain.ReviewRunDelivered {
			continue
		}
		if run.Verdict != domain.VerdictApproved && run.Verdict != domain.VerdictChangesRequested {
			continue
		}
		if !found || run.CreatedAt.After(latest.CreatedAt) {
			latest = run
			found = true
		}
	}
	return latest, found
}

// latestRunsByPRAndSHA picks the authoritative run per PR head: the newest
// running pass while any reviewer is still working on that head, otherwise the
// newest pass. Several reviewers may review one head at once, so a reviewer that
// finished first must not hide one that is still running.
func latestRunsByPRAndSHA(runs []domain.ReviewRun) map[string]domain.ReviewRun {
	latest := make(map[string]domain.ReviewRun)
	for _, run := range runs {
		if run.PRURL == "" || run.TargetSHA == "" {
			continue
		}
		key := run.PRURL + "\x00" + run.TargetSHA
		existing, ok := latest[key]
		if !ok {
			latest[key] = run
			continue
		}
		runRunning := run.Status == domain.ReviewRunRunning
		existingRunning := existing.Status == domain.ReviewRunRunning
		if runRunning != existingRunning {
			if runRunning {
				latest[key] = run
			}
			continue
		}
		if run.CreatedAt.After(existing.CreatedAt) {
			latest[key] = run
		}
	}
	return latest
}

// PlanReviewers plans review state when several reviewers must each judge
// every PR head. Each reviewer is planned only from its own runs, and the
// PR's Status is the combined verdict for its current head:
//
//   - ineligible when the PR itself cannot be reviewed;
//   - changes_requested when any reviewer requested changes;
//   - running when any reviewer is still reviewing;
//   - needs_review when any reviewer has not completed an approval (no run,
//     failed, cancelled, or a verdict on an older head);
//   - up_to_date only when every reviewer approved this exact head.
//
// A failed or timed-out reviewer therefore never counts as approval, and a new
// head commit resets every reviewer to needs_review. With fewer than two
// harnesses it is exactly Plan.
func PlanReviewers(prs []domain.PullRequest, runs []domain.ReviewRun, harnesses []domain.ReviewerHarness) []PRReviewState {
	reviews := Plan(prs, runs)
	if len(harnesses) < 2 {
		return reviews
	}
	perHarness := make([][]PRReviewState, len(harnesses))
	for i, h := range harnesses {
		own := make([]domain.ReviewRun, 0, len(runs))
		for _, run := range runs {
			if run.Harness == h {
				own = append(own, run)
			}
		}
		perHarness[i] = Plan(prs, own)
	}
	for i := range reviews {
		reviewers := make([]ReviewerState, 0, len(harnesses))
		for j, h := range harnesses {
			state := findReviewState(perHarness[j], reviews[i].PRURL, reviews[i].TargetSHA)
			reviewers = append(reviewers, ReviewerState{Harness: h, Status: state.Status, LatestRun: state.LatestRun})
		}
		reviews[i].Reviewers = reviewers
		reviews[i].Status = combinedReviewStatus(reviews[i].Status, reviewers)
	}
	return reviews
}

func findReviewState(states []PRReviewState, prURL, targetSHA string) PRReviewState {
	for _, s := range states {
		if s.PRURL == prURL && s.TargetSHA == targetSHA {
			return s
		}
	}
	return PRReviewState{Status: ReviewStateNeedsReview}
}

func combinedReviewStatus(prStatus StateStatus, reviewers []ReviewerState) StateStatus {
	if prStatus == ReviewStateIneligible {
		return ReviewStateIneligible
	}
	has := map[StateStatus]bool{}
	for _, r := range reviewers {
		has[r.Status] = true
	}
	switch {
	case has[ReviewStateChangesRequested]:
		return ReviewStateChangesRequested
	case has[ReviewStateRunning]:
		return ReviewStateRunning
	case has[ReviewStateNeedsReview]:
		return ReviewStateNeedsReview
	case len(reviewers) > 0 && len(has) == 1 && has[ReviewStateUpToDate]:
		return ReviewStateUpToDate
	default:
		return ReviewStateNeedsReview
	}
}
