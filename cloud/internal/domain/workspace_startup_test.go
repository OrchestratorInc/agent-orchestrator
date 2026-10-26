package domain

import "testing"

// A worker reports an occupied working folder with its own startup error
// code, which the control plane accepts like the other workspace codes.
func TestWorkspaceStartupErrorCodesIncludeOccupiedWorkingFolder(t *testing.T) {
	t.Parallel()
	if !IsWorkspaceStartupErrorCode(WorkspaceStartupErrorWorkingDirectoryOccupied) {
		t.Fatal("working_directory_occupied is not a workspace startup error code")
	}
}

// A terminal agent that could not start once the workspace was ready is a
// workspace startup error too, so the control plane takes the worker's report
// and its heartbeats do not clear it.
func TestWorkspaceStartupErrorCodesIncludeAgentStart(t *testing.T) {
	t.Parallel()
	if !IsWorkspaceStartupErrorCode(WorkspaceStartupErrorAgentStart) || WorkspaceStartupErrorAgentStart != "agent_start_failed" {
		t.Fatal("agent_start_failed is not a workspace startup error code")
	}
}
