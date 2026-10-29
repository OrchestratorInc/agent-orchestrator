package processalive

import "time"

const reuseTolerance = time.Minute

// AliveSince reports whether pid is alive and was not started after since. A
// recorded owner always started before it recorded since, so a process that
// started later is an unrelated process that reused the pid. A zero since, or
// a start time that cannot be read, falls back to Alive.
func AliveSince(pid int, since time.Time) bool {
	if !Alive(pid) {
		return false
	}
	if since.IsZero() {
		return true
	}
	started, ok := startTime(pid)
	// ps derives start times from boot time, which can drift by seconds (NTP steps,
	// VM resume); only a start well after since proves the pid was reused.
	return !ok || !started.After(since.Add(reuseTolerance))
}
