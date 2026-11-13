package storagemgr

import (
	"math"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const gib = int64(1) << 30

// Disk-pressure thresholds. A level is entered when free space falls below the
// smaller of a share of the disk and an absolute amount, so a large disk is
// not called "low" with hundreds of gigabytes free. It is left only once free
// space is back above a higher mark, so a disk hovering at a threshold does
// not flap.
var (
	lowEnter      = pressureMark{fraction: 0.15, bytes: 20 * gib}
	lowExit       = pressureMark{fraction: 0.20, bytes: 25 * gib}
	criticalEnter = pressureMark{fraction: 0.05, bytes: 5 * gib}
	criticalExit  = pressureMark{fraction: 0.07, bytes: 7 * gib}
)

// pressureReadingsToChange is how many readings in a row must agree before
// the level changes.
const pressureReadingsToChange = 2

type pressureMark struct {
	fraction float64
	bytes    int64
}

func (m pressureMark) threshold(total int64) int64 {
	share := int64(math.Round(float64(total) * m.fraction))
	if share < m.bytes {
		return share
	}
	return m.bytes
}

// DiskStatus is one reading of the volume holding AO's data.
type DiskStatus struct {
	Path       string
	TotalBytes int64
	FreeBytes  int64
	Level      domain.DiskPressureLevel
	CheckedAt  time.Time
	Err        string
}

// pressureTracker turns free-space readings into a stable level.
type pressureTracker struct {
	mu        sync.Mutex
	level     domain.DiskPressureLevel
	candidate domain.DiskPressureLevel
	streak    int
	last      DiskStatus
}

func newPressureTracker() *pressureTracker {
	return &pressureTracker{level: domain.DiskPressureNormal}
}

// rawLevel is the level a single reading implies given the current level:
// entering a worse level uses the enter marks, leaving it the exit marks.
func rawLevel(current domain.DiskPressureLevel, total, free int64) domain.DiskPressureLevel {
	if total <= 0 {
		return domain.DiskPressureNormal
	}
	switch current {
	case domain.DiskPressureCritical:
		if free < criticalExit.threshold(total) {
			return domain.DiskPressureCritical
		}
		if free < lowExit.threshold(total) {
			return domain.DiskPressureLow
		}
		return domain.DiskPressureNormal
	case domain.DiskPressureLow:
		if free < criticalEnter.threshold(total) {
			return domain.DiskPressureCritical
		}
		if free < lowExit.threshold(total) {
			return domain.DiskPressureLow
		}
		return domain.DiskPressureNormal
	default:
		if free < criticalEnter.threshold(total) {
			return domain.DiskPressureCritical
		}
		if free < lowEnter.threshold(total) {
			return domain.DiskPressureLow
		}
		return domain.DiskPressureNormal
	}
}

// observe records a reading and returns the resulting status and whether the
// level got worse with it.
func (t *pressureTracker) observe(path string, total, free int64, readErr error, now time.Time) (DiskStatus, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := DiskStatus{Path: path, TotalBytes: total, FreeBytes: free, CheckedAt: now}
	if readErr != nil {
		// An unreadable volume keeps the last level: a failed probe is not
		// evidence the disk is fine or full.
		status = t.last
		status.Path, status.CheckedAt, status.Err = path, now, readErr.Error()
		status.Level = t.level
		t.last = status
		return status, false
	}
	next := rawLevel(t.level, total, free)
	worsened := false
	if next == t.level {
		t.candidate, t.streak = "", 0
	} else {
		if next == t.candidate {
			t.streak++
		} else {
			t.candidate, t.streak = next, 1
		}
		if t.streak >= pressureReadingsToChange {
			worsened = severity(next) > severity(t.level)
			t.level, t.candidate, t.streak = next, "", 0
		}
	}
	status.Level = t.level
	t.last = status
	return status, worsened
}

func (t *pressureTracker) status() DiskStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := t.last
	status.Level = t.level
	return status
}

func severity(level domain.DiskPressureLevel) int {
	switch level {
	case domain.DiskPressureCritical:
		return 2
	case domain.DiskPressureLow:
		return 1
	default:
		return 0
	}
}

// pressureCheckInterval is how often free space is read at a level: every five
// minutes normally, every minute once space is low.
func pressureCheckInterval(level domain.DiskPressureLevel) time.Duration {
	if level == domain.DiskPressureNormal {
		return 5 * time.Minute
	}
	return time.Minute
}

// clampUint converts a byte count from the OS to int64.
func clampUint(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v) //nolint:gosec // bounded by the check above
}
