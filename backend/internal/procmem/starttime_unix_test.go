//go:build !windows

package procmem

import (
	"context"
	"os"
	"testing"
)

func TestParseStartTimes(t *testing.T) {
	got := ParseStartTimes("    1 Ss   Sun Nov  8 23:23:37 2026\n 4242 T    Mon Nov 16 10:00:00 2026\n 4243 Z+   Mon Nov 16 10:00:01 2026\nbad line\n")
	if got[1] != "Sun Nov 8 23:23:37 2026" || got[4242] != "Mon Nov 16 10:00:00 2026" || len(got) != 2 {
		t.Fatalf("start times = %#v", got)
	}
}

func TestStartTimesIncludesThisProcess(t *testing.T) {
	starts, err := StartTimes(context.Background(), nil)
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	if starts[os.Getpid()] == "" {
		t.Fatalf("no start time for this process among %d", len(starts))
	}
}
