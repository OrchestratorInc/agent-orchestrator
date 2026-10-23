package processalive

import "time"

// startTimeSlack absorbs coarse timestamps: Windows stamps process creation
// and file writes from a system tick of up to about 16 ms.
const startTimeSlack = time.Second

// AliveSince reports whether pid is alive and its process already existed at
// recordedAt, when the owner wrote a record naming pid. The OS hands an exited
// process's pid to new processes, so a live pid whose process started after
// the record belongs to someone else, and the recorded owner is gone (#6695).
// Where the start time can't be read, this is Alive.
func AliveSince(pid int, recordedAt time.Time) bool {
	if !Alive(pid) {
		return false
	}
	started, ok := startTime(pid)
	return !ok || recordedAt.IsZero() || !started.After(recordedAt.Add(startTimeSlack))
}
