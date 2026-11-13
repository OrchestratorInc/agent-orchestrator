package session

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/nativetransfer"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionmove"
)

// CloudPromptMaxBytes is the most AO Cloud accepts as a session prompt.
const CloudPromptMaxBytes = 65_536

// MoveNoter renders the handoff note a moved session starts from.
type MoveNoter interface {
	MoveNote(ctx context.Context, rec domain.SessionRecord, movedFrom string, wip bool, maxBytes int) (string, error)
}

type sessionMoveStore interface {
	SetSessionMovedTo(ctx context.Context, id domain.SessionID, move domain.SessionMove, updatedAt time.Time) (bool, error)
}

// SetMoveNoter wires the handoff note builder for session moves.
func (s *Service) SetMoveNoter(n MoveNoter) { s.moveNotes = n }

// MoveExportInput asks this machine to push a session for a move to AO Cloud.
type MoveExportInput struct {
	MoveID      string
	SourceLabel string
}

// MoveManifest is what the destination needs to start the session again.
type MoveManifest struct {
	SourceSessionID    domain.SessionID       `json:"sourceSessionId"`
	SourceLabel        string                 `json:"sourceLabel"`
	RepoKey            string                 `json:"repoKey"`
	Branch             string                 `json:"branch"`
	Tip                string                 `json:"tip"`
	WIP                bool                   `json:"wip"`
	Harness            domain.AgentHarness    `json:"harness"`
	Model              string                 `json:"model,omitempty"`
	Effort             string                 `json:"effort,omitempty"`
	Permissions        domain.PermissionMode  `json:"permissions,omitempty"`
	Mode               domain.SessionMode     `json:"mode,omitempty"`
	DisplayName        string                 `json:"displayName,omitempty"`
	IssueID            domain.IssueID         `json:"issueId,omitempty"`
	RequestedOutput    domain.RequestedOutput `json:"requestedOutput,omitempty"`
	PullRequests       []string               `json:"pullRequests,omitempty"`
	AutoReview         bool                   `json:"autoReview"`
	TerminateOnPRMerge bool                   `json:"terminateOnPrMerge"`
	AutoInjectReview   bool                   `json:"autoInjectReview"`
	AutoInjectCI       bool                   `json:"autoInjectCI"`
}

// MoveExport is the pushed session: what AO Cloud needs to continue it.
type MoveExport struct {
	Manifest    MoveManifest
	HandoffNote string
	Changes     int
	Submodules  []string
}

// MoveImportInput starts a session AO Cloud pushed, on this machine.
type MoveImportInput struct {
	ProjectID   domain.ProjectID
	MoveID      string
	Manifest    MoveManifest
	HandoffNote string
}

// MoveImportOutcome is the new session and anything that did not carry over.
type MoveImportOutcome struct {
	Session  domain.Session
	Warnings []string
}

// MoveCompleteInput records the AO Cloud session a session moved to.
type MoveCompleteInput struct {
	Destination domain.SessionMove
}

// ExportMove pushes a stopped session's branch, with its uncommitted work as
// one commit on top, and returns its settings and handoff note for AO Cloud.
func (s *Service) ExportMove(ctx context.Context, id domain.SessionID, in MoveExportInput) (MoveExport, error) {
	if in.MoveID == "" {
		return MoveExport{}, apierr.Invalid("MOVE_ID_REQUIRED", "moveId is required", nil)
	}
	rec, project, repoKey, err := s.moveSource(ctx, id, true)
	if err != nil {
		return MoveExport{}, err
	}

	label := strings.TrimSpace(in.SourceLabel)
	if label == "" {
		label = machineName()
	}
	wt := rec.Metadata.WorkspacePath
	c, err := sessionmove.CaptureWorktree(ctx, sessionmove.CaptureOptions{Worktree: wt, Label: label, Exclude: project.Config.Symlinks})
	if err != nil {
		return MoveExport{}, moveAPIError(err)
	}
	if err := sessionmove.PushBranch(ctx, wt, c, sessionmove.GitHubFileLimit); err != nil {
		return MoveExport{}, moveAPIError(err)
	}
	out := MoveExport{Changes: len(c.Changes), Submodules: c.Submodules}

	var prs []string
	if list, err := s.store.ListPRsBySession(ctx, id); err == nil {
		for _, pr := range list {
			if !pr.Merged && !pr.Closed {
				prs = append(prs, pr.URL)
			}
		}
	}
	out.Manifest = MoveManifest{
		SourceSessionID: id, SourceLabel: label, RepoKey: repoKey,
		Branch: c.Branch, Tip: c.Tip(), WIP: c.Commit != "",
		Harness: rec.Harness, Model: rec.Metadata.Model, Effort: rec.Metadata.Effort,
		Permissions: rec.Metadata.Permissions, Mode: rec.Mode, DisplayName: rec.DisplayName,
		IssueID: rec.IssueID, RequestedOutput: rec.RequestedOutput, PullRequests: prs,
		AutoReview: rec.AutoReviewEnabled, TerminateOnPRMerge: rec.TerminateOnPRMerge,
		AutoInjectReview: rec.AutoInjectReview, AutoInjectCI: rec.AutoInjectCI,
	}
	if s.moveNotes != nil {
		if out.HandoffNote, err = s.moveNotes.MoveNote(ctx, rec, label, out.Manifest.WIP, CloudPromptMaxBytes); err != nil {
			return MoveExport{}, err
		}
	}
	return out, nil
}

// MovePreview is what a move to AO Cloud would push now.
type MovePreview struct {
	Branch     string
	Changes    []sessionmove.Change
	Transcript MoveTranscriptInfo
}

// MoveTranscriptInfo says whether a move would carry the agent's conversation.
type MoveTranscriptInfo struct {
	Bytes int64
	// Unavailable is why it would not: "unsupported", "not_found" or "too_large".
	Unavailable string
}

// CloudTranscriptMaxBytes is the largest conversation a move carries: AO Cloud
// keeps a session's transcript up to 64 MiB.
const CloudTranscriptMaxBytes = 64 << 20

// cloudTranscripts reads a session's agent conversation for a move to AO Cloud.
type cloudTranscripts interface {
	CloudTranscriptSize(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (int64, error)
	WriteCloudTranscript(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, w io.Writer, maxBytes int64) (string, error)
}

// PreviewMove captures the session's worktree as ExportMove would, without
// pushing or moving its branch, so the app can show what a move publishes,
// and any refusal, before the agent stops.
func (s *Service) PreviewMove(ctx context.Context, id domain.SessionID) (MovePreview, error) {
	rec, project, _, err := s.moveSource(ctx, id, false)
	if err != nil {
		return MovePreview{}, err
	}
	wt := rec.Metadata.WorkspacePath
	c, err := sessionmove.CaptureWorktree(ctx, sessionmove.CaptureOptions{Worktree: wt, Exclude: project.Config.Symlinks})
	if err != nil {
		return MovePreview{}, moveAPIError(err)
	}
	if err := sessionmove.CheckPush(ctx, wt, c, sessionmove.GitHubFileLimit); err != nil {
		return MovePreview{}, moveAPIError(err)
	}
	return MovePreview{Branch: c.Branch, Changes: c.Changes, Transcript: s.moveTranscriptInfo(ctx, rec, project)}, nil
}

// moveSource loads a session that can move to AO Cloud and its project.
// stopped also requires its agent to have exited.
func (s *Service) moveSource(ctx context.Context, id domain.SessionID, stopped bool) (domain.SessionRecord, domain.ProjectRecord, string, error) {
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return rec, domain.ProjectRecord{}, "", err
	}
	if !found {
		return rec, domain.ProjectRecord{}, "", apierr.NotFound("SESSION_NOT_FOUND", "Session not found")
	}
	switch {
	case rec.MovedTo != nil:
		return rec, domain.ProjectRecord{}, "", apierr.Conflict("SESSION_MOVED", "This session already moved to Orchestrator Cloud.", nil)
	case rec.IsTerminated:
		return rec, domain.ProjectRecord{}, "", apierr.Conflict("SESSION_TERMINATED", "Restore the session before moving it.", nil)
	case rec.Kind != domain.KindWorker:
		return rec, domain.ProjectRecord{}, "", apierr.Conflict("MOVE_UNSUPPORTED_SESSION", "Only worker sessions can move.", nil)
	case stopped && rec.Activity.State != domain.ActivityExited:
		return rec, domain.ProjectRecord{}, "", apierr.Conflict("MOVE_AGENT_RUNNING", "Stop the agent before moving the session.", nil)
	case rec.Metadata.WorkspacePath == "":
		return rec, domain.ProjectRecord{}, "", apierr.Conflict("MOVE_NO_WORKSPACE", "The session has no worktree to move.", nil)
	}
	project, found, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return rec, project, "", err
	}
	if !found || project.Kind.WithDefault() != domain.ProjectKindSingleRepo {
		return rec, project, "", apierr.Conflict("MOVE_UNSUPPORTED_PROJECT", "Only sessions in a single-repository project can move.", nil)
	}
	repoKey := domain.RepositoryKey(project.RepoOriginURL)
	if repoKey == "" {
		return rec, project, "", apierr.Conflict("MOVE_NO_ORIGIN", "The project has no origin repository Orchestrator Cloud can fetch from.", nil)
	}
	return rec, project, repoKey, nil
}

// ImportMove starts a session AO Cloud pushed, on this machine: it places the
// pushed branch, spawns a worker on it with the session's settings and handoff
// note, and claims its pull requests. A retry with the same move id returns
// the session it already created.
func (s *Service) ImportMove(ctx context.Context, in MoveImportInput) (MoveImportOutcome, error) {
	m := in.Manifest
	if in.MoveID == "" || m.Branch == "" || m.Tip == "" || m.Harness == "" {
		return MoveImportOutcome{}, apierr.Invalid("MOVE_MANIFEST_INVALID", "moveId, branch, tip and harness are required", nil)
	}
	hash := moveRequestHash(in)
	if rec, found, err := s.replayClientRequest(ctx, in.MoveID, hash); err != nil {
		return MoveImportOutcome{}, err
	} else if found {
		sess, err := s.toSession(ctx, rec)
		return MoveImportOutcome{Session: sess}, err
	}
	project, found, err := s.store.GetProject(ctx, string(in.ProjectID))
	if err != nil {
		return MoveImportOutcome{}, err
	}
	if !found {
		return MoveImportOutcome{}, apierr.NotFound("PROJECT_NOT_FOUND", "Project not found")
	}
	if key := domain.RepositoryKey(project.RepoOriginURL); key == "" || key != m.RepoKey {
		return MoveImportOutcome{}, apierr.Conflict("MOVE_REPO_MISMATCH",
			"This project is a different repository from the moved session's.", map[string]any{"repoKey": key, "want": m.RepoKey})
	}
	if err := sessionmove.ImportBranch(ctx, project.Path, m.Branch, m.Tip); err != nil {
		return MoveImportOutcome{}, moveAPIError(err)
	}

	prompt := in.HandoffNote
	if prompt == "" {
		prompt = "This Orchestrator-managed session moved here from " + m.SourceLabel + ". Check the branch's files and git log, then wait for the user."
	}
	sess, _, _, err := s.Spawn(ctx, ports.SpawnConfig{
		ProjectID: in.ProjectID, IssueID: m.IssueID, ClientRequestID: in.MoveID, ClientRequestHash: hash,
		Kind: domain.KindWorker, Harness: m.Harness, Branch: m.Branch, Prompt: prompt,
		AgentConfig:     domain.AgentConfig{Model: m.Model, Effort: m.Effort, Permissions: m.Permissions},
		EffortOverride:  m.Effort != "",
		RequestedMode:   m.Mode,
		RequestedOutput: m.RequestedOutput,
		DisplayName:     m.DisplayName, KeepDisplayName: m.DisplayName != "",
	})
	if err != nil {
		return MoveImportOutcome{}, err
	}
	out := MoveImportOutcome{Session: sess}
	for _, url := range m.PullRequests {
		if _, err := s.ClaimPR(ctx, sess.ID, url, ClaimPROptions{AllowTakeover: true}); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("Could not claim %s: %v", url, err))
		}
	}
	for _, set := range []func() error{
		func() error { _, err := s.SetAutoReview(ctx, sess.ID, m.AutoReview); return err },
		func() error { _, err := s.SetTerminateOnPRMerge(ctx, sess.ID, m.TerminateOnPRMerge); return err },
		func() error { _, err := s.SetAutoInjectReview(ctx, sess.ID, m.AutoInjectReview); return err },
		func() error { _, err := s.SetAutoInjectCI(ctx, sess.ID, m.AutoInjectCI); return err },
	} {
		if err := set(); err != nil {
			out.Warnings = append(out.Warnings, "Could not copy a session setting: "+err.Error())
		}
	}
	if fresh, err := s.Get(ctx, sess.ID); err == nil {
		out.Session = fresh
	}
	return out, nil
}

// CompleteMove marks the session as moved to AO Cloud, then archives it. A
// live orchestrator of the project is told so it does not respawn the worker;
// the move does not wait for it to take the message.
func (s *Service) CompleteMove(ctx context.Context, id domain.SessionID, in MoveCompleteInput) (domain.Session, error) {
	d := in.Destination
	if d.MoveID == "" || d.SessionID == "" || d.OrgID == "" {
		return domain.Session{}, apierr.Invalid("MOVE_DESTINATION_INVALID", "moveId, orgId and sessionId are required", nil)
	}
	store, ok := s.store.(sessionMoveStore)
	if !ok {
		return domain.Session{}, apierr.NotImplemented("MOVE_UNSUPPORTED", "This build cannot record session moves")
	}
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	if !found {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Session not found")
	}
	if rec.MovedTo != nil && rec.MovedTo.MoveID != d.MoveID {
		return domain.Session{}, apierr.Conflict("SESSION_MOVED", "This session already moved to Orchestrator Cloud.", nil)
	}
	if rec.MovedTo == nil {
		d.At = s.clock().UTC()
		if _, err := store.SetSessionMovedTo(ctx, id, d, d.At); err != nil {
			return domain.Session{}, err
		}
		go s.tellOrchestrator(context.WithoutCancel(ctx), rec, d)
	}
	if !rec.IsTerminated {
		if _, err := s.manager.RequestKill(ctx, id); err != nil {
			return domain.Session{}, toAPIError(err)
		}
	}
	return s.Get(ctx, id)
}

func (s *Service) tellOrchestrator(ctx context.Context, rec domain.SessionRecord, d domain.SessionMove) {
	if rec.ProjectID == "" {
		return
	}
	orchestrators, err := s.activeOrchestrators(ctx, rec.ProjectID)
	if err != nil {
		return
	}
	msg := fmt.Sprintf("Worker %s moved to Orchestrator Cloud as session %s and continues there. Do not respawn it.", rec.ID, d.SessionID)
	for _, o := range orchestrators {
		if err := s.manager.Send(ctx, o.ID, msg, nil); err != nil {
			s.logger.WarnContext(ctx, "tell orchestrator about a session move", "orchestrator", o.ID, "err", err)
		}
	}
}

// machineName is this computer's name as the user sees it, for a move whose
// caller did not name the source.
func machineName() string {
	name, err := os.Hostname()
	if name = strings.TrimSuffix(strings.TrimSpace(name), ".local"); err != nil || name == "" {
		return "another machine"
	}
	return name
}

func moveRequestHash(in MoveImportInput) string {
	sum := sha256.Sum256([]byte(string(in.ProjectID) + "\x00" + in.Manifest.Branch + "\x00" + in.Manifest.Tip))
	return hex.EncodeToString(sum[:])
}

// moveAPIError turns a move refusal into a 409 the app can explain.
func moveAPIError(err error) error {
	var fileTooLarge *sessionmove.FileTooLargeError
	if errors.As(err, &fileTooLarge) {
		var paths []string
		for _, f := range fileTooLarge.Files {
			if !slices.Contains(paths, f.Path) {
				paths = append(paths, f.Path)
			}
		}
		return apierr.Conflict("MOVE_FILE_TOO_LARGE", "Over GitHub's 100 MiB file limit: "+strings.Join(paths, ", ")+".",
			map[string]any{"limit": fileTooLarge.Limit, "files": fileTooLarge.Files})
	}
	for _, c := range []struct {
		err  error
		code string
		msg  string
	}{
		{sessionmove.ErrOperationInProgress, "MOVE_GIT_OPERATION", "Finish or abort the merge, rebase or conflict in the worktree first."},
		{sessionmove.ErrDetachedHead, "MOVE_DETACHED_HEAD", "The worktree has no branch checked out."},
		{sessionmove.ErrNoCommits, "MOVE_NO_COMMITS", "The branch has no commits yet."},
		{sessionmove.ErrDirtySubmodule, "MOVE_DIRTY_SUBMODULE", "Commit or discard the changes inside %s first."},
		{sessionmove.ErrRemoteAhead, "MOVE_REMOTE_AHEAD", "Origin's branch has commits this machine does not. Pull them first."},
		{sessionmove.ErrLFSObjects, "MOVE_LFS", "Git LFS files cannot move yet: %s."},
		{sessionmove.ErrDiverged, "MOVE_BRANCH_DIVERGED", "The branch here has commits the moved branch does not."},
		{sessionmove.ErrBranchCheckedOut, "MOVE_BRANCH_CHECKED_OUT", "The branch is checked out in a worktree here."},
		{sessionmove.ErrMissingCommit, "MOVE_MISSING_COMMIT", "Origin does not have the commit Orchestrator Cloud pushed."},
	} {
		if errors.Is(err, c.err) {
			msg := c.msg
			if strings.Contains(msg, "%s") {
				// sessionmove formats these as "<sentinel>: <paths>".
				msg = fmt.Sprintf(msg, strings.TrimPrefix(err.Error(), c.err.Error()+": "))
			}
			return apierr.Conflict(c.code, msg, map[string]any{"detail": err.Error()})
		}
	}
	return err
}

func (s *Service) moveTranscriptInfo(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) MoveTranscriptInfo {
	t, ok := s.manager.(cloudTranscripts)
	if !ok {
		return MoveTranscriptInfo{Unavailable: "unsupported"}
	}
	size, err := t.CloudTranscriptSize(ctx, rec, project)
	switch {
	case errors.Is(err, sessionmanager.ErrCloudTranscriptUnsupported):
		return MoveTranscriptInfo{Unavailable: "unsupported"}
	case err != nil:
		return MoveTranscriptInfo{Unavailable: "not_found"}
	case size > CloudTranscriptMaxBytes:
		return MoveTranscriptInfo{Bytes: size, Unavailable: "too_large"}
	}
	return MoveTranscriptInfo{Bytes: size}
}

// MoveTranscript is a stopped session's conversation for AO Cloud.
type MoveTranscript struct {
	AgentSessionID string
	// Gzip is the transcript, gzipped, with this machine's paths replaced by
	// the placeholders AO Cloud's worker fills in.
	Gzip []byte
}

// MoveTranscript returns a stopped session's agent conversation for a move to
// AO Cloud, which resumes it instead of starting from the handoff note.
func (s *Service) MoveTranscript(ctx context.Context, id domain.SessionID) (MoveTranscript, error) {
	rec, project, _, err := s.moveSource(ctx, id, true)
	if err != nil {
		return MoveTranscript{}, err
	}
	t, ok := s.manager.(cloudTranscripts)
	if !ok {
		return MoveTranscript{}, apierr.NotImplemented("MOVE_UNSUPPORTED", "This build cannot read agent conversations")
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	nativeID, err := t.WriteCloudTranscript(ctx, rec, project, zw, CloudTranscriptMaxBytes)
	if err == nil {
		err = zw.Close()
	}
	switch {
	case errors.Is(err, sessionmanager.ErrCloudTranscriptUnsupported):
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_UNSUPPORTED", "Orchestrator Cloud can resume only Claude Code conversations.", nil)
	case errors.Is(err, nativetransfer.ErrTooLarge):
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_TOO_LARGE", "The conversation is over Orchestrator Cloud's 64 MiB limit.", map[string]any{"detail": err.Error()})
	case err != nil:
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_UNAVAILABLE", "The agent's conversation could not be read.", map[string]any{"detail": err.Error()})
	}
	return MoveTranscript{AgentSessionID: nativeID, Gzip: buf.Bytes()}, nil
}
