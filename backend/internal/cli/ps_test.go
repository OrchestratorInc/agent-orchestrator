package cli

import (
	"testing"
)

func TestFormatBytesCLI(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KB"},
		{1536 * 1024, "1.5 MB"},
		{45 * 1024 * 1024, "45 MB"},
		{2576980378, "2.4 GB"}, // 2.4 GiB rounded to whole bytes
		{int64(1536) * 1024 * 1024 * 1024, "1.5 TB"},
	}
	for _, tc := range cases {
		if got := formatBytesCLI(tc.bytes); got != tc.want {
			t.Fatalf("formatBytesCLI(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestSplitTrees(t *testing.T) {
	trees := []processTreeDTO{
		{SessionID: "a", State: "owned"},
		{SessionID: "b", State: "orphan"},
		{SessionID: "c", State: "foreign"},
		{SessionID: "d", State: "owned"},
	}
	owned, orphans, foreign := splitTrees(trees)
	if len(owned) != 2 || len(orphans) != 1 || len(foreign) != 1 {
		t.Fatalf("split = %d/%d/%d, want 2/1/1", len(owned), len(orphans), len(foreign))
	}
}
