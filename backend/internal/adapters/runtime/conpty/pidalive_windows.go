//go:build windows

package conpty

import (
	"fmt"
	"os"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/processalive"
)

// pidAlive probes PID liveness on Windows.
func pidAlive(pid int) bool {
	return processalive.Alive(pid)
}

// defaultOSProcessFinder wraps os.FindProcess for Windows.
func defaultOSProcessFinder(pid int) (processKiller, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("os.FindProcess(%d): %w", pid, err)
	}
	return p, nil
}
