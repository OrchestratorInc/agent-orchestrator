package minimaxcode

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthStatusWindowsAvoidsLockingProbe(t *testing.T) {
	t.Setenv("MINIMAX_DATA_DIR", t.TempDir())
	previous := runAuthProbe
	t.Cleanup(func() { runAuthProbe = previous })
	probes := 0
	// Model a wrapper whose child holds the native profile lock until timeout.
	// Windows must never enter this subprocess path, even with a short deadline.
	runAuthProbe = func(ctx context.Context, _ string) ([]byte, error) {
		probes++
		<-ctx.Done()
		return nil, ctx.Err()
	}
	plugin := New()
	plugin.resolvedBinary = "mcode.cmd"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got, err := plugin.AuthStatus(ctx); got != ports.AgentAuthStatusUnknown || err != nil {
		t.Fatalf("auth check = (%q, %v), want unknown without waiting for a provider probe", got, err)
	}
	if probes != 0 {
		t.Fatalf("auth check started %d lock-taking probes on Windows", probes)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := plugin.AuthStatus(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("auth check hid caller cancellation: %v", err)
	}
}
