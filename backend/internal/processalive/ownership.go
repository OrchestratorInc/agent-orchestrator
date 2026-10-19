package processalive

import "context"

// Ownership distinguishes a saved process incarnation from a reusable PID.
type Ownership string

// Ownership states describe the saved process incarnation, not PID liveness alone.
const (
	OwnershipUnknown  Ownership = "unknown"
	OwnershipMatching Ownership = "matching"
	OwnershipGone     Ownership = "gone"
	OwnershipReused   Ownership = "reused"
)

// CheckOwnership compares the saved OS start identity with the current process.
// Only Matching authorizes signaling. Gone and Reused allow reclaiming the old
// owner's metadata, never signaling its replacement. Unknown preserves both.
func CheckOwnership(ctx context.Context, pid int, expectedStartID string) Ownership {
	if ctx.Err() != nil || pid <= 0 {
		return OwnershipUnknown
	}
	gone := processGone(ctx, pid)
	if ctx.Err() != nil {
		return OwnershipUnknown
	}
	if gone {
		return OwnershipGone
	}
	if expectedStartID == "" {
		return OwnershipUnknown
	}
	startID := currentStartID(ctx, pid)
	if ctx.Err() != nil {
		return OwnershipUnknown
	}
	if startID == "" {
		// The owner may have exited between the liveness and identity reads.
		gone := processGone(ctx, pid)
		if ctx.Err() == nil && gone {
			return OwnershipGone
		}
		return OwnershipUnknown
	}
	if startID != expectedStartID {
		return OwnershipReused
	}
	return OwnershipMatching
}
