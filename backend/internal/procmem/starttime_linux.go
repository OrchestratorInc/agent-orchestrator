//go:build linux

package procmem

import (
	"context"
	"os"
	"strconv"
	"strings"
)

// StartTimes reads every process's start time, keyed by pid. A process keeps
// its start time across exec and a reused pid gets a new one, so pid plus
// start time identifies one process where pid plus command line cannot.
// Zombies, which have already exited, are left out. The value is opaque:
// compare it only with another StartTimes reading.
//
// Linux reads the kernel's start time (clock ticks since boot) from
// /proc/<pid>/stat. A non-nil run reads ps instead, for tests.
func StartTimes(ctx context.Context, run Runner) (map[int]string, error) {
	if run != nil {
		return psStartTimes(ctx, run)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return psStartTimes(ctx, nil)
	}
	starts := map[int]string{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue // it exited
		}
		if start, ok := parseProcStatStart(string(stat)); ok {
			starts[pid] = start
		}
	}
	return starts, nil
}

// parseProcStatStart returns field 22 (starttime) of /proc/<pid>/stat, or
// false for a zombie. The command name (field 2) is parenthesised and may hold
// spaces or parentheses, so fields are counted after its last ')'.
func parseProcStatStart(stat string) (string, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(stat[end+1:])
	// fields[0] is field 3 (state); starttime is field 22.
	const startField = 22 - 3
	if len(fields) <= startField || fields[0] == "Z" || fields[0] == "X" {
		return "", false
	}
	return fields[startField], true
}
