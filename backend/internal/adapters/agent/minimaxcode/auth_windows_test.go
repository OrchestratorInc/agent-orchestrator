package minimaxcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthStatusWindowsAvoidsLockingProbe(t *testing.T) {
	clearAuthCredentialEnv(t)
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got, err := plugin.AuthStatus(ctx); got != ports.AgentAuthStatusUnauthorized || err != nil {
		t.Fatalf("auth check = (%q, %v), want unauthorized without waiting for a provider probe", got, err)
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

func TestAuthStatusWindowsObservesOAuthLogout(t *testing.T) {
	clearAuthCredentialEnv(t)
	profile := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", profile)
	previous := runAuthProbe
	t.Cleanup(func() { runAuthProbe = previous })
	runAuthProbe = func(context.Context, string) ([]byte, error) {
		t.Fatal("Windows logout verification started a provider subprocess")
		return nil, nil
	}
	state := filepath.Join(profile, "auth", "auth-state.json")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte(`{"status":"authenticated"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := New()
	plugin.resolvedBinary = "mcode.cmd"
	check := func(want ports.AgentAuthStatus) {
		t.Helper()
		if got, err := plugin.AuthStatus(context.Background()); err != nil || got != want {
			t.Fatalf("auth status = (%q, %v), want %q", got, err, want)
		}
	}
	check(ports.AgentAuthStatusConfigured)
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	check(ports.AgentAuthStatusUnauthorized)
	if err := os.WriteFile(filepath.Join(profile, "config.yaml"), []byte("custom_provider:\n  other: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(ports.AgentAuthStatusUnknown)
}
