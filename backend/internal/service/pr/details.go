package pr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// detailsStore reads and stores a PR's details on their own.
type detailsStore interface {
	GetPRDetails(ctx context.Context, prURL string) (domain.PullRequestDetails, bool, error)
	SavePRDetails(ctx context.Context, prURL string, providerUpdatedAt time.Time, details domain.PullRequestDetails) error
}

// LoadDetails reads a tracked PR's details from its provider when none are
// stored. The observer reads details only for the open PRs it follows, so a PR
// that merged or closed before they were read would never get them, and its
// tab would show them loading for good. Stored details are left alone: the
// observer keeps an open PR's details current.
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
	// A ref with no stored details' updatedAt asks the provider for them.
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
	// A miss says why: a transient failure (a timeout, a rate limit) is not the
	// PR being gone, and a retry can still read it.
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
	// As the observer does: details are as of the read that carried them, and
	// a view treats details with no time as not read yet.
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
