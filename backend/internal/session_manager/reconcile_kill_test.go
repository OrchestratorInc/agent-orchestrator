package sessionmanager

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// blockingProbeRuntime holds the startup health probe for one handle open
// until the test releases it, so another session stays queued behind the
// single reconcile worker.
type blockingProbeRuntime struct {
	*fakeRuntime
	blockHandle string
	entered     chan struct{}
	release     chan struct{}
	mu          sync.Mutex
	probed      []string
}

func (r *blockingProbeRuntime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	r.mu.Lock()
	r.probed = append(r.probed, handle.ID)
	r.mu.Unlock()
	if handle.ID == r.blockHandle {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return r.fakeRuntime.IsAlive(ctx, handle)
}

func startBlockedStartupReconcile(t *testing.T) (*Manager, *fakeStore, *blockingProbeRuntime, <-chan struct{}) {
	t.Helper()
	m, st, rt, _ := newManager()
	slow := mkLive("slow")
	slow.Metadata.Branch = "ao/slow"
	slow.Metadata.RuntimeHandleID = "h-slow"
	queued := mkLive("queued")
	queued.Metadata.Branch = "ao/queued"
	queued.Metadata.RuntimeHandleID = "h-queued"
	st.sessions[slow.ID] = slow
	st.sessions[queued.ID] = queued
	rt.aliveByHandle = map[string]bool{"h-slow": true, "h-queued": true}
	blocking := &blockingProbeRuntime{fakeRuntime: rt, blockHandle: "h-slow", entered: make(chan struct{}), release: make(chan struct{})}
	m.runtime = blocking
	m.reconcileWorkers = 1

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.reconcileLivePass(context.Background(), []domain.SessionRecord{slow, queued})
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("startup probe never started")
	}
	return m, st, blocking, done
}

func TestKillPreemptsQueuedStartupReconcile(t *testing.T) {
	m, st, blocking, done := startBlockedStartupReconcile(t)
	defer func() {
		close(blocking.release)
		<-done
	}()

	if _, err := m.RequestKill(context.Background(), "queued"); err != nil {
		t.Fatalf("archive while startup recovery is still queued = %v, want success", err)
	}
	if !st.sessions["queued"].IsTerminated {
		t.Fatal("queued session was not terminated")
	}
}

func TestKillWaitsForRunningStartupProbe(t *testing.T) {
	m, st, blocking, done := startBlockedStartupReconcile(t)

	killed := make(chan error, 1)
	go func() {
		_, err := m.RequestKill(context.Background(), "slow")
		killed <- err
	}()
	select {
	case err := <-killed:
		t.Fatalf("kill returned while the startup probe still owned the session: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(blocking.release)
	select {
	case err := <-killed:
		if err != nil {
			t.Fatalf("archive after startup probe = %v, want success", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kill did not proceed after the startup probe finished")
	}
	<-done
	if !st.sessions["slow"].IsTerminated {
		t.Fatal("probed session was not terminated")
	}
}

func TestStartupReconcileSkipsSessionPreemptedByKill(t *testing.T) {
	m, _, blocking, done := startBlockedStartupReconcile(t)
	_, killErr := m.RequestKill(context.Background(), "queued")
	close(blocking.release)
	<-done
	if killErr != nil {
		t.Fatalf("kill = %v", killErr)
	}
	blocking.mu.Lock()
	probed := slices.Clone(blocking.probed)
	blocking.mu.Unlock()
	if slices.Contains(probed, "h-queued") {
		t.Fatalf("startup reconcile probed a session it no longer owned: %v", probed)
	}
	if m.SessionMutationInProgress("queued") || m.SessionMutationInProgress("slow") {
		t.Fatal("startup reconcile left an operation fence behind")
	}
	if got := m.SessionStatusReadiness(domain.SessionRecord{ID: "queued"}); got == "checking" {
		t.Fatalf("preempted session readiness = %q, want settled", got)
	}
}
