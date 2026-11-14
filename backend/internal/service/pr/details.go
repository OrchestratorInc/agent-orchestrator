package pr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type detailsStore interface {
	GetPRDetails(ctx context.Context, prURL string) (domain.PullRequestDetails, bool, error)
	SavePRDetails(ctx context.Context, prURL string, providerUpdatedAt time.Time, details domain.PullRequestDetails) error
}

func (s *ActionService) LoadDetails(ctx context.Context, prID, prURL string) error {
	if s.details == nil || s.reader == nil {
		return errors.New("pr: details loading is not configured")
	}
	tracked, repo, err := s.loadStackPR(ctx, prID, prURL)
	if err != nil {
		return err
	}
	if _, stored, err := s.details.GetPRDetails(ctx, tracked.URL); err != nil {
		return fmt.Errorf("load stored pull request details: %w", err)
	} else if stored {
		return nil
	}
	observations, err := s.reader.FetchPullRequests(ctx, []ports.SCMPRRef{{Repo: repo, Number: tracked.Number, URL: tracked.URL}})
	if err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return fmt.Errorf("%w: %w", ErrPRNotFound, err)
		}
		return fmt.Errorf("read pull request details: %w", err)
	}
	if len(observations) != 1 {
		return ErrPRNotFound
	}
	if err := observations[0].Error; err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return fmt.Errorf("%w: %w", ErrPRNotFound, err)
		}
		return fmt.Errorf("read pull request details: %w", err)
	}
	if !observations[0].Fetched || observations[0].PR.Number != tracked.Number {
		return ErrPRNotFound
	}
	fresh := observations[0].PR
	if fresh.Details == nil {
		return fmt.Errorf("read pull request details: %s sent none for #%d", tracked.Provider, tracked.Number)
	}
	details := *fresh.Details
	details.ObservedAt = observations[0].ObservedAt.UTC()
	if observations[0].ObservedAt.IsZero() {
		details.ObservedAt = s.clock().UTC()
	}
	if err := s.details.SavePRDetails(ctx, tracked.URL, fresh.UpdatedAtProvider, details); err != nil {
		return fmt.Errorf("store pull request details: %w", err)
	}
	return nil
}
