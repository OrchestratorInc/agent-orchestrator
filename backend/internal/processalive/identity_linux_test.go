//go:build linux

package processalive

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestIdentifyLinuxWithoutPSKeepsKernelIdentity(t *testing.T) {
	first := Identify(context.Background(), os.Getpid())
	bootStart, err := BootStartTime(context.Background(), os.Getpid())
	if err != nil || bootStart == "" {
		t.Fatalf("boot start = %q, err = %v, want kernel identity", bootStart, err)
	}
	t.Setenv("PATH", t.TempDir())
	withoutPS := Identify(context.Background(), os.Getpid())
	if first.StartID == "" || withoutPS.StartID != first.StartID || !withoutPS.StartedAt.IsZero() {
		t.Fatalf("identities = %+v, %+v; want same kernel identity without wall-clock time", first, withoutPS)
	}
	if got, err := BootStartTime(context.Background(), os.Getpid()); err != nil || got != bootStart {
		t.Fatalf("boot start without ps = %q, err = %v, want %q", got, err, bootStart)
	}
}

func TestBootStartTimeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := BootStartTime(ctx, os.Getpid()); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled boot start = %q, err = %v, want no identity and cancellation", got, err)
	}
}

func TestLinuxStartTicks(t *testing.T) {
	const fields = "S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 "
	for _, tc := range []struct {
		name string
		stat string
		want string
	}{
		{"ordinary", "123 (worker) " + fields + "123456 99", "123456"},
		{"spaces and parentheses", "123 (worker (a) b)) " + fields + "42 99", "42"},
		{"zero ticks", "123 (worker) " + fields + "0", "0"},
		{"missing comm terminator", "123 (worker " + fields + "42", ""},
		{"truncated", "123 (worker) S 1", ""},
		{"non-numeric ticks", "123 (worker) " + fields + "bad", ""},
		{"negative ticks", "123 (worker) " + fields + "-1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := linuxStartTicks(tc.stat); got != tc.want {
				t.Fatalf("linuxStartTicks(%q) = %q, want %q", tc.stat, got, tc.want)
			}
		})
	}
}
