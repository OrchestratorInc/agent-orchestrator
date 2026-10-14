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
		{"no timezone", "You've hit your session limit · resets 10:20pm", "", true},
		{"unknown timezone", "You've hit your limit · resets 10:20pm (Mars/Olympus)", "", true},
		{"ambient timezone", "You've hit your limit · resets 10:20pm (Local)", "", true},
		{"invalid clock", "You've hit your limit · resets 13:89pm (Asia/Kolkata)", "", true},
		{"transport error", "Connection lost", "", false},
		{"ordinary rate limiting", "Too many requests. Retry after 30 seconds.", "", false},
		{"quoted prose", "The tool says You've hit your session limit · resets 10:20pm (Asia/Calcutta)", "", false},
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
