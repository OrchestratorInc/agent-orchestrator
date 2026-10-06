package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrFindingForbidden marks a resolve attempt by a session that does not own
// the finding: only its worker or an orchestrator of the same project may.
var ErrFindingForbidden = errors.New("review: session may not resolve this finding")

// ErrFindingSuperseded marks a resolve attempt on a finding a newer completed
// pass already replaced. It is history and can no longer change.
var ErrFindingSuperseded = errors.New("review: finding was superseded by a newer review")

const (
	maxFindingsPerReview = 100
	maxFindingBodyBytes  = 16 << 10
	maxFindingPathBytes  = 1 << 10
	maxResolutionNote    = 4 << 10
	summaryPostTimeout   = 20 * time.Second
)

// Deliverer tells a worker about its completed AO review passes once.
type Deliverer interface {
	DeliverReviewRuns(ctx context.Context, id domain.SessionID, prURL string) error
}

// WithReviewSummaryPoster lets the daemon post each completed pass's summary to
// the PR. Without it nothing is posted and the pass lives only in AO.
func WithReviewSummaryPoster(poster ports.SCMReviewSummaryPoster) Option {
	return func(s *Service) { s.poster = poster }
}

// WithReviewDeliverer delivers completed passes to the worker as soon as the
// reviewer submits them, instead of waiting for the next delivery sweep.
func WithReviewDeliverer(deliverer Deliverer) Option {
	return func(s *Service) { s.deliverer = deliverer }
}

// normalizeFindings validates the findings submitted with a verdict. A
// changes_requested result must name at least one finding unless it comes from
// an older reviewer prompt that already posted its findings on the provider
// (githubReviewID set); an approval carries none.
func normalizeFindings(review SubmittedReview) ([]domain.ReviewFindingInput, error) {
	if len(review.Findings) > maxFindingsPerReview {
		return nil, fmt.Errorf("%w: at most %d findings per review", ErrInvalid, maxFindingsPerReview)
	}
	out := make([]domain.ReviewFindingInput, 0, len(review.Findings))
	for i, f := range review.Findings {
		body := strings.TrimSpace(f.Body)
		path := strings.TrimSpace(f.Path)
		switch {
		case body == "":
			return nil, fmt.Errorf("%w: finding %d needs a body", ErrInvalid, i+1)
		case len(body) > maxFindingBodyBytes || !utf8.ValidString(body):
			return nil, fmt.Errorf("%w: finding %d body must be valid UTF-8 of at most %d bytes", ErrInvalid, i+1, maxFindingBodyBytes)
		case len(path) > maxFindingPathBytes:
			return nil, fmt.Errorf("%w: finding %d path is too long", ErrInvalid, i+1)
		case f.Line < 0:
			return nil, fmt.Errorf("%w: finding %d line must not be negative", ErrInvalid, i+1)
		case f.Line > 0 && path == "":
			return nil, fmt.Errorf("%w: finding %d has a line but no path", ErrInvalid, i+1)
		}
		out = append(out, domain.ReviewFindingInput{Path: path, Line: f.Line, Body: body})
	}
	switch review.Verdict {
	case domain.VerdictApproved:
		if len(out) > 0 {
			return nil, fmt.Errorf("%w: an approved review has no findings; put optional suggestions in the body", ErrInvalid)
		}
	case domain.VerdictChangesRequested:
		if len(out) == 0 && strings.TrimSpace(review.GithubReviewID) == "" {
			return nil, fmt.Errorf("%w: a changes_requested review needs at least one finding", ErrInvalid)
		}
	}
	return out, nil
}

// sameFindings reports whether a re-submitted finding list matches what the
// first submit recorded, so a retried CLI call is accepted as a no-op.
func sameFindings(recorded []domain.ReviewFinding, submitted []domain.ReviewFindingInput) bool {
	if len(recorded) != len(submitted) {
		return false
	}
	for i := range recorded {
		if recorded[i].Path != submitted[i].Path || recorded[i].Line != submitted[i].Line || recorded[i].Body != submitted[i].Body {
			return false
		}
	}
	return true
}

// postSummary posts one COMMENT review carrying the verdict, the reviewer's
// summary, and the findings list. It opens no review threads. A failure is
// recorded on the run for the inspector to show; it is not retried.
func (s *Service) postSummary(ctx context.Context, run domain.ReviewRun, findings []domain.ReviewFindingInput) domain.ReviewRun {
	if s.poster == nil {
		return run
	}
	var ref ports.SCMPRRef
	var refErr error
	prs, err := s.store.ListPRsBySession(ctx, run.SessionID)
	if err == nil {
		pr, ok := selectRereviewPR(prs, run.PRURL)
		if !ok {
			refErr = fmt.Errorf("pull request is not tracked for this worker")
		} else {
			ref, refErr = reviewRequestRef(pr)
		}
	} else {
		refErr = err
	}
	var reviewID, postErr string
	if refErr != nil {
		postErr = refErr.Error()
	} else {
		postCtx, cancel := context.WithTimeout(ctx, summaryPostTimeout)
		id, err := s.poster.PostReviewSummary(postCtx, ports.SCMReviewSummaryRequest{
			PR: ref, CommitSHA: run.TargetSHA, Body: formatReviewSummary(run, findings),
		})
		cancel()
		if err != nil {
			postErr = err.Error()
		} else {
			reviewID = id
		}
	}
	if postErr != "" {
		postErr = domain.SanitizeControlChars(postErr)
		if len(postErr) > 500 {
			postErr = postErr[:500]
		}
		slog.Default().WarnContext(ctx, "review summary post failed", "session", run.SessionID, "run", run.ID, "err", postErr)
	}
	if err := s.store.SetReviewRunProviderPost(ctx, run.ID, reviewID, postErr); err != nil {
		slog.Default().WarnContext(ctx, "review summary post not recorded", "session", run.SessionID, "run", run.ID, "err", err)
		return run
	}
	run.GithubReviewID = reviewID
	run.ProviderPostError = postErr
	return run
}

func formatReviewSummary(run domain.ReviewRun, findings []domain.ReviewFindingInput) string {
	head := run.TargetSHA
	if len(head) > 7 {
		head = head[:7]
	}
	verdict := "changes requested"
	if run.Verdict == domain.VerdictApproved {
		verdict = "approved"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**AO review: %s** (reviewer: %s, commit %s)\n", verdict, run.Harness, head)
	if body := strings.TrimSpace(run.Body); body != "" {
		fmt.Fprintf(&b, "\n%s\n", body)
	}
	if len(findings) > 0 {
		b.WriteString("\n**Findings** (tracked and resolved in AO)\n")
		for i, f := range findings {
			location := ""
			if f.Path != "" {
				location = "`" + f.Path
				if f.Line > 0 {
					location += fmt.Sprintf(":%d", f.Line)
				}
				location += "`: "
			}
			fmt.Fprintf(&b, "\n%d. %s%s", i+1, location, f.Body)
		}
		b.WriteString("\n")
	}
	if run.Verdict == domain.VerdictApproved {
		b.WriteString("\n_AO's internal review. It is not a GitHub approval._\n")
	}
	return b.String()
}

// deliver hands just-submitted passes to the worker. A failure here is not the
// reviewer's to retry: the pass is recorded, and the delivery sweep retries.
func (s *Service) deliver(ctx context.Context, workerID domain.SessionID, runs []domain.ReviewRun) {
	if s.deliverer == nil {
		return
	}
	seen := map[string]bool{}
	for _, run := range runs {
		if seen[run.PRURL] {
			continue
		}
		seen[run.PRURL] = true
		if err := s.deliverer.DeliverReviewRuns(ctx, workerID, run.PRURL); err != nil {
			slog.Default().WarnContext(ctx, "review delivery deferred", "session", workerID, "pr", run.PRURL, "err", err)
		}
	}
}

// ResolveFindingsRequest resolves one or more of a worker's AO review findings.
type ResolveFindingsRequest struct {
	FindingIDs []string
	Note       string
	// ActorSessionID is the AO session asking, from its AO_SESSION_ID. Empty
	// means a person, from the app or a terminal outside any agent session.
	ActorSessionID domain.SessionID
}

// ResolveFindings marks a worker's open findings resolved. Resolving an already
// resolved finding is a no-op that keeps the first resolution.
func (s *Service) ResolveFindings(ctx context.Context, workerID domain.SessionID, req ResolveFindingsRequest) ([]domain.ReviewFinding, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if len(req.FindingIDs) == 0 {
		return nil, fmt.Errorf("%w: at least one finding id is required", ErrInvalid)
	}
	note := strings.TrimSpace(domain.SanitizeControlChars(req.Note))
	if len(note) > maxResolutionNote {
		return nil, fmt.Errorf("%w: resolution note must be at most %d bytes", ErrInvalid, maxResolutionNote)
	}
	worker, ok, err := s.store.GetSession(ctx, workerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	actorKind := "person"
	if req.ActorSessionID != "" {
		actorKind, err = s.findingActorKind(ctx, worker, req.ActorSessionID)
		if err != nil {
			return nil, err
		}
	}
	findings := make([]domain.ReviewFinding, 0, len(req.FindingIDs))
	for _, raw := range req.FindingIDs {
		id := strings.TrimSpace(raw)
		finding, ok, err := s.store.GetReviewFinding(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok || finding.SessionID != workerID {
			return nil, fmt.Errorf("%w: review finding %q is not one of this worker's findings", ErrNotFound, id)
		}
		if finding.Status == domain.ReviewFindingSuperseded {
			return nil, fmt.Errorf("%w: finding %q (a newer review replaced it)", ErrFindingSuperseded, id)
		}
		findings = append(findings, finding)
	}
	now := s.clock()
	for i, finding := range findings {
		if finding.Status != domain.ReviewFindingOpen {
			continue
		}
		updated, err := s.store.ResolveReviewFinding(ctx, finding.ID, note, req.ActorSessionID, now)
		if err != nil {
			return nil, err
		}
		if !updated {
			// A concurrent resolve or supersede won; report what is stored.
			current, ok, err := s.store.GetReviewFinding(ctx, finding.ID)
			if err != nil {
				return nil, err
			}
			if ok {
				findings[i] = current
			}
			continue
		}
		at := now
		findings[i].Status = domain.ReviewFindingResolved
		findings[i].ResolutionNote = note
		findings[i].ResolvedBySessionID = req.ActorSessionID
		findings[i].ResolvedAt = &at
		s.emit(ctx, "ao.review.finding_resolved", workerID, map[string]any{
			"actor":     actorKind,
			"has_note":  note != "",
			"age_ms":    now.Sub(finding.CreatedAt).Milliseconds(),
			"with_path": finding.Path != "",
		})
	}
	return findings, nil
}

// findingActorKind authorizes an agent session to resolve a worker's findings:
// the worker itself, or an orchestrator of the worker's project. A reviewer has
// no session of its own and never reaches this path with an actor id.
func (s *Service) findingActorKind(ctx context.Context, worker domain.SessionRecord, actorID domain.SessionID) (string, error) {
	if actorID == worker.ID {
		return "worker", nil
	}
	actor, ok, err := s.store.GetSession(ctx, actorID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: unknown acting session %q", ErrFindingForbidden, actorID)
	}
	if actor.Kind == domain.KindOrchestrator && actor.ProjectID == worker.ProjectID {
		return "orchestrator", nil
	}
	return "", fmt.Errorf("%w: %q is neither this worker nor an orchestrator of its project", ErrFindingForbidden, actorID)
}
