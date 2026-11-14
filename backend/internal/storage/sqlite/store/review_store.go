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

func (s *Store) UpsertReview(ctx context.Context, r domain.Review) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if r.InterfaceMode == "" {
		existing, err := s.qw.GetReviewBySessionAndHarness(ctx, gen.GetReviewBySessionAndHarnessParams{SessionID: r.SessionID, Harness: r.Harness})
		switch {
		case err == nil:
			r.InterfaceMode = domain.ReviewerInterfaceMode(existing.InterfaceMode)
		case errors.Is(err, sql.ErrNoRows):
			r.InterfaceMode = domain.ReviewerInterfaceTUI
		default:
			return fmt.Errorf("get review interface mode: %w", err)
		}
	}
	return s.qw.UpsertReview(ctx, gen.UpsertReviewParams{
		ID:                     r.ID,
		SessionID:              r.SessionID,
		ProjectID:              r.ProjectID,
		Harness:                r.Harness,
		PRURL:                  r.PRURL,
		ReviewerHandleID:       r.ReviewerHandleID,
		AgentSessionID:         r.AgentSessionID,
		ReviewerActivityState:  string(r.ReviewerActivityState),
		ReviewerLaunchID:       r.ReviewerLaunchID,
		InterfaceMode:          string(r.InterfaceMode),
		ProviderConversationID: r.ProviderConversationID,
		ControllerGeneration:   r.ControllerGeneration,
		ControllerError:        r.ControllerError,
		IsArchived:             r.IsArchived,
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              r.UpdatedAt,
	})
}

func (s *Store) SetReviewInterfaceMode(ctx context.Context, id string, mode domain.ReviewerInterfaceMode, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetReviewInterfaceMode(ctx, gen.SetReviewInterfaceModeParams{
		InterfaceMode: string(mode),
		Column2:       string(mode),
		Column3:       string(mode),
		Column4:       string(mode),
		UpdatedAt:     now,
		ID:            id,
	})
	return n > 0, err
}

func (s *Store) RestoreReviewLaunchState(ctx context.Context, review domain.Review, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.RestoreReviewLaunchState(ctx, gen.RestoreReviewLaunchStateParams{
		PRURL: review.PRURL, InterfaceMode: string(review.InterfaceMode), ReviewerHandleID: review.ReviewerHandleID,
		AgentSessionID: review.AgentSessionID, ReviewerActivityState: string(review.ReviewerActivityState),
		ReviewerLaunchID: review.ReviewerLaunchID, ProviderConversationID: review.ProviderConversationID,
		ControllerGeneration: review.ControllerGeneration, ControllerError: review.ControllerError, IsArchived: review.IsArchived,
		UpdatedAt: now, ID: review.ID,
	})
	return n > 0, err
}

func (s *Store) GetReviewBySession(ctx context.Context, id domain.SessionID) (domain.Review, bool, error) {
	row, err := s.qr.GetReviewBySession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Review{}, false, nil
	}
	if err != nil {
		return domain.Review{}, false, fmt.Errorf("get review by session %s: %w", id, err)
	}
	return reviewFromGetReviewBySessionRow(row), true, nil
}

func (s *Store) GetReviewBySessionAndHarness(ctx context.Context, id domain.SessionID, harness domain.ReviewerHarness) (domain.Review, bool, error) {
	row, err := s.qr.GetReviewBySessionAndHarness(ctx, gen.GetReviewBySessionAndHarnessParams{SessionID: id, Harness: harness})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Review{}, false, nil
	}
	if err != nil {
		return domain.Review{}, false, fmt.Errorf("get review by session %s harness %s: %w", id, harness, err)
	}
	return reviewFromGetReviewBySessionAndHarnessRow(row), true, nil
}

func (s *Store) GetReviewByID(ctx context.Context, id string) (domain.Review, bool, error) {
	row, err := s.qr.GetReviewByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Review{}, false, nil
	}
	if err != nil {
		return domain.Review{}, false, fmt.Errorf("get review %s: %w", id, err)
	}
	return reviewFromReview(row), true, nil
}

func (s *Store) ListReviewsBySession(ctx context.Context, id domain.SessionID) ([]domain.Review, error) {
	rows, err := s.qr.ListReviewsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list reviews for session %s: %w", id, err)
	}
	out := make([]domain.Review, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewFromListReviewsBySessionRow(row))
	}
	return out, nil
}

func (s *Store) ListLiveReviewerHandles(ctx context.Context) ([]domain.ReviewerHandle, error) {
	rows, err := s.qr.ListLiveReviewerHandles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live reviewer handles: %w", err)
	}
	out := make([]domain.ReviewerHandle, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ReviewerHandle{
			ReviewID: row.ID, SessionID: row.SessionID, Harness: row.Harness, HandleID: row.ReviewerHandleID,
		})
	}
	return out, nil
}

func (s *Store) ClaimReviewChatController(ctx context.Context, id, providerID, generation string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.ClaimReviewChatController(ctx, gen.ClaimReviewChatControllerParams{ProviderConversationID: providerID, ControllerGeneration: generation, UpdatedAt: now, ID: id})
	return n > 0, err
}

func (s *Store) RecordReviewChatControllerError(ctx context.Context, id, message string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.RecordReviewChatControllerError(ctx, gen.RecordReviewChatControllerErrorParams{ControllerError: message, UpdatedAt: now, ID: id})
	return n > 0, err
}

func (s *Store) ListRecoverableChatReviews(ctx context.Context) ([]domain.Review, error) {
	rows, err := s.qr.ListRecoverableChatReviews(ctx)
	if err != nil {
		return nil, fmt.Errorf("list recoverable reviewer chats: %w", err)
	}
	out := make([]domain.Review, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewFromListRecoverableChatReviewsRow(row))
	}
	return out, nil
}

func (s *Store) ClearReviewerHandle(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ClearReviewerHandle(ctx, id)
}

func (s *Store) ClearReviewerHandleByHarness(ctx context.Context, id domain.SessionID, harness domain.ReviewerHarness) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ClearReviewerHandleByHarness(ctx, gen.ClearReviewerHandleByHarnessParams{SessionID: id, Harness: harness})
}

func (s *Store) UpdateReviewAgentSessionID(ctx context.Context, id, agentSessionID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateReviewAgentSessionID(ctx, gen.UpdateReviewAgentSessionIDParams{
		AgentSessionID: agentSessionID,
		ID:             id,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) UpdateReviewActivity(ctx context.Context, id string, state domain.ActivityState, agentSessionID, launchID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateReviewActivity(ctx, gen.UpdateReviewActivityParams{
		Column1:               agentSessionID,
		AgentSessionID:        agentSessionID,
		Column3:               state,
		ReviewerActivityState: string(state),
		Column6:               launchID,
		ReviewerLaunchID:      launchID,
		Column8:               launchID,
		ID:                    id,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) InsertReviewRun(ctx context.Context, r domain.ReviewRun) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if r.TriggerSource == "" {
		r.TriggerSource = domain.ReviewTriggerManual
	}
	err := s.qw.InsertReviewRun(ctx, gen.InsertReviewRunParams{
		ID:               r.ID,
		ReviewID:         r.ReviewID,
		SessionID:        r.SessionID,
		BatchID:          r.BatchID,
		Harness:          r.Harness,
		TriggerSource:    r.TriggerSource,
		PRURL:            r.PRURL,
		TargetSha:        r.TargetSHA,
		Status:           r.Status,
		Verdict:          r.Verdict,
		Body:             r.Body,
		GithubReviewID:   r.GithubReviewID,
		CreatedAt:        r.CreatedAt,
		AutoInjectReview: r.AutoInjectReview,
	})
	if isSQLiteUnique(err) {
		return fmt.Errorf("insert review run for session %s pr %s sha %s: %w", r.SessionID, r.PRURL, r.TargetSHA, domain.ErrDuplicateReviewRun)
	}
	return err
}

func (s *Store) UpdateReviewRunResult(ctx context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string, autoInjectReview bool) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateReviewRunResult(ctx, gen.UpdateReviewRunResultParams{
		Status:           status,
		Verdict:          verdict,
		Body:             body,
		GithubReviewID:   githubReviewID,
		AutoInjectReview: autoInjectReview,
		ID:               id,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) SupersedeStaleRunningReviewRuns(ctx context.Context, sessionID domain.SessionID, prURL, targetSHA, body string) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.SupersedeStaleRunningReviewRuns(ctx, gen.SupersedeStaleRunningReviewRunsParams{
		Body:      body,
		SessionID: sessionID,
		PRURL:     prURL,
		TargetSha: targetSHA,
	})
}

func (s *Store) CancelRunningReviewRunsBySession(ctx context.Context, sessionID domain.SessionID, body string) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.CancelRunningReviewRunsBySession(ctx, gen.CancelRunningReviewRunsBySessionParams{
		Body:      body,
		SessionID: sessionID,
	})
}

func (s *Store) CancelRunningReviewRunsBySessionAndHarness(ctx context.Context, sessionID domain.SessionID, harness domain.ReviewerHarness, body string) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.CancelRunningReviewRunsBySessionAndHarness(ctx, gen.CancelRunningReviewRunsBySessionAndHarnessParams{
		Body:      body,
		SessionID: sessionID,
		Harness:   harness,
	})
}

func (s *Store) GetReviewRun(ctx context.Context, id string) (domain.ReviewRun, bool, error) {
	row, err := s.qr.GetReviewRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReviewRun{}, false, nil
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("get review run %s: %w", id, err)
	}
	return reviewRunFromRow(row), true, nil
}

func (s *Store) GetReviewRunBySessionPRAndSHA(ctx context.Context, id domain.SessionID, prURL, targetSHA string) (domain.ReviewRun, bool, error) {
	row, err := s.qr.GetReviewRunBySessionPRAndSHA(ctx, gen.GetReviewRunBySessionPRAndSHAParams{SessionID: id, PRURL: prURL, TargetSha: targetSHA})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReviewRun{}, false, nil
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("get review run for session %s pr %s sha %s: %w", id, prURL, targetSHA, err)
	}
	return reviewRunFromRow(row), true, nil
}

func (s *Store) GetReviewRunBySessionPRSHAAndHarness(ctx context.Context, id domain.SessionID, prURL, targetSHA string, harness domain.ReviewerHarness) (domain.ReviewRun, bool, error) {
	row, err := s.qr.GetReviewRunBySessionPRSHAAndHarness(ctx, gen.GetReviewRunBySessionPRSHAAndHarnessParams{SessionID: id, PRURL: prURL, TargetSha: targetSHA, Harness: harness})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReviewRun{}, false, nil
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("get review run for session %s pr %s sha %s harness %s: %w", id, prURL, targetSHA, harness, err)
	}
	return reviewRunFromRow(row), true, nil
}

func (s *Store) ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListReviewRunsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list review runs for session %s: %w", id, err)
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewRunFromRow(row))
	}
	return out, nil
}

func (s *Store) ListRunningReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListRunningReviewRunsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list running review runs for session %s: %w", id, err)
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewRunFromRow(row))
	}
	return out, nil
}

func (s *Store) ListReviewRunsByBatch(ctx context.Context, id domain.SessionID, batchID string) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListReviewRunsByBatch(ctx, gen.ListReviewRunsByBatchParams{SessionID: id, BatchID: batchID})
	if err != nil {
		return nil, fmt.Errorf("list review runs for session %s batch %s: %w", id, batchID, err)
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewRunFromRow(row))
	}
	return out, nil
}

func reviewFromGetReviewBySessionRow(r gen.GetReviewBySessionRow) domain.Review {
	return domain.Review{
		ID:                     r.ID,
		SessionID:              r.SessionID,
		ProjectID:              r.ProjectID,
		Harness:                r.Harness,
		PRURL:                  r.PRURL,
		ReviewerHandleID:       r.ReviewerHandleID,
		AgentSessionID:         r.AgentSessionID,
		ReviewerActivityState:  domain.ActivityState(r.ReviewerActivityState),
		ReviewerLaunchID:       r.ReviewerLaunchID,
		InterfaceMode:          domain.ReviewerInterfaceMode(r.InterfaceMode),
		ProviderConversationID: r.ProviderConversationID,
		ControllerGeneration:   r.ControllerGeneration,
		ControllerError:        r.ControllerError,
		IsArchived:             r.IsArchived,
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              r.UpdatedAt,
	}
}

func reviewFromGetReviewBySessionAndHarnessRow(r gen.GetReviewBySessionAndHarnessRow) domain.Review {
	return domain.Review{
		ID:                     r.ID,
		SessionID:              r.SessionID,
		ProjectID:              r.ProjectID,
		Harness:                r.Harness,
		PRURL:                  r.PRURL,
		ReviewerHandleID:       r.ReviewerHandleID,
		AgentSessionID:         r.AgentSessionID,
		ReviewerActivityState:  domain.ActivityState(r.ReviewerActivityState),
		ReviewerLaunchID:       r.ReviewerLaunchID,
		InterfaceMode:          domain.ReviewerInterfaceMode(r.InterfaceMode),
		ProviderConversationID: r.ProviderConversationID,
		ControllerGeneration:   r.ControllerGeneration,
		ControllerError:        r.ControllerError,
		IsArchived:             r.IsArchived,
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              r.UpdatedAt,
	}
}

func reviewFromListReviewsBySessionRow(r gen.ListReviewsBySessionRow) domain.Review {
	return domain.Review{
		ID:                     r.ID,
		SessionID:              r.SessionID,
		ProjectID:              r.ProjectID,
		Harness:                r.Harness,
		PRURL:                  r.PRURL,
		ReviewerHandleID:       r.ReviewerHandleID,
		AgentSessionID:         r.AgentSessionID,
		ReviewerActivityState:  domain.ActivityState(r.ReviewerActivityState),
		ReviewerLaunchID:       r.ReviewerLaunchID,
		InterfaceMode:          domain.ReviewerInterfaceMode(r.InterfaceMode),
		ProviderConversationID: r.ProviderConversationID,
		ControllerGeneration:   r.ControllerGeneration,
		ControllerError:        r.ControllerError,
		IsArchived:             r.IsArchived,
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              r.UpdatedAt,
	}
}

func reviewFromReview(r gen.GetReviewByIDRow) domain.Review {
	return domain.Review{
		ID:                     r.ID,
		SessionID:              r.SessionID,
		ProjectID:              r.ProjectID,
		Harness:                r.Harness,
		PRURL:                  r.PRURL,
		ReviewerHandleID:       r.ReviewerHandleID,
		AgentSessionID:         r.AgentSessionID,
		ReviewerActivityState:  domain.ActivityState(r.ReviewerActivityState),
		ReviewerLaunchID:       r.ReviewerLaunchID,
		InterfaceMode:          domain.ReviewerInterfaceMode(r.InterfaceMode),
		ProviderConversationID: r.ProviderConversationID,
		ControllerGeneration:   r.ControllerGeneration,
		ControllerError:        r.ControllerError,
		IsArchived:             r.IsArchived,
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              r.UpdatedAt,
	}
}

func reviewFromListRecoverableChatReviewsRow(r gen.ListRecoverableChatReviewsRow) domain.Review {
	return domain.Review{ID: r.ID, SessionID: r.SessionID, ProjectID: r.ProjectID, Harness: r.Harness, PRURL: r.PRURL, ReviewerHandleID: r.ReviewerHandleID, AgentSessionID: r.AgentSessionID, ReviewerActivityState: domain.ActivityState(r.ReviewerActivityState), ReviewerLaunchID: r.ReviewerLaunchID, InterfaceMode: domain.ReviewerInterfaceMode(r.InterfaceMode), ProviderConversationID: r.ProviderConversationID, ControllerGeneration: r.ControllerGeneration, ControllerError: r.ControllerError, IsArchived: r.IsArchived, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func reviewRunFromRow(r gen.ReviewRun) domain.ReviewRun {
	var deliveredAt *time.Time
	if r.DeliveredAt.Valid {
		t := r.DeliveredAt.Time
		deliveredAt = &t
	}
	return domain.ReviewRun{
		ID:               r.ID,
		ReviewID:         r.ReviewID,
		SessionID:        r.SessionID,
		BatchID:          r.BatchID,
		Harness:          r.Harness,
		TriggerSource:    r.TriggerSource,
		PRURL:            r.PRURL,
		TargetSHA:        r.TargetSha,
		Status:           r.Status,
		Verdict:          r.Verdict,
		Body:             r.Body,
		GithubReviewID:   r.GithubReviewID,
		CreatedAt:        r.CreatedAt,
		DeliveredAt:      deliveredAt,
		AutoInjectReview: r.AutoInjectReview,
	}
}

func (s *Store) ListCurrentHeadReviewRunsForSession(ctx context.Context, id domain.SessionID) ([]domain.CurrentHeadReviewRun, error) {
	rows, err := s.qr.ListCurrentHeadReviewRunsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list current-head review runs for %s: %w", id, err)
	}
	out := make([]domain.CurrentHeadReviewRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.CurrentHeadReviewRun{
			SessionID:        id,
			Harness:          r.Harness,
			PRURL:            r.PRURL,
			Status:           r.Status,
			Verdict:          r.Verdict,
			ID:               r.ID,
			CreatedAt:        r.CreatedAt,
			FailedAutoRuns:   int(r.FailedAutoRuns),
			AutoRetryBlocked: r.AutoRetryBlocked != 0,
		})
	}
	return out, nil
}

func (s *Store) ListCurrentHeadReviewRunsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.CurrentHeadReviewRun, error) {
	out := make(map[domain.SessionID][]domain.CurrentHeadReviewRun, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListCurrentHeadReviewRunsBySessions(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list current-head review runs for sessions: %w", err)
	}
	for _, r := range rows {
		out[r.SessionID] = append(out[r.SessionID], domain.CurrentHeadReviewRun{
			SessionID:        r.SessionID,
			Harness:          r.Harness,
			PRURL:            r.PRURL,
			Status:           r.Status,
			Verdict:          r.Verdict,
			ID:               r.ID,
			CreatedAt:        r.CreatedAt,
			FailedAutoRuns:   int(r.FailedAutoRuns),
			AutoRetryBlocked: r.AutoRetryBlocked != 0,
		})
	}
	for _, id := range ids {
		if out[id] == nil {
			out[id] = []domain.CurrentHeadReviewRun{}
		}
	}
	return out, nil
}

func (s *Store) ArchiveReviewsBySession(ctx context.Context, workerID domain.SessionID, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ArchiveReviewsBySession(ctx, gen.ArchiveReviewsBySessionParams{SessionID: workerID, UpdatedAt: now})
}
