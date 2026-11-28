//go:build !windows

package httpd

import (
	"errors"
	"syscall"
)

// canRetryOnEphemeralPort reports whether a failed bind on the configured port
// should fall back to an OS-assigned ephemeral port. On unix only EADDRINUSE
// qualifies; EACCES (e.g. a privileged port) is a configuration error.
func canRetryOnEphemeralPort(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
