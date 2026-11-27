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
	"log/slog"
	"os"
	"path/filepath"
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
	// wipCommit is the commit the move made of the uncommitted work, or "".
	MoveNote(ctx context.Context, rec domain.SessionRecord, movedFrom, wipCommit string, maxBytes int) (string, error)
}

type sessionMoveStore interface {
	MarkSessionMoved(ctx context.Context, id domain.SessionID, move domain.SessionMove, updatedAt time.Time) (bool, error)
}

type sessionMoveStartedStore interface {
	SetSessionMoveStarted(ctx context.Context, id domain.SessionID, moveID string, updatedAt time.Time) (bool, error)
}

type sessionMoveImportStore interface {
	SetSessionMoveConversationPlaced(ctx context.Context, id domain.SessionID, updatedAt time.Time) (bool, error)
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
	// Transcript is the agent's conversation in AO Cloud. The session resumes
	// it, and starts from HandoffNote only when it cannot be placed.
	Transcript *MoveImportTranscript
}

// MoveImportTranscript is an agent conversation an AO Cloud sandbox kept.
type MoveImportTranscript struct {
	// ProjectDir is the folder it was in. For Claude Code its paths, and the
	// sandbox's working directory, become this machine's.
	ProjectDir string
	// AgentSessionID names the conversation; Gzip is its transcript.
	AgentSessionID string
	Gzip           []byte
	// SidecarGzip is its sidecar folder as a gzipped tar, or nil.
	SidecarGzip []byte
}

// MoveImportOutcome is the new session and anything that did not carry over.
type MoveImportOutcome struct {
	Session  domain.Session
	Warnings []string
	// ConversationIncomplete says the conversation, or its sidecar folder,
	// came with the move but was not placed here, so the source holds the
	// only full copy.
	ConversationIncomplete bool
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
		label = defaultMoveSource
	}
	wt := rec.Metadata.WorkspacePath
	if _, err := sessionmove.RefuseDefaultBranch(ctx, wt, project.Config.WorktreeBaseBranch()); err != nil {
		return MoveExport{}, s.moveAPIError(ctx, err)
	}
	c, err := sessionmove.CaptureWorktree(ctx, sessionmove.CaptureOptions{Worktree: wt, Exclude: project.Config.Symlinks})
	if err != nil {
		return MoveExport{}, s.moveAPIError(ctx, err)
	}
	out := MoveExport{Changes: len(c.Changes), Submodules: c.Submodules}

	// Everything that can fail without pushing runs first, so a failed export
	// leaves origin and the local branch as they were.
	prs, err := s.openPullRequests(ctx, id)
	if err != nil {
		return MoveExport{}, err
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
		if out.HandoffNote, err = s.moveNotes.MoveNote(ctx, rec, label, c.Commit, CloudPromptMaxBytes); err != nil {
			return MoveExport{}, err
		}
	}
	// Recorded before the push, since a push cut short may still have reached
	// origin and AO Cloud may then start the session: restoring at startup
	// leaves it stopped here until the move finishes or the user resumes it.
	// A failed push leaves it set too; the app's rollback (resume-agent, or an
	// explicit restore) ends it, and a later move reuses its id.
	if store, ok := s.store.(sessionMoveStartedStore); ok {
		if _, err := store.SetSessionMoveStarted(ctx, id, in.MoveID, s.clock().UTC()); err != nil {
			return MoveExport{}, err
		}
	}
	if err := sessionmove.PushBranch(ctx, wt, c, sessionmove.GitHubFileLimit); err != nil {
		return MoveExport{}, s.moveAPIError(ctx, err)
	}
	return out, nil
}

// MovePreview is what a move to AO Cloud would push now.
type MovePreview struct {
	Branch string
	// DefaultBranch is the repository's default branch, which a Cloud project
	// the move creates gets; "" when it cannot be resolved.
	DefaultBranch string
	Changes       []sessionmove.Change
	// Withheld lists the secret environment files that stay on this computer.
	Withheld   []string
	Transcript MoveTranscriptInfo
}

// MoveTranscriptInfo says whether a move would carry the agent's conversation.
type MoveTranscriptInfo struct {
	// Bytes counts the transcript and the sidecar folder that moves with it.
	Bytes int64
	// Subagents counts the subagent transcripts in the sidecar folder.
	Subagents int
	// Unavailable is why it would not: "unsupported", "not_found" or
	// "too_large" (over CloudTranscriptMaxBytes, or over AO Cloud's upload
	// limit once gzipped).
	Unavailable string
	// SidecarUnavailable is "too_large" when the sidecar folder is over AO
	// Cloud's limits: the transcript moves without it.
	SidecarUnavailable string
}

// CloudTranscriptMaxBytes is the largest conversation a move carries: AO Cloud
// keeps a session's transcript up to 64 MiB.
const CloudTranscriptMaxBytes = 64 << 20

// cloudUploadMaxBytes is the most AO Cloud takes in one upload of a gzipped
// conversation, or of its gzipped sidecar folder.
var cloudUploadMaxBytes = 32 << 20

// countingWriter counts what is written through it.
type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// cloudTranscripts reads a session's agent conversation for a move to AO Cloud.
type cloudTranscripts interface {
	CloudTranscriptSize(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (int64, error)
	WriteCloudTranscript(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, w io.Writer, maxBytes int64) (string, error)
	CloudSidecarInfo(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (nativetransfer.ClaudeSidecar, error)
	WriteCloudSidecar(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, w io.Writer, maxBytes int64) (bool, error)
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
	defaultBranch, err := sessionmove.RefuseDefaultBranch(ctx, wt, project.Config.WorktreeBaseBranch())
	if err != nil {
		return MovePreview{}, s.moveAPIError(ctx, err)
	}
	c, err := sessionmove.CaptureWorktree(ctx, sessionmove.CaptureOptions{Worktree: wt, Exclude: project.Config.Symlinks})
	if err != nil {
		return MovePreview{}, s.moveAPIError(ctx, err)
	}
	if err := sessionmove.CheckPush(ctx, wt, c, sessionmove.GitHubFileLimit); err != nil {
		return MovePreview{}, s.moveAPIError(ctx, err)
	}
	return MovePreview{Branch: c.Branch, DefaultBranch: defaultBranch, Changes: c.Changes, Withheld: c.Withheld, Transcript: s.moveTranscriptInfo(ctx, rec, project)}, nil
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
	// Orchestrator Cloud's opencode is OpenCode 2, which this machine runs as opencode-v2.
	if in.Manifest.Harness == domain.HarnessOpenCode {
		in.Manifest.Harness = domain.HarnessOpenCodeV2
	}
	m := in.Manifest
	if !validPermissionMode(m.Permissions) {
		m.Permissions = ""
	}
	// An interface this machine does not know leaves the choice to its default.
	if m.Mode != "" && !m.Mode.Valid() {
		m.Mode = ""
	}
	// AO Cloud names a GitHub issue owner/repo#N; this machine's tracker ids
	// carry their provider (github:owner/repo#N), which intake matches on.
	if id := strings.TrimSpace(string(m.IssueID)); id != "" && !strings.Contains(id, ":") {
		m.IssueID = domain.IssueID(string(domain.TrackerProviderGitHub) + ":" + id)
	}
	if in.MoveID == "" || m.Branch == "" || m.Tip == "" || m.Harness == "" {
		return MoveImportOutcome{}, apierr.Invalid("MOVE_MANIFEST_INVALID", "moveId, branch, tip and harness are required", nil)
	}
	// git would resolve any revision, such as HEAD, in place of the pushed commit.
	if !fullCommitSHA.MatchString(m.Tip) {
		return MoveImportOutcome{}, apierr.Invalid("MOVE_MANIFEST_INVALID", "tip must be a full commit id", nil)
	}
	hash := moveRequestHash(in)
	if rec, found, err := s.replayClientRequest(ctx, in.MoveID, hash); err != nil {
		return MoveImportOutcome{}, err
	} else if found {
		sess, err := s.toSession(ctx, rec)
		// Only a placement recorded after the spawn counts: a crash or a failed
		// write in between leaves the source's copy in place.
		incomplete := carriesConversation(in) && (!rec.MoveConversationPlaced || conversationNotPlaced(in, sess))
		return MoveImportOutcome{Session: sess, ConversationIncomplete: incomplete}, err
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
		return MoveImportOutcome{}, s.moveAPIError(ctx, err)
	}

	// A session that moved from here to this Cloud session supplies what Cloud
	// did not send: its name and its approval mode.
	// It also supplies the tracker issue and the pull requests it had here.
	if origin, ok := s.sessionMovedTo(ctx, m.SourceSessionID); ok {
		if m.DisplayName == "" {
			m.DisplayName = origin.DisplayName
		}
		if m.Permissions == "" {
			m.Permissions = origin.Metadata.Permissions
		}
		if m.IssueID == "" {
			m.IssueID = origin.IssueID
		}
		if len(m.PullRequests) == 0 {
			if prs, err := s.openPullRequests(ctx, origin.ID); err == nil {
				m.PullRequests = prs
			}
		}
	}
	// AO Cloud writes paths under the user's home folder with a placeholder.
	prompt := nativetransfer.FillCloudHomeText(in.HandoffNote)
	if prompt == "" {
		prompt = "This Orchestrator-managed session moved here from " + m.SourceLabel + ". Check the branch's files and git log, then wait for the user."
	}
	wipNotice := moveWIPNotice(m)
	if wipNotice != "" && !strings.Contains(prompt, m.Tip) {
		prompt += "\n\n" + wipNotice
	}
	cfg := ports.SpawnConfig{
		ProjectID: in.ProjectID, IssueID: m.IssueID, ClientRequestID: in.MoveID, ClientRequestHash: hash,
		Kind: domain.KindWorker, Harness: m.Harness, Branch: m.Branch, Prompt: prompt,
		AgentConfig:     domain.AgentConfig{Model: m.Model, Effort: m.Effort, Permissions: m.Permissions},
		EffortOverride:  m.Effort != "",
		RequestedMode:   m.Mode,
		RequestedOutput: m.RequestedOutput,
		// The session's own name outranks the title Claude Code gives its terminal.
		DisplayName: m.DisplayName, KeepDisplayName: m.DisplayName != "", DisplayNameExplicit: m.DisplayName != "",
	}
	var warnings []string
	incomplete := false
	if t := in.Transcript; t != nil && movesConversation(m.Harness) {
		dir, err := os.MkdirTemp("", "ao-move-import-*")
		if err != nil {
			return MoveImportOutcome{}, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		archive, sidecarWarning, sidecarErr, err := writeImportArchive(ctx, dir, m.Harness, *t)
		if sidecarWarning != "" {
			s.moveLog().WarnContext(ctx, "move: sidecar folder stays in AO Cloud", "move", in.MoveID, "err", sidecarErr)
			warnings = append(warnings, sidecarWarning)
			incomplete = true
		}
		if err != nil {
			s.moveLog().WarnContext(ctx, "move: conversation cannot be read", "move", in.MoveID, "err", err)
			warnings = append(warnings, "The conversation could not be read, so the session starts from the handoff note.")
			incomplete = true
		} else {
			// The resumed conversation holds the task; the note is only its fallback.
			cfg.Transfer = &ports.SpawnTransfer{NativeArchive: archive, FallbackPrompt: prompt, SystemNote: wipNotice}
			cfg.Prompt = ""
		}
	}
	// A session in Chat where it came from starts here in Chat when its agent
	// runs in Chat on this computer, and otherwise in the terminal, saying so.
	// A carried conversation resumes in its CLI's terminal first (Spawn starts
	// it there) and switches to Chat once it has.
	chat := m.Mode == domain.SessionModeChat
	if chat && cfg.Transfer != nil {
		cfg.RequestedMode = domain.SessionModeTUI
	}
	sess, _, _, err := s.Spawn(ctx, cfg)
	if err != nil && cfg.RequestedMode == domain.SessionModeChat && chatUnavailable(err) {
		// An explicit Chat this computer cannot run costs nothing, so the
		// terminal start runs under the same move.
		s.moveLog().WarnContext(ctx, "move: Chat unavailable here; starting in the terminal", "move", in.MoveID, "err", err)
		cfg.RequestedMode = domain.SessionModeTUI
		sess, _, _, err = s.Spawn(ctx, cfg)
		if err == nil {
			warnings = append(warnings, movedChatInTerminal)
		}
	}
	if err != nil {
		return MoveImportOutcome{}, err
	}
	if chat && cfg.Transfer != nil {
		if err := s.switchMovedSessionToChat(ctx, sess.ID); err != nil {
			s.moveLog().WarnContext(ctx, "move: the moved session stays in the terminal", "move", in.MoveID, "err", err)
			warnings = append(warnings, movedChatInTerminal)
		}
	}
	if cfg.Transfer != nil && conversationNotPlaced(in, sess) {
		warnings = append(warnings, "The conversation could not be placed here, so the session starts from the handoff note.")
		incomplete = true
	}
	out := MoveImportOutcome{Session: sess, Warnings: warnings, ConversationIncomplete: incomplete}
	// A replay reads this; without it the replay reports the conversation
	// incomplete, which only keeps the source's copy. So a failed write needs no warning.
	if store, ok := s.store.(sessionMoveImportStore); ok && carriesConversation(in) && !incomplete {
		_, _ = store.SetSessionMoveConversationPlaced(ctx, sess.ID, s.clock().UTC())
	}
	for _, url := range m.PullRequests {
		if _, err := s.ClaimPR(ctx, sess.ID, url, ClaimPROptions{AllowTakeover: true}); err != nil {
			s.moveLog().WarnContext(ctx, "move: claim a pull request", "session", sess.ID, "pr", url, "err", err)
			out.Warnings = append(out.Warnings, fmt.Sprintf("Could not claim %s: %s", url, publicMessage(err, "claim it from the session menu")))
		}
	}
	for _, set := range []func() error{
		func() error { _, err := s.SetAutoReview(ctx, sess.ID, m.AutoReview); return err },
		func() error { _, err := s.SetTerminateOnPRMerge(ctx, sess.ID, m.TerminateOnPRMerge); return err },
		func() error { _, err := s.SetAutoInjectReview(ctx, sess.ID, m.AutoInjectReview); return err },
		func() error { _, err := s.SetAutoInjectCI(ctx, sess.ID, m.AutoInjectCI); return err },
	} {
		if err := set(); err != nil {
			s.moveLog().WarnContext(ctx, "move: copy a session setting", "session", sess.ID, "err", err)
			out.Warnings = append(out.Warnings, "Could not copy a session setting; check the session's settings.")
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
//
// The mark is one conditional write: it holds only while the move that pushed
// the session is still its move in progress and the agent is stopped. A
// session the user resumed here meanwhile (a retried complete arriving late)
// is refused with MOVE_NOT_IN_PROGRESS and never archived.
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
		marked, err := store.MarkSessionMoved(ctx, id, d, d.At)
		if err != nil {
			return domain.Session{}, err
		}
		if !marked {
			// Read again: a concurrent complete of the same move may have marked it.
			now, found, err := s.store.GetSession(ctx, id)
			if err != nil {
				return domain.Session{}, err
			}
			if !found || now.MovedTo == nil || now.MovedTo.MoveID != d.MoveID {
				return domain.Session{}, apierr.Conflict("MOVE_NOT_IN_PROGRESS",
					"This session was resumed on this computer after the move started, so it stays here. Its Orchestrator Cloud copy is still there; archive the one you don't need.", nil)
			}
			rec = now
		} else {
			go s.tellOrchestrator(context.WithoutCancel(ctx), rec, d)
		}
	}
	if !rec.IsTerminated {
		// The branch continues in AO Cloud. A worktree the archive keeps for
		// files the move left here (an untracked .env) must not hold it.
		if wt := rec.Metadata.WorkspacePath; wt != "" {
			if err := sessionmove.ReleaseBranch(ctx, wt); err != nil {
				s.moveLog().WarnContext(ctx, "move: release the branch in the moved worktree", "session", id, "err", err)
			}
		}
		if _, err := s.manager.RequestKill(ctx, id); err != nil {
			return domain.Session{}, toAPIError(err)
		}
	}
	return s.Get(ctx, id)
}

// moveNoticeTimeout bounds telling the orchestrator, which runs detached from
// the request.
const moveNoticeTimeout = 30 * time.Second

func (s *Service) tellOrchestrator(ctx context.Context, rec domain.SessionRecord, d domain.SessionMove) {
	if rec.ProjectID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, moveNoticeTimeout)
	defer cancel()
	orchestrators, err := s.activeOrchestrators(ctx, rec.ProjectID)
	if err != nil {
		return
	}
	msg := fmt.Sprintf("Worker %s moved to Orchestrator Cloud as session %s and continues there. Do not respawn it.", rec.ID, d.SessionID)
	for _, o := range orchestrators {
		if err := s.manager.Send(ctx, o.ID, msg, nil); err != nil {
			s.moveLog().WarnContext(ctx, "tell orchestrator about a session move", "orchestrator", o.ID, "err", err)
		}
	}
}

// defaultMoveSource names this computer in a moved session's note when the
// caller does not. It is not the hostname, which often holds the user's name.
const defaultMoveSource = "the user's computer"

func moveRequestHash(in MoveImportInput) string {
	sum := sha256.Sum256([]byte(string(in.ProjectID) + "\x00" + in.Manifest.Branch + "\x00" + in.Manifest.Tip))
	return hex.EncodeToString(sum[:])
}

// moveAPIError is moveAPIError for a request: git's raw output for a failure
// it explains without it, or one it cannot explain, goes to the log only.
func (s *Service) moveAPIError(ctx context.Context, err error) error {
	mapped := moveAPIError(err)
	var apiErr *apierr.Error
	if errors.As(mapped, &apiErr) {
		if slices.ContainsFunc(quietMoveErrors, func(e error) bool { return errors.Is(err, e) }) {
			s.moveLog().WarnContext(ctx, "move: git failed", "code", apiErr.Code, "err", err)
		}
		return mapped
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return apierr.InternalCause("MOVE_GIT_FAILED", "Git failed while moving the session. The Orchestrator log has the details.", err)
}

// quietMoveErrors are refusals whose message says what to fix, so git's raw
// output stays out of the response.
var quietMoveErrors = []error{
	sessionmove.ErrRemoteAuth, sessionmove.ErrRemoteRejected, sessionmove.ErrRemoteUnreachable, sessionmove.ErrRemoteAhead,
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
		{sessionmove.ErrSubmoduleUnpushed, "MOVE_SUBMODULE_UNPUSHED", "Submodule %s is at a commit its remote does not have. Push it first."},
		{sessionmove.ErrDiverged, "MOVE_BRANCH_DIVERGED", "The branch here has commits the moved branch does not."},
		{sessionmove.ErrBranchCheckedOut, "MOVE_BRANCH_CHECKED_OUT", "The branch is checked out in a worktree here."},
		{sessionmove.ErrLocalAhead, "MOVE_LOCAL_AHEAD", "%s here has commits that Orchestrator Cloud's does not. Push them first."},
		{sessionmove.ErrMissingCommit, "MOVE_MISSING_COMMIT", "Origin does not have the commit Orchestrator Cloud pushed."},
		{sessionmove.ErrDefaultBranch, "MOVE_DEFAULT_BRANCH", "The session is on %s, the repository's default branch. A move pushes the session's uncommitted work to its branch, so only a session on its own branch can move."},
		{sessionmove.ErrRemoteRejected, "MOVE_PUSH_REJECTED", "Origin refused the push, for example because the branch is protected. Only a session on a branch you can push to can move."},
		{sessionmove.ErrRemoteAuth, "MOVE_ORIGIN_AUTH", "Origin refused this computer's Git credentials. Check that you can fetch and push the repository here, then try again."},
		{sessionmove.ErrRemoteUnreachable, "MOVE_ORIGIN_UNREACHABLE", "Could not reach origin. Check the network connection, then try again."},
	} {
		if errors.Is(err, c.err) {
			msg := c.msg
			if strings.Contains(msg, "%s") {
				// sessionmove formats these as "<sentinel>: <paths>".
				msg = fmt.Sprintf(msg, strings.TrimPrefix(err.Error(), c.err.Error()+": "))
			}
			if slices.Contains(quietMoveErrors, c.err) {
				return apierr.Conflict(c.code, msg, nil)
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
	case errors.Is(err, sessionmanager.ErrCloudTranscriptUnsupported), errors.Is(err, nativetransfer.ErrSegmented):
		return MoveTranscriptInfo{Unavailable: "unsupported"}
	case err != nil:
		return MoveTranscriptInfo{Unavailable: "not_found"}
	case size > CloudTranscriptMaxBytes:
		return MoveTranscriptInfo{Bytes: size, Unavailable: "too_large"}
	}
	// AO Cloud takes the conversation gzipped, up to its upload limit.
	var gz countingWriter
	zw := gzip.NewWriter(&gz)
	if _, err := t.WriteCloudTranscript(ctx, rec, project, zw, CloudTranscriptMaxBytes); err == nil && zw.Close() == nil && gz.n > int64(cloudUploadMaxBytes) {
		return MoveTranscriptInfo{Bytes: size, Unavailable: "too_large"}
	}
	info := MoveTranscriptInfo{Bytes: size}
	// A sidecar folder that cannot move leaves the transcript moving alone,
	// which the app says before the move.
	if sidecar, err := t.CloudSidecarInfo(ctx, rec, project); err == nil && sidecar.Bytes > 0 {
		var packed countingWriter
		if _, err := t.WriteCloudSidecar(ctx, rec, project, &packed, CloudTranscriptMaxBytes); err != nil || packed.n > int64(cloudUploadMaxBytes) {
			info.SidecarUnavailable = "too_large"
		} else {
			info.Bytes += sidecar.Bytes
			info.Subagents = sidecar.Subagents
		}
	}
	return info
}

// MoveTranscript is a stopped session's conversation for AO Cloud.
type MoveTranscript struct {
	AgentSessionID string
	// Gzip is the transcript, gzipped, with this machine's paths replaced by
	// the placeholders AO Cloud's worker fills in.
	Gzip []byte
	// SidecarGzip is the sidecar folder as a gzipped tar, paths replaced the
	// same way; nil when there is none or it is over the limit.
	SidecarGzip []byte
	// SidecarOmitted says why a sidecar folder the conversation has stays
	// here: "too_large" (over AO Cloud's limits) or "unreadable".
	SidecarOmitted string
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
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_UNSUPPORTED", "Orchestrator Cloud can resume only Claude Code, Codex, OpenCode 2 and Cursor conversations.", nil)
	case errors.Is(err, nativetransfer.ErrSegmented):
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_UNSUPPORTED", "The Codex conversation continues in segment files, which a move does not carry yet.", nil)
	case errors.Is(err, nativetransfer.ErrTooLarge):
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_TOO_LARGE", "The conversation is over Orchestrator Cloud's 64 MiB limit.", map[string]any{"detail": err.Error()})
	case err != nil:
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_UNAVAILABLE", "The agent's conversation could not be read.", map[string]any{"detail": err.Error()})
	}
	if buf.Len() > cloudUploadMaxBytes {
		return MoveTranscript{}, apierr.Conflict("MOVE_TRANSCRIPT_TOO_LARGE",
			fmt.Sprintf("The conversation is over Orchestrator Cloud's %d MiB upload limit once compressed.", cloudUploadMaxBytes>>20), nil)
	}
	out := MoveTranscript{AgentSessionID: nativeID, Gzip: buf.Bytes()}
	var sidecar bytes.Buffer
	switch ok, err := t.WriteCloudSidecar(ctx, rec, project, &sidecar, CloudTranscriptMaxBytes); {
	case errors.Is(err, nativetransfer.ErrTooLarge), err == nil && sidecar.Len() > cloudUploadMaxBytes:
		out.SidecarOmitted = "too_large"
	case err != nil:
		s.moveLog().WarnContext(ctx, "move: sidecar folder stays behind", "session", id, "err", err)
		out.SidecarOmitted = "unreadable"
	case ok:
		out.SidecarGzip = sidecar.Bytes()
	}
	return out, nil
}

// MoveTarget is a project on this machine a session from AO Cloud could
// move into.
type MoveTarget struct {
	Project domain.ProjectRecord
	// Refusal says why the session cannot move into it, or is nil.
	Refusal *apierr.Error
}

type projectLister interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
}

// MoveTargets lists this machine's single-repository projects of repoKey and
// whether branch could be placed in each, as move/import would.
func (s *Service) MoveTargets(ctx context.Context, repoKey, branch string) ([]MoveTarget, error) {
	lister, ok := s.store.(projectLister)
	if !ok {
		return nil, apierr.NotImplemented("MOVE_UNSUPPORTED", "This build cannot list projects for a move")
	}
	projects, err := lister.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var out []MoveTarget
	for _, project := range projects {
		if !project.ArchivedAt.IsZero() || project.Kind.WithDefault() != domain.ProjectKindSingleRepo ||
			domain.RepositoryKey(project.RepoOriginURL) != repoKey {
			continue
		}
		target := MoveTarget{Project: project}
		if err := sessionmove.CheckImportBranch(ctx, project.Path, branch); err != nil {
			target.Refusal = s.moveTargetRefusal(ctx, project, branch, err)
		}
		out = append(out, target)
	}
	return out, nil
}

// moveTargetRefusal turns a CheckImportBranch error into the dialog's reason,
// naming the session that has the branch checked out when there is one.
func (s *Service) moveTargetRefusal(ctx context.Context, project domain.ProjectRecord, branch string, err error) *apierr.Error {
	if errors.Is(err, sessionmove.ErrBranchCheckedOut) {
		if sessions, listErr := s.store.ListSessions(ctx, domain.ProjectID(project.ID)); listErr == nil {
			for _, rec := range sessions {
				if !rec.IsTerminated && rec.Metadata.Branch == branch {
					name := rec.DisplayName
					if name == "" {
						name = string(rec.ID)
					}
					return apierr.Conflict("MOVE_BRANCH_CHECKED_OUT", fmt.Sprintf("%s is checked out here by %s. Archive that session first.", branch, name), nil)
				}
			}
		}
	}
	var refusal *apierr.Error
	if errors.As(s.moveAPIError(ctx, err), &refusal) && refusal.Kind == apierr.KindConflict {
		return refusal
	}
	s.moveLog().WarnContext(ctx, "move: check a branch for a move here", "project", project.ID, "branch", branch, "err", err)
	return apierr.Conflict("MOVE_CHECK_FAILED", "Could not check the branch here. The Orchestrator log has the details.", nil)
}

// publicMessage is an API error's own message, which is written for the user,
// or fallback for anything else, whose text may be raw tool output.
func publicMessage(err error, fallback string) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) && apiErr.Kind != apierr.KindInternal && apiErr.Message != "" {
		return apiErr.Message
	}
	return fallback
}

// movesConversation reports a harness whose conversation a move carries.
func movesConversation(h domain.AgentHarness) bool {
	return h == domain.HarnessClaudeCode || h == domain.HarnessCodex || h == domain.HarnessOpenCodeV2 || h == domain.HarnessCursor
}

// carriesConversation reports a move/import carrying a conversation to place.
func carriesConversation(in MoveImportInput) bool {
	return in.Transcript != nil && movesConversation(in.Manifest.Harness)
}

// conversationNotPlaced reports a moved conversation the session does not
// resume: the spawn records the conversation id only once it placed it.
func conversationNotPlaced(in MoveImportInput, sess domain.Session) bool {
	t := in.Transcript
	return carriesConversation(in) && sess.Metadata.AgentSessionID != t.AgentSessionID
}

// writeImportArchive unzips a transcript from AO Cloud, and its sidecar
// folder, into dir and writes the archive the spawn places it from: for
// Codex the rollout itself, for OpenCode 2 its session export document. A sidecar that cannot be read is left out and
// described in the warning.
// sidecarErr is the raw cause of the warning, for the log.
func writeImportArchive(ctx context.Context, dir string, harness domain.AgentHarness, t MoveImportTranscript) (archive, warning string, sidecarErr, err error) {
	zr, err := gzip.NewReader(bytes.NewReader(t.Gzip))
	if err != nil {
		return "", "", nil, err
	}
	transcript := filepath.Join(dir, t.AgentSessionID+".jsonl")
	if filepath.Base(transcript) != t.AgentSessionID+".jsonl" {
		return "", "", nil, fmt.Errorf("invalid conversation id %q", t.AgentSessionID)
	}
	f, err := os.OpenFile(transcript, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", nil, err
	}
	n, err := io.Copy(f, io.LimitReader(zr, CloudTranscriptMaxBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", nil, err
	}
	if n > CloudTranscriptMaxBytes {
		return "", "", nil, nativetransfer.ErrTooLarge
	}
	// AO Cloud writes paths under the user's home folder with a placeholder;
	// the JSON Lines transcripts get this machine's home folder back. OpenCode's
	// export document is filled in where it is placed, and a Cursor store is
	// binary.
	if harness == domain.HarnessClaudeCode || harness == domain.HarnessCodex {
		if err := nativetransfer.FillCloudHome(transcript); err != nil {
			return "", "", nil, err
		}
	}
	if harness != domain.HarnessClaudeCode {
		return transcript, "", nil, nil
	}
	if len(t.SidecarGzip) > 0 {
		sidecar := filepath.Join(dir, t.AgentSessionID)
		sidecarErr = nativetransfer.ExtractClaudeSidecar(bytes.NewReader(t.SidecarGzip), sidecar, CloudTranscriptMaxBytes)
		if sidecarErr == nil {
			sidecarErr = nativetransfer.FillCloudHome(sidecar)
		}
		if sidecarErr != nil {
			_ = os.RemoveAll(sidecar)
			warning = "Subagent transcripts and saved tool output stayed in Orchestrator Cloud."
		}
	}
	archive = filepath.Join(dir, "conversation.tgz")
	out, err := os.OpenFile(archive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", warning, sidecarErr, err
	}
	_, err = nativetransfer.WriteClaudeArchiveFromCloud(ctx, out, transcript, t.ProjectDir)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return archive, warning, sidecarErr, err
}

// openPullRequests lists the URLs of a session's pull requests that are
// neither merged nor closed: the ones a moved session keeps.
func (s *Service) openPullRequests(ctx context.Context, id domain.SessionID) ([]string, error) {
	list, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list pull requests of %s: %w", id, err)
	}
	var prs []string
	for _, pr := range list {
		if !pr.Merged && !pr.Closed {
			prs = append(prs, pr.URL)
		}
	}
	return prs, nil
}

// moveWIPNotice tells the agent the move committed the source's uncommitted
// work, so it does not take that commit for one it made; "" without one.
func moveWIPNotice(m MoveManifest) string {
	if !m.WIP {
		return ""
	}
	source := strings.TrimSpace(m.SourceLabel)
	if source == "" {
		source = "Orchestrator Cloud"
	}
	return fmt.Sprintf("This session moved here from %s. The move committed the work that was not committed there as commit %s on branch %s. Orchestrator made that commit, not you or the user. Keep it, amend it, or squash it.",
		source, m.Tip, m.Branch)
}

// validPermissionMode accepts an approval mode this machine knows, and none.
func validPermissionMode(mode domain.PermissionMode) bool {
	switch mode {
	case "", domain.PermissionModeDefault, domain.PermissionModeAcceptEdits, domain.PermissionModeAuto, domain.PermissionModeBypassPermissions:
		return true
	}
	return false
}

// sessionMovedTo finds the session on this machine that moved to AO Cloud
// session cloudSessionID, when there is one.
func (s *Service) sessionMovedTo(ctx context.Context, cloudSessionID domain.SessionID) (domain.SessionRecord, bool) {
	lister, ok := s.store.(interface {
		ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	})
	if cloudSessionID == "" || !ok {
		return domain.SessionRecord{}, false
	}
	recs, err := lister.ListAllSessions(ctx)
	if err != nil {
		return domain.SessionRecord{}, false
	}
	for _, rec := range recs {
		if rec.MovedTo != nil && rec.MovedTo.SessionID == string(cloudSessionID) {
			return rec, true
		}
	}
	return domain.SessionRecord{}, false
}

// movedChatInTerminal is the import's warning for a session that was in Chat
// where it came from and continues in the terminal here.
const movedChatInTerminal = "This computer can't run the session in Chat, so it continues in the terminal."

// switchMovedSessionToChat asks a moved session that resumed its conversation
// in the terminal to switch to Chat, as the user's Switch to Chat does: once
// its agent is idle, Chat takes over the same conversation.
func (s *Service) switchMovedSessionToChat(ctx context.Context, id domain.SessionID) error {
	manager, ok := s.manager.(interfaceTransitionCommander)
	if !ok {
		return errors.New("this build cannot switch session interfaces")
	}
	for attempt := 1; ; attempt++ {
		_, err := manager.StartInterfaceTransition(ctx, id, domain.SessionModeChat,
			domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
		// An agent whose resume does not name its conversation confirms it from
		// its own hook shortly after it starts: wait for that, a little while.
		if err == nil || !errors.Is(err, sessionmanager.ErrNativeConversationUnverified) || attempt >= moveChatSwitchAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(moveChatSwitchWait):
		}
	}
}

// How long a moved session's switch to Chat waits for its terminal launch to
// confirm the conversation it resumed. Tests shorten the wait.
var (
	moveChatSwitchAttempts = 15
	moveChatSwitchWait     = time.Second
)

// chatUnavailable reports a spawn refused because Chat cannot run here: the
// agent has no Chat on this computer, its driver is missing or incompatible,
// it is not signed in for Chat, or Chat refuses the approval mode.
func chatUnavailable(err error) bool {
	for _, target := range []error{ports.ErrChatUnsupported, ports.ErrChatDriverUnavailable, ports.ErrChatDriverIncompatible,
		ports.ErrChatAuthRequired, ports.ErrChatPermissionModeUnsupported} {
		if errors.Is(err, target) {
			return true
		}
	}
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "SESSION_MODE_UNSUPPORTED", "CHAT_PERMISSION_MODE_UNSUPPORTED", "CHAT_DRIVER_UNAVAILABLE", "CHAT_DRIVER_INCOMPATIBLE", "CHAT_AUTH_REQUIRED":
			return true
		}
	}
	return false
}

// moveLog is the service's logger, or one that drops records when it has none.
func (s *Service) moveLog() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.New(slog.DiscardHandler)
}
