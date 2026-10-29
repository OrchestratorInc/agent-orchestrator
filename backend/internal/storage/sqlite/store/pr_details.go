package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// writePRDetails stores a PR's details in the transaction that wrote its row.
// An observation without details leaves the stored ones as they are.
func writePRDetails(ctx context.Context, q *gen.Queries, prURL string, details *domain.PullRequestDetails) error {
	if details == nil {
		return nil
	}
	labels, err := json.Marshal(nonNilSlice(details.Labels))
	if err != nil {
		return fmt.Errorf("encode pr labels %s: %w", prURL, err)
	}
	reviewers, err := json.Marshal(nonNilSlice(details.RequestedReviewers))
	if err != nil {
		return fmt.Errorf("encode pr requested reviewers %s: %w", prURL, err)
	}
	commits, err := json.Marshal(nonNilSlice(details.Commits))
	if err != nil {
		return fmt.Errorf("encode pr commits %s: %w", prURL, err)
	}
	if err := q.UpsertPRDetails(ctx, gen.UpsertPRDetailsParams{
		PRURL:                prURL,
		Description:          details.Description,
		DescriptionTruncated: boolInt(details.DescriptionTruncated),
		Labels:               string(labels),
		RequestedReviewers:   string(reviewers),
		Commits:              string(commits),
		CommitCount:          int64(details.CommitCount),
		ObservedAt:           details.ObservedAt,
	}); err != nil {
		return fmt.Errorf("write pr details %s: %w", prURL, err)
	}
	return nil
}

// GetPRDetails returns a PR's stored details. ok is false until an
// observation has carried them, which is not the same as a PR with none.
func (s *Store) GetPRDetails(ctx context.Context, prURL string) (domain.PullRequestDetails, bool, error) {
	row, err := s.qr.GetPRDetails(ctx, prURL)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PullRequestDetails{}, false, nil
	}
	if err != nil {
		return domain.PullRequestDetails{}, false, fmt.Errorf("get pr details %s: %w", prURL, err)
	}
	details := domain.PullRequestDetails{
		Description:          row.Description,
		DescriptionTruncated: row.DescriptionTruncated != 0,
		CommitCount:          int(row.CommitCount),
		ObservedAt:           row.ObservedAt,
	}
	// A malformed stored list reads as empty, never as a failed PR read: the
	// details are view-only and the next observation rewrites them.
	_ = json.Unmarshal([]byte(row.Labels), &details.Labels)
	_ = json.Unmarshal([]byte(row.RequestedReviewers), &details.RequestedReviewers)
	_ = json.Unmarshal([]byte(row.Commits), &details.Commits)
	return details, true, nil
}

// nonNilSlice keeps an empty list encoding as [] and not null.
func nonNilSlice[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
