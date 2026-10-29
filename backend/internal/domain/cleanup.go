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
}
