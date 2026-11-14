package telemetry

import (
	"os"
	"path/filepath"
	"regexp"
)

// Milestones records install-lifetime "firsts" (first session spawned, first
// project added) as marker files in the data dir, so first_for_install is true
// once per install rather than whenever a table happens to be empty. Markers
// are written whether or not telemetry is exported: they describe the install,
// and an install that opted back in must not report a later session as its
// first.
type Milestones struct {
	dataDir string
}

// NewMilestones returns nil for an empty data dir; a nil Milestones claims
// nothing.
func NewMilestones(dataDir string) *Milestones {
	if dataDir == "" {
		return nil
	}
	return &Milestones{dataDir: dataDir}
}

var milestoneNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Claim reports whether this call is the first ever to claim name for this
// install. Exclusive create makes concurrent callers agree on one winner. The
// second result is false when the marker could not be inspected or written,
// in which case the caller should omit first_for_install rather than guess.
func (m *Milestones) Claim(name string) (first, known bool) {
	if m == nil || !milestoneNamePattern.MatchString(name) {
		return false, false
	}
	path := filepath.Join(m.dataDir, "telemetry_first_"+name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	if err := f.Close(); err != nil {
		return false, false
	}
	return true, true
}
