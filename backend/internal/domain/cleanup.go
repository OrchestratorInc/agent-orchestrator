package domain

import (
	"errors"
	"time"
)

var ErrSessionHistoryShared = errors.New("session is part of a conversation it does not own")

type WorkspaceDisposition string

const (
	DispositionPending        WorkspaceDisposition = "pending"
	DispositionRemoved        WorkspaceDisposition = "removed"
	DispositionPreservedDirty WorkspaceDisposition = "preserved_dirty"
	DispositionFailed         WorkspaceDisposition = "failed"
	DispositionNotApplicable  WorkspaceDisposition = "not_applicable"
)

func (d WorkspaceDisposition) Settled() bool {
	return d == DispositionRemoved || d == DispositionNotApplicable
}

type CleanupSkipCode string

const (
	CleanupSkipDirty               CleanupSkipCode = "dirty"
	CleanupSkipLockedWorktree      CleanupSkipCode = "locked_worktree"
	CleanupSkipMissingRepo         CleanupSkipCode = "missing_repo"
	CleanupSkipProjectUnresolvable CleanupSkipCode = "archived_project_unresolvable"
	CleanupSkipPermissionDenied    CleanupSkipCode = "permission_error"
	CleanupSkipScriptFailed        CleanupSkipCode = "cleanup_script_failed"
	CleanupSkipWorktreeBusy        CleanupSkipCode = "worktree_busy"
	CleanupSkipInUse               CleanupSkipCode = "in_use_by_live_session"
	CleanupSkipShellTerminalOpen   CleanupSkipCode = "shell_terminal_open"
	CleanupSkipAlreadyRunning      CleanupSkipCode = "cleanup_running"
	CleanupSkipAttachmentsFailed   CleanupSkipCode = "attachment_preservation_failed"
	CleanupSkipOutsideManagedRoot  CleanupSkipCode = "outside_managed_root"
	CleanupSkipTeardownFailed      CleanupSkipCode = "teardown_failed"
)

func (c CleanupSkipCode) Retryable() bool {
	switch c {
	case CleanupSkipWorktreeBusy, CleanupSkipInUse, CleanupSkipShellTerminalOpen, CleanupSkipAlreadyRunning:
		return true
	}
	return false
}

var CleanupSkipCodes = []CleanupSkipCode{
	CleanupSkipDirty, CleanupSkipLockedWorktree, CleanupSkipMissingRepo, CleanupSkipProjectUnresolvable,
	CleanupSkipPermissionDenied, CleanupSkipScriptFailed, CleanupSkipWorktreeBusy, CleanupSkipInUse,
	CleanupSkipShellTerminalOpen, CleanupSkipAlreadyRunning, CleanupSkipAttachmentsFailed, CleanupSkipOutsideManagedRoot,
	CleanupSkipTeardownFailed,
}

type ArchivedSession struct {
	ID         SessionID
	ArchivedAt time.Time
}

type SessionCleanupRecord struct {
	SessionID            SessionID
	SessionGeneration    int64
	RuntimeReleasedAt    time.Time
	WorkspaceDisposition WorkspaceDisposition
	AttemptCount         int64
	LastAttemptAt        time.Time
	NextAttemptAt        time.Time
	FailureCode          string
	ReportOnly           bool
}
