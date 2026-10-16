package testfixture

import "testing"

func TestPromptLabelCorrelatesNativeEnrichmentWithoutMatchingPrefixes(t *testing.T) {
	for _, tc := range []struct {
		text string
		want string
	}{
		{"ao-seed", "ao-seed"},
		{"<system-reminder>\n<agent-context>native context</agent-context>\n</system-reminder>\n<user-query>ao-approve</user-query>", "ao-approve"},
		{"context mentions ao-seed\nactual task: ao-reject", "ao-reject"},
		{"ao-after-cancel", "ao-after-cancel"},
		{"ao-after-restore", "ao-after-restore"},
		{"not-ao-seed-extension", ""},
		{"ao-seed_extra", ""},
		{"no fixture task here", ""},
	} {
		if got := promptLabel(tc.text); got != tc.want {
			t.Fatalf("promptLabel(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
