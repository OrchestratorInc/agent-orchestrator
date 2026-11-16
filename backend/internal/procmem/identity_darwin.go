//go:build darwin

package procmem

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// sZomb is SZOMB from <sys/proc.h>: the process exited and awaits reaping.
const sZomb = 5

// Identities reads every live process's parent and start time (microseconds)
// from one kern.proc.all sysctl, so each pid's parent and start time describe
// the same process. Zombies are left out. A failed read is an error: there is
// no fallback reader whose identities would not compare with these.
func Identities(ctx context.Context) (map[int]Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("procmem: sysctl kern.proc.all: %w", err)
	}
	out := make(map[int]Identity, len(procs))
	for _, p := range procs {
		if p.Proc.P_stat == sZomb {
			continue
		}
		start := p.Proc.P_starttime
		out[int(p.Proc.P_pid)] = Identity{
			PPID:      int(p.Eproc.Ppid),
			Start:     strconv.FormatInt(int64(start.Sec), 10) + "." + strconv.FormatInt(int64(start.Usec), 10),
			StartedAt: time.Unix(int64(start.Sec), int64(start.Usec)*int64(time.Microsecond)),
		}
	}
	return out, nil
}
