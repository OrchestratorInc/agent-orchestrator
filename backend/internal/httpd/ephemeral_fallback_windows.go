//go:build windows

package httpd

import (
	"errors"

	"golang.org/x/sys/windows"
)

// canRetryOnEphemeralPort reports whether a failed bind on the configured port
// should fall back to an OS-assigned ephemeral port. Windows reports a port
// held by another process as WSAEADDRINUSE, but a port inside a reserved or
// excluded range (Hyper-V, WinNAT; see `netsh int ipv4 show excludedportrange`)
// as WSAEACCES even when nothing visible is using it. Both mean "pick another
// port", not a fatal error.
func canRetryOnEphemeralPort(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES)
}
