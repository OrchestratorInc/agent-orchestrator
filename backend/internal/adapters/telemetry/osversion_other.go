//go:build !darwin && !windows

package telemetry

import "golang.org/x/sys/unix"

// osVersion returns the kernel release (6.8.0-45-generic): Linux has no single
// distribution version, and the kernel is what posthog-js cannot see anyway.
func osVersion() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	return unix.ByteSliceToString(u.Release[:])
}
