package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ErrWorkingDirUnsupported reports a platform where another process's working
// directory cannot be read.
var ErrWorkingDirUnsupported = errors.New("process working directory is unavailable on this platform")

// WorkingDir returns the current working directory of the process pid.
func WorkingDir(ctx context.Context, pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("process working directory: invalid pid %d", pid)
	}
	switch runtime.GOOS {
	case "linux":
		return os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	case "darwin":
		out, err := CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
		if err != nil {
			return "", fmt.Errorf("process working directory of %d: %w", pid, err)
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			if dir, ok := strings.CutPrefix(line, "n"); ok && dir != "" {
				return dir, nil
			}
		}
		return "", fmt.Errorf("process working directory of %d: not reported", pid)
	default:
		return "", ErrWorkingDirUnsupported
	}
}
