//go:build !darwin && !windows

package telemetry

import "golang.org/x/sys/unix"

func osVersion() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	return unix.ByteSliceToString(u.Release[:])
}
