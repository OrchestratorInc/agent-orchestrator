package systeminstall

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type updateObserverStub struct {
	mu       sync.Mutex
	marked   []string
	stale    map[domain.AgentHarness]int64
	gateHeld func() bool
	heldMark bool
}

func (o *updateObserverStub) MarkHarnessUpdated(_ context.Context, harness domain.AgentHarness, version string, _ time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.marked = append(o.marked, string(harness)+"@"+version)
	if o.gateHeld != nil {
		o.heldMark = o.gateHeld()
	}
	return nil
}

func (o *updateObserverStub) CountStaleBinarySessions(context.Context) (map[domain.AgentHarness]int64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stale, nil
}

func TestSuccessfulUpdateMarksSessionsWhileHoldingTheHarness(t *testing.T) {
	s, _ := updateWhileRunningService(t, []domain.SessionRecord{{ID: "kimi-1", Harness: domain.AgentHarness(TargetKimi)}})
	observer := &updateObserverStub{}
	observer.gateHeld = func() bool { return harnessHeld(s, domain.AgentHarness(TargetKimi)) }
	s.SetHarnessUpdateObserver(observer)
	if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetKimi, StatusSucceeded)
	if len(observer.marked) != 1 || observer.marked[0] != "kimi@1.3.0" {
		t.Fatalf("marked = %v, want kimi@1.3.0", observer.marked)
	}
	if !observer.heldMark {
		t.Fatal("sessions were marked after launches could start on the new release")
	}
}

func TestFailedUpdateMarksNothing(t *testing.T) {
	s, _ := updateWhileRunningService(t, nil)
	observer := &updateObserverStub{}
	s.SetHarnessUpdateObserver(observer)
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		if command.Argv[1] == "upgrade" {
			return errors.New("upgrade failed")
		}
		return nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetKimi, StatusFailed)
	if len(observer.marked) != 0 {
		t.Fatalf("marked = %v after a failed update", observer.marked)
	}
}

func TestKeptKegIsCleanedUpOnceSessionsMove(t *testing.T) {
	s, ran := updateWhileRunningService(t, []domain.SessionRecord{{ID: "kimi-1", Harness: domain.AgentHarness(TargetKimi)}})
	observer := &updateObserverStub{stale: map[domain.AgentHarness]int64{domain.AgentHarness(TargetKimi): 1}}
	s.SetHarnessUpdateObserver(observer)
	if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetKimi, StatusSucceeded)
	cleanups := func() int {
		count := 0
		for _, command := range *ran {
			if len(command.Argv) == 3 && command.Argv[1] == "cleanup" && command.Argv[2] == "kimi-code" {
				count++
			}
		}
		return count
	}

	s.RunDeferredCleanups(context.Background())
	if cleanups() != 0 {
		t.Fatal("cleaned up the keg a session still runs")
	}
	observer.mu.Lock()
	observer.stale = nil
	observer.mu.Unlock()
	s.RunDeferredCleanups(context.Background())
	s.RunDeferredCleanups(context.Background())
	if cleanups() != 1 {
		t.Fatalf("cleanups = %d, want exactly one once sessions moved", cleanups())
	}
}

func TestUpdateWithoutSessionsDefersNoCleanup(t *testing.T) {
	s, ran := updateWhileRunningService(t, nil)
	s.SetHarnessUpdateObserver(&updateObserverStub{})
	if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetKimi, StatusSucceeded)
	before := len(*ran)
	s.RunDeferredCleanups(context.Background())
	if len(*ran) != before {
		t.Fatalf("ran %v without a kept keg", (*ran)[before:])
	}
}

// gatedObserverStub reports a session on the kept keg only when counted under
// the harness gate: an update finished between an earlier read and the gate.
type gatedObserverStub struct {
	updateObserverStub
	held func() bool
}

func (o *gatedObserverStub) CountStaleBinarySessions(context.Context) (map[domain.AgentHarness]int64, error) {
	if o.held() {
		return map[domain.AgentHarness]int64{domain.AgentHarness(TargetKimi): 1}, nil
	}
	return nil, nil
}

func TestKeptKegCleanupCountsSessionsUnderTheHarnessGate(t *testing.T) {
	s, ran := updateWhileRunningService(t, []domain.SessionRecord{{ID: "kimi-1", Harness: domain.AgentHarness(TargetKimi)}})
	s.SetHarnessUpdateObserver(&updateObserverStub{stale: map[domain.AgentHarness]int64{domain.AgentHarness(TargetKimi): 1}})
	if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetKimi, StatusSucceeded)

	s.SetHarnessUpdateObserver(&gatedObserverStub{held: func() bool { return harnessHeld(s, domain.AgentHarness(TargetKimi)) }})
	s.RunDeferredCleanups(context.Background())
	for _, command := range *ran {
		if len(command.Argv) == 3 && command.Argv[1] == "cleanup" {
			t.Fatalf("ran %v on a count read before the harness gate", command.Argv)
		}
	}
}
