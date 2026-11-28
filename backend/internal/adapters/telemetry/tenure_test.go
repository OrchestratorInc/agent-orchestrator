package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestTenureBucketBoundaries(t *testing.T) {
	cases := map[int]TenureBucket{
		0:    TenureFirstDay,
		1:    TenureFirstWeek,
		6:    TenureFirstWeek,
		7:    TenureFirstMonth,
		29:   TenureFirstMonth,
		30:   TenureQuarter,
		89:   TenureQuarter,
		90:   TenureHalfYear,
		179:  TenureHalfYear,
		180:  TenureVeteran,
		2000: TenureVeteran,
	}
	for age, want := range cases {
		if got := tenureBucketFor(age); got != want {
			t.Errorf("tenureBucketFor(%d) = %q, want %q", age, got, want)
		}
	}
}

func TestTenureFirstObservationIsDayZero(t *testing.T) {
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	tr := newTenureTracker(t.TempDir(), func() time.Time { return now })

	age, active, bucket := tr.observe()
	if age != 0 || active != 1 || bucket != TenureFirstDay {
		t.Fatalf("fresh install = (age %d, active %d, %q), want (0, 1, d0)", age, active, bucket)
	}
}

// Active days must count calendar days, not observations. The sink calls this on
// every event, so counting calls would turn a busy afternoon into months of
// apparent tenure.
func TestTenureCountsCalendarDaysNotCalls(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
	tr := newTenureTracker(t.TempDir(), func() time.Time { return now })

	for range 50 {
		tr.observe()
	}
	if _, active, _ := tr.observe(); active != 1 {
		t.Fatalf("51 observations in one day = %d active days, want 1", active)
	}

	now = now.Add(24 * time.Hour)
	if _, active, _ := tr.observe(); active != 2 {
		t.Fatalf("second calendar day = %d active days, want 2", active)
	}

	// A gap does not backfill: eight days later is one more active day, but the
	// age jumps, which is exactly the difference the two numbers exist to show.
	now = now.Add(8 * 24 * time.Hour)
	age, active, bucket := tr.observe()
	if active != 3 {
		t.Fatalf("after a gap = %d active days, want 3", active)
	}
	if age != 9 {
		t.Fatalf("age = %d days, want 9", age)
	}
	if bucket != TenureFirstMonth {
		t.Fatalf("bucket = %q, want w1_4", bucket)
	}
}

func TestTenureSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	first := newTenureTracker(dir, func() time.Time { return now })
	first.observe()

	// A month later, a fresh process must read the original first-seen date
	// rather than treating the install as new.
	later := now.Add(40 * 24 * time.Hour)
	second := newTenureTracker(dir, func() time.Time { return later })
	age, active, bucket := second.observe()
	if age != 40 {
		t.Fatalf("age after restart = %d, want 40", age)
	}
	if active != 2 {
		t.Fatalf("active days after restart = %d, want 2", active)
	}
	if bucket != TenureQuarter {
		t.Fatalf("bucket = %q, want m1_2", bucket)
	}
}

// A state file copied from another machine, or a clock moved backwards, must not
// report a negative age.
func TestTenureClampsBackwardsClock(t *testing.T) {
	dir := t.TempDir()
	body := `{"first_seen":"2026-09-01","last_active":"2026-09-01","active_days":4}`
	if err := os.WriteFile(filepath.Join(dir, tenureStateFile), []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	tr := newTenureTracker(dir, func() time.Time { return now })

	age, _, bucket := tr.observe()
	if age != 0 {
		t.Fatalf("age with a future first_seen = %d, want 0", age)
	}
	if bucket != TenureFirstDay {
		t.Fatalf("bucket = %q, want d0", bucket)
	}
}

func TestTenureSurvivesCorruptState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tenureStateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	tr := newTenureTracker(dir, func() time.Time { return now })

	if age, active, bucket := tr.observe(); age != 0 || active != 1 || bucket != TenureFirstDay {
		t.Fatalf("corrupt state = (%d, %d, %q), want (0, 1, d0)", age, active, bucket)
	}
}

// Telemetry must never be the reason a read-only data dir breaks the daemon.
func TestTenureWorksWithNoDataDir(t *testing.T) {
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	tr := newTenureTracker("", func() time.Time { return now })
	if age, active, _ := tr.observe(); age != 0 || active != 1 {
		t.Fatalf("no data dir = (%d, %d), want (0, 1)", age, active)
	}
}

// End-to-end: the properties must survive the sink's own sanitisation and land in
// the request body, because they are stamped outside the per-event payload
// allowlist and that is easy to get wrong.
func TestPostHogSinkStampsDefaultAgentAndTenure(t *testing.T) {
	var got map[string]any
	sink, err := NewPostHogSink(t.TempDir(), "phc_test", "https://us.i.posthog.com", "0.11.3", "codex",
		roundTripClient(func(req *http.Request) (*http.Response, error) {
			defer req.Body.Close()
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			got, _ = body["properties"].(map[string]any)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}), nil)
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	sink.Emit(context.Background(), ports.TelemetryEvent{
		Name: "ao.daemon.started", Source: "daemon", OccurredAt: time.Now().UTC(),
		Level: ports.TelemetryLevelInfo,
	})
	_ = sink.Close(context.Background())

	if got == nil {
		t.Fatal("no properties reached the wire")
	}
	if got["default_agent"] != "codex" {
		t.Errorf("default_agent = %v, want codex", got["default_agent"])
	}
	if got["tenure"] != string(TenureFirstDay) {
		t.Errorf("tenure = %v, want d0", got["tenure"])
	}
	// The classifier: every daemon/CLI event must carry client="cli" so a
	// shared event like ao.app.active splits by client across desktop/mobile/cli.
	if got["client"] != "cli" {
		t.Errorf("client = %v, want cli", got["client"])
	}
	for _, key := range []string{"install_age_days", "active_days"} {
		if _, ok := got[key]; !ok {
			t.Errorf("%s missing from the payload", key)
		}
	}
}

func TestVersionChannel(t *testing.T) {
	cases := map[string]string{
		"0.11.3":            "stable",
		"1.0.0":             "stable",
		"0.11.3-nightly.5":  "nightly",
		"0.12.0-NIGHTLY.1":  "nightly",
		"":                  "stable", // absent version is not nightly
		"0.11.3-feature.42": "stable", // a pinned feature build is not a nightly
	}
	for v, want := range cases {
		if got := versionChannel(v); got != want {
			t.Errorf("versionChannel(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestSafeAgentSlug(t *testing.T) {
	keep := []string{"claude-code", "codex", "cursor", "grok", "opencode", "a"}
	for _, v := range keep {
		if got := safeAgentSlug(v); got != v {
			t.Errorf("safeAgentSlug(%q) = %q, want it kept", v, got)
		}
	}
	// A path, whitespace, uppercase, or an over-long value must be dropped so it
	// cannot leak on ao.daemon.started before the resolver rejects it.
	drop := []string{
		"/Users/me/bin/agent", "../codex", "claude code", "Claude-Code",
		"agent;rm -rf", strings.Repeat("x", 60), "", "   ",
	}
	for _, v := range drop {
		if got := safeAgentSlug(v); got != "" {
			t.Errorf("safeAgentSlug(%q) = %q, want dropped", v, got)
		}
	}
}
