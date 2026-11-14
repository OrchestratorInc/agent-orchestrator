package mobilebridge

import (
	"context"
	"log/slog"
	"sync"
)

type ManagedTunnelDeps struct {
	Run     func(ctx context.Context, localPort int, rt *TunnelRuntime)
	Binary  string
	PIDPath string
	Log     *slog.Logger
	Fixed   *FixedTunnelSource
	HostID  string
}

type ManagedTunnel struct {
	deps ManagedTunnelDeps

	mu      sync.Mutex
	runtime *TunnelRuntime
	cancel  context.CancelFunc
	port    int
	done    chan struct{}
	nudge   chan struct{}
}

func NewManagedTunnel(deps ManagedTunnelDeps) *ManagedTunnel {
	return &ManagedTunnel{deps: deps, runtime: &TunnelRuntime{}}
}

func (m *ManagedTunnel) Nudge() {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case m.nudge <- struct{}{}:
	default:
	}
}

func (m *ManagedTunnel) Start(localPort int) {
	m.mu.Lock()
	if m.cancel != nil && m.port == localPort {
		m.mu.Unlock()
		return
	}
	m.stopLocked()

	ctx, cancel := context.WithCancel(context.Background())
	rt := &TunnelRuntime{}
	done := make(chan struct{})
	nudge := make(chan struct{}, 1)
	m.cancel, m.port, m.runtime, m.done, m.nudge = cancel, localPort, rt, done, nudge
	run := m.deps.Run
	if run == nil {
		run = func(ctx context.Context, localPort int, rt *TunnelRuntime) { m.runReal(ctx, localPort, rt, nudge) }
	}
	m.mu.Unlock()

	go func() {
		defer close(done)
		run(ctx, localPort, rt)
	}()
}

func (m *ManagedTunnel) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *ManagedTunnel) stopLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.port = 0
	m.done = nil
	m.runtime = &TunnelRuntime{}
}

func (m *ManagedTunnel) Endpoint() *TunnelEndpoint {
	m.mu.Lock()
	rt := m.runtime
	m.mu.Unlock()
	return rt.Endpoint()
}

func (m *ManagedTunnel) Status() TunnelStatus {
	m.mu.Lock()
	rt := m.runtime
	m.mu.Unlock()
	return rt.Snapshot()
}

func (m *ManagedTunnel) runReal(ctx context.Context, localPort int, rt *TunnelRuntime, nudge <-chan struct{}) {
	(&TunnelRunner{
		Binary:    m.deps.Binary,
		LocalPort: localPort,
		PIDPath:   m.deps.PIDPath,
		Runtime:   rt,
		Log:       m.deps.Log,
		Fixed:     m.deps.Fixed,
		HostID:    m.deps.HostID,
		nudge:     nudge,
	}).Run(ctx)
}
