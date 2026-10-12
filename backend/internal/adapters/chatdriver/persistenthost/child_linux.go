//go:build linux

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// One detached host owns one provider. ZCode Bash deliberately creates new
// sessions/groups, so a process-group signal alone cannot own its descendants.
// A Linux subreaper retains those children if the provider exits first.
var providerSubreaperEnabled bool

func configureProviderOwnership(protocol Protocol) error {
	if protocol != ProtocolZCode {
		return nil
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return fmt.Errorf("native provider ownership requires Linux pidfds: %w", err)
	}
	_ = unix.Close(fd)
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("configure native provider child ownership: %w", err)
	}
	providerSubreaperEnabled = true
	return nil
}
func configureProviderProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func startProviderProcess(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if !providerSubreaperEnabled {
		return func() {}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reapAdoptedProviderZombies(cmd.Process.Pid)
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}

// Interrupt can orphan already-dead native shell descendants while the provider
// remains live. Reap only direct adopted zombies; exec.Cmd.Wait owns providerPID.
func reapAdoptedProviderZombies(providerPID int) {
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return
	}
	for _, task := range tasks {
		data, err := os.ReadFile(filepath.Join("/", "proc", "self", "task", task.Name(), "children"))
		if err != nil {
			continue
		}
		for _, value := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(value)
			if err != nil || pid == providerPID {
				continue
			}
			identity, err := readProcessIdentity(pid)
			if err != nil || identity.parent != os.Getpid() || identity.state != "Z" {
				continue
			}
			// An unreaped direct child cannot have its PID reused before Wait4.
			var status unix.WaitStatus
			_, _ = unix.Wait4(pid, &status, unix.WNOHANG, nil)
		}
	}
}

type ownedProcess struct {
	pid, parent, depth int
	birth              string
	state              string
}

func readProcessIdentity(pid int) (ownedProcess, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ownedProcess{}, err
	}
	// comm can contain spaces and parentheses; fields start after its final ')'.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return ownedProcess{}, errors.New("invalid process identity")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return ownedProcess{}, errors.New("short process identity")
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return ownedProcess{}, err
	}
	return ownedProcess{pid: pid, parent: parent, birth: fields[19], state: fields[0]}, nil
}
func ownedProviderDescendants(ctx context.Context) ([]ownedProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	all := make(map[int]ownedProcess, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if identity, err := readProcessIdentity(pid); err == nil {
			all[pid] = identity
		}
	}
	root := os.Getpid()
	owned := make([]ownedProcess, 0)
	for pid, identity := range all {
		if pid == root {
			continue
		}
		current := identity.parent
		seen := map[int]bool{pid: true}
		depth := 1
		for current > 0 && !seen[current] {
			if current == root {
				identity.depth = depth
				owned = append(owned, identity)
				break
			}
			seen[current] = true
			parent, ok := all[current]
			if !ok {
				break
			}
			current = parent.parent
			depth++
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].depth > owned[j].depth })
	return owned, nil
}
func signalOwnedProcess(identity ownedProcess) error {
	// The pidfd fences PID reuse between identity observation and signalling.
	fd, err := unix.PidfdOpen(identity.pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open owned process handle: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	current, err := readProcessIdentity(identity.pid)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.birth != identity.birth {
		return nil
	}
	err = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
func killProviderProcess(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if !providerSubreaperEnabled {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		owned, err := ownedProviderDescendants(ctx)
		if err != nil {
			return err
		}
		remaining := false
		for _, identity := range owned {
			if identity.state != "Z" {
				remaining = true
				if err := signalOwnedProcess(identity); err != nil {
					return err
				}
			}
			// exec.Cmd.Wait owns the original provider. Reap only descendants adopted
			// by this subreaper, using their exact observed PID rather than wait(-1).
			if identity.pid != cmd.Process.Pid {
				current, err := readProcessIdentity(identity.pid)
				if err == nil && current.birth == identity.birth && current.parent == os.Getpid() {
					var status unix.WaitStatus
					_, err = unix.Wait4(identity.pid, &status, unix.WNOHANG, nil)
					if err != nil && !errors.Is(err, unix.ECHILD) && !errors.Is(err, unix.ESRCH) {
						return err
					}
					if _, err = readProcessIdentity(identity.pid); err == nil {
						remaining = true
					}
				}
			}
		}
		if !remaining {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("reap owned native provider descendants: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
