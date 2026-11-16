package procmem

import "time"

// Identity is one process as a single consistent reading of the process table
// saw it: its parent and its start identity, taken together so they describe
// the same process. Start survives exec and changes when a pid is reused, so
// pid plus Start identifies one process where pid plus command line cannot.
// Start is opaque and only comparable with another Identities reading on the
// same machine. An empty Start means the process exists but its identity
// could not be read: callers must treat it as unknown, never as exited.
type Identity struct {
	PPID  int
	Start string
	// StartedAt is when the process started, by the wall clock, read in the
	// same observation as Start. Zero when unknown. It is coarser than Start
	// (Linux derives it from the boot time), so compare it only with slack.
	StartedAt time.Time
}

// Known reports whether the process's start identity could be read.
func (i Identity) Known() bool { return i.Start != "" }
