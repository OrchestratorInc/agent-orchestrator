//go:build darwin

package processalive

import (
	"time"

	"golang.org/x/sys/unix"
)

// startTime is the wall-clock time the kernel recorded when pid forked. A
// later clock step doesn't move it.
func startTime(pid int) (time.Time, bool) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info.Proc.P_starttime.Sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(info.Proc.P_starttime.Unix()), true
}
