//go:build linux

package processalive

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
)

func identify(ctx context.Context, pid int) Identity {
	startID := currentStartID(ctx, pid)
	if startID == "" {
		return Identity{}
	}
	startedAt := processStartedAt(ctx, pid)
	// The slower wall-clock lookup must describe the same process incarnation.
	if currentStartID(ctx, pid) != startID {
		return Identity{}
	}
	return Identity{StartID: startID, StartedAt: startedAt}
}

// BootStartTime returns boot+<boot id>+<start ticks> without invoking ps. A boot
// lookup error is distinct from an unavailable process and must not match a record.
func BootStartTime(ctx context.Context, pid int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if pid <= 0 {
		return "", nil
	}
	bootID, err := readLinuxBootID(ctx)
	if err != nil {
		return "", err
	}
	ticks := readLinuxStartTicks(ctx, pid)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ticks == "" {
		return "", nil
	}
	return "boot+" + bootID + "+" + ticks, nil
}

func readLinuxBootID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	bootID := strings.TrimSpace(string(data))
	if bootID == "" {
		return "", errors.New("linux boot identity is unavailable")
	}
	return bootID, nil
}

func readLinuxStartTicks(ctx context.Context, pid int) string {
	if pid <= 0 || ctx.Err() != nil {
		return ""
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil || ctx.Err() != nil {
		return ""
	}
	return linuxStartTicks(string(data))
}

func linuxStartTicks(stat string) string {
	fields := linuxProcessFields(stat)
	if len(fields) < 20 {
		return ""
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(ticks, 10)
}

func linuxProcessFields(stat string) []string {
	// comm (field 2) can contain spaces and parentheses; field 3 follows its last ).
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return nil
	}
	return strings.Fields(stat[end+1:])
}
