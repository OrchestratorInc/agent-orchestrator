//go:build !darwin

package cua

import (
	"context"
	"time"
)

// ProcessStartedAt refuses platforms without the macOS Driver app identity.
func ProcessStartedAt(ctx context.Context, _ int) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, refuse("unsupported_platform", "Cua adapter requires macOS")
}
