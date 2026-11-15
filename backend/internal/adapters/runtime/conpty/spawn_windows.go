//go:build windows

package conpty

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - len(b.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func defaultSpawnHost(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string, opts HostOptions) (string, int, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", 0, fmt.Errorf("conpty spawn: resolve executable: %w", err)
	}

	envAssignments, argv := stripEnvAssignments(argv)

	args := ptyHostArgs(sessionID, cwd, argv, opts)
	if err := validateWindowsCommandLine(append([]string{exe}, args...)); err != nil {
		return "", 0, fmt.Errorf("conpty spawn: %w", err)
	}

	merged := interactiveTerminalEnv(os.Environ(), env, envAssignments)

	cmd := exec.CommandContext(ctx, exe, args...) //nolint:forbidigo // detached ConPTY host requires specialized creation flags
	cmd.Dir = cwd
	cmd.Env = merged

	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, fmt.Errorf("conpty spawn: stdout pipe: %w", err)
	}
	stderr := &boundedBuffer{max: maxCapturedStderr}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return "", 0, fmt.Errorf("conpty spawn: start: %w", err)
	}

	readyC := make(chan struct {
		addr string
		pid  int
		err  error
	}, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			m := readyRE.FindStringSubmatch(line)
			if m != nil {
				pid, _ := strconv.Atoi(m[1])
				port, _ := strconv.Atoi(m[2])
				readyC <- struct {
					addr string
					pid  int
					err  error
				}{"127.0.0.1:" + strconv.Itoa(port), pid, nil}
				return
			}
		}
		msg := "conpty spawn: pty-host exited without printing READY"
		if diag := strings.TrimSpace(stderr.String()); diag != "" {
			msg += ": " + diag
		}
		readyC <- struct {
			addr string
			pid  int
			err  error
		}{"", 0, fmt.Errorf("%s", msg)}
	}()

	timer := time.NewTimer(spawnReadyTimeout)
	defer timer.Stop()

	select {
	case r := <-readyC:
		if r.err != nil {
			pid, err := cleanupStartedHostFailure(cmd.Process.Pid, r.err, cmd.Process.Kill)
			return "", pid, err
		}
		stdout.Close()
		cmd.Process.Release()
		return r.addr, cmd.Process.Pid, nil
	case <-timer.C:
		pid, err := cleanupStartedHostFailure(cmd.Process.Pid, fmt.Errorf("conpty spawn: pty-host startup timeout (%s)", spawnReadyTimeout), cmd.Process.Kill)
		return "", pid, err
	case <-ctx.Done():
		pid, err := cleanupStartedHostFailure(cmd.Process.Pid, ctx.Err(), cmd.Process.Kill)
		return "", pid, err
	}
}
