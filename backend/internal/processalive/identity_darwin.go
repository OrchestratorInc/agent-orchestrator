//go:build darwin

package processalive

import (
	"context"
	"math"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

func identify(_ context.Context, pid int) Identity {
	if pid > math.MaxInt32 {
		return Identity{}
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Identity{}
	}
	start := info.Proc.P_starttime
	if start.Sec <= 0 || start.Usec < 0 || start.Usec >= 1_000_000 {
		return Identity{}
	}
	return Identity{
		StartID:   "darwin:" + strconv.FormatInt(start.Sec, 10) + ":" + strconv.FormatInt(int64(start.Usec), 10),
		StartedAt: time.Unix(start.Sec, int64(start.Usec)*1_000).UTC(),
	}
}
