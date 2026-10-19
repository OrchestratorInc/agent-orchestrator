//go:build !windows

package processalive

import (
	"context"
	"strconv"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// StartTime returns the legacy ps start-time token, or "" when unavailable.
func StartTime(ctx context.Context, pid int) string {
	return psStartTime(ctx, pid, nil)
}

func psStartTime(ctx context.Context, pid int, env []string) string {
	if pid <= 0 || ctx.Err() != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(cmd.Environ(), env...)
	out, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
