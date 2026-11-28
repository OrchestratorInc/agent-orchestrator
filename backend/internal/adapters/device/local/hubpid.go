package local

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The hub runs as its own process group. A daemon that is killed outright (a
// crash, kill -9) cannot stop it, and the next daemon starts another: each
// leftover holds memory and its simulator streams. The running hub's pid is
// recorded so the next start can stop it first.

func (p *Provider) hubPIDPath() string { return filepath.Join(p.stateRoot(), "hub.pid") }

func (p *Provider) recordHubPID(pid int) {
	if err := os.WriteFile(p.hubPIDPath(), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		p.log.Debug("record device hub pid", "err", err)
	}
}

// forgetHubPID removes the record if it still names pid.
func (p *Provider) forgetHubPID(pid int) {
	if recorded, ok := p.recordedHubPID(); ok && recorded == pid {
		_ = os.Remove(p.hubPIDPath())
	}
}

func (p *Provider) recordedHubPID() (int, bool) {
	raw, err := os.ReadFile(p.hubPIDPath())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid, err == nil && pid > 1
}
