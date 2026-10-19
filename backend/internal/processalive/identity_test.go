package processalive

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestIdentifyCurrentProcessIsStable(t *testing.T) {
	first := Identify(context.Background(), os.Getpid())
	second := Identify(context.Background(), os.Getpid())
	if first.StartID == "" || first.StartID != second.StartID {
		t.Fatalf("self identities = %+v, %+v; want stable process start identity", first, second)
	}
	if first.StartedAt.IsZero() || second.StartedAt.IsZero() || first.StartedAt.After(time.Now()) || second.StartedAt.After(time.Now()) {
		t.Fatalf("self start times = %v, %v; want known starts before now", first.StartedAt, second.StartedAt)
	}
	if first.StartedAt.Location() != time.UTC {
		t.Fatalf("self start timezone = %v, want UTC", first.StartedAt.Location())
	}
}

func TestIdentifyInvalidOrCancelled(t *testing.T) {
	for _, pid := range []int{-1, 0} {
		if got := Identify(context.Background(), pid); got != (Identity{}) {
			t.Fatalf("Identify(%d) = %+v, want zero", pid, got)
		}
		if got := StartTime(context.Background(), pid); got != "" {
			t.Fatalf("StartTime(%d) = %q, want unknown", pid, got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Identify(ctx, os.Getpid()); got != (Identity{}) {
		t.Fatalf("cancelled Identify = %+v, want zero", got)
	}
	if got := StartTime(ctx, os.Getpid()); got != "" {
		t.Fatalf("cancelled StartTime = %q, want unknown", got)
	}
}

func TestIdentifyExitedProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestIdentifyInvalidOrCancelled$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run child: %v", err)
	}
	if got := Identify(context.Background(), cmd.Process.Pid); got != (Identity{}) {
		t.Fatalf("Identify(exited child %d) = %+v, want zero", cmd.Process.Pid, got)
	}
}
