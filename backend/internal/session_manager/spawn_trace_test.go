package sessionmanager

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// adapterFailure stands in for an adapter error that names its own cause
// (the git adapter's commandError, the ACP and chat-host markers).
type adapterFailure struct {
	cause string
	err   error
}

func (e adapterFailure) Error() string {
	if e.err == nil {
		return "adapter failure"
	}
	return e.err.Error()
}
func (e adapterFailure) Unwrap() error        { return e.err }
func (e adapterFailure) FailureCause() string { return e.cause }

// The worktree and agent-start buckets used to collapse into cause=other.
// Each case is a failure the daemon can hit; the cause is from a closed list.
func TestClassifySpawnFailureNamesTheUnderlyingCause(t *testing.T) {
	worktree := func(err error) error { return fmt.Errorf("spawn w-1: %w: %w", ErrWorkspaceCreate, err) }
	agent := func(err error) error { return fmt.Errorf("spawn w-1: %w: %w", ErrChatController, err) }
	peerDisconnected := errors.New(`ACP session/new: {"code":-32603,"message":"Internal error","data":{"error":"peer disconnected before response"}}`)
	for _, tc := range []struct {
		name      string
		err       error
		wantStep  string
		wantCause string
	}{
		{"path occupied", worktree(fmt.Errorf("%w: %q", ports.ErrWorkspacePathOccupied, "/w")), "worktree", "path_exists"},
		{"branch checked out elsewhere", worktree(ports.ErrWorkspaceBranchCheckedOutElsewhere), "worktree", "branch_exists"},
		{"branch already exists", worktree(ports.ErrWorkspaceBranchAlreadyExists), "worktree", "branch_exists"},
		{"workspace locked", worktree(ports.ErrWorkspaceLocked), "worktree", "locked"},
		{"project folder lost .git", worktree(ports.ErrWorkspaceNotARepo), "worktree", "not_a_repo"},
		{"no default branch", worktree(ports.ErrWorkspaceDefaultBranchUnresolved), "worktree", "no_default_branch"},
		{"branch not fetched", worktree(ports.ErrWorkspaceBranchNotFetched), "worktree", "ref_missing"},
		{"invalid branch", worktree(ports.ErrWorkspaceBranchInvalid), "worktree", "invalid_branch"},
		{"project not resolvable", worktree(ErrProjectNotResolvable), "worktree", "repo_missing"},
		{"git names its cause", worktree(adapterFailure{cause: "unsafe_repo"}), "worktree", "unsafe_repo"},
		{"git did not run, OS says why", worktree(adapterFailure{cause: "other", err: &os.PathError{Op: "fork/exec", Path: "/g", Err: syscall.EACCES}}), "worktree", "permission"},
		{"managed folder on a full disk", worktree(&os.PathError{Op: "mkdir", Path: "/w", Err: syscall.ENOSPC}), "worktree", "disk_full"},
		{"managed folder not writable", worktree(&os.PathError{Op: "mkdir", Path: "/w", Err: syscall.EACCES}), "worktree", "permission"},
		{"errno wins over path text", worktree(&os.PathError{Op: "mkdir", Path: "/work/no space left on device/p", Err: syscall.EACCES}), "worktree", "permission"},
		{"git not installed", worktree(&exec.Error{Name: "git", Err: exec.ErrNotFound}), "worktree", "git_missing"},
		{"unrecognised worktree error", worktree(errors.New("gitworktree: no unused branch")), "worktree", "other"},

		{"agent signed out", agent(ports.ErrChatAuthRequired), "agent_start", "not_logged_in"},
		{"agent binary missing inside host start", agent(adapterFailure{cause: "host_failed", err: ports.ErrAgentBinaryNotFound}), "agent_start", "binary_missing"},
		{"agent not on PATH", agent(&exec.Error{Name: "opencode", Err: exec.ErrNotFound}), "agent_start", "binary_missing"},
		{"windows binary unusable", agent(errors.New(`start C:\a\agent.exe: %1 is not a valid Win32 application.`)), "agent_start", "binary_missing"},
		{"host could not write its files", agent(adapterFailure{cause: "host_failed", err: &os.PathError{Op: "open", Path: "/d", Err: syscall.EACCES}}), "agent_start", "permission"},
		{"agent refused session/new", agent(adapterFailure{cause: "agent_error", err: errors.New(`ACP session/new: {"code":-32603,"message":"Internal error"}`)}), "agent_start", "agent_error"},
		{"agent refused initialize", agent(adapterFailure{cause: "agent_error", err: fmt.Errorf("%w: ACP initialize: %w", ports.ErrChatDriverIncompatible, errors.New(`{"code":-32602,"message":"Invalid params"}`))}), "agent_start", "incompatible"},
		{"peer disconnected during initialize", agent(fmt.Errorf("%w: ACP initialize: %w", ports.ErrChatDriverIncompatible, peerDisconnected)), "agent_start", "exited"},
		{"chat host never came up", agent(adapterFailure{cause: "host_failed", err: errors.New("start chat host: connection refused")}), "agent_start", "host_failed"},
		{"agent exited during start", agent(peerDisconnected), "agent_start", "exited"},
		{"agent disk full", agent(errors.New("write /d/x: no space left on device")), "agent_start", "disk_full"},
		{"incompatible agent", agent(fmt.Errorf("%w: ACP initialize: %w", ports.ErrChatDriverIncompatible, errors.New("unsupported protocol"))), "agent_start", "incompatible"},
		{"unrecognised agent error", agent(errors.New("something odd")), "agent_start", "other"},

		{"agent not installed before any step", ports.ErrAgentBinaryNotFound, "other", "binary_missing"},
	} {
		step, cause := ClassifySpawnFailure(tc.err, SpawnTraceFacts{})
		if step != tc.wantStep || cause != tc.wantCause {
			t.Errorf("%s: ClassifySpawnFailure = (%q, %q), want (%q, %q)", tc.name, step, cause, tc.wantStep, tc.wantCause)
		}
		if !telemetrymeta.IsSpawnFailureCause(cause) {
			t.Errorf("%s: cause %q is not in the closed vocabulary Sentry accepts", tc.name, cause)
		}
	}
}
