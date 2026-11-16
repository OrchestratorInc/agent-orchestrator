//go:build darwin

package procmem

import (
	"context"
	"strconv"

	"golang.org/x/sys/unix"
)

// sZomb is SZOMB from <sys/proc.h>: the process exited and awaits reaping.
const sZomb = 5

// StartTimes reads every process's start time, keyed by pid. A process keeps
// its start time across exec and a reused pid gets a new one, so pid plus
// start time identifies one process where pid plus command line cannot.
// Zombies, which have already exited, are left out. The value is opaque:
// compare it only with another StartTimes reading.
//
// macOS reads the kernel's start time (microseconds) from kern.proc.all. A
// non-nil run reads ps instead, for tests.
func StartTimes(ctx context.Context, run Runner) (map[int]string, error) {
	if run != nil {
		return psStartTimes(ctx, run)
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return psStartTimes(ctx, nil)
	}
	starts := make(map[int]string, len(procs))
	for _, p := range procs {
		if p.Proc.P_stat == sZomb {
			continue
		}
		start := p.Proc.P_starttime
		starts[int(p.Proc.P_pid)] = strconv.FormatInt(int64(start.Sec), 10) + "." + strconv.FormatInt(int64(start.Usec), 10)
	}
	return starts, nil
}
