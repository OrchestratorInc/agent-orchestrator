package conpty

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

var _ ports.WorkloadCommandInspector = (*Runtime)(nil)

func snapshotProcesses(ctx context.Context) ([]procmem.Process, error) {
	table, err := procmem.Snapshot(ctx, nil)
	if err != nil {
		return nil, err
	}
	return table.All(), nil
}

// RunningWorkloadCommands counts shell commands below the supervised agent for
// ref (see ports.WorkloadCommandInspector). The PTY host starts the launch argv
// directly, so its child is AO's supervisor, which the registry already fences
// by launch id; the agent is the supervisor's child.
func (r *Runtime) RunningWorkloadCommands(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (int, error) {
	if ref.SessionID == "" || strings.TrimSpace(ref.LaunchID) == "" {
		return 0, errors.New("conpty: exact supervisor session and launch are required")
	}
	if string(ref.SessionID) != handle.ID {
		return 0, errors.New("conpty: supervisor session does not match the runtime handle")
	}
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return 0, fmt.Errorf("conpty: resolve runtime %q: %w", handle.ID, err)
	}
	if sess == nil || sess.pid <= 0 || sess.launchID != ref.LaunchID {
		return 0, errors.New("conpty: exact supervisor is not running")
	}
	processes, err := r.processTable(ctx)
	if err != nil {
		return 0, fmt.Errorf("conpty: read process table: %w", err)
	}
	return countWorkloadCommands(processes, sess.pid)
}

// countWorkloadCommands counts the shells the agents below hostPID's
// supervisor spawned, with everything below them. The supervisor is found by
// its command line where the table has one; Windows lists only executable
// names, so there it is the host's child. ConPTY also gives the host a conhost
// child on Windows; it has no children and is skipped naturally.
func countWorkloadCommands(processes []procmem.Process, hostPID int) (int, error) {
	children := map[int][]int{}
	byPID := map[int]procmem.Process{}
	for _, process := range processes {
		children[process.PPID] = append(children[process.PPID], process.PID)
		byPID[process.PID] = process
	}
	var subtree func(pid int, into map[int]bool)
	subtree = func(pid int, into map[int]bool) {
		for _, child := range children[pid] {
			if !into[child] {
				into[child] = true
				subtree(child, into)
			}
		}
	}
	hostTree := map[int]bool{}
	subtree(hostPID, hostTree)
	supervisors := children[hostPID]
	for pid := range hostTree {
		// A shell whose -c script starts the supervisor is not the supervisor.
		if command := byPID[pid].Command; isSupervisorCommand(command) && !isShellCommand(command) {
			supervisors = []int{pid}
			break
		}
	}
	agents, commands := 0, 0
	for _, supervisor := range supervisors {
		for _, agent := range children[supervisor] {
			agents++
			below := map[int]bool{}
			subtree(agent, below)
			for pid := range below {
				process := byPID[pid]
				if !isShellCommand(process.Command) {
					continue
				}
				// Count each shell once, with its subtree, from its outermost shell.
				if parent, ok := byPID[process.PPID]; ok && parent.PID != agent && below[parent.PID] && isShellCommand(parent.Command) {
					continue
				}
				shell := map[int]bool{pid: true}
				subtree(pid, shell)
				commands += len(shell)
			}
		}
	}
	if agents == 0 {
		return 0, errors.New("conpty: supervised agent is not running")
	}
	return commands, nil
}

func isSupervisorCommand(command string) bool {
	fields := strings.Fields(command)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "agent-process" && fields[i+1] == "supervise" {
			return true
		}
	}
	return false
}

// isShellCommand reports a shell, the way agents run tool commands: POSIX
// shells, and cmd and PowerShell on Windows, where the table holds only the
// executable name.
func isShellCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	name := strings.ToLower(strings.TrimPrefix(filepath.Base(strings.ReplaceAll(fields[0], `\`, "/")), "-"))
	switch strings.TrimSuffix(name, ".exe") {
	case "sh", "bash", "zsh", "dash", "fish", "ksh", "cmd", "powershell", "pwsh":
		return true
	}
	return false
}
