package processalive

import (
	"context"
	"time"
)

// Identity identifies a process incarnation independently of its reusable PID.
// StartedAt is UTC and may be unavailable even when StartID is known.
type Identity struct {
	StartID   string
	StartedAt time.Time
}

// Identify returns the OS process start identity, or zero when it cannot be
// observed. An unavailable identity is not proof that the process is dead.
func Identify(ctx context.Context, pid int) Identity {
	if pid <= 0 || ctx.Err() != nil {
		return Identity{}
	}
	identity := identify(ctx, pid)
	if ctx.Err() != nil {
		return Identity{}
	}
	return identity
}
