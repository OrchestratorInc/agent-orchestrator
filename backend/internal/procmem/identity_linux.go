//go:build linux

package procmem

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Identities reads every live process's parent and start time (clock ticks
// since boot) from /proc/<pid>/stat. Both come from one read of one file, so
// they always describe the same process. Exited processes (gone, or zombies)
// are left out; a process whose stat cannot be read for another reason is
// kept with an unknown identity.
func Identities(ctx context.Context) (map[int]Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("procmem: read /proc: %w", err)
	}
	out := map[int]Identity{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if errors.Is(err, fs.ErrNotExist) {
			continue // it exited
		}
		if err != nil {
			out[pid] = Identity{PPID: -1}
			continue
		}
		identity, alive, ok := parseProcStat(string(stat))
		switch {
		case !ok:
			out[pid] = Identity{PPID: -1}
		case alive:
			out[pid] = identity
		}
	}
	return out, nil
}

// parseProcStat returns field 4 (ppid) and field 22 (starttime) of
// /proc/<pid>/stat, and whether the process is still alive (not a zombie).
// The command name (field 2) is parenthesised and may hold spaces or
// parentheses, so fields are counted after its last ')'.
func parseProcStat(stat string) (identity Identity, alive, ok bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return Identity{}, false, false
	}
	fields := strings.Fields(stat[end+1:])
	// fields[0] is field 3 (state), fields[1] field 4 (ppid); starttime is
	// field 22.
	const startField = 22 - 3
	if len(fields) <= startField {
		return Identity{}, false, false
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return Identity{}, false, true
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return Identity{}, false, false
	}
	return Identity{PPID: ppid, Start: fields[startField]}, true, true
}
