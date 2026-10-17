package mobilebridge

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// processProbeTimeout bounds the shell-out used to identify a process. A hung
// probe must not stall daemon startup.
const processProbeTimeout = 3 * time.Second

// IsLiveCloudflared reports whether pid is a running cloudflared.
//
// ReapStaleTunnel separately verifies the recorded process start identity.
// This command check is an additional requirement before signalling it.
func IsLiveCloudflared(pid int) bool {
	if pid <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	if runtime.GOOS == "windows" {
		out, err := aoprocess.CommandContext(ctx, "tasklist",
			"/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
		if err != nil {
			return false
		}
		return strings.Contains(strings.ToLower(string(out)), "cloudflared")
	}

	out, err := aoprocess.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false // no such process, or ps unavailable
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(string(out))), "cloudflared")
}

// KillProcess terminates pid. Callers must have confirmed the process's
// identity first — see IsLiveCloudflared.
func KillProcess(pid int) error {
	if runtime.GOOS == "windows" {
		ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
		defer cancel()
		// Windows has no signals; /T takes the child tree with it, which
		// matters because cloudflared may have spawned helpers.
		return aoprocess.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
