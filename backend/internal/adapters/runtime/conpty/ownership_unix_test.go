//go:build darwin || linux

package conpty

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty/ptyregistry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestRuntimeRecoveryOnlyStopsMatchingHostProcess(t *testing.T) {
	isolateRegistry(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	original := New(Options{})
	handle, err := original.Create(ctx, ports.RuntimeConfig{
		SessionID: "ownership-recovery", WorkspacePath: t.TempDir(),
		Argv: []string{"/bin/sh", "-c", "sleep 30"}, Env: map[string]string{runtimeLaunchIDEnv: "launch-recovery"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		if err := original.Destroy(cleanupCtx, handle); err != nil {
			t.Errorf("cleanup owned host: %v", err)
		}
	})
	entries, _, err := ptyregistry.Scan(ctx)
	if err != nil || len(entries) != 1 || entries[0].ProcessStartID == "" {
		t.Fatalf("started host registry = %+v, %v; want captured process identity", entries, err)
	}
	owned := entries[0]
	if ownership := processalive.CheckOwnership(ctx, owned.PtyHostPID, owned.ProcessStartID); ownership != processalive.OwnershipMatching {
		t.Fatalf("started host ownership = %q, want matching", ownership)
	}
	stale := owned
	stale.ProcessStartID = "previous-process-incarnation"
	if err := ptyregistry.Register(ctx, stale); err != nil {
		t.Fatal(err)
	}
	recovered := New(Options{})
	if err := recovered.Destroy(ctx, handle); err != nil {
		t.Fatalf("recover stale owner: %v", err)
	}
	if ownership := processalive.CheckOwnership(ctx, owned.PtyHostPID, owned.ProcessStartID); ownership != processalive.OwnershipMatching {
		t.Fatalf("stale recovery signaled unrelated live host: ownership=%q", ownership)
	}
	if err := ptyregistry.Register(ctx, owned); err != nil {
		t.Fatal(err)
	}
	recovered = New(Options{})
	// A re-executed -race host has an extra one-second runtime exit delay.
	// Keep that instrumentation delay outside this test's force-kill boundary.
	recovered.destroyWait = 3 * time.Second
	if err := recovered.Destroy(ctx, handle); err != nil {
		t.Fatalf("recover matching owner: %v", err)
	}
	if ownership := processalive.CheckOwnership(ctx, owned.PtyHostPID, owned.ProcessStartID); ownership != processalive.OwnershipGone && ownership != processalive.OwnershipReused {
		t.Fatalf("matching host survived teardown: ownership=%q", ownership)
	}
	entries, _, err = ptyregistry.Scan(ctx)
	if err != nil || len(entries) != 0 {
		t.Fatalf("matching teardown retained registry: %+v, %v", entries, err)
	}
}
