package timestamps

import "testing"

func TestStoredTimeComparison(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"2026-10-21 09:00:00.123456789 +0530 IST", "2026-10-21 04:00:00.123456789 +0000 UTC", -1},
		{"2026-10-21 09:00:00.123456789 +0530 +0530 m=+1.234", "2026-10-21 03:30:00.123456789 +0000 UTC", 0},
		{"2026-10-21T09:00:00.123456790+05:30", "2026-10-21 03:30:00.123456789 +0000 UTC", 1},
		{"2026-10-21 03:30:00", "2026-10-21 03:30:00 +0000 UTC", 0},
		{"2026-10-21", "2026-10-21T00:00:00Z", 0},
		{"invalid", "2026-10-21", 1},
		{"invalid-a", "invalid-b", -1},
	} {
		if got := compareStored(tc.left, tc.right); got != tc.want {
			t.Errorf("compare(%q,%q)=%d, want %d", tc.left, tc.right, got, tc.want)
		}
		if got := compareStored(tc.right, tc.left); got != -tc.want {
			t.Errorf("reverse compare(%q,%q)=%d, want %d", tc.left, tc.right, got, -tc.want)
		}
	}
}
