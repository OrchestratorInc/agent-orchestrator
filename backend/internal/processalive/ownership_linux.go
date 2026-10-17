//go:build linux

package processalive

import (
	"context"
	"errors"
	"os"
	"strconv"
	"syscall"
)

func currentStartID(ctx context.Context, pid int) string {
	bootID, err := readLinuxBootID(ctx)
	if err != nil {
		return ""
	}
	ticks := readLinuxStartTicks(ctx, pid)
	if ticks == "" {
		return ""
	}
	return "linux:" + bootID + ":" + ticks
}

func processGone(ctx context.Context, pid int) bool {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil || ctx.Err() != nil {
		return false
	}
	fields := linuxProcessFields(string(data))
	return len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X")
}
