package domain

import (
	"strings"
	"testing"
	"time"
)

func starterMetadata() map[string]string {
	return map[string]string{
		MetadataPlan:                 "starter",
		MetadataMaxActiveSandboxes:   "2",
		MetadataOrchestratorSlots:    "1",
		MetadataWindowHours:          "5",
		MetadataWindowSessionHours:   "6",
		MetadataWeeklySessionHours:   "20",
		MetadataManualResetsPerMonth: "1",
	}
}

func TestParsePlanLimits(t *testing.T) {
	limits, err := ParsePlanLimits(starterMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if limits.Plan != "starter" || limits.WorkerSlots() != 1 || limits.WindowMinutes() != 360 ||
		limits.WeeklyMinutes() != 1200 || limits.ManualResetsPerMonth != 1 || limits.WindowHours != 5 {
		t.Fatalf("limits = %+v", limits)
	}
}

func TestParsePlanLimitsRejectsIncompleteOrInconsistentMetadata(t *testing.T) {
	missing := starterMetadata()
	delete(missing, MetadataWeeklySessionHours)
	if _, err := ParsePlanLimits(missing); err == nil || !strings.Contains(err.Error(), MetadataWeeklySessionHours) {
		t.Fatalf("missing key: err = %v", err)
	}
	tooManyOrchestrators := starterMetadata()
	tooManyOrchestrators[MetadataOrchestratorSlots] = "3"
	if _, err := ParsePlanLimits(tooManyOrchestrators); err == nil {
		t.Fatal("orchestrator slots above the sandbox limit were accepted")
	}
	notANumber := starterMetadata()
	notANumber[MetadataWindowSessionHours] = "six"
	if _, err := ParsePlanLimits(notANumber); err == nil {
		t.Fatal("a non-numeric limit was accepted")
	}
}

func TestSubscriptionEntitled(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	recent, old := now.Add(-2*24*time.Hour), now.Add(-8*24*time.Hour)
	grace := 7 * 24 * time.Hour
	for _, tc := range []struct {
		status string
		since  *time.Time
		want   bool
	}{
		{"active", nil, true},
		{"trialing", nil, true},
		{"past_due", &recent, true},
		{"past_due", &old, false},
		{"unpaid", nil, false},
		{"canceled", nil, false},
		{"incomplete", nil, false},
		{"", nil, false},
	} {
		if got := SubscriptionEntitled(tc.status, tc.since, grace, now); got != tc.want {
			t.Errorf("%s since %v: entitled = %v, want %v", tc.status, tc.since, got, tc.want)
		}
	}
}

func TestUsageWeekBounds(t *testing.T) {
	anchor := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC) // a Monday
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)  // the following Thursday
	start, resets := UsageWeekBounds(anchor, nil, now)
	if !start.Equal(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)) || !resets.Equal(time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("week = %v .. %v", start, resets)
	}

	// A manual reset this week restarts the window but keeps the renewal day.
	reset := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	start, resets = UsageWeekBounds(anchor, &reset, now)
	if !start.Equal(reset) || !resets.Equal(time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("after reset: week = %v .. %v", start, resets)
	}

	// A reset from an earlier week no longer matters.
	earlier := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	if start, _ = UsageWeekBounds(anchor, &earlier, now); !start.Equal(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("stale reset moved the window: %v", start)
	}
}

func TestResetPeriodStart(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	period := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	if got := ResetPeriodStart(&period, now); !got.Equal(period) {
		t.Fatalf("with a billing period: %v", got)
	}
	if got := ResetPeriodStart(nil, now); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("calendar fallback: %v", got)
	}
}

func TestRunningSandboxesDefaultToTheTotalAndCannotExceedIt(t *testing.T) {
	limits, err := ParsePlanLimits(starterMetadata())
	if err != nil || limits.MaxRunningSandboxes != 2 || limits.RunningSlots() != 2 {
		t.Fatalf("missing running cap must default to the total: %+v %v", limits, err)
	}
	metadata := starterMetadata()
	metadata[MetadataMaxActiveSandboxes], metadata[MetadataMaxRunningSandboxes] = "8", "2"
	limits, err = ParsePlanLimits(metadata)
	if err != nil || limits.MaxActiveSandboxes != 8 || limits.RunningSlots() != 2 || limits.WorkerSlots() != 1 {
		t.Fatalf("limits = %+v, err = %v", limits, err)
	}
	metadata[MetadataMaxRunningSandboxes] = "9"
	if _, err := ParsePlanLimits(metadata); err == nil {
		t.Fatal("a running cap above the total was accepted")
	}
}

func TestParsePlanLimitsReadsOptionalFeatures(t *testing.T) {
	limits, err := ParsePlanLimits(starterMetadata())
	if err != nil || len(limits.Features) != 0 {
		t.Fatalf("no ao_features: %+v %v", limits.Features, err)
	}
	metadata := starterMetadata()
	metadata[MetadataFeatures] = " SSO, priority-support,,sso "
	limits, err = ParsePlanLimits(metadata)
	if err != nil || strings.Join(limits.Features, ",") != "sso,priority-support" {
		t.Fatalf("features = %v err = %v", limits.Features, err)
	}
	if !limits.HasFeature("sso") || limits.HasFeature("audit-log") {
		t.Fatalf("HasFeature wrong for %v", limits.Features)
	}
}

func TestPendingCheckoutOpen(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var none *PendingCheckout
	if none.Open(now) {
		t.Fatal("nil checkout is open")
	}
	if !(&PendingCheckout{ID: "cs_1", ExpiresAt: now.Add(time.Hour)}).Open(now) {
		t.Fatal("an hour left must be open")
	}
	if (&PendingCheckout{ID: "cs_1", ExpiresAt: now.Add(30 * time.Second)}).Open(now) {
		t.Fatal("a link about to expire must not be handed out")
	}
}
