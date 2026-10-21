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

// idleCoordinatorStub stands in for Session Manager. Stopping a session puts
// it to sleep, so the installer no longer sees it as live.
type idleCoordinatorStub struct {
	mu        sync.Mutex
	sessions  *[]domain.SessionRecord
	busy      []domain.SessionWait
	stops     int
	resumed   [][]domain.SessionID
	stopErr   error
	resumeErr error
}

func (c *idleCoordinatorStub) BusyHarnessSessions(context.Context, domain.AgentHarness) ([]domain.SessionWait, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]domain.SessionWait(nil), c.busy...), nil
}

func (c *idleCoordinatorStub) StopHarnessSessions(context.Context, domain.AgentHarness) ([]domain.SessionID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	if c.stopErr != nil {
		return nil, c.stopErr
	}
	now := time.Now()
	var stopped []domain.SessionID
	for i := range *c.sessions {
		(*c.sessions)[i].HibernatedAt = &now
		stopped = append(stopped, (*c.sessions)[i].ID)
	}
	return stopped, nil
}

func (c *idleCoordinatorStub) ResumeHarnessSessions(_ context.Context, ids []domain.SessionID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resumed = append(c.resumed, ids)
	return c.resumeErr
}

type mutableSessionLister struct {
	mu       *sync.Mutex
	sessions *[]domain.SessionRecord
}

func (l mutableSessionLister) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]domain.SessionRecord(nil), *l.sessions...), nil
}

// windowsIdleService is a Windows service with update-when-idle on, one live
// Auggie session, and an npm update that succeeds unless failUpdate is set.
func windowsIdleService(t *testing.T, failUpdate bool) (*Service, *idleCoordinatorStub, *[]ports.InstallCommand) {
	t.Helper()
	s := newTestService("windows", "npm")
	s.windowsUpdateWhenIdle = true
	sessions := []domain.SessionRecord{{ID: "auggie-1", Harness: domain.AgentHarness(TargetAuggie), Activity: domain.Activity{State: domain.ActivityIdle}}}
	coordinator := &idleCoordinatorStub{sessions: &sessions}
	s.sessions = mutableSessionLister{mu: &coordinator.mu, sessions: &sessions}
	s.SetIdleUpdateCoordinator(coordinator)
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	var mu sync.Mutex
	version := "0.5.0"
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		mu.Lock()
		defer mu.Unlock()
		return VerifyResult{ResolvedPath: `C:\Users\test\AppData\Roaming\npm\auggie.cmd`, Output: "auggie " + version}, nil
	})
	var ran []ports.InstallCommand
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		mu.Lock()
		defer mu.Unlock()
		ran = append(ran, command)
		if failUpdate {
			return errors.New("npm failed")
		}
		version = "0.6.0"
		return nil
	})
	return s, coordinator, &ran
}

func waitForJob(t *testing.T, s *Service, target Target, done func(Job) bool) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := s.Status(context.Background(), target)
		if err == nil && done(job) {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _ := s.Status(context.Background(), target)
	t.Fatalf("job never reached the expected state: %+v", job)
	return job
}

func TestWindowsUpdateWaitsForIdleSessions(t *testing.T) {
	s, coordinator, ran := windowsIdleService(t, false)
	coordinator.busy = []domain.SessionWait{{SessionID: "auggie-1", Reason: domain.SessionWaitCommand}}
	// No confirmation: the update just waits for the sessions.
	job, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, "")
	if err != nil || job.Status != StatusWaitingForIdle {
		t.Fatalf("update = %+v, %v; want waiting_for_idle", job, err)
	}
	// While a session runs a command, nothing is stopped and npm never runs.
	waiting := waitForJob(t, s, TargetAuggie, func(job Job) bool { return len(job.WaitingOn) == 1 })
	if waiting.WaitingOn[0].Reason != domain.SessionWaitCommand || coordinator.stops != 0 || len(*ran) != 0 {
		t.Fatalf("waiting job = %+v, stops=%d ran=%v", waiting, coordinator.stops, *ran)
	}
	// New sessions may start while the update waits.
	if release, ok := s.TryBeginHarnessUse(domain.AgentHarness(TargetAuggie)); !ok {
		t.Fatal("a launch was refused while the update only waited")
	} else {
		release()
	}
	s.CancelAgentOperation(context.Background(), TargetAuggie) //nolint:errcheck // exercised below
	s.workers.Wait()
}

func TestWindowsIdleUpdateStopsUpdatesAndResumes(t *testing.T) {
	s, coordinator, ran := windowsIdleService(t, false)
	if _, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	job := waitForJob(t, s, TargetAuggie, func(job Job) bool { return job.Status == StatusSucceeded || job.Status == StatusFailed })
	if job.Status != StatusSucceeded {
		t.Fatalf("job = %+v", job)
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.stops != 1 || len(coordinator.resumed) != 1 || coordinator.resumed[0][0] != "auggie-1" || len(*ran) == 0 {
		t.Fatalf("stops=%d resumed=%v ran=%v", coordinator.stops, coordinator.resumed, *ran)
	}
}

func TestWindowsIdleUpdateFailureResumesOnTheOldRelease(t *testing.T) {
	s, coordinator, _ := windowsIdleService(t, true)
	if _, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	job := waitForJob(t, s, TargetAuggie, func(job Job) bool { return job.Status == StatusSucceeded || job.Status == StatusFailed })
	if job.Status != StatusFailed {
		t.Fatalf("job = %+v, want failed", job)
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.resumed) != 1 {
		t.Fatalf("resumed = %v, want the stopped session resumed after a failed update", coordinator.resumed)
	}
}

func TestCancelledIdleUpdateLeavesSessionsUntouched(t *testing.T) {
	s, coordinator, ran := windowsIdleService(t, false)
	coordinator.busy = []domain.SessionWait{{SessionID: "auggie-1", Reason: domain.SessionWaitWorking}}
	if _, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	job, err := s.CancelAgentOperation(context.Background(), TargetAuggie)
	if err != nil || job.Status != StatusInterrupted {
		t.Fatalf("cancel = %+v, %v", job, err)
	}
	s.workers.Wait()
	if coordinator.stops != 0 || len(coordinator.resumed) != 0 || len(*ran) != 0 {
		t.Fatalf("cancel touched sessions: stops=%d resumed=%v ran=%v", coordinator.stops, coordinator.resumed, *ran)
	}
	if _, err := s.CancelAgentOperation(context.Background(), TargetAuggie); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("second cancel = %v, want ErrNotWaiting", err)
	}
}

func TestIdleUpdateNeverRunsNPMWhileASessionStillRuns(t *testing.T) {
	s, coordinator, ran := windowsIdleService(t, false)
	// Stopping fails, so a session keeps running.
	coordinator.stopErr = errors.New("chat has work in progress")
	if _, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	waitForJob(t, s, TargetAuggie, func(Job) bool {
		coordinator.mu.Lock()
		defer coordinator.mu.Unlock()
		return coordinator.stops > 0
	})
	if len(*ran) != 0 {
		t.Fatalf("npm ran while a session still ran: %v", *ran)
	}
	if _, err := s.CancelAgentOperation(context.Background(), TargetAuggie); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
}

func TestLaunchWaitsForAHarnessChangeInsteadOfFailing(t *testing.T) {
	s := newTestService("windows")
	gate := s.harnessGate(domain.AgentHarness(TargetAuggie))
	gate.Lock()
	s.setHarnessChanging(TargetAuggie, true)
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.setHarnessChanging(TargetAuggie, false)
		gate.Unlock()
	}()
	started := time.Now()
	release, ok := s.TryBeginHarnessUse(domain.AgentHarness(TargetAuggie))
	if !ok {
		t.Fatal("launch failed instead of waiting for the update batch")
	}
	release()
	if time.Since(started) < 250*time.Millisecond {
		t.Fatal("launch did not wait for the batch")
	}
}

// eventRecorder keeps the telemetry events a test emitted.
type eventRecorder struct {
	mu     sync.Mutex
	events []ports.TelemetryEvent
}

func (r *eventRecorder) Emit(_ context.Context, ev ports.TelemetryEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (*eventRecorder) Close(context.Context) error { return nil }

func TestIdleUpdateReportsSessionsItCouldNotResume(t *testing.T) {
	s, coordinator, _ := windowsIdleService(t, false)
	coordinator.resumeErr = errors.New("provider did not start")
	events := &eventRecorder{}
	s.SetTelemetry(events)
	if _, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	waitForJob(t, s, TargetAuggie, func(job Job) bool { return job.Status == StatusSucceeded || job.Status == StatusFailed })
	s.workers.Wait()
	events.mu.Lock()
	defer events.mu.Unlock()
	for _, ev := range events.events {
		if ev.Name == EventHarnessResumeFailed && ev.Payload["harness"] == string(TargetAuggie) && ev.Payload["error"] == "provider did not start" {
			return
		}
	}
	t.Fatalf("no resume failure was reported: %+v", events.events)
}
