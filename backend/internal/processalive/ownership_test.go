package processalive

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestCheckOwnership(t *testing.T) {
	ctx := context.Background()
	pid := os.Getpid()
	identity := Identify(ctx, pid)
	if identity.StartID == "" {
		t.Fatal("could not identify the running test process")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		pid     int
		startID string
		want    Ownership
	}{
		{"same process", ctx, pid, identity.StartID, OwnershipMatching},
		{"reused PID", ctx, pid, "previous:" + identity.StartID, OwnershipReused},
		{"legacy live PID", ctx, pid, "", OwnershipUnknown},
		{"cancelled observation", cancelled, pid, identity.StartID, OwnershipUnknown},
		{"invalid PID", ctx, 0, identity.StartID, OwnershipUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CheckOwnership(tc.ctx, tc.pid, tc.startID); got != tc.want {
				t.Fatalf("ownership = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckOwnershipAfterProcessExit(t *testing.T) {
	if os.Getenv("AO_OWNERSHIP_EXIT_HELPER") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCheckOwnershipAfterProcessExit$")
	cmd.Env = append(os.Environ(), "AO_OWNERSHIP_EXIT_HELPER=1")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := CheckOwnership(context.Background(), cmd.Process.Pid, ""); got != OwnershipGone {
		t.Fatalf("exited process ownership = %q, want %q", got, OwnershipGone)
	}
}
