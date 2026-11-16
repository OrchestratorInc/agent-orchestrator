//go:build !windows

package procmem

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// psStartTimes reads start times from ps. lstart has one-second resolution,
// so it is the fallback: Linux and macOS read the kernel's own start time.
func psStartTimes(ctx context.Context, run Runner) (map[int]string, error) {
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
