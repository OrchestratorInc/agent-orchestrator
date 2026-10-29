//go:build !windows

package processalive

import (
	"os"
	"testing"
	"time"
)

func TestAliveSince(t *testing.T) {
	pid := os.Getpid()
	now := time.Now()
	cases := map[string]struct {
		since time.Time
		want  bool
	}{
		"zero since falls back to Alive":      {time.Time{}, true},
		"owner recorded after process start":  {now.Add(time.Second), true},
		"start within clock drift of since":   {now.Add(-30 * time.Second), true},
		"pid reused after the recorded owner": {now.Add(-time.Hour), false},
	}
	for name, tc := range cases {
		if got := AliveSince(pid, tc.since); got != tc.want {
			t.Errorf("%s: AliveSince = %v, want %v", name, got, tc.want)
		}
	}
	if AliveSince(0, now) {
		t.Error("pid 0 reported alive")
	}
}
