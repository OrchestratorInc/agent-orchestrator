//go:build !linux && !windows

package processalive

import (
	"context"
	"errors"
)

// BootStartTime reports that boot-based process identities are Linux-only.
func BootStartTime(ctx context.Context, _ int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.ErrUnsupported
}
