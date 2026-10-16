package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func newInputLeaseTestManager() *Manager {
	return &Manager{
		agentOperations: make(map[domain.SessionID]agentOperationKind),
		inputLeases:     make(map[domain.SessionID]int),
		inputDrained:    make(map[domain.SessionID]chan struct{}),
	}
}

func TestAgentOperationDrainHonorsContextAndReopensAdmission(t *testing.T) {
	m := newInputLeaseTestManager()
	release, ok := m.AcquireSessionInput("worker-1")
	if !ok {
		t.Fatal("initial input lease was refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	beginDone := make(chan error, 1)
	go func() {
		beginDone <- m.beginAgentOperation(ctx, "worker-1", agentOperationRestore)
	}()
	eventuallySessionInput(t, time.Second, func() bool { return m.SessionMutationInProgress("worker-1") })
	cancel()
	if err := <-beginDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("begin operation error = %v, want context.Canceled", err)
	}
	if m.SessionMutationInProgress("worker-1") {
		t.Fatal("cancelled operation left input admission closed")
	}
	release()
}

func TestAgentOperationAndInputLeaseAreScopedPerSession(t *testing.T) {
	m := newInputLeaseTestManager()
	if err := m.beginAgentOperation(context.Background(), "worker-1", agentOperationKill); err != nil {
		t.Fatalf("begin worker-1 operation: %v", err)
	}
	defer m.endAgentOperation("worker-1", agentOperationKill)

	if _, ok := m.AcquireSessionInput("worker-1"); ok {
		t.Fatal("worker-1 input admitted during its mutation")
	}
	release, ok := m.AcquireSessionInput("worker-2")
	if !ok {
		t.Fatal("worker-2 input was incorrectly gated by worker-1 mutation")
	}
	release()
	if err := m.beginAgentOperation(context.Background(), "worker-2", agentOperationRestore); err != nil {
		t.Fatalf("worker-2 operation was incorrectly blocked: %v", err)
	}
	m.endAgentOperation("worker-2", agentOperationRestore)
}

func TestResumeWaitsForHibernation(t *testing.T) {
	m := newInputLeaseTestManager()
	id := domain.SessionID("worker-1")
	if err := m.beginAgentOperation(context.Background(), id, agentOperationHibernate); err != nil {
		t.Fatal(err)
	}
	resumed := make(chan error, 1)
	go func() { resumed <- m.beginAgentResume(context.Background(), id) }()
	select {
	case err := <-resumed:
		t.Fatalf("resume overtook hibernation: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	m.endAgentOperation(id, agentOperationHibernate)
	if err := <-resumed; err != nil {
		t.Fatal(err)
	}
	m.endAgentResume(id)
}

func TestExplicitOperationsWaitForHibernation(t *testing.T) {
	for _, kind := range []agentOperationKind{agentOperationKill, agentOperationExit, agentOperationProviderSwitch, agentOperationRestore} {
		t.Run(string(kind), func(t *testing.T) {
			m := newInputLeaseTestManager()
			id := domain.SessionID("worker-1")
			if err := m.beginAgentOperation(context.Background(), id, agentOperationHibernate); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			acquired := make(chan error, 1)
			go func() { acquired <- m.beginAgentOperation(ctx, id, kind) }()
			select {
			case err := <-acquired:
				t.Fatalf("explicit operation failed/overtook hibernation: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			m.endAgentOperation(id, agentOperationHibernate)
			if err := <-acquired; err != nil {
				t.Fatal(err)
			}
			m.endAgentOperation(id, kind)
		})
	}
}

func eventuallySessionInput(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
