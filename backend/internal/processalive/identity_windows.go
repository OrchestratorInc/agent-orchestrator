//go:build windows

package processalive

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// StartTime returns the legacy decimal FILETIME token, or "" when unavailable.
func StartTime(ctx context.Context, pid int) string {
	return strings.TrimPrefix(Identify(ctx, pid).StartID, "windows:")
}

func identify(_ context.Context, pid int) Identity {
	if uint64(pid) > math.MaxUint32 {
		return Identity{}
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return Identity{}
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return Identity{}
	}
	ticks := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	if ticks == 0 {
		return Identity{}
	}
	return Identity{
		StartID:   "windows:" + strconv.FormatUint(ticks, 10),
		StartedAt: time.Unix(0, creation.Nanoseconds()).UTC(),
	}
}
