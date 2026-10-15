package domain

// Workspace startup errors are reported by a running worker that could not
// prepare its session's workspace: the repository checkout, the Coder
// connection's working directory, or the project's Setup commands. The worker
// has checked in, so its heartbeats must not clear them the way they clear a
// startup error the reconciler recorded; the worker clears one itself by
// reporting the workspace ready (after the user retries).
const (
	WorkspaceStartupErrorCheckout         = "repository_checkout_failed"
	WorkspaceStartupErrorWorkingDirectory = "working_directory_unusable"
	WorkspaceStartupErrorProjectSetup     = "project_setup_failed"
)

// WorkspaceStartupErrorCodes lists the codes a worker may report.
func WorkspaceStartupErrorCodes() []string {
	return []string{
		WorkspaceStartupErrorCheckout,
		WorkspaceStartupErrorWorkingDirectory,
		WorkspaceStartupErrorProjectSetup,
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
