package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const tenureStateFile = "telemetry_tenure.json"

type TenureBucket string

const (
	TenureFirstDay   TenureBucket = "d0"
	TenureFirstWeek  TenureBucket = "d1_6"
	TenureFirstMonth TenureBucket = "w1_4"
	TenureQuarter    TenureBucket = "m1_2"
	TenureHalfYear   TenureBucket = "m3_5"
	TenureVeteran    TenureBucket = "m6_plus"
)

func tenureBucketFor(ageDays int) TenureBucket {
	switch {
	case ageDays <= 0:
		return TenureFirstDay
	case ageDays < 7:
		return TenureFirstWeek
	case ageDays < 30:
		return TenureFirstMonth
	case ageDays < 90:
		return TenureQuarter
	case ageDays < 180:
		return TenureHalfYear
	default:
		return TenureVeteran
	}
}

type tenureState struct {
	FirstSeen  string `json:"first_seen"`
	LastActive string `json:"last_active"`
	ActiveDays int    `json:"active_days"`
}

type tenureTracker struct {
	path string
	now  func() time.Time

	mu    sync.Mutex
	state tenureState
}

func newTenureTracker(dataDir string, now func() time.Time) *tenureTracker {
	if now == nil {
		now = time.Now
	}
	t := &tenureTracker{now: now}
	if dataDir != "" {
		t.path = filepath.Join(dataDir, tenureStateFile)
	}
	t.load()
	return t
}

func (t *tenureTracker) load() {
	if t.path == "" {
		return
	}
	b, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var st tenureState
	if err := json.Unmarshal(b, &st); err != nil {
		return
	}
	t.state = st
}

func (t *tenureTracker) saveLocked() {
	if t.path == "" {
		return
	}
	body, err := json.Marshal(t.state)
	if err != nil {
		return
	}
	_ = os.WriteFile(t.path, append(body, '\n'), 0o600)
}

func (t *tenureTracker) observe() (ageDays, activeDays int, bucket TenureBucket) {
	now := t.now().UTC()
	today := now.Format("2006-01-02")

	t.mu.Lock()
	defer t.mu.Unlock()

	dirty := false
	if t.state.FirstSeen == "" {
		t.state.FirstSeen = today
		dirty = true
	}
	if t.state.LastActive != today {
		t.state.LastActive = today
		t.state.ActiveDays++
		dirty = true
	}
	if dirty {
		t.saveLocked()
	}

	first, err := time.Parse("2006-01-02", t.state.FirstSeen)
	if err != nil {
		return 0, t.state.ActiveDays, TenureFirstDay
	}
	age := int(now.Truncate(24*time.Hour).Sub(first.UTC()).Hours() / 24)
	if age < 0 {
		age = 0
	}
	return age, t.state.ActiveDays, tenureBucketFor(age)
}

func (t *tenureTracker) String() string {
	age, active, bucket := t.observe()
	return fmt.Sprintf("age=%dd active=%dd bucket=%s", age, active, bucket)
}
