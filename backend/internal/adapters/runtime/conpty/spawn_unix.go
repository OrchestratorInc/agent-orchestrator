//go:build darwin || linux

package conpty

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func defaultSpawnHost(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string, opts HostOptions) (string, int, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", 0, fmt.Errorf("%s pty spawn: resolve executable: %w", runtime.GOOS, err)
	}

	envAssignments, argv := stripEnvAssignments(argv)
	args := ptyHostArgs(sessionID, cwd, argv, opts)
	merged := interactiveTerminalEnv(os.Environ(), env, envAssignments)

	cmd := exec.Command(exe, args...) //nolint:forbidigo // detached PTY host must outlive request cancellation
	cmd.Dir = cwd
	cmd.Env = merged
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdout, childStdout, err := os.Pipe()
	if err != nil {
		return "", 0, fmt.Errorf("%s pty spawn: stdout pipe: %w", runtime.GOOS, err)
	}
	stderrFile, err := os.CreateTemp("", "ao-pty-host-stderr-*.log")
	if err != nil {
		_ = stdout.Close()
		_ = childStdout.Close()
		return "", 0, fmt.Errorf("%s pty spawn: stderr file: %w", runtime.GOOS, err)
	}
	_ = os.Remove(stderrFile.Name())
	cmd.Stdout = childStdout
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = childStdout.Close()
		_ = stderrFile.Close()
		return "", 0, fmt.Errorf("%s pty spawn: start: %w", runtime.GOOS, err)
	}
	_ = childStdout.Close()

	type readyResult struct {
		addr string
		pid  int
		err  error
	}
	readyC := make(chan readyResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			match := readyRE.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
			if match == nil {
				continue
			}
			pid, _ := strconv.Atoi(match[1])
			port, _ := strconv.Atoi(match[2])
			readyC <- readyResult{addr: "127.0.0.1:" + strconv.Itoa(port), pid: pid}
			return
		}
		msg := fmt.Sprintf("%s pty spawn: pty-host exited without printing READY", runtime.GOOS)
		if diag := strings.TrimSpace(readUnixSpawnDiagnostics(stderrFile)); diag != "" {
			msg += ": " + diag
		}
		readyC <- readyResult{err: fmt.Errorf("%s", msg)}
	}()

	timer := time.NewTimer(spawnReadyTimeout)
	defer timer.Stop()
	select {
	case result := <-readyC:
		if result.err != nil {
			stopFailedUnixSpawn(cmd, stdout, stderrFile)
			return "", 0, result.err
		}
		pid := cmd.Process.Pid
		_ = stdout.Close()
		_ = stderrFile.Close()
		go func() { _ = cmd.Wait() }()
		return result.addr, pid, nil
	case <-timer.C:
		stopFailedUnixSpawn(cmd, stdout, stderrFile)
		return "", 0, fmt.Errorf("%s pty spawn: pty-host startup timeout (%s)", runtime.GOOS, spawnReadyTimeout)
	case <-ctx.Done():
		stopFailedUnixSpawn(cmd, stdout, stderrFile)
		return "", 0, ctx.Err()
	}
}

func readUnixSpawnDiagnostics(stderr *os.File) string {
	if _, err := stderr.Seek(0, 0); err != nil {
		return ""
	}
	buf := make([]byte, maxCapturedStderr)
	n, _ := stderr.Read(buf)
	return string(buf[:n])
}

func stopFailedUnixSpawn(cmd *exec.Cmd, stdout, stderr *os.File) {
	_ = stdout.Close()
	_ = stderr.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}
