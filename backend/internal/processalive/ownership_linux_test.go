//go:build linux

package processalive

import (
	"context"
	"os"
	"testing"
)

func TestCheckOwnershipDoesNotDependOnPS(t *testing.T) {
	ctx := context.Background()
	pid := os.Getpid()
	startID := Identify(ctx, pid).StartID
	if startID == "" {
		t.Fatal("missing kernel process identity")
	}
	t.Setenv("PATH", t.TempDir())
	if got := CheckOwnership(ctx, pid, startID); got != OwnershipMatching {
		t.Fatalf("ownership without ps = %q, want matching", got)
	}
}
