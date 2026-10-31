package domain

import (
	"errors"
	"time"
)

// ErrSessionHistoryShared refuses deleting a session that a conversation it
// does not own still records (a project orchestrator conversation's turns or
// current session). Deleting the row would cascade those turns out of, or
// unlink it from, a conversation that outlives the session.
var ErrSessionHistoryShared = errors.New("session is part of a conversation it does not own")

// WorkspaceDisposition is the session-level rollup of a terminated session's
// workspace teardown, mirrored to session_cleanup_facts.workspace_disposition.
// Per-repo state for workspace projects stays in session_worktrees.state so a
// mixed clean/dirty multi-repo session is still representable; this is only the
// summary the UI renders. It is a durable teardown fact, never a display status.
type WorkspaceDisposition string

// Workspace dispositions.
const (
	// DispositionPending means teardown has not yet succeeded and is eligible to
	// be attempted or retried (subject to next_attempt_at).
	DispositionPending WorkspaceDisposition = "pending"
	// DispositionRemoved means the workspace was released cleanly.
	DispositionRemoved WorkspaceDisposition = "removed"
	// DispositionPreservedDirty means the worktree was kept because it had
	// uncommitted changes; auto-retry is paused until the user retries.
	DispositionPreservedDirty WorkspaceDisposition = "preserved_dirty"
	// DispositionFailed is the exhausted terminal state after the retry cap for a
	// non-dirty teardown failure: auto-retry stops and only a user-triggered retry
	// clears it. Distinct from DispositionPending, which is still auto-retrying.
	DispositionFailed WorkspaceDisposition = "failed"
	// DispositionNotApplicable means the session never had a workspace to release
	// (no-worktree / spawn-failed / orchestrator terminal sessions).
	DispositionNotApplicable WorkspaceDisposition = "not_applicable"
)

// Settled reports a disposition that leaves nothing for cleanup to retry: the
// workspace was released, or there never was one.
func (d WorkspaceDisposition) Settled() bool {
	return d == DispositionRemoved || d == DispositionNotApplicable
}

// CleanupSkipCode is the stable class of a workspace that cleanup left alone.
// Clients and scripts branch on it; the reason reported beside it is wording
// for people and may change.
type CleanupSkipCode string

// Cleanup skip codes.
const (
	// CleanupSkipDirty: the worktree has uncommitted changes and is kept.
	CleanupSkipDirty CleanupSkipCode = "dirty"
	// CleanupSkipLockedWorktree: git holds a deliberate lock on the worktree.
	CleanupSkipLockedWorktree CleanupSkipCode = "locked_worktree"
	// CleanupSkipMissingRepo: the project's repository is gone from disk.
	CleanupSkipMissingRepo CleanupSkipCode = "missing_repo"
	// CleanupSkipProjectUnresolvable: the project is archived or unregistered.
	CleanupSkipProjectUnresolvable CleanupSkipCode = "archived_project_unresolvable"
	// CleanupSkipPermissionDenied: the OS refused removing the worktree.
	CleanupSkipPermissionDenied CleanupSkipCode = "permission_error"
	// CleanupSkipScriptFailed: a project Cleanup step failed, so the worktree
	// was kept for a retry.
	CleanupSkipScriptFailed CleanupSkipCode = "cleanup_script_failed"
	// CleanupSkipWorktreeBusy: files in the worktree are still held open
	// (Windows); a later run retries.
	CleanupSkipWorktreeBusy CleanupSkipCode = "worktree_busy"
	// CleanupSkipInUse: a live session works in the same worktree.
	CleanupSkipInUse CleanupSkipCode = "in_use_by_live_session"
	// CleanupSkipShellTerminalOpen: a shell terminal in the worktree could not
	// be confirmed closed.
	CleanupSkipShellTerminalOpen CleanupSkipCode = "shell_terminal_open"
	// CleanupSkipAlreadyRunning: another cleanup of the session is running.
	CleanupSkipAlreadyRunning CleanupSkipCode = "cleanup_running"
	// CleanupSkipAttachmentsFailed: the session's attachments could not be
	// preserved first.
	CleanupSkipAttachmentsFailed CleanupSkipCode = "attachment_preservation_failed"
	// CleanupSkipTeardownFailed: any other failure; the daemon log has the cause.
	CleanupSkipTeardownFailed CleanupSkipCode = "teardown_failed"
)

// Retryable reports a refusal that says "not now" rather than "cannot": the
// worktree is busy or shared, so a later run may remove it.
func (c CleanupSkipCode) Retryable() bool {
	switch c {
	case CleanupSkipWorktreeBusy, CleanupSkipInUse, CleanupSkipShellTerminalOpen, CleanupSkipAlreadyRunning:
		return true
	}
	return false
}

// CleanupSkipCodes lists every CleanupSkipCode, for API schemas and tests.
var CleanupSkipCodes = []CleanupSkipCode{
	CleanupSkipDirty, CleanupSkipLockedWorktree, CleanupSkipMissingRepo, CleanupSkipProjectUnresolvable,
	CleanupSkipPermissionDenied, CleanupSkipScriptFailed, CleanupSkipWorktreeBusy, CleanupSkipInUse,
	CleanupSkipShellTerminalOpen, CleanupSkipAlreadyRunning, CleanupSkipAttachmentsFailed, CleanupSkipTeardownFailed,
}

// ArchivedSession is an archived session whose workspace is released, with
// when it was archived, for the retention that deletes old ones.
type ArchivedSession struct {
	ID         SessionID
	ArchivedAt time.Time
}

// SessionCleanupRecord is the persistence shape for one terminated session's
// runtime + workspace teardown facts (session_cleanup_facts). It records durable
// disposition only — never a derived display status. Nullable timestamps use the
// zero-time convention (zero = not set), bridged to SQL NULL by the store.
type SessionCleanupRecord struct {
	SessionID SessionID
	// SessionGeneration is the sessions.cleanup_generation this row was written
	// for; the finalizer compares it against the authoritative session counter.
	SessionGeneration int64
	// RuntimeReleasedAt is when the runtime handle was genuinely released; zero
	// means the runtime has not been confirmed released.
	RuntimeReleasedAt    time.Time
	WorkspaceDisposition WorkspaceDisposition
	// AttemptCount is the number of teardown attempts made so far, feeding the
	// retry cap that transitions a stuck cleanup to DispositionFailed.
	AttemptCount int64
	// LastAttemptAt is when the most recent teardown attempt ran; zero = never.
	LastAttemptAt time.Time
	// NextAttemptAt is when the next capped-backoff retry is due; zero = none
	// scheduled (e.g. terminal disposition or preserved-dirty pause).
	NextAttemptAt time.Time
	// FailureCode is a machine code for the last teardown failure; "" = none.
	FailureCode string
	// ReportOnly marks a result first recorded by a cleanup sweep rather than
	// by archiving the session: its failure is listed but raises no app error.
	ReportOnly bool
}
