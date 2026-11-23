package timestamps

import (
	"testing"
	"time"
)

func TestParseLegacyDriverLayouts(t *testing.T) {
	cases := []struct {
		value string
		want  time.Time
	}{
		{"2026-10-21 18:15:40.123456789 +0200 +0200 m=+5.31", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21 18:15:40.123456789+02:00", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21T18:15:40.123456789+02:00", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21 10:45:40.123456789-05:30", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21 16:15:40.123456789", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21T16:15:40.123456789", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21 16:15:40Z", time.Date(2026, 10, 21, 16, 15, 40, 0, time.UTC)},
		{"2026-10-21 16:15", time.Date(2026, 10, 21, 16, 15, 0, 0, time.UTC)},
		{"2026-10-21T16:15", time.Date(2026, 10, 21, 16, 15, 0, 0, time.UTC)},
		{"2026-10-21 16:15Z", time.Date(2026, 10, 21, 16, 15, 0, 0, time.UTC)},
		{"2026-10-21", time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			got, ok := ParseLegacy(tc.value)
			if !ok || !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Fatalf("ParseLegacy(%q) = %v, %v; want %v in UTC", tc.value, got, ok, tc.want)
			}
		})
	}
}

func TestParseLegacyDriverLayoutsCompareInstants(t *testing.T) {
	cutoff := time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC)
	for _, value := range []string{"2026-10-21 03:30:00+00:00", "2026-10-21 09:00:00+05:30", "2026-10-21 03:30", "2026-10-21T03:30"} {
		got, ok := ParseLegacy(value)
		if !ok || !got.Before(cutoff) {
			t.Errorf("%q should be earlier than %v; got %v, parsed=%v", value, cutoff, got, ok)
		}
	}
}

func TestParseLegacyRejectsImpossibleDates(t *testing.T) {
	for _, value := range []string{"2026-02-30 03:30:00+05:30", "2026-02-30 03:30", "2026-02-30T03:30", "2026-02-30", "2026-10-21 25:30", "2026-10-21 03:61:00+05:30", "not a timestamp"} {
		if got, ok := ParseLegacy(value); ok {
			t.Errorf("ParseLegacy(%q) accepted impossible date %v", value, got)
		}
	}
}
