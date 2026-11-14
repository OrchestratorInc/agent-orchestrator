//go:build !windows

package agentlaunch

// retryableShimRenameError is Windows-only: the shim executable is refreshed
// with Windows sharing rules in mind, and the unix shim is a script that
// os.Rename replaces atomically.
func retryableShimRenameError(error) bool {
	return false
}
