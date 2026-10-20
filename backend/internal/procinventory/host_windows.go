//go:build windows

package procinventory

import (
	"context"
)

// defaultHostStats has no Windows implementation: the orphan problem this
// package solves is launchd-adoption specific (macOS), and the status bar
// degrades to a host-less bar via the nil-host tolerance path.
func defaultHostStats(ctx context.Context) (HostStats, error) {
	return HostStats{}, ErrHostStatsUnsupported
}
