//go:build !windows

package procmem

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// StartTimes reads every process's start time, keyed by pid. A process keeps
// its start time across exec and a reused pid gets a new one, so pid plus
// start time identifies one process where pid plus command line cannot. The
// value is opaque: compare it only with another StartTimes reading. A nil run
// uses exec.
func StartTimes(ctx context.Context, run Runner) (map[int]string, error) {
	if run == nil {
		run = execRunner
	}
	// lstart= prints the full start timestamp in the same fixed format on
	// Linux and macOS ("Mon Nov 16 10:00:00 2026"). stat= lets zombies, which
	// have exited but are not reaped yet, be left out as Snapshot does.
	out, err := run(ctx, "ps", "-ax", "-o", "pid=,stat=,lstart=")
	if err != nil {
		return nil, fmt.Errorf("procmem: ps lstart: %w", err)
	}
	return ParseStartTimes(string(out)), nil
}

// ParseStartTimes parses `ps -o pid=,stat=,lstart=` output, leaving zombies
// out: they have already exited.
func ParseStartTimes(out string) map[int]string {
	starts := map[int]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || strings.HasPrefix(fields[1], "Z") {
			continue
		}
		starts[pid] = strings.Join(fields[2:], " ")
	}
	return starts
}
