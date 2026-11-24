//go:build !windows

package local

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

// stopStaleHub stops the hub a previous daemon left running. The pid is only
// trusted when that process is still this data directory's hub (entry is its
// script path): a recycled pid belongs to something else and is left alone.
func (p *Provider) stopStaleHub(entry string) {
	pid, ok := p.recordedHubPID()
	if !ok {
		return
	}
	defer func() { _ = os.Remove(p.hubPIDPath()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := process.CommandContext(ctx, "ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.Contains(string(out), entry) {
		return
	}
	p.log.Info("stopping a device hub a previous daemon left running", "pid", pid)
	if err := process.TerminateGroup(pid, 3*time.Second); err != nil {
		p.log.Warn("stop stale device hub", "pid", pid, "err", err)
	}
}
