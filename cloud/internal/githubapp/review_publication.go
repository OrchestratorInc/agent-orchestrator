package githubapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

var ErrReviewPublicationUncertain = errors.New("GitHub review publication is uncertain; retry to reconcile it")

type reviewPublicationStore interface {
	BeginReviewPublication(context.Context, string, string, string, domain.SubmitReviewResult) (bool, error)
	MarkReviewPublicationUncertain(context.Context, string, string, string, string) error
	CompleteAndDeliverReviewRun(context.Context, string, string, string, domain.SubmitReviewResult, string) (domain.ReviewRun, error)
}

func reviewPublicationMarker(reviewRunID string) string {
	return "<!-- ao-review-run:" + reviewRunID + " -->"
}

func submitReviewOnce(
	ctx context.Context, store reviewPublicationStore, client *Client,
	orgID, sessionID, reviewRunID, token, owner, repo string, number int,
	result domain.SubmitReviewResult,
) (domain.ReviewRun, error) {
	claimed, err := store.BeginReviewPublication(ctx, orgID, reviewRunID, sessionID, result)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	marker := reviewPublicationMarker(reviewRunID)
	var providerReviewID int64
	if claimed {
		providerReviewID, err = client.CreatePullRequestReview(
			ctx, token, owner, repo, number, result.Body+"\n\n"+marker,
		)
		if err != nil {
			markReviewPublicationUncertain(ctx, store, orgID, reviewRunID, sessionID, err)
			return domain.ReviewRun{}, fmt.Errorf("%w: %v", ErrReviewPublicationUncertain, err)
		}
	} else {
		providerReviewID, err = client.FindPullRequestReviewByMarker(ctx, token, owner, repo, number, marker)
		if err != nil {
			return domain.ReviewRun{}, err
		}
		if providerReviewID == 0 {
			markReviewPublicationUncertain(ctx, store, orgID, reviewRunID, sessionID, ErrReviewPublicationUncertain)
			return domain.ReviewRun{}, ErrReviewPublicationUncertain
		}
	}
	delivered, err := store.CompleteAndDeliverReviewRun(
		ctx, orgID, reviewRunID, sessionID, result, formatProviderReviewID(providerReviewID),
	)
	if err != nil {
		markReviewPublicationUncertain(ctx, store, orgID, reviewRunID, sessionID, err)
		return domain.ReviewRun{}, err
	}
	return delivered, nil
}

func markReviewPublicationUncertain(
	ctx context.Context, store reviewPublicationStore, orgID, reviewRunID, sessionID string, cause error,
) {
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = store.MarkReviewPublicationUncertain(markCtx, orgID, reviewRunID, sessionID, cause.Error())
}
