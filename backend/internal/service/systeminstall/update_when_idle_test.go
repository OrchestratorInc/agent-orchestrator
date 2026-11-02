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
// it to sleep, so the installer no longer sees it as live. With terminals set
// it stops them the way Session Manager stops a terminal: the agent exits,
// unhibernated.
type idleCoordinatorStub struct {
	mu        sync.Mutex
	sessions  *[]domain.SessionRecord
	busy      []domain.SessionWait
	stops     int
	resumed   [][]domain.SessionID
	stopErr   error
	resumeErr error
	terminals bool
	// unstopped is left running and not reported as stopped.
	unstopped domain.SessionID
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
		if (*c.sessions)[i].ID == c.unstopped {
			continue
		}
		if c.terminals {
			(*c.sessions)[i].Activity.State = domain.ActivityExited
		} else {
			(*c.sessions)[i].HibernatedAt = &now
		}
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
	s.updateWhenIdle = true
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

// A stopped terminal is never hibernated: its agent exits. The update must
// still run instead of resuming the old release and trying again every poll.
func TestIdleUpdateRunsAfterStoppingTerminalSessions(t *testing.T) {
	s, coordinator, ran := windowsIdleService(t, false)
	coordinator.terminals = true
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

// A live session the coordinator did not stop still keeps the installer out.
func TestIdleUpdateNeverRunsBesideASessionThatWasNotStopped(t *testing.T) {
	s, coordinator, ran := windowsIdleService(t, false)
	coordinator.terminals = true
	coordinator.mu.Lock()
	*coordinator.sessions = append(*coordinator.sessions, domain.SessionRecord{ID: "auggie-2", Harness: domain.AgentHarness(TargetAuggie), Activity: domain.Activity{State: domain.ActivityIdle}})
	coordinator.unstopped = "auggie-2"
	coordinator.mu.Unlock()
	if _, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	waitForJob(t, s, TargetAuggie, func(Job) bool {
		coordinator.mu.Lock()
		defer coordinator.mu.Unlock()
		return len(coordinator.resumed) > 0
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

func TestUpdatesThatCannotRunBesideSessionsWaitForIdleOnEveryPlatform(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		s := newTestService(goos, "npm")
		s.updateWhenIdle = true
		s.SetIdleUpdateCoordinator(&idleCoordinatorStub{})
		for _, level := range []UpdateWhileRunning{UpdateWhileRunningWarn, UpdateWhileRunningBlocked} {
			if got := s.effectiveUpdateLevel(level); got != UpdateWhileRunningIdle {
				t.Errorf("%s %s = %s, want idle", goos, level, got)
			}
		}
		if got := s.effectiveUpdateLevel(UpdateWhileRunningSafe); got != UpdateWhileRunningSafe {
			t.Errorf("%s safe = %s", goos, got)
		}
		s.updateWhenIdle = false
		if got := s.effectiveUpdateLevel(UpdateWhileRunningWarn); got != UpdateWhileRunningBlocked {
			t.Errorf("%s warn without update-when-idle = %s, want blocked", goos, got)
		}
	}
}

// Regression: StartAgentOperation recorded the level under s.mu, and
// effectiveUpdateLevel locks s.mu to check the coordinator, so an update with
// update-when-idle on and no live session hung the daemon.
func TestUpdateWhenIdleWithoutSessionsDoesNotDeadlock(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		s, coordinator, ran := windowsIdleService(t, false)
		s.goos = goos
		*coordinator.sessions = nil
		started := make(chan error, 1)
		go func() {
			_, err := s.StartAgentOperation(context.Background(), TargetAuggie, "npm", AgentOperationUpdate, "")
			started <- err
		}()
		select {
		case err := <-started:
			if err != nil {
				t.Fatalf("%s: update = %v", goos, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: StartAgentOperation deadlocked", goos)
		}
		waitForJob(t, s, TargetAuggie, func(job Job) bool { return job.Status == StatusSucceeded })
		if len(*ran) != 1 {
			t.Fatalf("%s: installs = %v", goos, *ran)
		}
	}
}
