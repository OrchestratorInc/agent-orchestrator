package conpty

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// managedChildren is the evidence the supervised-workload probe needs: whether
// the supervisor itself was listed, and how many of its direct children are
// still running. It deliberately carries no command text: argv is user
// content (launch prompts can be argv) and must never decide process state.
type managedChildren struct {
	supervisorListed bool
	running          int
}

// parseManagedChildren reads `ps -A -o pid=,ppid=,stat=` output. A zombie
// (Z) or dead (X) row has exited and is not running. Every row must parse;
// a malformed row makes the whole snapshot unusable rather than silently
// shrinking it into a false negative.
func parseManagedChildren(out string, supervisorPID int) (managedChildren, error) {
	var got managedChildren
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return managedChildren{}, fmt.Errorf("conpty: malformed process row %q", line)
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || pid <= 0 || ppid < 0 {
			return managedChildren{}, fmt.Errorf("conpty: malformed process row %q", line)
		}
		exited := strings.HasPrefix(fields[2], "Z") || strings.HasPrefix(fields[2], "X")
		if pid == supervisorPID && !exited {
			got.supervisorListed = true
		}
		if ppid == supervisorPID && !exited {
			got.running++
		}
	}
	if err := sc.Err(); err != nil {
		return managedChildren{}, fmt.Errorf("conpty: scan process table: %w", err)
	}
	return got, nil
}
