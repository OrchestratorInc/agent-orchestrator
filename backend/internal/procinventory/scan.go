// Package procinventory snapshots the AO-owned process table, classifies
// worker/orchestrator trees as owned, orphaned, or foreign, and kills
// orphaned trees. It is the shared backend for the `ao ps` command and the
// desktop status bar.
package procinventory

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrProcessGone reports that the target process group no longer exists
// (ESRCH) — treated as success by the kill path.
var ErrProcessGone = errors.New("procinventory: process group is gone")

// ErrScanUnsupported reports that the platform has no process-table scanner.
var ErrScanUnsupported = errors.New("procinventory: process scan unsupported on this platform")

// Entry is one row of a process-table snapshot.
type Entry struct {
	PID     int
	PPID    int
	PGID    int
	RSSKB   int64
	Lstart  string // verbatim kernel start time; compared against snapshots from the same parser
	Command string
}

// RSSBytes converts the ps rss column (kilobytes on macOS and Linux) to bytes.
func (e Entry) RSSBytes() int64 { return e.RSSKB * 1024 }

// ParseTable parses `ps -ww -axo pid=,ppid=,pgid=,rss=,lstart=,args=` output.
// Exactly nine space-separated tokens precede the command: pid, ppid, pgid,
// rss, then the five lstart tokens ("Thu Jan  1 00:00:00 2025" — note the
// padded day, which Fields collapses); everything after them is re-joined as
// the command line. Adapted from the tmux runtime's process-table walker
// (backend/internal/adapters/runtime/tmux/tmux.go parseProcessTable), extended
// with pgid, rss, and lstart.
func ParseTable(out string) ([]Entry, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	entries := make([]Entry, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, fmt.Errorf("incomplete process row %q", line)
		}
		nums := make([]int64, 4)
		for i := range nums {
			v, err := strconv.ParseInt(fields[i], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric field %q in %q", fields[i], line)
			}
			nums[i] = v
		}
		entries = append(entries, Entry{
			PID:     int(nums[0]),
			PPID:    int(nums[1]),
			PGID:    int(nums[2]),
			RSSKB:   nums[3],
			Lstart:  strings.Join(fields[4:9], " "),
			Command: strings.Join(fields[9:], " "),
		})
	}
	return entries, nil
}

// Descendants returns rootPID and every process transitively parented to it
// within the snapshot (adapted from the tmux runtime's descendantPIDs).
func Descendants(entries []Entry, rootPID int) map[int]bool {
	descendants := map[int]bool{rootPID: true}
	for changed := true; changed; {
		changed = false
		for _, entry := range entries {
			if descendants[entry.PID] || !descendants[entry.PPID] {
				continue
			}
			descendants[entry.PID] = true
			changed = true
		}
	}
	return descendants
}
