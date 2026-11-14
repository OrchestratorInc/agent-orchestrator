package codexops

import (
	"context"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Gate struct {
	mu            sync.Mutex
	shared        int
	exclusive     bool
	exclusiveDone chan struct{}
	drained       chan struct{}
}

func NewGate() *Gate {
	drained := make(chan struct{})
	close(drained)
	return &Gate{drained: drained}
}

func (g *Gate) AcquireShared(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.exclusive {
		g.mu.Unlock()
		return nil, ports.ErrCodexAccountSwitchInProgress
	}
	release := g.acquireSharedLocked()
	g.mu.Unlock()
	return release, nil
}

func (g *Gate) AcquireSharedWait(ctx context.Context) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		if !g.exclusive {
			release := g.acquireSharedLocked()
			g.mu.Unlock()
			return release, nil
		}
		done := g.exclusiveDone
		g.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (g *Gate) acquireSharedLocked() func() {
	if g.shared == 0 {
		g.drained = make(chan struct{})
	}
	g.shared++

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.shared--
			if g.shared == 0 {
				close(g.drained)
			}
			g.mu.Unlock()
		})
	}
}

func (g *Gate) AcquireExclusive(ctx context.Context) (ports.CodexOperationLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.exclusive {
		g.mu.Unlock()
		return nil, ports.ErrCodexAccountSwitchInProgress
	}
	g.exclusive = true
	g.exclusiveDone = make(chan struct{})
	drained := g.drained
	g.mu.Unlock()

	select {
	case <-drained:
		return &exclusiveLease{gate: g}, nil
	case <-ctx.Done():
		g.mu.Lock()
		g.exclusive = false
		close(g.exclusiveDone)
		g.exclusiveDone = nil
		g.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (g *Gate) ExclusivePendingOrHeld() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.exclusive
}

type exclusiveLease struct {
	gate *Gate
	once sync.Once
}

func (l *exclusiveLease) Release() {
	if l == nil || l.gate == nil {
		return
	}
	l.once.Do(func() {
		l.gate.mu.Lock()
		l.gate.exclusive = false
		close(l.gate.exclusiveDone)
		l.gate.exclusiveDone = nil
		l.gate.mu.Unlock()
	})
}

var _ ports.CodexOperationGate = (*Gate)(nil)
