package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// writePRDetails stores a PR's details in the transaction that wrote its row,
// with the provider updatedAt of the observation they came from. An
// observation without details leaves the stored ones, and their updatedAt, as
// they are.
func writePRDetails(ctx context.Context, q *gen.Queries, prURL string, providerUpdatedAt time.Time, details *domain.PullRequestDetails) error {
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
	conversation, err := json.Marshal(nonNilSlice(details.Conversation))
	if err != nil {
		return fmt.Errorf("encode pr conversation %s: %w", prURL, err)
	}
	if err := q.UpsertPRDetails(ctx, gen.UpsertPRDetailsParams{
		PRURL:                prURL,
		Description:          details.Description,
		DescriptionTruncated: boolInt(details.DescriptionTruncated),
		Labels:               string(labels),
		RequestedReviewers:   string(reviewers),
		Commits:              string(commits),
		CommitCount:          int64(details.CommitCount),
		Conversation:         string(conversation),
		ObservedAt:           details.ObservedAt,
		ProviderUpdatedAt:    nullTime(providerUpdatedAt),
	}); err != nil {
		return fmt.Errorf("write pr details %s: %w", prURL, err)
	}
	return nil
}

// SavePRDetails stores details read for a PR outside an observation, as for a
// PR that merged or closed before the observer read them. The PR's row is left
// as it is, so nothing else about the PR changes.
func (s *Store) SavePRDetails(ctx context.Context, prURL string, providerUpdatedAt time.Time, details domain.PullRequestDetails) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writePRDetails(ctx, s.qw, prURL, providerUpdatedAt, &details)
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
	_ = json.Unmarshal([]byte(row.Conversation), &details.Conversation)
	return details, true, nil
}

// PRDetailsUpdatedAt returns the provider updatedAt of the PR state a PR's
// stored details were read from, so the observer can skip re-reading details
// that cannot have changed. ok is false when the PR has no stored details, or
// has details stored before the updatedAt was recorded: either way they are
// due a read.
func (s *Store) PRDetailsUpdatedAt(ctx context.Context, prURL string) (time.Time, bool, error) {
	updatedAt, err := s.qr.GetPRDetailsProviderUpdatedAt(ctx, prURL)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get pr details updated at %s: %w", prURL, err)
	}
	if !updatedAt.Valid || updatedAt.Time.IsZero() {
		return time.Time{}, false, nil
	}
	return updatedAt.Time, true, nil
}

// AppendPRConversationComment adds one just-posted conversation comment to a
// PR's stored details so the view shows it before the next observation, which
// rewrites the list from the provider. ok is false when the PR has no stored
// details yet: the comment then waits for that observation instead of
// fabricating a details row.
func (s *Store) AppendPRConversationComment(ctx context.Context, prURL string, comment domain.PullRequestConversationComment) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	appended := false
	err := s.inTx(ctx, "append pr conversation comment", func(q *gen.Queries) error {
		row, err := q.GetPRDetails(ctx, prURL)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get pr details %s: %w", prURL, err)
		}
		var conversation []domain.PullRequestConversationComment
		_ = json.Unmarshal([]byte(row.Conversation), &conversation)
		for _, existing := range conversation {
			if existing.ID == comment.ID {
				appended = true
				return nil
			}
		}
		conversation = append(conversation, comment)
		if extra := len(conversation) - domain.MaxPullRequestConversationComments; extra > 0 {
			conversation = conversation[extra:]
		}
		encoded, err := json.Marshal(conversation)
		if err != nil {
			return fmt.Errorf("encode pr conversation %s: %w", prURL, err)
		}
		if _, err := q.SetPRDetailsConversation(ctx, gen.SetPRDetailsConversationParams{Conversation: string(encoded), PRURL: prURL}); err != nil {
			return fmt.Errorf("write pr conversation %s: %w", prURL, err)
		}
		appended = true
		return nil
	})
	return appended, err
}

// nonNilSlice keeps an empty list encoding as [] and not null.
func nonNilSlice[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
