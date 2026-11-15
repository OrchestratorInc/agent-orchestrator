package mobilebridge

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingRun struct {
	mu     sync.Mutex
	ports  []int
	active int
}

func (r *recordingRun) run(ctx context.Context, port int, _ *TunnelRuntime) {
	r.mu.Lock()
	r.ports = append(r.ports, port)
	r.active++
	r.mu.Unlock()
	<-ctx.Done()
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
}

func (r *recordingRun) snapshot() ([]int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.ports...), r.active
}

func settleFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never held")
}

func TestManagedTunnelStartIsIdempotent(t *testing.T) {
	rec := &recordingRun{}
	m := NewManagedTunnel(ManagedTunnelDeps{Run: rec.run})
	defer m.Stop()

	m.Start(3011)
	m.Start(3011)
	m.Start(3011)

	settleFor(t, func() bool { p, _ := rec.snapshot(); return len(p) >= 1 })
	time.Sleep(50 * time.Millisecond)
	ports, active := rec.snapshot()
	if len(ports) != 1 {
		t.Fatalf("launched %d times %v, want 1", len(ports), ports)
	}
	if active != 1 {
		t.Fatalf("%d connectors alive, want 1", active)
	}
}

func TestManagedTunnelRestartsWhenThePortChanges(t *testing.T) {
	rec := &recordingRun{}
	m := NewManagedTunnel(ManagedTunnelDeps{Run: rec.run})
	defer m.Stop()

	m.Start(3011)
	settleFor(t, func() bool { p, _ := rec.snapshot(); return len(p) == 1 })
	m.Start(49152)
	settleFor(t, func() bool { p, _ := rec.snapshot(); return len(p) == 2 })

	ports, _ := rec.snapshot()
	if ports[0] != 3011 || ports[1] != 49152 {
		t.Fatalf("ports = %v, want [3011 49152]", ports)
	}
	settleFor(t, func() bool { _, active := rec.snapshot(); return active == 1 })
}

func TestManagedTunnelStopEndsTheConnectorAndClearsTheEndpoint(t *testing.T) {
	rec := &recordingRun{}
	m := NewManagedTunnel(ManagedTunnelDeps{Run: rec.run})

	m.Start(3011)
	settleFor(t, func() bool { _, active := rec.snapshot(); return active == 1 })

	m.Stop()

	settleFor(t, func() bool { _, active := rec.snapshot(); return active == 0 })
	if m.Endpoint() != nil {
		t.Error("still advertising after Stop")
	}
	if m.Status().Running {
		t.Error("Status still reports running after Stop")
	}
}

func TestManagedTunnelStopBeforeStartIsHarmless(t *testing.T) {
	m := NewManagedTunnel(ManagedTunnelDeps{Run: func(context.Context, int, *TunnelRuntime) {}})
	m.Stop()
	m.Stop()
	if m.Endpoint() != nil {
		t.Error("endpoint from a tunnel that never started")
	}
}

func TestManagedTunnelStopDoesNotWaitForASlowConnector(t *testing.T) {
	released := make(chan struct{})
	m := NewManagedTunnel(ManagedTunnelDeps{
		Run: func(ctx context.Context, _ int, _ *TunnelRuntime) {
			<-ctx.Done()
			<-released
		},
	})
	m.Start(3011)
	settleFor(t, func() bool { return m.Status().Running || true })

	go func() {
		time.Sleep(2 * time.Second)
		close(released)
	}()

	start := time.Now()
	m.Stop()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("Stop blocked for %v waiting on the connector; it must return promptly", elapsed)
	}
}

func TestManagedTunnelStopWithdrawsTheEndpointBeforeReturning(t *testing.T) {
	released := make(chan struct{})
	defer close(released)
	m := NewManagedTunnel(ManagedTunnelDeps{
		Run: func(ctx context.Context, _ int, rt *TunnelRuntime) {
			rt.Started()
			rt.Line(`INF |  https://abc.trycloudflare.com  |`)
			rt.Line(`INF Registered tunnel connection connIndex=0`)
			rt.Settled()
			<-ctx.Done()
			<-released
		},
	})
	m.Start(3011)
	settleFor(t, func() bool { return m.Endpoint() != nil })

	m.Stop()

	if m.Endpoint() != nil {
		t.Fatal("still advertising immediately after Stop returned")
	}
}

func TestManagedTunnelStartDropsAnEarlierNudge(t *testing.T) {
	m := NewManagedTunnel(ManagedTunnelDeps{Run: func(ctx context.Context, _ int, _ *TunnelRuntime) { <-ctx.Done() }})
	defer m.Stop()
	m.Nudge()
	m.Start(4321)
	if len(m.nudge) != 0 {
		t.Fatal("a new run inherited an earlier nudge")
	}
	old := m.nudge
	m.Start(4322)
	m.Nudge()
	if len(old) != 0 || len(m.nudge) != 1 {
		t.Fatalf("the old run's channel holds %d, the new run's %d", len(old), len(m.nudge))
	}
}
