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
	"syscall"
	"time"
)

// Identities reads every live process's parent and start time (clock ticks
// since boot) from /proc/<pid>/stat. Both come from one read of one file, so
// they always describe the same process. Exited processes (gone, or zombies)
// are left out; a process of this user whose stat cannot be read for another
// reason is kept with an unknown identity and unknown parent.
func Identities(ctx context.Context) (map[int]Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("procmem: read /proc: %w", err)
	}
	bootTime, bootErr := readBootTime()
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
			if ownedBySelf(entry.Name()) {
				out[pid] = Identity{PPID: UnknownPPID}
			}
			continue
		}
		identity, alive, ok := parseProcStat(string(stat))
		switch {
		case !ok:
			out[pid] = Identity{PPID: UnknownPPID}
		case alive:
			if bootErr == nil {
				if ticks, err := strconv.ParseInt(identity.Start, 10, 64); err == nil {
					identity.StartedAt = bootTime.Add(time.Duration(ticks) * time.Second / userHZ)
				}
			}
			out[pid] = identity
		}
	}
	return out, nil
}

// ownedBySelf reports whether /proc/<pid> belongs to this process's user. A
// process whose stat cannot be read is reported with unknown ancestry only
// then: with /proc mounted hidepid, other users' processes are unreadable as
// a matter of course, and those cannot be descendants of a runtime this user
// started, so they are left out rather than making every reading uncertain.
func ownedBySelf(pid string) bool {
	info, err := os.Stat("/proc/" + pid)
	if err != nil {
		return true // cannot tell: stay uncertain
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Getuid()
}

// userHZ is the clock-tick rate /proc reports start times in. The kernel's
// USER_HZ is 100 on every architecture Linux supports today.
const userHZ = 100

// readBootTime reads the boot time (btime) from /proc/stat.
func readBootTime() (time.Time, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(string(stat), "\n") {
		if rest, ok := strings.CutPrefix(line, "btime "); ok {
			secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(secs, 0), nil
		}
	}
	return time.Time{}, errors.New("procmem: no btime in /proc/stat")
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
