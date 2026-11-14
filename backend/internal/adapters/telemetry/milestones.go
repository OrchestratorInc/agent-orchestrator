package telemetry

import (
	"os"
	"path/filepath"
	"regexp"
)

type Milestones struct {
	dataDir string
}

func NewMilestones(dataDir string) *Milestones {
	if dataDir == "" {
		return nil
	}
	return &Milestones{dataDir: dataDir}
}

var milestoneNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

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
