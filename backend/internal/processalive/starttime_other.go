//go:build !windows && !darwin

package processalive

import "time"

// startTime is unknown here. Linux keeps a start as ticks since boot, and
// converting that to wall-clock time moves with every clock step, so a live
// owner could read as started after its own record.
//
// ponytail: Linux keeps bare-pid liveness; record boot id + start ticks next
// to the pid (as previewserver does) if pid reuse shows up there.
func startTime(int) (time.Time, bool) { return time.Time{}, false }
