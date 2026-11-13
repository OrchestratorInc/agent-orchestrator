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

// UpsertReview inserts the per-worker, per-harness review row, or reuses the
// existing one by refreshing its handle/native-session state.
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

// SetReviewInterfaceMode persists the surface before its controller starts.
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

// RestoreReviewLaunchState puts the previous reviewer back after a replacement
// launch fails, including the identifiers cleared while changing surfaces.
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

// GetReviewBySession returns the latest review row for a worker session,
// ok=false if none.
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

// GetReviewBySessionAndHarness returns the review row for one reviewer harness
// on a worker session, ok=false if none.
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

// GetReviewByID returns one reviewer session row by its stable review id.
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

// ListReviewsBySession returns every harness-specific review row for a worker
// session, newest first.
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

// ListLiveReviewerHandles returns every review row currently holding a live
// TUI reviewer pane, across every project and session (including one whose
// worker has since terminated) — the only lookup for a reviewer process that
// has no session row of its own.
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

// ClaimReviewChatController records ownership of a reviewer chat controller.
func (s *Store) ClaimReviewChatController(ctx context.Context, id, providerID, generation string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.ClaimReviewChatController(ctx, gen.ClaimReviewChatControllerParams{ProviderConversationID: providerID, ControllerGeneration: generation, UpdatedAt: now, ID: id})
	return n > 0, err
}

// RecordReviewChatControllerError records the most recent controller failure.
func (s *Store) RecordReviewChatControllerError(ctx context.Context, id, message string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.RecordReviewChatControllerError(ctx, gen.RecordReviewChatControllerErrorParams{ControllerError: message, UpdatedAt: now, ID: id})
	return n > 0, err
}

// ListRecoverableChatReviews returns durable reviewer chats eligible for recovery.
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

// ClearReviewerHandle removes all persisted terminal handles for a worker
// after a hard reviewer pane teardown.
func (s *Store) ClearReviewerHandle(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ClearReviewerHandle(ctx, id)
}

// ClearReviewerHandleByHarness removes only the runtime handle for one
// reviewer harness, preserving its native agent session id for later restore.
func (s *Store) ClearReviewerHandleByHarness(ctx context.Context, id domain.SessionID, harness domain.ReviewerHarness) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ClearReviewerHandleByHarness(ctx, gen.ClearReviewerHandleByHarnessParams{SessionID: id, Harness: harness})
}

// UpdateReviewAgentSessionID records engine-owned reviewer native session
// metadata without applying the reviewer-hook launch generation fence.
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

// UpdateReviewActivity records the native reviewer conversation id and/or
// reviewer activity reported by the reviewer harness hooks.
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

// InsertReviewRun records a new review pass. A unique-constraint hit on the
// (session_id, pr_url, target_sha) index (migration 0020) is surfaced as the sentinel
// domain.ErrDuplicateReviewRun so the engine can fall back to the existing run.
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

// TransitionReviewRun moves one run to `to` only while it is still in one of
// `from`, writing patch alongside. It reports false when another actor already
// moved the run, which callers treat as a no-op.
func (s *Store) TransitionReviewRun(ctx context.Context, id string, from []domain.ReviewRunStatus, to domain.ReviewRunStatus, patch domain.ReviewRunPatch) (bool, error) {
	fromJSON, err := reviewRunStatusesJSON(from)
	if err != nil {
		return false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	p := reviewRunPatchParams(patch)
	n, err := s.qw.TransitionReviewRun(ctx, gen.TransitionReviewRunParams{
		ToStatus:          to,
		Verdict:           p.verdict,
		Body:              p.body,
		GithubReviewID:    p.githubReviewID,
		AutoInjectReview:  p.autoInjectReview,
		FailureReason:     p.failureReason,
		CancelledBy:       p.cancelledBy,
		LaunchID:          p.launchID,
		LaunchingAt:       p.launchingAt,
		StartedAt:         p.startedAt,
		FirstActivityAt:   p.firstActivityAt,
		CancelRequestedAt: p.cancelRequestedAt,
		EndedAt:           p.endedAt,
		ID:                id,
		FromStatuses:      fromJSON,
	})
	if err != nil {
		return false, fmt.Errorf("transition review run %s to %s: %w", id, to, err)
	}
	return n > 0, nil
}

// TransitionReviewRunsBySession moves every run of a worker that is still in
// one of `from` to `to`. An empty harness matches every reviewer; otherwise
// only that harness's runs move. It returns the ids it moved.
func (s *Store) TransitionReviewRunsBySession(ctx context.Context, sessionID domain.SessionID, harness *domain.ReviewerHarness, from []domain.ReviewRunStatus, to domain.ReviewRunStatus, patch domain.ReviewRunPatch) ([]string, error) {
	fromJSON, err := reviewRunStatusesJSON(from)
	if err != nil {
		return nil, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	p := reviewRunPatchParams(patch)
	params := gen.TransitionReviewRunsBySessionParams{
		ToStatus:          to,
		Verdict:           p.verdict,
		Body:              p.body,
		GithubReviewID:    p.githubReviewID,
		AutoInjectReview:  p.autoInjectReview,
		FailureReason:     p.failureReason,
		CancelledBy:       p.cancelledBy,
		LaunchID:          p.launchID,
		LaunchingAt:       p.launchingAt,
		StartedAt:         p.startedAt,
		FirstActivityAt:   p.firstActivityAt,
		CancelRequestedAt: p.cancelRequestedAt,
		EndedAt:           p.endedAt,
		SessionID:         sessionID,
		AnyHarness:        harness == nil,
		FromStatuses:      fromJSON,
	}
	if harness != nil {
		params.Harness = *harness
	}
	ids, err := s.qw.TransitionReviewRunsBySession(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("transition review runs for session %s to %s: %w", sessionID, to, err)
	}
	return ids, nil
}

// TransitionReviewRunsForOlderSHA moves a worker's runs on prURL against any
// head other than targetSHA that are still in one of `from` to `to`.
func (s *Store) TransitionReviewRunsForOlderSHA(ctx context.Context, sessionID domain.SessionID, prURL, targetSHA string, from []domain.ReviewRunStatus, to domain.ReviewRunStatus, patch domain.ReviewRunPatch) ([]string, error) {
	fromJSON, err := reviewRunStatusesJSON(from)
	if err != nil {
		return nil, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	p := reviewRunPatchParams(patch)
	ids, err := s.qw.TransitionReviewRunsForOlderSHA(ctx, gen.TransitionReviewRunsForOlderSHAParams{
		ToStatus:          to,
		Verdict:           p.verdict,
		Body:              p.body,
		GithubReviewID:    p.githubReviewID,
		AutoInjectReview:  p.autoInjectReview,
		FailureReason:     p.failureReason,
		CancelledBy:       p.cancelledBy,
		LaunchID:          p.launchID,
		LaunchingAt:       p.launchingAt,
		StartedAt:         p.startedAt,
		FirstActivityAt:   p.firstActivityAt,
		CancelRequestedAt: p.cancelRequestedAt,
		EndedAt:           p.endedAt,
		SessionID:         sessionID,
		PRURL:             prURL,
		TargetSha:         targetSHA,
		FromStatuses:      fromJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("transition older review runs for session %s pr %s to %s: %w", sessionID, prURL, to, err)
	}
	return ids, nil
}

// MarkReviewRunsFirstActivity records the first activity signal from launchID
// on the review's delivered runs that are still waiting for one.
func (s *Store) MarkReviewRunsFirstActivity(ctx context.Context, reviewID, launchID string, at time.Time) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.MarkReviewRunsFirstActivity(ctx, gen.MarkReviewRunsFirstActivityParams{
		At:       sql.NullTime{Time: at, Valid: true},
		ReviewID: reviewID,
		LaunchID: launchID,
	})
}

type reviewRunPatchSQL struct {
	verdict           string
	body              sql.NullString
	githubReviewID    sql.NullString
	autoInjectReview  sql.NullBool
	failureReason     string
	cancelledBy       string
	launchID          string
	launchingAt       sql.NullTime
	startedAt         sql.NullTime
	firstActivityAt   sql.NullTime
	cancelRequestedAt sql.NullTime
	endedAt           sql.NullTime
}

func reviewRunPatchParams(p domain.ReviewRunPatch) reviewRunPatchSQL {
	out := reviewRunPatchSQL{
		verdict:       string(p.Verdict),
		failureReason: string(p.FailureReason),
		cancelledBy:   string(p.CancelledBy),
		launchID:      p.LaunchID,
	}
	if p.Body != nil {
		out.body = sql.NullString{String: *p.Body, Valid: true}
	}
	if p.GithubReviewID != nil {
		out.githubReviewID = sql.NullString{String: *p.GithubReviewID, Valid: true}
	}
	if p.AutoInjectReview != nil {
		out.autoInjectReview = sql.NullBool{Bool: *p.AutoInjectReview, Valid: true}
	}
	out.launchingAt = timePtrToNullTime(p.LaunchingAt)
	out.startedAt = timePtrToNullTime(p.StartedAt)
	out.firstActivityAt = timePtrToNullTime(p.FirstActivityAt)
	out.cancelRequestedAt = timePtrToNullTime(p.CancelRequestedAt)
	out.endedAt = timePtrToNullTime(p.EndedAt)
	return out
}

func reviewRunStatusesJSON(statuses []domain.ReviewRunStatus) (string, error) {
	if len(statuses) == 0 {
		return "", fmt.Errorf("review run transition needs at least one from-state")
	}
	encoded, err := json.Marshal(statuses)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// GetReviewRun returns one review pass by id.
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

// GetReviewRunBySessionPRAndSHA returns the most recent review pass for a
// worker session, PR, and commit, ok=false if none. It lets a repeat trigger for
// the same PR head short-circuit to the existing run without colliding with
// another PR that happens to share the same head commit.
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

// GetReviewRunBySessionPRSHAAndHarness returns the most recent review pass for
// a worker session, PR, commit, and reviewer harness, ok=false if none.
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

// ListReviewRunsBySession returns all review passes for a worker session, newest first.
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

// ListActiveReviewRunsBySession returns a worker's queued, launching, running
// and cancelling review passes, newest first.
func (s *Store) ListActiveReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListActiveReviewRunsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list active review runs for session %s: %w", id, err)
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewRunFromRow(row))
	}
	return out, nil
}

// ListActiveReviewRuns returns every active review pass across the daemon,
// oldest first, for the run supervisor and the startup sweep.
func (s *Store) ListActiveReviewRuns(ctx context.Context) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListActiveReviewRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active review runs: %w", err)
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewRunFromRow(row))
	}
	return out, nil
}

// ListReviewRunsByBatch returns all passes in one trigger-created batch, oldest first.
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
		FailureReason:     r.FailureReason,
		CancelledBy:       r.CancelledBy,
		LaunchID:          r.LaunchID,
		LaunchingAt:       nullTimeToTimePtr(r.LaunchingAt),
		StartedAt:         nullTimeToTimePtr(r.StartedAt),
		FirstActivityAt:   nullTimeToTimePtr(r.FirstActivityAt),
		CancelRequestedAt: nullTimeToTimePtr(r.CancelRequestedAt),
		EndedAt:           nullTimeToTimePtr(r.EndedAt),
		ID:                r.ID,
		ReviewID:          r.ReviewID,
		SessionID:         r.SessionID,
		BatchID:           r.BatchID,
		Harness:           r.Harness,
		TriggerSource:     r.TriggerSource,
		PRURL:             r.PRURL,
		TargetSHA:         r.TargetSha,
		Status:            r.Status,
		Verdict:           r.Verdict,
		Body:              r.Body,
		GithubReviewID:    r.GithubReviewID,
		CreatedAt:         r.CreatedAt,
		DeliveredAt:       deliveredAt,
		AutoInjectReview:  r.AutoInjectReview,
	}
}

// ListCurrentHeadReviewRunsForSession returns AO's review passes against the
// current head commit of each PR the session owns. Passes recorded for an
// earlier head are filtered out in SQL, so callers cannot let a stale run
// decide a derived column.
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

// ListCurrentHeadReviewRunsForSessions batches
// ListCurrentHeadReviewRunsForSession for session-list reads, returning every
// requested session id with an empty slice when the current head has no runs.
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

// ArchiveReviewsBySession retires surfaces after their controllers are settled.
func (s *Store) ArchiveReviewsBySession(ctx context.Context, workerID domain.SessionID, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ArchiveReviewsBySession(ctx, gen.ArchiveReviewsBySessionParams{SessionID: workerID, UpdatedAt: now})
}
