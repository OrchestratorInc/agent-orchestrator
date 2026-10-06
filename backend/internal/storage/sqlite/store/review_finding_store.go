package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CompleteReviewRun records a running pass's result, files its findings, and
// supersedes the open findings it replaces, all in one transaction, so a
// retried submit can never leave a completed run without its findings or file
// them twice. It reports false when the run was no longer running.
func (s *Store) CompleteReviewRun(ctx context.Context, run domain.ReviewRun, findings []domain.ReviewFindingInput, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin complete review run %s: %w", run.ID, err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	n, err := q.UpdateReviewRunResult(ctx, gen.UpdateReviewRunResultParams{
		Status:           domain.ReviewRunComplete,
		Verdict:          run.Verdict,
		Body:             run.Body,
		GithubReviewID:   run.GithubReviewID,
		AutoInjectReview: run.AutoInjectReview,
		ID:               run.ID,
	})
	if err != nil {
		return false, fmt.Errorf("complete review run %s: %w", run.ID, err)
	}
	if n == 0 {
		return false, nil
	}
	for i, f := range findings {
		if err := q.InsertReviewFinding(ctx, gen.InsertReviewFindingParams{
			ID:        uuid.NewString(),
			RunID:     run.ID,
			SessionID: run.SessionID,
			PRURL:     run.PRURL,
			TargetSha: run.TargetSHA,
			Ordinal:   int64(i + 1),
			Path:      f.Path,
			Line:      int64(f.Line),
			Body:      f.Body,
			CreatedAt: now,
		}); err != nil {
			return false, fmt.Errorf("insert review finding %d for run %s: %w", i+1, run.ID, err)
		}
	}
	if _, err := q.SupersedeOpenReviewFindings(ctx, gen.SupersedeOpenReviewFindingsParams{
		NewRunID:  run.ID,
		SessionID: run.SessionID,
		PRURL:     run.PRURL,
		CreatedAt: run.CreatedAt,
		TargetSha: run.TargetSHA,
		Harness:   run.Harness,
	}); err != nil {
		return false, fmt.Errorf("supersede review findings for run %s: %w", run.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit complete review run %s: %w", run.ID, err)
	}
	return true, nil
}

// ListReviewFindingsBySession returns every finding AO's reviewer filed for a
// worker, oldest first. Resolved and superseded findings are history and stay.
func (s *Store) ListReviewFindingsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewFinding, error) {
	rows, err := s.qr.ListReviewFindingsBySession(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ReviewFinding, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewFindingFromRow(r))
	}
	return out, nil
}

// ListReviewFindingsByRun returns one pass's findings in the reviewer's order.
func (s *Store) ListReviewFindingsByRun(ctx context.Context, runID string) ([]domain.ReviewFinding, error) {
	rows, err := s.qr.ListReviewFindingsByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ReviewFinding, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewFindingFromRow(r))
	}
	return out, nil
}

// GetReviewFinding returns one finding by id.
func (s *Store) GetReviewFinding(ctx context.Context, id string) (domain.ReviewFinding, bool, error) {
	row, err := s.qr.GetReviewFinding(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReviewFinding{}, false, nil
	}
	if err != nil {
		return domain.ReviewFinding{}, false, err
	}
	return reviewFindingFromRow(row), true, nil
}

// ResolveReviewFinding moves an open finding to resolved. It reports false when
// the finding was not open (already resolved, or superseded).
func (s *Store) ResolveReviewFinding(ctx context.Context, id, note string, resolvedBy domain.SessionID, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.ResolveReviewFinding(ctx, gen.ResolveReviewFindingParams{
		ResolutionNote:      note,
		ResolvedBySessionID: string(resolvedBy),
		ResolvedAt:          sql.NullTime{Time: at, Valid: true},
		ID:                  id,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetReviewRunProviderPost records the outcome of posting a pass's summary to
// the provider: the created review's id, or why the post failed.
func (s *Store) SetReviewRunProviderPost(ctx context.Context, id, githubReviewID, postError string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.SetReviewRunProviderPost(ctx, gen.SetReviewRunProviderPostParams{
		GithubReviewID:    githubReviewID,
		ProviderPostError: postError,
		ID:                id,
	})
	return err
}

// MarkReviewRunDelivered stamps a completed pass as delivered to its worker. It
// reports false when another delivery already stamped it.
func (s *Store) MarkReviewRunDelivered(ctx context.Context, id string, deliveredAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkReviewRunDelivered(ctx, gen.MarkReviewRunDeliveredParams{
		DeliveredAt: sql.NullTime{Time: deliveredAt, Valid: true},
		ID:          id,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListUndeliveredReviewRunsForPR returns a worker's completed, not yet
// delivered passes on one PR whose session wanted review feedback.
func (s *Store) ListUndeliveredReviewRunsForPR(ctx context.Context, id domain.SessionID, prURL string) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListUndeliveredReviewRunsForPR(ctx, gen.ListUndeliveredReviewRunsForPRParams{SessionID: id, PRURL: prURL})
	if err != nil {
		return nil, err
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewRunFromRow(r))
	}
	return out, nil
}

func reviewFindingFromRow(r gen.ReviewFinding) domain.ReviewFinding {
	f := domain.ReviewFinding{
		ID:                  r.ID,
		RunID:               r.RunID,
		SessionID:           r.SessionID,
		PRURL:               r.PRURL,
		TargetSHA:           r.TargetSha,
		Ordinal:             int(r.Ordinal),
		Path:                r.Path,
		Line:                int(r.Line),
		Body:                r.Body,
		Status:              r.Status,
		ResolutionNote:      r.ResolutionNote,
		ResolvedBySessionID: domain.SessionID(r.ResolvedBySessionID),
		SupersededByRunID:   r.SupersededByRunID,
		CreatedAt:           r.CreatedAt,
	}
	if r.ResolvedAt.Valid {
		t := r.ResolvedAt.Time
		f.ResolvedAt = &t
	}
	return f
}

// ListUndeliveredReviewRuns returns every worker's completed passes that have
// not reached their worker yet and whose session wanted review feedback.
func (s *Store) ListUndeliveredReviewRuns(ctx context.Context) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListUndeliveredReviewRuns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewRunFromRow(r))
	}
	return out, nil
}
