//go:build !windows

package processalive

import (
	"strconv"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// startTime reads the process start time from ps, which has one-second
// resolution on macOS and Linux. The result is floored to the second, so it
// never lies after the moment the process recorded its own later timestamp.
func startTime(pid int) (time.Time, bool) {
	cmd := aoprocess.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(cmd.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	started, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(strings.Fields(string(out)), " "), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return started, true
}
