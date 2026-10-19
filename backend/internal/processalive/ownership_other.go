//go:build !linux

package processalive

import "context"

func currentStartID(ctx context.Context, pid int) string {
	return Identify(ctx, pid).StartID
}
