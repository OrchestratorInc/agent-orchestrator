package domain

import (
	"testing"
	"time"
)

func TestParseConversationUsageLimit(t *testing.T) {
	failedAt := time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, message, reset string
		limited              bool
	}{
		{"reference", "You've hit your session limit · resets 10:20pm (Asia/Calcutta)", "2026-10-14T16:50:00Z", true},
		{"curly apostrophe", "You’ve hit your limit · resets 10:20pm (Asia/Kolkata)", "2026-10-14T16:50:00Z", true},
		{"date rollover", "You've hit your weekly limit · resets 9pm (Asia/Kolkata)", "2026-10-15T15:30:00Z", true},
		{"midnight", "You've hit your usage limit · resets 12am (Asia/Kolkata)", "2026-10-14T18:30:00Z", true},
		{"explicit tomorrow", "Usage limit reached\n\nResets tomorrow 10:20pm (Asia/Kolkata)", "2026-10-15T16:50:00Z", true},
		{"absolute", "Usage limit reached. Resets at 2026-10-15T12:00:00+05:30", "2026-10-15T06:30:00Z", true},
		{"unknown deadline", "Usage limit reached\n\nResets tomorrow.", "", true},
		{"Claude individual spend", "You've hit your individual spend limit · run /usage-credits to ask your admin for a higher limit", "", true},
		{"Claude no quota", "The Claude account has no available quota.", "", true},
		{"no timezone", "You've hit your session limit · resets 10:20pm", "", true},
		{"unknown timezone", "You've hit your limit · resets 10:20pm (Mars/Olympus)", "", true},
		{"ambient timezone", "You've hit your limit · resets 10:20pm (Local)", "", true},
		{"invalid clock", "You've hit your limit · resets 13:89pm (Asia/Kolkata)", "", true},
		{"transport error", "Connection lost", "", false},
		{"ordinary rate limiting", "Too many requests. Retry after 30 seconds.", "", false},
		{"quoted prose", "The tool says You've hit your session limit · resets 10:20pm (Asia/Calcutta)", "", false},
		{"quoted Claude quota", "The tool says The Claude account has no available quota.", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit := ParseConversationUsageLimit(tc.message, &failedAt)
			if (limit != nil) != tc.limited {
				t.Fatalf("limit = %+v, want limited %v", limit, tc.limited)
			}
			if !tc.limited {
				return
			}
			reset := ""
			if limit.ResetsAt != nil {
				reset = limit.ResetsAt.Format(time.RFC3339)
			}
			if reset != tc.reset || limit.AutoResume {
				t.Fatalf("limit = %+v (reset %q), want %q and no automatic consent", limit, reset, tc.reset)
			}
		})
	}
}

func TestUsageLimitWithoutFailureTimeCannotInferDate(t *testing.T) {
	limit := ParseConversationUsageLimit("You've hit your session limit · resets 10:20pm (Asia/Calcutta)", nil)
	if limit == nil || limit.ResetsAt != nil {
		t.Fatalf("limit = %+v, want unknown reset", limit)
	}
}

func TestUsageLimitNonexistentDSTClockRemainsUnknown(t *testing.T) {
	failedAt := time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)
	limit := ParseConversationUsageLimit("You've hit your session limit · resets 2:30am (America/New_York)", &failedAt)
	if limit == nil || limit.ResetsAt != nil {
		t.Fatalf("limit = %+v, want unknown reset", limit)
	}
}

func TestUsageLimitRepeatedDSTClockRemainsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, failedAt, reset string
	}{
		{"New York before repeated hour", "2026-11-01T04:15:00Z", "1:30am (America/New_York)"},
		{"New York between occurrences", "2026-11-01T05:45:00Z", "1:30am (America/New_York)"},
		{"New York during second occurrence", "2026-11-01T06:15:00Z", "1:30am (America/New_York)"},
		{"New York explicit tomorrow", "2026-10-31T12:00:00Z", "tomorrow 1:30am (America/New_York)"},
		{"New York implicit date rollover", "2026-10-31T12:00:00Z", "1:30am (America/New_York)"},
		{"Lord Howe before repeated half hour", "2026-04-04T14:15:00Z", "1:45am (Australia/Lord_Howe)"},
		{"Lord Howe between occurrences", "2026-04-04T14:50:00Z", "1:45am (Australia/Lord_Howe)"},
		{"Lord Howe during second occurrence", "2026-04-04T15:05:00Z", "1:45am (Australia/Lord_Howe)"},
		{"Lord Howe explicit tomorrow", "2026-04-03T21:00:00Z", "tomorrow 1:45am (Australia/Lord_Howe)"},
		{"Lord Howe implicit date rollover", "2026-04-03T21:00:00Z", "1:45am (Australia/Lord_Howe)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failedAt, err := time.Parse(time.RFC3339, tc.failedAt)
			if err != nil {
				t.Fatal(err)
			}
			limit := ParseConversationUsageLimit("You've hit your session limit · resets "+tc.reset, &failedAt)
			if limit == nil || limit.ResetsAt != nil {
				t.Fatalf("limit = %+v, want unknown reset for a repeated wall clock", limit)
			}
		})
	}
}

func TestUsageLimitUnambiguousResetClocks(t *testing.T) {
	for _, tc := range []struct {
		name, failedAt, clock, reset string
	}{
		{"UTC", "2026-11-01T01:15:00Z", "1:30am (UTC)", "2026-11-01T01:30:00Z"},
		{"no DST", "2026-11-01T01:15:00Z", "7am (Asia/Kolkata)", "2026-11-01T01:30:00Z"},
		{"New York after repeated hour", "2026-11-01T06:15:00Z", "2:30am (America/New_York)", "2026-11-01T07:30:00Z"},
		{"New York tomorrow skips current fold", "2026-11-01T05:15:00Z", "tomorrow 1:30am (America/New_York)", "2026-11-02T06:30:00Z"},
		{"Lord Howe after repeated half hour", "2026-04-04T14:15:00Z", "2:15am (Australia/Lord_Howe)", "2026-04-04T15:45:00Z"},
		{"Lord Howe tomorrow skips current fold", "2026-04-04T14:15:00Z", "tomorrow 1:45am (Australia/Lord_Howe)", "2026-04-05T15:15:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failedAt, err := time.Parse(time.RFC3339, tc.failedAt)
			if err != nil {
				t.Fatal(err)
			}
			limit := ParseConversationUsageLimit("You've hit your session limit · resets "+tc.clock, &failedAt)
			if limit == nil || limit.ResetsAt == nil || limit.ResetsAt.Format(time.RFC3339) != tc.reset {
				t.Fatalf("limit = %+v, want reset %s", limit, tc.reset)
			}
		})
	}
}
