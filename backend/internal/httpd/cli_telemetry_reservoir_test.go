package httpd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCLITelemetryReservoirPersistsDailyReservations(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	first := newCLITelemetryReservoir(dir)
	if !first.reserveActive(now) {
		t.Fatal("first active reservation = false, want true")
	}
	if !first.reserveInvoked(now, "user", "ao status") {
		t.Fatal("first invoked reservation = false, want true")
	}

	second := newCLITelemetryReservoir(dir)
	if second.reserveActive(now) {
		t.Fatal("active reservation in same slot after reload = true, want false")
	}
	if second.reserveInvoked(now, "user", "ao status") {
		t.Fatal("invoked reservation after reload = true, want false")
	}
	if !second.reserveInvoked(now, "agent", "ao status") {
		t.Fatal("same command with different actor after reload = false, want true")
	}
	if !second.reserveInvoked(now, "user", "ao session ls") {
		t.Fatal("new command reservation after reload = false, want true")
	}
}

func TestCLITelemetryReservoirResetsOnNewUTCDay(t *testing.T) {
	dir := t.TempDir()
	firstDay := time.Date(2026, 7, 20, 23, 59, 0, 0, time.UTC)
	secondDay := time.Date(2026, 7, 21, 0, 1, 0, 0, time.UTC)

	r := newCLITelemetryReservoir(dir)
	if !r.reserveActive(firstDay) || !r.reserveInvoked(firstDay, "user", "ao status") {
		t.Fatal("initial reservations failed")
	}
	if !r.reserveActive(secondDay) {
		t.Fatal("active reservation on new UTC day = false, want true")
	}
	if !r.reserveInvoked(secondDay, "user", "ao status") {
		t.Fatal("invoked reservation on new UTC day = false, want true")
	}
}

func TestCLITelemetryReservoirNormalizesCommandPathReservations(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	r := newCLITelemetryReservoir(dir)
	if !r.reserveInvoked(now, "user", "AO   SEND") {
		t.Fatal("first normalized command reservation = false, want true")
	}
	if r.reserveInvoked(now, "user", "ao send") {
		t.Fatal("same normalized command reservation = true, want false")
	}
	if !r.reserveInvoked(now, "agent", "ao send") {
		t.Fatal("same normalized command with different actor = false, want true")
	}
}

func TestCLITelemetryReservoirActiveReservationIsOncePerUTCDay(t *testing.T) {
	dir := t.TempDir()
	r := newCLITelemetryReservoir(dir)

	if !r.reserveActive(time.Date(2026, 7, 20, 0, 1, 0, 0, time.UTC)) {
		t.Fatal("first reservation of the day = false, want true")
	}
	for _, hour := range []int{5, 6, 12, 18, 23} {
		at := time.Date(2026, 7, 20, hour, 0, 0, 0, time.UTC)
		if r.reserveActive(at) {
			t.Fatalf("second reservation at %02d:00 = true, want false", hour)
		}
	}
	if !r.reserveActive(time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("next UTC day = false, want true")
	}
}

func TestCLITelemetryReservoirTreatsSlotStateFromOlderBuildAsSpent(t *testing.T) {
	dir := t.TempDir()
	body := `{"active_day":"2026-07-20","active_slots":[0],"invoked_day":"","invoked_seen":[]}`
	if err := os.WriteFile(filepath.Join(dir, cliTelemetryStateFile), []byte(body), 0o600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	r := newCLITelemetryReservoir(dir)
	if r.reserveActive(time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC)) {
		t.Fatal("reservation on a day an older build already reported = true, want false")
	}
	if !r.reserveActive(time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC)) {
		t.Fatal("next UTC day = false, want true")
	}
}
