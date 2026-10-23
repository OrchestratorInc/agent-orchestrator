//go:build linux

package conpty

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processArgv reads a process's complete, untruncated argv from procfs.
func processArgv(ctx context.Context, pid int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return nil, fmt.Errorf("conpty: read argv of pid %d: %w", pid, err)
	}
	trimmed := strings.TrimSuffix(string(raw), "\x00")
	if trimmed == "" {
		// Zombies and kernel threads expose no argv.
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}
