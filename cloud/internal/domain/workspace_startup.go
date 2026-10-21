package domain

// Workspace startup errors are reported by a running worker that could not
// prepare its session's workspace (the repository checkout, the Coder
// connection's working directory, or the project's Setup commands), or could
// not start the terminal agent in it. The worker has checked in, so its
// heartbeats must not clear them the way they clear a startup error the
// reconciler recorded; the worker clears one itself by reporting the
// workspace ready (after the user retries).
const (
	WorkspaceStartupErrorCheckout         = "repository_checkout_failed"
	WorkspaceStartupErrorWorkingDirectory = "working_directory_unusable"
	WorkspaceStartupErrorProjectSetup     = "project_setup_failed"
	// WorkspaceStartupErrorWorkingDirectoryOccupied means the project's
	// working folder already holds a folder AO did not create (for example a
	// checkout the Coder template cloned), so AO, running as its own ao-worker
	// user, will not check the repository out there.
	WorkspaceStartupErrorWorkingDirectoryOccupied = "working_directory_occupied"
	// WorkspaceStartupErrorAgentStart means the workspace is ready but the
	// terminal agent a switch to the terminal made during Setup could not
	// start (for example its credential could not be read), even after a few
	// attempts. The session's Retry tries again.
	WorkspaceStartupErrorAgentStart = "agent_start_failed"
)

// WorkspaceStartupErrorCodes lists the codes a worker may report.
func WorkspaceStartupErrorCodes() []string {
	return []string{
		WorkspaceStartupErrorCheckout,
		WorkspaceStartupErrorWorkingDirectory,
		WorkspaceStartupErrorProjectSetup,
		WorkspaceStartupErrorWorkingDirectoryOccupied,
		WorkspaceStartupErrorAgentStart,
	}
}

// IsWorkspaceStartupErrorCode reports whether code is reported by a worker.
func IsWorkspaceStartupErrorCode(code string) bool {
	for _, known := range WorkspaceStartupErrorCodes() {
		if code == known {
			return true
		}
	}
	return false
}
