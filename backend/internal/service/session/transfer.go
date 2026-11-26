package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// transferCommander moves sessions between machines. Production delegates to
// Session Manager; focused fakes need not implement it.
type transferCommander interface {
	PrepareTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination, stopTurn bool) (domain.SessionTransfer, error)
	CancelTransfer(ctx context.Context, id domain.SessionID, transferID string) error
	TransferArchivePath(id domain.SessionID, transferID string) (string, error)
	StoreTransferArchive(transferID string, r io.Reader) error
	ReceiveTransfer(ctx context.Context, projectID domain.ProjectID, t domain.SessionTransfer, prompt string) (domain.SessionRecord, error)
	CommitTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination) (domain.SessionMovedTo, error)
	SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error)
	ProjectOrchestrator(ctx context.Context, project domain.ProjectID) (domain.SessionID, bool, error)
}

// SetRemoteHandoffNote late-binds the note a moved session starts from when
// its conversation cannot be resumed on the destination.
func (s *Service) SetRemoteHandoffNote(build func(ctx context.Context, rec domain.SessionRecord, source string) (string, error)) {
	s.remoteHandoffNote = build
}

func (s *Service) transfers() (transferCommander, error) {
	tc, ok := s.manager.(transferCommander)
	if !ok {
		return nil, apierr.NotImplemented("TRANSFER_UNAVAILABLE", "Moving sessions is not available in this build")
	}
	return tc, nil
}

// PrepareTransfer stops the session's agent, pushes its branch and uncommitted
// work, exports its conversation, and returns what the destination needs.
// sourceLabel names this machine as the user sees it; empty uses MachineName.
// dest names the machine the session is moving to, for the pending marker.
// stopTurn ends a turn in progress instead of refusing.
func (s *Service) PrepareTransfer(ctx context.Context, id domain.SessionID, transferID, sourceLabel string, dest domain.SessionTransferDestination, stopTurn bool) (domain.SessionTransfer, error) {
	tc, err := s.transfers()
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	t, err := tc.PrepareTransfer(ctx, id, transferID, dest, stopTurn)
	if err != nil {
		return domain.SessionTransfer{}, transferError(err)
	}
	t.Source.Label = strings.TrimSpace(sourceLabel)
	if t.Source.Label == "" {
		t.Source.Label = MachineName()
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	if !ok {
		return domain.SessionTransfer{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if s.remoteHandoffNote != nil {
		if t.HandoffNote, err = s.remoteHandoffNote(ctx, rec, t.Source.Label); err != nil {
			s.logger.Warn("transfer: handoff note failed; using the task only", "sessionID", id, "error", err)
		}
	}
	if t.HandoffNote == "" {
		t.HandoffNote = minimalTransferNote(rec, t.Source.Label)
	}
	prs, err := s.ListPRs(ctx, id)
	if err != nil {
		s.logger.Warn("transfer: list PRs failed", "sessionID", id, "error", err)
	}
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			t.PullRequests = append(t.PullRequests, pr.URL)
		}
	}
	return t, nil
}

func minimalTransferNote(rec domain.SessionRecord, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This AO session moved here from %s. Its earlier conversation could not be resumed, so this is what is known.\n", source)
	fmt.Fprintf(&b, "Git branch: %s. The branch and uncommitted changes came with it; check the workspace before continuing.\n", rec.Metadata.Branch)
	if task := strings.TrimSpace(rec.Metadata.Prompt); task != "" {
		fmt.Fprintf(&b, "\nOriginal task:\n%s\n", task)
	}
	if latest := strings.TrimSpace(rec.Metadata.LatestUserPrompt); latest != "" && latest != strings.TrimSpace(rec.Metadata.Prompt) {
		fmt.Fprintf(&b, "\nLatest request:\n%s\n", latest)
	}
	if update := strings.TrimSpace(rec.Metadata.LatestAssistantUpdate); update != "" {
		fmt.Fprintf(&b, "\nLatest update from the previous agent:\n%s\n", update)
	}
	b.WriteString("\nReply with a few lines on where things stand, then wait for the user.")
	return b.String()
}

// CancelTransfer forgets an unfinished move so the session can resume here.
func (s *Service) CancelTransfer(ctx context.Context, id domain.SessionID, transferID string) error {
	tc, err := s.transfers()
	if err != nil {
		return err
	}
	return transferError(tc.CancelTransfer(ctx, id, transferID))
}

// TransferArchivePath is the file session id's prepared transfer carries.
func (s *Service) TransferArchivePath(id domain.SessionID, transferID string) (string, error) {
	tc, err := s.transfers()
	if err != nil {
		return "", err
	}
	path, err := tc.TransferArchivePath(id, transferID)
	return path, transferError(err)
}

// StoreTransferArchive keeps the file uploaded for a transfer.
func (s *Service) StoreTransferArchive(transferID string, r io.Reader) error {
	tc, err := s.transfers()
	if err != nil {
		return err
	}
	return transferError(tc.StoreTransferArchive(transferID, r))
}

// ReceiveTransfer starts a session in projectID that continues t, then claims
// its pull requests. A claim that fails is reported, not fatal. resumed is
// false when the agent started from the handoff note instead of its own
// conversation.
func (s *Service) ReceiveTransfer(ctx context.Context, projectID domain.ProjectID, t domain.SessionTransfer) (sess domain.Session, resumed bool, warnings []string, err error) {
	tc, err := s.transfers()
	if err != nil {
		return domain.Session{}, false, nil, err
	}
	orchestrator, _, err := tc.ProjectOrchestrator(ctx, projectID)
	if err != nil {
		return domain.Session{}, false, nil, err
	}
	rec, err := tc.ReceiveTransfer(ctx, projectID, t, transferResumeNote(t, orchestrator))
	if err != nil {
		return domain.Session{}, false, nil, transferError(err)
	}
	resumed = t.Native != nil && rec.Metadata.AgentSessionID == t.Native.NativeID
	for _, pr := range t.PullRequests {
		if _, err := s.ClaimPR(ctx, rec.ID, pr, ClaimPROptions{AllowTakeover: true}); err != nil {
			warnings = append(warnings, fmt.Sprintf("Could not claim %s here: %v", pr, err))
		}
	}
	sess, err = s.Get(ctx, rec.ID)
	return sess, resumed, warnings, err
}

// transferResumeNote is the first turn of a resumed moved conversation.
// orchestrator is this project's live orchestrator, empty when it has none.
func transferResumeNote(t domain.SessionTransfer, orchestrator domain.SessionID) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This session just moved to another computer from %s.", t.Source.Label)
	b.WriteString(" File paths in this conversation were updated to this computer's worktree.")
	fmt.Fprintf(&b, " The branch %s", t.Branch)
	if t.PreservedRef != "" {
		b.WriteString(" and your uncommitted changes")
	}
	b.WriteString(" came with it; check `git status` before relying on earlier output.")
	if len(t.PullRequests) > 0 {
		fmt.Fprintf(&b, " This session now owns %s.", strings.Join(t.PullRequests, ", "))
	}
	if orchestrator != "" {
		fmt.Fprintf(&b, " You now report to the orchestrator %s on this computer; the one on %s cannot be reached from here.", orchestrator, t.Source.Label)
	} else {
		fmt.Fprintf(&b, " This project has no orchestrator on this computer, and the one on %s cannot be reached from here.", t.Source.Label)
	}
	b.WriteString(" Reply with one line saying you are ready, then wait for the user.")
	return b.String()
}

// CommitTransfer marks a moved session and ends it here.
func (s *Service) CommitTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination) (domain.SessionMovedTo, error) {
	tc, err := s.transfers()
	if err != nil {
		return domain.SessionMovedTo{}, err
	}
	moved, err := tc.CommitTransfer(ctx, id, transferID, dest)
	return moved, transferError(err)
}

// SessionMovedTo reports where a moved session continues.
func (s *Service) SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	tc, ok := s.manager.(transferCommander)
	if !ok {
		return domain.SessionMovedTo{}, false, nil
	}
	return tc.SessionMovedTo(ctx, id)
}

// transferError gives transfer refusals stable API codes.
func transferError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimPrefix(err.Error(), "session: ")
	switch {
	// First: it wraps why the start failed, which says nothing about what still runs.
	case errors.Is(err, sessionmanager.ErrTransferUnconfirmed):
		return apierr.Conflict("TRANSFER_UNCONFIRMED", shortenTransferText(msg), nil)
	case errors.Is(err, sessionmanager.ErrNotFound):
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	case errors.Is(err, sessionmanager.ErrTransferAgentBusy):
		return apierr.Conflict("TRANSFER_AGENT_BUSY", msg, nil)
	case errors.Is(err, sessionmanager.ErrTransferRepoMismatch):
		return apierr.Conflict("TRANSFER_REPO_MISMATCH", "The project on that computer is a different repository.", nil)
	case errors.Is(err, ports.ErrTransferBranchDiverged):
		return apierr.Conflict("TRANSFER_BRANCH_DIVERGED", fmt.Sprintf("Branch %s on that computer has commits this session does not. Push or delete it there, then move the session.", transferDetail(err, ports.ErrTransferBranchDiverged)), nil)
	case errors.Is(err, sessionmanager.ErrTransferPreservedMismatch):
		return apierr.Conflict("TRANSFER_PRESERVED_MISMATCH", msg, nil)
	case errors.Is(err, ports.ErrWorkspaceBranchCheckedOutElsewhere):
		return apierr.Conflict("TRANSFER_BRANCH_IN_USE", fmt.Sprintf("The branch is in use on that computer: %s. Close it there, then move the session.", transferDetail(err, ports.ErrWorkspaceBranchCheckedOutElsewhere)), nil)
	case errors.Is(err, ports.ErrTransferGitBusy):
		doing := transferDetail(err, ports.ErrTransferGitBusy)
		text := "Git is in the middle of " + doing + " here. Finish or abort it, then move the session."
		if doing == "unresolved conflicts" {
			text = "Git has unresolved conflicts here. Resolve or undo them, then move the session."
		}
		return apierr.Conflict("TRANSFER_GIT_BUSY", text, nil)
	case errors.Is(err, ports.ErrTransferNestedRepo):
		return apierr.Conflict("TRANSFER_NESTED_REPO", fmt.Sprintf("The folder %s is its own git repository, which a move cannot carry. Move it out, or add it to .gitignore, then move the session.", transferDetail(err, ports.ErrTransferNestedRepo)), nil)
	case errors.Is(err, ports.ErrTransferSubmodule):
		return apierr.Conflict("TRANSFER_SUBMODULE", fmt.Sprintf("The submodule %s has work that is not pushed, which a move cannot carry. Commit and push it, then move the session.", transferDetail(err, ports.ErrTransferSubmodule)), nil)
	case errors.Is(err, ports.ErrTransferLFS):
		return apierr.Conflict("TRANSFER_LFS", "This work has Git LFS files that are not pushed, which a move cannot carry. Commit and push them, then move the session.", nil)
	case errors.Is(err, sessionmanager.ErrTransferTooLarge):
		return apierr.Conflict("TRANSFER_TOO_LARGE", fmt.Sprintf("The unpushed work is %s, over the %d MB a move carries. Push the branch, then move the session.", transferDetail(err, sessionmanager.ErrTransferTooLarge), sessionmanager.MaxTransferArchiveBytes>>20), nil)
	case errors.Is(err, ports.ErrTransferFetch):
		return apierr.Conflict("TRANSFER_FETCH_FAILED", "Could not bring in the moved work: "+transferDetail(err, ports.ErrTransferFetch), nil)
	case errors.Is(err, ports.ErrDetachedHead):
		return apierr.Conflict("TRANSFER_DETACHED_HEAD", fmt.Sprintf("The session's folder is not on its branch %s. Switch back to it, then move the session.", transferDetail(err, ports.ErrDetachedHead)), nil)
	case errors.Is(err, sessionmanager.ErrSessionMoved):
		return apierr.Conflict("SESSION_MOVED", msg, nil)
	case errors.Is(err, sessionmanager.ErrTransferPending):
		return apierr.Conflict("TRANSFER_PENDING", msg, nil)
	case errors.Is(err, sessionmanager.ErrTransferNotPending):
		return apierr.Conflict("TRANSFER_NOT_PENDING", msg, nil)
	case errors.Is(err, sessionmanager.ErrClientRequestConflict):
		return apierr.Conflict("TRANSFER_CONFLICT", "That computer already received a different move with this ID.", nil)
	case errors.Is(err, sessionmanager.ErrTransferDefaultBranch):
		return apierr.Conflict("TRANSFER_DEFAULT_BRANCH", msg, nil)
	case errors.Is(err, sessionmanager.ErrTransferArchive):
		return apierr.Invalid("TRANSFER_ARCHIVE_INVALID", msg, nil)
	case errors.Is(err, sessionmanager.ErrTransferUnsupported):
		return apierr.Invalid("TRANSFER_UNSUPPORTED", msg, nil)
	case errors.Is(err, ports.ErrWorkspaceBranchInvalid):
		return apierr.Invalid("TRANSFER_UNSUPPORTED", msg, nil)
	}
	// Receive spawns a session, so spawn failures (missing tmux, agent binary) keep their codes.
	mapped := mapSessionError(err)
	var apiErr *apierr.Error
	if errors.As(mapped, &apiErr) {
		return mapped
	}
	// Anything else is this computer's own problem, such as a file it cannot
	// read, which the user can only fix when told.
	return apierr.Internal("TRANSFER_FAILED", "The move failed: "+shortenTransferText(msg))
}

// shortenTransferText keeps the end of an error, where the cause is.
func shortenTransferText(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > 300 {
		runes = append([]rune("..."), runes[len(runes)-297:]...)
	}
	return string(runes)
}

// transferDetail is what an error says after the sentinel it wraps.
func transferDetail(err, sentinel error) string {
	_, detail, _ := strings.Cut(err.Error(), sentinel.Error()+": ")
	return detail
}

// MachineName is this computer's hostname without the ".local" suffix, used
// when the app has only "This computer" to call it.
func MachineName() string {
	name, _ := os.Hostname()
	if name = strings.TrimSuffix(name, ".local"); name == "" {
		return "another computer"
	}
	return name
}
