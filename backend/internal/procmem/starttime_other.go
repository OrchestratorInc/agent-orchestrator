//go:build !linux && !darwin && !windows

package procmem

import "context"

// StartTimes reads every process's start time, keyed by pid, from ps (see
// psStartTimes). Linux, macOS and Windows read the kernel's own start time.
func StartTimes(ctx context.Context, run Runner) (map[int]string, error) {
	return psStartTimes(ctx, run)
}
