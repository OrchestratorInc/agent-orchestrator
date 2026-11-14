package pr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// EditWriter records a provider-confirmed title or description locally so the
// PR view shows it before the next SCM observation reconciles it.
type EditWriter interface {
	SetPRContent(ctx context.Context, prURL string, title, description *string, changedAt time.Time) error
}

// maxEditTitleRunes mirrors the providers' title limits (GitHub 256, GitLab
// 255) so an oversized title fails fast with a 400.
const maxEditTitleRunes = 255

// maxEditBodyBytes bounds a description well above what a provider takes
// (GitHub 65,536 characters, GitLab 1 MB); the provider's own limit decides.
const maxEditBodyBytes = 1 << 20

// Reasons a pull request cannot be edited, in EditContentResult.
const (
	// EditReasonNoPermission is an account that is neither the PR's author nor
	// allowed to write to its repository.
	EditReasonNoPermission = "no_permission"
	// EditReasonNotOpen is a merged or closed PR.
	EditReasonNotOpen = "not_open"
	// EditReasonUnsupported is a provider that cannot edit pull requests.
	EditReasonUnsupported = "unsupported"
)

// EditContentResult is a tracked PR's title and whole description as its
// provider holds them now, and whether they can be edited. An editor starts
// from these, not from the stored description, which is cut to a view-sized cap
// and can be a poll behind.
type EditContentResult struct {
	Title   string
	Body    string
	CanEdit bool
	// Reason says why not, when CanEdit is false.
	Reason string
}

// EditRequest changes the title, description, or both of the tracked PR
// identified by PRID and PRURL. A nil field is left as it is.
//
// BaseTitle and BaseBody are what the editor started from, and are required
// for the field being changed: the edit is refused with ErrPREditConflict when
// the provider no longer holds that text, so someone else's edit made in the
// meantime is never silently overwritten.
type EditRequest struct {
	PRID      string
	PRURL     string
	Title     *string
	Body      *string
	BaseTitle *string
	BaseBody  *string
}

// EditResult is the PR's title and description after a successful edit.
type EditResult struct {
	PRNumber  int
	Title     string
	Body      string
	UpdatedAt time.Time
}

// EditContent reads a tracked PR's current title and description from its
// provider for an editor to start from.
func (s *ActionService) EditContent(ctx context.Context, prID, prURL string) (EditContentResult, error) {
	tracked, ref, err := s.loadEditPR(ctx, prID, prURL)
	if err != nil {
		return EditContentResult{}, err
	}
	if s.editor == nil {
		return EditContentResult{Title: tracked.Title, Reason: EditReasonUnsupported}, nil
	}
	content, err := s.editor.PullRequestContent(ctx, ref)
	if err != nil {
		if errors.Is(err, ports.ErrSCMUnsupported) {
			return EditContentResult{Title: tracked.Title, Reason: EditReasonUnsupported}, nil
		}
		return EditContentResult{}, mapEditError("read pull request content", err)
	}
	out := EditContentResult{Title: content.Title, Body: content.Body, CanEdit: content.CanEdit}
	switch {
	case tracked.Merged || tracked.Closed:
		out.CanEdit, out.Reason = false, EditReasonNotOpen
	case !content.CanEdit:
		out.Reason = EditReasonNoPermission
	}
	return out, nil
}

// Edit changes a tracked PR's title, description, or both. It reads the
// provider's current text first and refuses when that is not what the editor
// started from; on success the new text is stored at once and the observer is
// asked to reconcile the rest.
func (s *ActionService) Edit(ctx context.Context, request EditRequest) (EditResult, error) {
	if request.Title == nil && request.Body == nil {
		return EditResult{}, fmt.Errorf("%w: a title or a description is required", ErrInvalidEdit)
	}
	var title *string
	if request.Title != nil {
		trimmed := strings.TrimSpace(*request.Title)
		switch {
		case request.BaseTitle == nil:
			return EditResult{}, fmt.Errorf("%w: the title the edit started from is required", ErrInvalidEdit)
		case trimmed == "":
			return EditResult{}, fmt.Errorf("%w: title is required", ErrInvalidEdit)
		case strings.ContainsAny(trimmed, "\r\n"):
			return EditResult{}, fmt.Errorf("%w: title must be one line", ErrInvalidEdit)
		case len([]rune(trimmed)) > maxEditTitleRunes:
			return EditResult{}, fmt.Errorf("%w: title exceeds %d characters", ErrInvalidEdit, maxEditTitleRunes)
		}
		title = &trimmed
	}
	if request.Body != nil {
		switch {
		case request.BaseBody == nil:
			return EditResult{}, fmt.Errorf("%w: the description the edit started from is required", ErrInvalidEdit)
		case len(*request.Body) > maxEditBodyBytes:
			return EditResult{}, fmt.Errorf("%w: description exceeds %d bytes", ErrInvalidEdit, maxEditBodyBytes)
		}
	}
	tracked, ref, err := s.loadEditPR(ctx, request.PRID, request.PRURL)
	if err != nil {
		return EditResult{}, err
	}
	if s.editor == nil {
		return EditResult{}, fmt.Errorf("%w: pull request edits", ErrUnsupported)
	}
	if tracked.Merged || tracked.Closed {
		return EditResult{}, fmt.Errorf("%w: pull request is not open", ErrInvalidTransition)
	}

	current, err := s.editor.PullRequestContent(ctx, ref)
	if err != nil {
		return EditResult{}, mapEditError("read pull request before editing", err)
	}
	if !current.CanEdit {
		return EditResult{}, ErrPREditForbidden
	}
	titleMoved := title != nil && strings.TrimSpace(current.Title) != strings.TrimSpace(*request.BaseTitle)
	bodyMoved := request.Body != nil && !sameText(current.Body, *request.BaseBody)
	if titleMoved || bodyMoved {
		// Show what the provider holds now, so the view is no longer the stale
		// text the editor started from.
		s.storeEdit(ctx, tracked, &current.Title, &current.Body)
		return EditResult{}, ErrPREditConflict
	}

	edited, err := s.editor.EditPullRequest(ctx, ports.SCMPREditRequest{PR: ref, Title: title, Body: request.Body})
	if err != nil {
		return EditResult{}, mapEditError("edit pull request", err)
	}
	// Only the fields this edit set are stored: the other one is whatever the
	// last observation read.
	var storedTitle, storedBody *string
	if title != nil {
		storedTitle = &edited.Title
	}
	if request.Body != nil {
		storedBody = &edited.Body
	}
	s.storeEdit(ctx, tracked, storedTitle, storedBody)
	updatedAt := edited.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = s.clock().UTC()
	}
	return EditResult{PRNumber: tracked.Number, Title: edited.Title, Body: edited.Body, UpdatedAt: updatedAt}, nil
}

// loadEditPR resolves the tracked PR an edit names and its provider reference.
func (s *ActionService) loadEditPR(ctx context.Context, prID, prURL string) (domain.PullRequest, ports.SCMPRRef, error) {
	prNumber, err := parsePRNumber(prID)
	if err != nil || strings.TrimSpace(prURL) == "" {
		return domain.PullRequest{}, ports.SCMPRRef{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	if s.store == nil {
		return domain.PullRequest{}, ports.SCMPRRef{}, errors.New("pr: edit action is not configured")
	}
	tracked, ok, err := s.store.GetPR(ctx, prURL)
	if err != nil {
		return domain.PullRequest{}, ports.SCMPRRef{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return domain.PullRequest{}, ports.SCMPRRef{}, ErrPRNotFound
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return domain.PullRequest{}, ports.SCMPRRef{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	return tracked, ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}, nil
}

// storeEdit writes provider-confirmed text locally and asks the observer to
// re-read the PR. The provider already holds the text, so a failed local write
// only logs: the observer reconciles the row.
func (s *ActionService) storeEdit(ctx context.Context, tracked domain.PullRequest, title, body *string) {
	if s.editWriter != nil {
		if err := s.editWriter.SetPRContent(ctx, tracked.URL, title, body, s.clock().UTC()); err != nil {
			s.logger.Error("pr edit: local write failed", "pr", tracked.URL, "err", err)
		}
	}
	if s.refresher != nil {
		s.refresher.RequestReviewRefresh(tracked.URL)
	}
}

// sameText compares two descriptions as an editor shows them: a provider may
// hold CRLF line endings that a text field reads back as LF.
func sameText(a, b string) bool {
	return strings.ReplaceAll(a, "\r\n", "\n") == strings.ReplaceAll(b, "\r\n", "\n")
}

// mapEditError maps provider sentinels from reading or editing a PR's text.
func mapEditError(op string, err error) error {
	if errors.Is(err, ports.ErrSCMForbidden) {
		return fmt.Errorf("%w: %w", ErrPREditForbidden, err)
	}
	return mapMutationError(op, err)
}
