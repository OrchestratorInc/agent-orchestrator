//go:build !linux && !darwin && !windows

package procmem

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Identities reads every live process's parent and start time from one ps
// listing, so each row's parent and start time describe the same process.
// lstart has one-second resolution; Linux, macOS and Windows read the
// kernel's own start time instead. Zombies are left out.
func Identities(ctx context.Context) (map[int]Identity, error) {
	out, err := execRunner(ctx, "ps", "-ax", "-o", "pid=,ppid=,stat=,lstart=")
	if err != nil {
		return nil, fmt.Errorf("procmem: ps: %w", err)
	}
	ids := map[int]Identity{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || strings.HasPrefix(fields[2], "Z") {
			continue
		}
		start := strings.Join(fields[3:], " ")
		identity := Identity{PPID: ppid, Start: start}
		if at, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", start, time.Local); err == nil {
			identity.StartedAt = at
		}
		ids[pid] = identity
	}
	return ids, nil
}
