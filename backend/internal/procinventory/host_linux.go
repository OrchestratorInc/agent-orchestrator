//go:build linux

package procinventory

import (
	"context"
	"fmt"
	"os"
)

// defaultHostStats snapshots host memory from /proc/meminfo (values in kiB).
// Linux swap does not auto-grow, so composeLinuxHostStats caps SwapMaxBytes
// at the current swap size — no disk lookup needed.
func defaultHostStats(ctx context.Context) (HostStats, error) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return HostStats{}, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	info, err := ParseMeminfo(string(raw))
	if err != nil {
		return HostStats{}, err
	}
	return composeLinuxHostStats(info), nil
}
