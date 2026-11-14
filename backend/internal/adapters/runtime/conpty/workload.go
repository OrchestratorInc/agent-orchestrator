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
