package cua

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// ProcessStartedAt reads the kernel process birth timestamp at microsecond
// precision. The target environment must use the same timestamp convention.
func ProcessStartedAt(ctx context.Context, pid int) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	if pid <= 0 {
		return time.Time{}, refuse("invalid_pid", "PID must be positive")
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, fmt.Errorf("read process birth time: %w", err)
	}
	if int(info.Proc.P_pid) != pid || info.Proc.P_starttime.Sec <= 0 || info.Proc.P_stat == 5 {
		return time.Time{}, refuse("process_missing", "process is missing or a zombie")
	}
	return time.Unix(info.Proc.P_starttime.Sec, int64(info.Proc.P_starttime.Usec)*1000).UTC(), nil
}
