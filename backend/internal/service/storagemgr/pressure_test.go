package storagemgr

import (
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPressureNeedsTwoReadingsAndHysteresis(t *testing.T) {
	const total = 500 * gib
	tracker := newPressureTracker()
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	observe := func(free int64) (domain.DiskPressureLevel, bool) {
		now = now.Add(time.Minute)
		status, worsened := tracker.observe("/data", total, free, nil, now)
		return status.Level, worsened
	}

	if level, _ := observe(100 * gib); level != domain.DiskPressureNormal {
		t.Fatalf("plenty free: level=%s", level)
	}
	// 15 GiB free is under the 20 GiB low mark, but one reading is not enough.
	if level, worsened := observe(15 * gib); level != domain.DiskPressureNormal || worsened {
		t.Fatalf("first low reading: level=%s worsened=%v", level, worsened)
	}
	if level, worsened := observe(15 * gib); level != domain.DiskPressureLow || !worsened {
		t.Fatalf("second low reading: level=%s worsened=%v", level, worsened)
	}
	// 22 GiB is above the enter mark but below the 25 GiB exit mark: stays low.
	observe(22 * gib)
	if level, _ := observe(22 * gib); level != domain.DiskPressureLow {
		t.Fatalf("hysteresis: level=%s, want low", level)
	}
	observe(4 * gib)
	if level, worsened := observe(4 * gib); level != domain.DiskPressureCritical || !worsened {
		t.Fatalf("critical: level=%s worsened=%v", level, worsened)
	}
	observe(30 * gib)
	if level, worsened := observe(30 * gib); level != domain.DiskPressureNormal || worsened {
		t.Fatalf("recovered: level=%s worsened=%v", level, worsened)
	}
}

func TestPressureSmallDiskUsesShareOfDisk(t *testing.T) {
	const total = 64 * gib // 15% is 9.6 GiB, under the 20 GiB cap
	if got := rawLevel(domain.DiskPressureNormal, total, 12*gib); got != domain.DiskPressureNormal {
		t.Fatalf("12 GiB of 64: %s", got)
	}
	if got := rawLevel(domain.DiskPressureNormal, total, 9*gib); got != domain.DiskPressureLow {
		t.Fatalf("9 GiB of 64: %s", got)
	}
	if got := rawLevel(domain.DiskPressureNormal, total, 3*gib); got != domain.DiskPressureCritical {
		t.Fatalf("3 GiB of 64: %s", got)
	}
}

func TestPressureReadErrorKeepsLevel(t *testing.T) {
	tracker := newPressureTracker()
	now := time.Now()
	tracker.observe("/data", 100*gib, 1*gib, nil, now)
	tracker.observe("/data", 100*gib, 1*gib, nil, now)
	status, worsened := tracker.observe("/data", 0, 0, errors.New("unmounted"), now)
	if status.Level != domain.DiskPressureCritical || worsened || status.Err == "" {
		t.Fatalf("status=%+v worsened=%v, want critical kept with the error", status, worsened)
	}
}

func TestPressureCheckIntervals(t *testing.T) {
	if pressureCheckInterval(domain.DiskPressureNormal) != 5*time.Minute ||
		pressureCheckInterval(domain.DiskPressureLow) != time.Minute ||
		pressureCheckInterval(domain.DiskPressureCritical) != time.Minute {
		t.Fatal("check every 5 minutes normally and every minute under pressure")
	}
}
