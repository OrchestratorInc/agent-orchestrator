package procmem

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
}

// Known reports whether the process's start identity could be read.
func (i Identity) Known() bool { return i.Start != "" }
