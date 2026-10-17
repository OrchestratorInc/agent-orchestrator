// Package instructions owns the standing instructions AO adds to new sessions:
// the user's own, for every project, and each project's. People edit them by
// hand; dreaming automations edit them after a session ends.
package instructions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// historyDepth reads every revision the store keeps.
const historyDepth = 100

// maxRemovedLines bounds the lines Get reports as removed by people.
const maxRemovedLines = 50

// maxHistory bounds the changes Get reports.
const maxHistory = 20

// Store is the persistence the service needs.
type Store interface {
	ListInstructionRevisions(context.Context, domain.ProjectID, int) ([]domain.InstructionsRevision, error)
	SaveInstructionRevision(context.Context, domain.InstructionsRevision, *int64) (domain.InstructionsRevision, bool, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	GetAutomationRun(context.Context, domain.AutomationRunID) (domain.AutomationRun, bool, error)
	GetAutomation(context.Context, domain.AutomationID) (domain.Automation, bool, error)
}

// Service reads and saves standing instructions.
type Service struct {
	store Store
	clock func() time.Time
}

// New returns a Service. A nil clock uses time.Now.
func New(store Store, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{store: store, clock: clock}
}

// View is one set of instructions as people and dreaming see it.
type View struct {
	// ProjectID is empty for the user's own instructions.
	ProjectID domain.ProjectID
	Body      string
	// Revision is 0 until the first save.
	Revision  int64
	UpdatedAt *time.Time
	// UpdatedBy names the AO session that saved the latest revision; nil when
	// the user saved it.
	UpdatedBy *Author
	// PreviousBody is the revision before the latest, which undo restores.
	PreviousBody *string
	// ProtectedLines are the lines the user typed or edited. Dreaming must
	// keep them word for word; it may rewrite every other line.
	ProtectedLines []string
	// RemovedLines are lines people deleted. Dreaming must not add them back.
	RemovedLines []string
	// History is the newest changes, oldest first, so dreaming can tell what
	// it learned when and from which session.
	History []Change
}

// Change is one saved edit to a set of instructions.
type Change struct {
	At time.Time
	// Session titles the AO session that saved it, such as a dream; empty
	// when the user saved it.
	Session string
	Dreamed bool
	Added   []string
	Removed []string
}

// Author is the AO session that saved a revision.
type Author struct {
	SessionID domain.SessionID
	Title     string
	Dreamed   bool
}

// SaveInput is one save.
type SaveInput struct {
	Body string
	// Revision, when set, makes the save fail unless it is still the latest
	// revision. Dreaming must set it; people's saves always win.
	Revision *int64
	// SessionID is the AO session saving; empty when the user saves. Only a
	// dreaming session may save.
	SessionID domain.SessionID
}

var (
	errTooLong = apierr.Invalid("INSTRUCTIONS_TOO_LONG", fmt.Sprintf("Instructions must be %d KiB or less", domain.MaxInstructionsBytes>>10), nil)
	errChanged = apierr.Conflict("INSTRUCTIONS_CHANGED", "The instructions changed after you read them. Read them again and redo your edit.", nil)
	errNoRev   = apierr.Invalid("INSTRUCTIONS_REVISION_REQUIRED", "Dreaming must pass the revision it read", nil)
	// An agent that read a hostile file must not plant standing
	// instructions in every later session. Inside AO only dreaming writes
	// them, and it learns from what the user typed.
	errSessionWrite = apierr.Forbidden("INSTRUCTIONS_READ_ONLY", "Only dreaming can change instructions from inside a session. Ask the user to edit them on the Automations page.")
)

// Get returns one set of instructions. An empty projectID selects the user's
// own.
func (s *Service) Get(ctx context.Context, projectID domain.ProjectID) (View, error) {
	if err := s.checkProject(ctx, projectID); err != nil {
		return View{}, err
	}
	revisions, err := s.store.ListInstructionRevisions(ctx, projectID, historyDepth)
	if err != nil {
		return View{}, apierr.Internal("INSTRUCTIONS_READ_FAILED", "Failed to read instructions")
	}
	return s.view(ctx, projectID, revisions), nil
}

// Save stores a new revision of one set of instructions and returns the
// result. A dreaming session must name the revision it read and keep every
// line the user typed or edited.
func (s *Service) Save(ctx context.Context, projectID domain.ProjectID, input SaveInput) (View, error) {
	if err := s.checkProject(ctx, projectID); err != nil {
		return View{}, err
	}
	body := strings.TrimSpace(strings.ReplaceAll(input.Body, "\r\n", "\n"))
	if len(body) > domain.MaxInstructionsBytes {
		return View{}, errTooLong
	}
	rev := domain.InstructionsRevision{ProjectID: projectID, Body: body, CreatedAt: s.clock().UTC()}
	if input.SessionID != "" {
		session, ok, err := s.store.GetSession(ctx, input.SessionID)
		if err != nil {
			return View{}, apierr.Internal("INSTRUCTIONS_SAVE_FAILED", "Failed to save instructions")
		}
		if !ok {
			return View{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
		}
		dreamed, err := s.isDream(ctx, session)
		if err != nil {
			return View{}, apierr.Internal("INSTRUCTIONS_SAVE_FAILED", "Failed to save instructions")
		}
		if !dreamed {
			return View{}, errSessionWrite
		}
		rev.SessionID, rev.Dreamed = &session.ID, true
	}
	if rev.Dreamed && input.Revision == nil {
		return View{}, errNoRev
	}
	latest, err := s.store.ListInstructionRevisions(ctx, projectID, 1)
	if err != nil {
		return View{}, apierr.Internal("INSTRUCTIONS_SAVE_FAILED", "Failed to save instructions")
	}
	var previous domain.InstructionsRevision
	if len(latest) > 0 {
		previous = latest[0]
	}
	if rev.Dreamed {
		if *input.Revision != previous.ID {
			return View{}, errChanged
		}
		if missing := missingLines(previous.UserLines, body); len(missing) > 0 {
			return View{}, apierr.Invalid("INSTRUCTIONS_LINES_REMOVED",
				"Keep every line the user typed or edited, word for word. Put these back: "+strings.Join(missing, " | "),
				map[string]any{"lines": missing})
		}
		rev.UserLines = previous.UserLines
	} else {
		rev.UserLines = userLinesAfter(previous, body)
	}
	if _, _, err := s.store.SaveInstructionRevision(ctx, rev, input.Revision); err != nil {
		if errors.Is(err, domain.ErrInstructionsChanged) {
			return View{}, errChanged
		}
		return View{}, apierr.Internal("INSTRUCTIONS_SAVE_FAILED", "Failed to save instructions")
	}
	return s.Get(ctx, projectID)
}

func (s *Service) checkProject(ctx context.Context, projectID domain.ProjectID) error {
	if projectID == "" {
		return nil
	}
	project, ok, err := s.store.GetProject(ctx, string(projectID))
	if err != nil {
		return apierr.Internal("INSTRUCTIONS_READ_FAILED", "Failed to read instructions")
	}
	if !ok || !project.ArchivedAt.IsZero() {
		return apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return nil
}

// isDream reports whether a dreaming automation started the session.
func (s *Service) isDream(ctx context.Context, session domain.SessionRecord) (bool, error) {
	if session.AutomationRunID == nil {
		return false, nil
	}
	run, ok, err := s.store.GetAutomationRun(ctx, *session.AutomationRunID)
	if err != nil || !ok {
		return false, err
	}
	automation, ok, err := s.store.GetAutomation(ctx, run.AutomationID)
	if err != nil || !ok {
		return false, err
	}
	return automation.Purpose == domain.AutomationPurposeDreaming, nil
}

func (s *Service) view(ctx context.Context, projectID domain.ProjectID, revisions []domain.InstructionsRevision) View {
	view := View{
		ProjectID: projectID, ProtectedLines: make([]string, 0),
		RemovedLines: removedLines(revisions), History: s.history(ctx, revisions),
	}
	if len(revisions) == 0 {
		return view
	}
	latest := revisions[0]
	view.Body, view.Revision = latest.Body, latest.ID
	view.ProtectedLines = append(view.ProtectedLines, latest.UserLines...)
	updatedAt := latest.CreatedAt
	view.UpdatedAt = &updatedAt
	if latest.SessionID != nil || latest.Dreamed {
		author := &Author{Dreamed: latest.Dreamed}
		if latest.SessionID != nil {
			author.SessionID, author.Title = *latest.SessionID, s.sessionTitle(ctx, *latest.SessionID)
		}
		view.UpdatedBy = author
	}
	switch {
	case len(revisions) > 1:
		previous := revisions[1].Body
		view.PreviousBody = &previous
	case latest.Dreamed:
		// Undoing the first dream restores the empty text it started from.
		previous := ""
		view.PreviousBody = &previous
	}
	return view
}

// sessionTitle returns a session's display name, else its id.
func (s *Service) sessionTitle(ctx context.Context, id domain.SessionID) string {
	if session, ok, err := s.store.GetSession(ctx, id); err == nil && ok && strings.TrimSpace(session.DisplayName) != "" {
		return strings.TrimSpace(session.DisplayName)
	}
	return string(id)
}

// history returns the newest changes between kept revisions, oldest first.
// revisions are newest first. The oldest kept revision counts as a change from
// empty text only when no older revision was pruned.
func (s *Service) history(ctx context.Context, revisions []domain.InstructionsRevision) []Change {
	out := make([]Change, 0)
	for i, rev := range revisions {
		if len(out) == maxHistory {
			break
		}
		var before []string
		if i+1 < len(revisions) {
			before = lines(revisions[i+1].Body)
		} else if len(revisions) == historyDepth {
			break
		}
		after := lines(rev.Body)
		change := Change{At: rev.CreatedAt, Dreamed: rev.Dreamed, Added: subtract(after, before), Removed: subtract(before, after)}
		if len(change.Added) == 0 && len(change.Removed) == 0 {
			continue
		}
		if rev.SessionID != nil {
			change.Session = s.sessionTitle(ctx, *rev.SessionID)
		}
		out = append(out, change)
	}
	slices.Reverse(out)
	return out
}

// subtract returns the lines of a that b lacks, in order.
func subtract(a, b []string) []string {
	out := make([]string, 0)
	for _, line := range a {
		if !slices.Contains(b, line) && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// lines returns body's non-blank lines, trimmed. Dreaming's rules compare
// lines, not raw text, so reflowing whitespace never counts as a change.
func lines(body string) []string {
	out := make([]string, 0)
	for line := range strings.SplitSeq(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// userLinesAfter returns the user's lines once they save body over previous:
// the lines they typed or edited now, plus their earlier lines body still has.
// Lines body keeps unchanged from a dream stay the dream's.
func userLinesAfter(previous domain.InstructionsRevision, body string) []string {
	before := lines(previous.Body)
	out := make([]string, 0)
	for _, line := range lines(body) {
		if (!slices.Contains(before, line) || slices.Contains(previous.UserLines, line)) && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// removedLines returns lines that a revision dreaming did not write dropped
// from the revision before it, newest first, skipping lines the latest text
// has again.
func removedLines(revisions []domain.InstructionsRevision) []string {
	out := make([]string, 0)
	if len(revisions) == 0 {
		return out
	}
	current := lines(revisions[0].Body)
	for i := 0; i+1 < len(revisions) && len(out) < maxRemovedLines; i++ {
		if revisions[i].Dreamed {
			continue
		}
		kept := lines(revisions[i].Body)
		for _, line := range lines(revisions[i+1].Body) {
			if !slices.Contains(kept, line) && !slices.Contains(current, line) && !slices.Contains(out, line) && len(out) < maxRemovedLines {
				out = append(out, line)
			}
		}
	}
	return out
}

// missingLines returns the protected lines body no longer has.
func missingLines(protected []string, body string) []string {
	have := lines(body)
	var missing []string
	for _, line := range protected {
		if !slices.Contains(have, line) {
			missing = append(missing, line)
		}
	}
	return missing
}
