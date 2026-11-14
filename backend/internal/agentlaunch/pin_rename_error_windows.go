//go:build windows

package agentlaunch

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// retryableShimRenameError reports a move that can succeed on a later attempt:
// the target is a running image (access denied) or something has the freshly
// written copy open, such as an antivirus scanner (sharing violation).
func retryableShimRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
