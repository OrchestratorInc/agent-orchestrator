//go:build !windows

package httpd

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

// TestCanRetryOnEphemeralPortUnix pins the unix classification: only a port
// that is already in use falls back to an ephemeral port. EACCES (for example a
// privileged port) must still fail startup rather than silently moving ports.
func TestCanRetryOnEphemeralPortUnix(t *testing.T) {
	listenErr := func(errno error) error {
		return &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", errno)}
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"address in use", listenErr(syscall.EADDRINUSE), true},
		{"access denied", listenErr(syscall.EACCES), false},
		{"address not available", listenErr(syscall.EADDRNOTAVAIL), false},
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
