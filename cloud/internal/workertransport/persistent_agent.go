package workertransport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// persistentAgent keeps the interactive coding agent running across worker
// restarts. On a provider that freezes and restores a VM's memory (Freestyle),
// waking a paused session restarts the worker; without this, the agent and
// everything it started (a dev server, a test run, a shell) die with it.
//
// The agent runs under dtach, which detaches it from the worker's PTY while
// passing bytes through untouched (no screen re-rendering, so terminal
// scrollback behaves exactly as without it). Every agent launch is
// attach-or-create: a restarted worker re-attaches to the agent still running,
// and a fresh worker starts one. A worker that exits only loses its dtach
// client. Every path that means to end the agent (closing its terminal, a
// handoff to Chat) calls stop, which ends the agent's process group and waits
// for it to exit.
type persistentAgent struct {
	dtach string
	dir   string
}

// persistentAgentMarker is the launcher's $0, so a recorded PID can be
// verified as this agent before it is signalled (PIDs are reused).
const persistentAgentMarker = "ao-persistent-agent"

// persistentAgentLauncher records the agent's process group and its exit
// status, which the dtach client cannot report.
const persistentAgentLauncher = `echo $$ > "$AO_AGENT_PID_FILE"
rm -f "$AO_AGENT_EXIT_FILE"
"$@"
status=$?
echo "$status" > "$AO_AGENT_EXIT_FILE"
exit "$status"`

func newPersistentAgent(dataDir string) *persistentAgent {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	path, err := exec.LookPath("dtach")
	if err != nil {
		return nil
	}
	return &persistentAgent{dtach: path, dir: dataDir}
}

func (p *persistentAgent) socket() string   { return filepath.Join(p.dir, "agent.dtach") }
func (p *persistentAgent) pidFile() string  { return filepath.Join(p.dir, "agent.pid") }
func (p *persistentAgent) exitFile() string { return filepath.Join(p.dir, "agent.exit") }

// command returns the dtach client that attaches to the running agent or, when
// none runs, starts path with args under dtach. -E and -z pass every key
// through (no detach or suspend key), and -r winch makes the agent redraw on
// attach.
func (p *persistentAgent) command(ctx context.Context, path string, args []string) *exec.Cmd {
	dtachArgs := []string{
		"-A", p.socket(), "-E", "-z", "-r", "winch",
		"/bin/sh", "-c", persistentAgentLauncher, persistentAgentMarker, path,
	}
	dtachArgs = append(dtachArgs, args...)
	return exec.CommandContext(ctx, p.dtach, dtachArgs...)
}

func (p *persistentAgent) environment() map[string]string {
	return map[string]string{
		"AO_AGENT_PID_FILE":  p.pidFile(),
		"AO_AGENT_EXIT_FILE": p.exitFile(),
	}
}

// exitCode reports the agent's own exit status after it ended, or false when
// it is unknown (the agent is still running or was killed by a signal).
func (p *persistentAgent) exitCode() (int, bool) {
	data, err := os.ReadFile(p.exitFile())
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return code, true
}

// running returns the agent's process-group leader when the recorded PID is
// alive and is this agent's launcher.
func (p *persistentAgent) running() (int, bool) {
	data, err := os.ReadFile(p.pidFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return 0, false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || !strings.Contains(string(cmdline), persistentAgentMarker) {
		return 0, false
	}
	return pid, true
}

// stop ends the persisted agent and its process group and waits until the
// launcher has exited, escalating to SIGKILL after grace. It is a no-op when no
// agent runs. The socket is removed so the next launch starts fresh.
func (p *persistentAgent) stop(grace time.Duration) error {
	defer func() {
		_ = os.Remove(p.socket())
		_ = os.Remove(p.pidFile())
	}()
	pid, ok := p.running()
	if !ok {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if p.waitExit(pid, grace) {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if p.waitExit(pid, 2*time.Second) {
		return nil
	}
	return fmt.Errorf("persistent agent %d did not exit", pid)
}

func (p *persistentAgent) waitExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, ok := p.running(); !ok {
			return true
		}
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
