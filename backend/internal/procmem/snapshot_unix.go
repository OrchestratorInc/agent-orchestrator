//go:build !windows

package procmem

import (
	"context"
	"fmt"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, name, args...).Output()
}

func Snapshot(ctx context.Context, run Runner) (*Table, error) {
	if run == nil {
		run = execRunner
	}
	out, err := run(ctx, "ps", "-axww", "-o", "pid=,ppid=,rss=,time=,args=")
	if err != nil {
		return nil, fmt.Errorf("procmem: ps: %w", err)
	}
	return Parse(string(out))
}
