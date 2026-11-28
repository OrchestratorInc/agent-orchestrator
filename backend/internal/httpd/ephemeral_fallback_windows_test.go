//go:build windows

package httpd

import (
	"errors"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestCanRetryOnEphemeralPortWindows covers both ways Windows refuses a bind
// on the configured port: WSAEADDRINUSE when another process holds it, and
// WSAEACCES when it sits in a reserved or excluded range (Hyper-V, WinNAT).
// Both must fall back to an ephemeral port (issue #7452).
func TestCanRetryOnEphemeralPortWindows(t *testing.T) {
	listenErr := func(errno error) error {
		return &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", errno)}
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"occupied port", listenErr(windows.WSAEADDRINUSE), true},
		{"reserved port", listenErr(windows.WSAEACCES), true},
		{"address not available", listenErr(windows.WSAEADDRNOTAVAIL), false},
		{"unrelated", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canRetryOnEphemeralPort(tc.err); got != tc.want {
				t.Fatalf("canRetryOnEphemeralPort(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
