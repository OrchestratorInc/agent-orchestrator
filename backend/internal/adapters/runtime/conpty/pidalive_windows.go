//go:build windows

package conpty

import (
	"fmt"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func pidAlive(pid int) bool {
	return processalive.Alive(pid)
}

func defaultOSProcessFinder(pid int) (processKiller, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("os.FindProcess(%d): %w", pid, err)
	}
	return p, nil
}
