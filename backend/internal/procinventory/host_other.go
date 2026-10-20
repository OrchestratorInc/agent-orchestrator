//go:build !darwin && !linux && !windows

package procinventory

import (
	"context"
)

// defaultHostStats has no implementation on other platforms.
func defaultHostStats(ctx context.Context) (HostStats, error) {
	return HostStats{}, ErrHostStatsUnsupported
}
