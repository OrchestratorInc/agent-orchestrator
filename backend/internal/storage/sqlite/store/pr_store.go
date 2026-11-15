package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var (
	_ ports.PRWriter  = (*Store)(nil)
	_ ports.SCMWriter = (*Store)(nil)
	_ ports.PRClaimer = (*Store)(nil)
)

func (s *Store) WritePR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, comments []domain.PullRequestComment) error {
	return s.writePR(ctx, pr, checks, nil, nil, comments, ports.ReviewWritePreserve, true)
}

func (s *Store) WriteSCMObservation(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode) error {
	return s.writePR(ctx, pr, checks, reviews, threads, comments, reviewMode, false)
}

func (s *Store) MarkPRMerged(ctx context.Context, prURL, mergeCommitSHA string, mergedAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qr.MarkPRMerged(ctx, gen.MarkPRMergedParams{
		MergeCommitSha: mergeCommitSHA,
		StateChangedAt: sql.NullTime{Time: mergedAt, Valid: true},
		UpdatedAt:      mergedAt,
		ObservedAt:     sql.NullTime{Time: mergedAt, Valid: true},
		URL:            prURL,
	})
	if err != nil {
		return fmt.Errorf("mark pr merged %s: %w", prURL, err)
	}
	if rows != 1 {
		return fmt.Errorf("mark pr merged %s: updated %d rows", prURL, rows)
	}
	if err := s.qw.RecordWorkerPRUsage(ctx, gen.RecordWorkerPRUsageParams{PRURL: prURL, ExportAllowed: s.usageAllowed()}); err != nil {
		slog.Warn("record merged pr usage failed", "error", err)
	}
	return nil
}

func (s *Store) MarkPRState(ctx context.Context, prURL string, state domain.PRState, changedAt time.Time) error {
	switch state {
	case domain.PRStateOpen, domain.PRStateDraft, domain.PRStateClosed:
	default:
		return fmt.Errorf("mark pr state %s: unsupported state %q", prURL, state)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	draft := state == domain.PRStateDraft
	if state == domain.PRStateClosed {
		current, err := s.qw.GetPR(ctx, prURL)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("mark pr state %s: updated 0 rows", prURL)
		}
		if err != nil {
			return fmt.Errorf("mark pr state %s: %w", prURL, err)
		}
		draft = current.IsDraft != 0
	}
	rows, err := s.qw.MarkPRState(ctx, gen.MarkPRStateParams{
		PRState:        state,
		IsDraft:        boolInt(draft),
		IsClosed:       boolInt(state == domain.PRStateClosed),
		StateChangedAt: sql.NullTime{Time: changedAt, Valid: true},
		UpdatedAt:      changedAt,
		URL:            prURL,
	})
	if err != nil {
		return fmt.Errorf("mark pr state %s: %w", prURL, err)
	}
	if rows != 1 {
		return fmt.Errorf("mark pr state %s: updated %d rows", prURL, rows)
	}
	return nil
}

func (s *Store) UpsertPRReviewComment(ctx context.Context, prURL string, comment domain.PullRequestComment) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertPRComment(ctx, genCommentParams(prURL, comment)); err != nil {
		return fmt.Errorf("upsert pr review comment %s/%s: %w", prURL, comment.ID, err)
	}
	return nil
}

func (s *Store) ClaimPR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode, allowActiveTakeover bool) (ports.ClaimOutcome, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	pr = normalizedPRIdentity(pr)
	var outcome ports.ClaimOutcome
	err := s.inTx(ctx, "claim pr", func(q *gen.Queries) error {
		var err error
		pr, err = resolveWeakPRAlias(ctx, q, pr)
		if err != nil {
			return err
		}
		candidates, err := findPRIdentityCandidates(ctx, q, pr)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			owner, err := q.GetPRClaimAndOwner(ctx, candidate.URL)
			if err != nil {
				return err
			}
			if owner.SessionID == pr.SessionID {
				continue
			}
			if outcome.PreviousOwner != "" && outcome.PreviousOwner != owner.SessionID {
				return fmt.Errorf("pr identity resolves to multiple sessions: %s and %s", outcome.PreviousOwner, owner.SessionID)
			}
			outcome.PreviousOwner = owner.SessionID
			outcome.OwnerTerminated = owner.IsTerminated
			if owner.SessionID != pr.SessionID && !owner.IsTerminated && !allowActiveTakeover {
				return ports.PRClaimedByActiveSessionError{Owner: owner.SessionID}
			}
		}
		if err := q.ClaimPRForSession(ctx, gen.ClaimPRForSessionParams{
			URL: pr.URL, SessionID: pr.SessionID, Number: int64(pr.Number), PRState: prState(pr),
			ReviewDecision: reviewOrDefault(pr.Review), CIState: ciOrDefault(pr.CI), Mergeability: mergeabilityOrDefault(pr.Mergeability), UpdatedAt: pr.UpdatedAt,
			StateChangedAt: nullTime(initialPRStateChangedAt(pr)), ID: pr.SessionID,
		}); err != nil {
			return err
		}
		return writePRRows(ctx, q, pr, checks, reviews, threads, comments, reviewMode, false, false, s.usageAllowed())
	})
	return outcome, err
}

func (s *Store) writePR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode, replaceLegacyComments bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "write pr observation", func(q *gen.Queries) error {
		return writePRRows(ctx, q, pr, checks, reviews, threads, comments, reviewMode, replaceLegacyComments, true, s.usageAllowed())
	})
}

func writePRRows(ctx context.Context, q *gen.Queries, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode, replaceLegacyComments, rejectReassignment, exportAllowed bool) error {
	pr = normalizedPRIdentity(pr)
	var err error
	pr, err = resolveWeakPRAlias(ctx, q, pr)
	if err != nil {
		return err
	}
	if replaceLegacyComments {
		if rejectReassignment {
			existing, err := q.GetPR(ctx, pr.URL)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && existing.SessionID != pr.SessionID {
				return fmt.Errorf("pr %s already belongs to session %s", pr.URL, existing.SessionID)
			}
		}
		if err := q.UpsertLegacyPR(ctx, genLegacyPRParams(pr)); err != nil {
			return err
		}
	} else {
		if err := upsertPRWithIdentity(ctx, q, pr, rejectReassignment); err != nil {
			return err
		}
	}
	if err := writePRDetails(ctx, q, pr.URL, pr.UpdatedAtProvider, pr.Details); err != nil {
		return err
	}
	if err := q.UpgradeWorkerPRUsageIdentity(ctx, pr.URL); err != nil {
		slog.Warn("upgrade worker pr usage identity failed", "error", err)
	}
	if err := q.RecordWorkerPRUsage(ctx, gen.RecordWorkerPRUsageParams{PRURL: pr.URL, ExportAllowed: exportAllowed}); err != nil {
		slog.Warn("record worker pr usage failed", "error", err)
	}
	for _, c := range checks {
		if err := q.UpsertPRCheck(ctx, genCheckParams(pr.URL, c)); err != nil {
			return err
		}
	}
	if replaceLegacyComments {
		if err := q.DeleteLegacyPRComments(ctx, pr.URL); err != nil {
			return err
		}
	}
	if reviewMode == ports.ReviewWriteReplace || reviewMode == ports.ReviewWriteMerge {
		for _, review := range reviews {
			if err := q.UpsertPRReview(ctx, genReviewParams(pr.URL, review)); err != nil {
				return fmt.Errorf("review %q: %w", review.ID, err)
			}
		}
	}
	if reviewMode == ports.ReviewWriteReplace {
		observed := make(map[string]struct{}, len(reviews))
		for _, review := range reviews {
			observed[review.ID] = struct{}{}
		}
		existing, err := q.ListPRReviews(ctx, pr.URL)
		if err != nil {
			return fmt.Errorf("list reviews for prune %s: %w", pr.URL, err)
		}
		for _, row := range existing {
			if _, ok := observed[row.ReviewID]; ok {
				continue
			}
			if err := q.DeletePRReview(ctx, gen.DeletePRReviewParams{PRURL: pr.URL, ReviewID: row.ReviewID}); err != nil {
				return fmt.Errorf("prune review %q: %w", row.ReviewID, err)
			}
		}
	}
	if reviewMode == ports.ReviewWriteReplace || reviewMode == ports.ReviewWriteMerge {
		for _, th := range threads {
			if err := q.UpsertPRReviewThread(ctx, genReviewThreadParams(pr.URL, th)); err != nil {
				return fmt.Errorf("review thread %q: %w", th.ThreadID, err)
			}
		}
	}
	if reviewMode == ports.ReviewWriteReplace {
		observed := make(map[string]struct{}, len(threads))
		for _, th := range threads {
			observed[th.ThreadID] = struct{}{}
		}
		existing, err := q.ListPRReviewThreads(ctx, pr.URL)
		if err != nil {
			return fmt.Errorf("list review threads for prune %s: %w", pr.URL, err)
		}
		for _, row := range existing {
			if _, ok := observed[row.ThreadID]; ok {
				continue
			}
			if err := q.DeletePRReviewThread(ctx, gen.DeletePRReviewThreadParams{PRURL: pr.URL, ThreadID: row.ThreadID}); err != nil {
				return fmt.Errorf("prune review thread %q: %w", row.ThreadID, err)
			}
		}
	}
	if reviewMode == ports.ReviewWriteReplace || reviewMode == ports.ReviewWriteMerge {
		for _, c := range comments {
			if err := q.UpsertPRComment(ctx, genCommentParams(pr.URL, c)); err != nil {
				return fmt.Errorf("comment %q: %w", c.ID, err)
			}
		}
		observed := make(map[string]struct{}, len(comments))
		for _, c := range comments {
			observed[c.ID] = struct{}{}
		}
		touchedThreads := map[string]struct{}{}
		if reviewMode == ports.ReviewWriteMerge {
			for _, threadID := range reviewThreadIDs(threads, comments) {
				touchedThreads[threadID] = struct{}{}
			}
		}
		existing, err := q.ListPRComments(ctx, pr.URL)
		if err != nil {
			return fmt.Errorf("list comments for prune %s: %w", pr.URL, err)
		}
		for _, row := range existing {
			if _, ok := observed[row.CommentID]; ok {
				continue
			}
			if reviewMode == ports.ReviewWriteMerge {
				if _, touched := touchedThreads[row.ThreadID]; !touched {
					continue
				}
			}
			if err := q.DeletePRComment(ctx, gen.DeletePRCommentParams{PRURL: pr.URL, CommentID: row.CommentID}); err != nil {
				return fmt.Errorf("prune comment %q: %w", row.CommentID, err)
			}
		}
	} else if replaceLegacyComments {
		for _, c := range comments {
			if err := q.InsertLegacyPRComment(ctx, genLegacyCommentParams(pr.URL, c)); err != nil {
				return fmt.Errorf("legacy comment %q: %w", c.ID, err)
			}
		}
	}
	return nil
}

func normalizedPRIdentity(pr domain.PullRequest) domain.PullRequest {
	pr.Provider = strings.ToLower(strings.TrimSpace(pr.Provider))
	pr.Host = strings.ToLower(strings.TrimSpace(pr.Host))
	pr.ProviderID = strings.TrimSpace(pr.ProviderID)
	pr.URLAlias = strings.TrimSpace(pr.URLAlias)
	return pr
}

func resolveWeakPRAlias(ctx context.Context, q *gen.Queries, pr domain.PullRequest) (domain.PullRequest, error) {
	if pr.ProviderID != "" || pr.URLAlias != "" {
		return pr, nil
	}
	existing, err := q.GetPRByURLOrAlias(ctx, pr.URL)
	if errors.Is(err, sql.ErrNoRows) {
		return pr, nil
	}
	if err != nil {
		return domain.PullRequest{}, err
	}
	if existing.URL == pr.URL {
		return pr, nil
	}
	pr.URLAlias = pr.URL
	pr.URL = existing.URL
	if pr.Provider == "" {
		pr.Provider = existing.Provider
	}
	if pr.Host == "" {
		pr.Host = existing.Host
	}
	if pr.Repo == "" {
		pr.Repo = existing.Repo
	}
	pr.ProviderID = existing.ProviderID
	return pr, nil
}

func upsertPRWithIdentity(ctx context.Context, q *gen.Queries, pr domain.PullRequest, rejectReassignment bool) error {
	if pr.ProviderID == "" && pr.URLAlias == "" {
		return q.UpsertPR(ctx, genPRParams(pr))
	}
	candidates, err := findPRIdentityCandidates(ctx, q, pr)
	if err != nil {
		return err
	}
	for _, existing := range candidates {
		if rejectReassignment && existing.SessionID != pr.SessionID {
			return fmt.Errorf("pr %s already belongs to session %s", existing.URL, existing.SessionID)
		}
		if existing.URL != pr.URL && existing.ProviderID != "" {
			if err := q.ClearPRProviderIdentity(ctx, existing.URL); err != nil {
				return err
			}
		}
	}
	if err := q.UpsertPR(ctx, genPRParams(pr)); err != nil {
		return err
	}
	if err := q.DeletePRAlias(ctx, pr.URL); err != nil {
		return err
	}
	for previousURL := range candidates {
		if previousURL == pr.URL {
			continue
		}
		if err := movePRAliasRows(ctx, q, previousURL, pr.URL); err != nil {
			return err
		}
		if err := q.RepointPRAliases(ctx, gen.RepointPRAliasesParams{CanonicalURL: pr.URL, PreviousURL: previousURL}); err != nil {
			return err
		}
		if err := q.DeletePRByURL(ctx, previousURL); err != nil {
			return err
		}
		if err := q.UpsertPRAlias(ctx, gen.UpsertPRAliasParams{AliasURL: previousURL, CanonicalURL: pr.URL}); err != nil {
			return err
		}
	}
	if pr.URLAlias != "" && pr.URLAlias != pr.URL {
		if err := q.UpsertPRAlias(ctx, gen.UpsertPRAliasParams{AliasURL: pr.URLAlias, CanonicalURL: pr.URL}); err != nil {
			return err
		}
	}
	return nil
}

func findPRIdentityCandidates(ctx context.Context, q *gen.Queries, pr domain.PullRequest) (map[string]gen.PR, error) {
	candidates := map[string]gen.PR{}
	addCandidate := func(row gen.PR, err error) error {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		candidates[row.URL] = row
		return nil
	}
	if err := addCandidate(q.GetPR(ctx, pr.URL)); err != nil {
		return nil, err
	}
	if pr.URLAlias != "" && pr.URLAlias != pr.URL {
		if err := addCandidate(q.GetPRByURLOrAlias(ctx, pr.URLAlias)); err != nil {
			return nil, err
		}
	}
	if pr.ProviderID != "" && pr.Provider != "" && pr.Host != "" {
		if err := addCandidate(q.GetPRByProviderIdentity(ctx, gen.GetPRByProviderIdentityParams{
			Provider: pr.Provider, Host: pr.Host, ProviderID: pr.ProviderID,
		})); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func movePRAliasRows(ctx context.Context, q *gen.Queries, previousURL, canonicalURL string) error {
	if err := q.MovePRAliasChecks(ctx, gen.MovePRAliasChecksParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasChecks(ctx, previousURL); err != nil {
		return err
	}
	if err := q.MovePRAliasReviews(ctx, gen.MovePRAliasReviewsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasReviews(ctx, previousURL); err != nil {
		return err
	}
	if err := q.MovePRAliasReviewThreads(ctx, gen.MovePRAliasReviewThreadsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasReviewThreads(ctx, previousURL); err != nil {
		return err
	}
	if err := q.MovePRAliasComments(ctx, gen.MovePRAliasCommentsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasComments(ctx, previousURL); err != nil {
		return err
	}
	if err := q.ResolveConflictingPRAliasNotifications(ctx, gen.ResolveConflictingPRAliasNotificationsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.MovePRAliasNotifications(ctx, gen.MovePRAliasNotificationsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.MovePRAliasReviewState(ctx, gen.MovePRAliasReviewStateParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.MovePRAliasReviewRuns(ctx, gen.MovePRAliasReviewRunsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	return q.DeletePRAliasReviewRuns(ctx, previousURL)
}

func reviewThreadIDs(threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(threads))
	for _, th := range threads {
		if th.ThreadID == "" || seen[th.ThreadID] {
			continue
		}
		seen[th.ThreadID] = true
		out = append(out, th.ThreadID)
	}
	for _, c := range comments {
		if c.ThreadID == "" || seen[c.ThreadID] {
			continue
		}
		seen[c.ThreadID] = true
		out = append(out, c.ThreadID)
	}
	return out
}

func (s *Store) GetPRLastNudgeSignature(ctx context.Context, url string) (string, error) {
	sig, err := s.qr.GetPRLastNudgeSignature(ctx, url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get pr nudge signature %s: %w", url, err)
	}
	return sig, nil
}

func (s *Store) UpdatePRLastNudgeSignature(ctx context.Context, url, payload string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpdatePRLastNudgeSignature(ctx, gen.UpdatePRLastNudgeSignatureParams{LastNudgeSignature: payload, URL: url}); err != nil {
		return fmt.Errorf("update pr nudge signature %s: %w", url, err)
	}
	return nil
}

func (s *Store) RecordRepositoryRename(ctx context.Context, r domain.RepositoryRename) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertRepositoryRename(ctx, gen.UpsertRepositoryRenameParams(r)); err != nil {
		return fmt.Errorf("record repository rename %s/%s: %w", r.Host, r.OldName, err)
	}
	return nil
}

func (s *Store) ListRepositoryRenames(ctx context.Context) ([]domain.RepositoryRename, error) {
	rows, err := s.qr.ListRepositoryRenames(ctx)
	if err != nil {
		return nil, fmt.Errorf("list repository renames: %w", err)
	}
	out := make([]domain.RepositoryRename, len(rows))
	for i, row := range rows {
		out[i] = domain.RepositoryRename(row)
	}
	return out, nil
}

func (s *Store) GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error) {
	p, err := s.qr.GetPRByURLOrAlias(ctx, url)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PullRequest{}, false, nil
	}
	if err != nil {
		return domain.PullRequest{}, false, fmt.Errorf("get pr %s: %w", url, err)
	}
	return prRowFromGen(p), true, nil
}

func (s *Store) GetPRByNumber(ctx context.Context, number int) (domain.PullRequest, bool, error) {
	if number <= 0 {
		return domain.PullRequest{}, false, nil
	}
	p, err := s.qr.GetPRByNumber(ctx, int64(number))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PullRequest{}, false, nil
	}
	if err != nil {
		return domain.PullRequest{}, false, fmt.Errorf("get pr by number %d: %w", number, err)
	}
	return prRowFromGen(p), true, nil
}

func (s *Store) CountActivePRsByNumber(ctx context.Context, number int) (int, error) {
	if number <= 0 {
		return 0, nil
	}
	count, err := s.qr.CountActivePRsByNumber(ctx, int64(number))
	if err != nil {
		return 0, fmt.Errorf("count active prs by number %d: %w", number, err)
	}
	return int(count), nil
}

func (s *Store) ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error) {
	rows, err := s.qr.ListPRsBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prs for %s: %w", sessionID, err)
	}
	out := make([]domain.PullRequest, 0, len(rows))
	for _, p := range rows {
		out = append(out, prRowFromGen(p))
	}
	return out, nil
}

func (s *Store) ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	rows, err := s.qr.ListChecksByPR(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list checks %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestCheck, 0, len(rows))
	for _, c := range rows {
		out = append(out, checkRowFromGen(c))
	}
	return out, nil
}

func (s *Store) ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error) {
	rows, err := s.qr.ListPRComments(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr comments %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestComment, 0, len(rows))
	for _, c := range rows {
		out = append(out, commentFromGen(c))
	}
	return out, nil
}

func (s *Store) MarkPRReviewThreadResolved(ctx context.Context, prURL, threadID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "mark pr review thread resolved", func(q *gen.Queries) error {
		if _, err := q.MarkPRReviewThreadResolved(ctx, gen.MarkPRReviewThreadResolvedParams{PRURL: prURL, ThreadID: threadID}); err != nil {
			return fmt.Errorf("mark pr review thread resolved %s/%s: %w", prURL, threadID, err)
		}
		if _, err := q.MarkPRCommentsResolvedForThread(ctx, gen.MarkPRCommentsResolvedForThreadParams{PRURL: prURL, ThreadID: threadID}); err != nil {
			return fmt.Errorf("mark pr review comments resolved %s/%s: %w", prURL, threadID, err)
		}
		return nil
	})
}

func (s *Store) ListPRReviewThreads(ctx context.Context, prURL string) ([]domain.PullRequestReviewThread, error) {
	rows, err := s.qr.ListPRReviewThreads(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr review threads %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestReviewThread, 0, len(rows))
	for _, th := range rows {
		out = append(out, reviewThreadFromGen(th))
	}
	return out, nil
}

func (s *Store) ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error) {
	rows, err := s.qr.ListPRReviews(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr reviews %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestReview, 0, len(rows))
	for _, review := range rows {
		out = append(out, reviewFromGen(review))
	}
	return out, nil
}

func prState(r domain.PullRequest) domain.PRState {
	switch {
	case r.Merged:
		return domain.PRStateMerged
	case r.Closed:
		return domain.PRStateClosed
	case r.Draft:
		return domain.PRStateDraft
	default:
		return domain.PRStateOpen
	}
}

func genPRParams(r domain.PullRequest) gen.UpsertPRParams {
	return gen.UpsertPRParams{
		URL:                      r.URL,
		SessionID:                r.SessionID,
		Number:                   int64(r.Number),
		PRState:                  prState(r),
		ReviewDecision:           reviewOrDefault(r.Review),
		CIState:                  ciOrDefault(r.CI),
		Mergeability:             mergeabilityOrDefault(r.Mergeability),
		UpdatedAt:                r.UpdatedAt,
		StateChangedAt:           nullTime(initialPRStateChangedAt(r)),
		Provider:                 r.Provider,
		Host:                     r.Host,
		Repo:                     r.Repo,
		ProviderID:               r.ProviderID,
		SourceBranch:             r.SourceBranch,
		TargetBranch:             r.TargetBranch,
		HeadSha:                  r.HeadSHA,
		Title:                    r.Title,
		Additions:                int64(r.Additions),
		Deletions:                int64(r.Deletions),
		ChangedFiles:             int64(r.ChangedFiles),
		Author:                   r.Author,
		AuthorAvatarURL:          r.AuthorAvatarURL,
		BaseSha:                  r.BaseSHA,
		MergeCommitSha:           r.MergeCommitSHA,
		IsDraft:                  boolInt(r.Draft),
		IsMerged:                 boolInt(r.Merged),
		IsClosed:                 boolInt(r.Closed),
		ProviderState:            r.ProviderState,
		ProviderMergeable:        r.ProviderMergeable,
		ProviderMergeStateStatus: r.ProviderMergeStateStatus,
		HtmlURL:                  r.HTMLURL,
		CreatedAtProvider:        nullTime(r.CreatedAtProvider),
		UpdatedAtProvider:        nullTime(r.UpdatedAtProvider),
		MergedAtProvider:         nullTime(r.MergedAtProvider),
		ClosedAtProvider:         nullTime(r.ClosedAtProvider),
		MetadataHash:             r.MetadataHash,
		CIHash:                   r.CIHash,
		ReviewHash:               r.ReviewHash,
		ObservedAt:               nullTime(r.ObservedAt),
		CIObservedAt:             nullTime(r.CIObservedAt),
		ReviewObservedAt:         nullTime(r.ReviewObservedAt),
		ReviewPartial:            r.ReviewPartial,
		ID:                       r.SessionID,
	}
}

func genLegacyPRParams(r domain.PullRequest) gen.UpsertLegacyPRParams {
	return gen.UpsertLegacyPRParams{
		URL:              r.URL,
		SessionID:        r.SessionID,
		Number:           int64(r.Number),
		PRState:          prState(r),
		ReviewDecision:   reviewOrDefault(r.Review),
		CIState:          ciOrDefault(r.CI),
		Mergeability:     mergeabilityOrDefault(r.Mergeability),
		UpdatedAt:        r.UpdatedAt,
		StateChangedAt:   nullTime(initialPRStateChangedAt(r)),
		IsDraft:          boolInt(r.Draft),
		IsMerged:         boolInt(r.Merged),
		IsClosed:         boolInt(r.Closed),
		ReviewObservedAt: nullTime(r.ReviewObservedAt),
		ReviewPartial:    r.ReviewPartial,
		ID:               r.SessionID,
	}
}

func reviewOrDefault(v domain.ReviewDecision) domain.ReviewDecision {
	if v == "" {
		return domain.ReviewNone
	}
	return v
}

func ciOrDefault(v domain.CIState) domain.CIState {
	if v == "" {
		return domain.CIUnknown
	}
	return v
}

func mergeabilityOrDefault(v domain.Mergeability) domain.Mergeability {
	if v == "" {
		return domain.MergeUnknown
	}
	return v
}

func initialPRStateChangedAt(r domain.PullRequest) time.Time {
	if !r.StateChangedAt.IsZero() {
		return r.StateChangedAt
	}
	switch prState(r) {
	case domain.PRStateMerged:
		return r.MergedAtProvider
	case domain.PRStateClosed:
		return r.ClosedAtProvider
	case domain.PRStateDraft, domain.PRStateOpen:
		return r.CreatedAtProvider
	default:
		return time.Time{}
	}
}

func prRowFromGen(p gen.PR) domain.PullRequest {
	return domain.PullRequest{
		URL:                      p.URL,
		SessionID:                p.SessionID,
		Number:                   int(p.Number),
		Draft:                    p.PRState == domain.PRStateDraft || p.IsDraft != 0,
		Merged:                   p.PRState == domain.PRStateMerged || p.IsMerged != 0,
		Closed:                   p.PRState == domain.PRStateClosed || p.IsClosed != 0,
		CI:                       p.CIState,
		Review:                   p.ReviewDecision,
		Mergeability:             p.Mergeability,
		UpdatedAt:                p.UpdatedAt,
		StateChangedAt:           timeFromNull(p.StateChangedAt),
		Provider:                 p.Provider,
		Host:                     p.Host,
		Repo:                     p.Repo,
		ProviderID:               p.ProviderID,
		SourceBranch:             p.SourceBranch,
		TargetBranch:             p.TargetBranch,
		HeadSHA:                  p.HeadSha,
		Title:                    p.Title,
		Additions:                int(p.Additions),
		Deletions:                int(p.Deletions),
		ChangedFiles:             int(p.ChangedFiles),
		Author:                   p.Author,
		AuthorAvatarURL:          p.AuthorAvatarURL,
		BaseSHA:                  p.BaseSha,
		MergeCommitSHA:           p.MergeCommitSha,
		ProviderState:            p.ProviderState,
		ProviderMergeable:        p.ProviderMergeable,
		ProviderMergeStateStatus: p.ProviderMergeStateStatus,
		HTMLURL:                  p.HtmlURL,
		CreatedAtProvider:        timeFromNull(p.CreatedAtProvider),
		UpdatedAtProvider:        timeFromNull(p.UpdatedAtProvider),
		MergedAtProvider:         timeFromNull(p.MergedAtProvider),
		ClosedAtProvider:         timeFromNull(p.ClosedAtProvider),
		MetadataHash:             p.MetadataHash,
		CIHash:                   p.CIHash,
		ReviewHash:               p.ReviewHash,
		ObservedAt:               timeFromNull(p.ObservedAt),
		CIObservedAt:             timeFromNull(p.CIObservedAt),
		ReviewObservedAt:         timeFromNull(p.ReviewObservedAt),
		ReviewPartial:            p.ReviewPartial,
		AutoInjectCI:             p.AutoInjectCI,
	}
}

func genCheckParams(prURL string, c domain.PullRequestCheck) gen.UpsertPRCheckParams {
	status := c.Status
	if status == "" {
		status = domain.PRCheckUnknown
	}
	return gen.UpsertPRCheckParams{
		PRURL: prURL, Name: c.Name, CommitHash: c.CommitHash,
		Status: status, URL: c.URL, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
		Conclusion: c.Conclusion, Details: c.Details,
	}
}

func checkRowFromGen(c gen.PRCheck) domain.PullRequestCheck {
	return domain.PullRequestCheck{
		Name: c.Name, CommitHash: c.CommitHash, Status: c.Status,
		Conclusion: c.Conclusion, URL: c.URL, Details: c.Details,
		LogTail: c.LogTail, CreatedAt: c.CreatedAt,
	}
}

func genCommentParams(prURL string, c domain.PullRequestComment) gen.UpsertPRCommentParams {
	return gen.UpsertPRCommentParams{
		PRURL: prURL, CommentID: c.ID, Author: c.Author, File: c.File,
		Line: int64(c.Line), Body: c.Body, Resolved: c.Resolved, CreatedAt: c.CreatedAt,
		ThreadID: c.ThreadID, ReviewID: c.ReviewID, URL: c.URL, IsBot: boolInt(c.IsBot), AutoInjectReview: c.AutoInjectReview,
	}
}

func genLegacyCommentParams(prURL string, c domain.PullRequestComment) gen.InsertLegacyPRCommentParams {
	return gen.InsertLegacyPRCommentParams{
		PRURL: prURL, CommentID: c.ID, Author: c.Author, File: c.File,
		Line: int64(c.Line), Body: c.Body, Resolved: c.Resolved, CreatedAt: c.CreatedAt,
		ThreadID: "", ReviewID: c.ReviewID, URL: "", IsBot: 0,
	}
}

func commentFromGen(c gen.PRComment) domain.PullRequestComment {
	return domain.PullRequestComment{
		ThreadID: c.ThreadID, ReviewID: c.ReviewID, ID: c.CommentID, Author: c.Author,
		File: c.File, Line: int(c.Line), Body: c.Body, URL: c.URL,
		Resolved: c.Resolved, IsBot: c.IsBot != 0, CreatedAt: c.CreatedAt,
		AutoInjectReview: c.AutoInjectReview,
	}
}

func genReviewThreadParams(prURL string, th domain.PullRequestReviewThread) gen.UpsertPRReviewThreadParams {
	return gen.UpsertPRReviewThreadParams{
		PRURL: prURL, ThreadID: th.ThreadID, Path: th.Path,
		Line: int64(th.Line), Resolved: boolInt(th.Resolved),
		IsBot: boolInt(th.IsBot), SemanticHash: th.SemanticHash,
		UpdatedAt: th.UpdatedAt,
	}
}

func reviewThreadFromGen(th gen.PRReviewThread) domain.PullRequestReviewThread {
	return domain.PullRequestReviewThread{
		ThreadID: th.ThreadID, Path: th.Path, Line: int(th.Line),
		Resolved: th.Resolved != 0, IsBot: th.IsBot != 0,
		SemanticHash: th.SemanticHash, UpdatedAt: th.UpdatedAt,
	}
}

func genReviewParams(prURL string, review domain.PullRequestReview) gen.UpsertPRReviewParams {
	id := review.ID
	if id == "" {
		id = review.URL
	}
	return gen.UpsertPRReviewParams{
		PRURL:            prURL,
		ReviewID:         id,
		Author:           review.Author,
		State:            string(reviewOrDefault(review.State)),
		URL:              review.URL,
		IsBot:            boolInt(review.IsBot),
		SubmittedAt:      review.SubmittedAt,
		Body:             review.Body,
		TargetSha:        review.TargetSHA,
		AutoInjectReview: review.AutoInjectReview,
	}
}

func reviewFromGen(review gen.PRReview) domain.PullRequestReview {
	return domain.PullRequestReview{
		ID:               review.ReviewID,
		Author:           review.Author,
		State:            domain.ReviewDecision(review.State),
		URL:              review.URL,
		Body:             review.Body,
		IsBot:            review.IsBot != 0,
		TargetSHA:        review.TargetSha,
		SubmittedAt:      review.SubmittedAt,
		AutoInjectReview: review.AutoInjectReview,
	}
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func timeFromNull(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
