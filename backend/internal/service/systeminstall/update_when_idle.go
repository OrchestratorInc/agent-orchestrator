package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var errUpdateWhenIdle = errors.New("systeminstall: update waits for idle sessions")

var ErrNotWaiting = errors.New("systeminstall: no update is waiting for this harness")

type IdleUpdateCoordinator interface {
	BusyHarnessSessions(ctx context.Context, harness domain.AgentHarness) ([]domain.SessionWait, error)
	StopHarnessSessions(ctx context.Context, harness domain.AgentHarness) ([]domain.SessionID, error)
	ResumeHarnessSessions(ctx context.Context, ids []domain.SessionID) error
}

const (
	idleUpdatePoll    = 10 * time.Second
	idleUpdateMaxWait = 12 * time.Hour
	harnessUseWait    = 45 * time.Second
)

const EventHarnessResumeFailed = "ao.harness.update_resume_failed"

type idleUpdateRun struct {
	cancelled chan struct{}
	cancel    sync.Once
	running   bool
}

func (s *Service) SetIdleUpdateCoordinator(coordinator IdleUpdateCoordinator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleCoordinator = coordinator
}

func (s *Service) idleCoordinatorReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idleCoordinator != nil
}

func (s *Service) startIdleUpdate(ctx context.Context, plan Plan, baseline *installedBaseline) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	target := plan.Target
	now := time.Now().UTC()
	s.mu.Lock()
	if current, ok := s.jobs[target]; ok && activeStatus(current.Status) {
		s.mu.Unlock()
		return Job{}, ErrInstallActive
	}
	job := &Job{
		Target: target, Status: StatusWaitingForIdle, Method: plan.Method,
		Command: displayCommand(plan), ExpectedDestination: plan.ExpectedDestination,
		Notice: plan.Notice, StartedAt: &now, UpdatedAt: &now,
	}
	run := &idleUpdateRun{cancelled: make(chan struct{})}
	s.jobs[target] = job
	if s.idleUpdates == nil {
		s.idleUpdates = make(map[Target]*idleUpdateRun)
	}
	s.idleUpdates[target] = run
	s.mu.Unlock()
	initial := *job
	if !s.beginWorker() {
		s.finishIdleUpdate(job, run, StatusInterrupted, "daemon shutdown interrupted the update")
		return initial, nil
	}
	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		s.runIdleUpdate(s.backgroundContext, plan, baseline, job, run)
	}()
	return initial, nil
}

func (s *Service) runIdleUpdate(ctx context.Context, plan Plan, baseline *installedBaseline, job *Job, run *idleUpdateRun) {
	target := plan.Target
	harness := domain.AgentHarness(target)
	s.mu.Lock()
	coordinator := s.idleCoordinator
	s.mu.Unlock()
	deadline := time.Now().Add(idleUpdateMaxWait)
	ticker := time.NewTicker(idleUpdatePoll)
	defer ticker.Stop()
	for {
		if ready := s.idleUpdateReady(ctx, coordinator, harness, job); ready {
			if stopped, ok := s.runIdleBatch(ctx, coordinator, plan, baseline, job, run); ok {
				s.resumeAfterIdleBatch(target, coordinator, stopped)
				return
			}
		}
		if time.Now().After(deadline) {
			s.finishIdleUpdate(job, run, StatusFailed, fmt.Sprintf("sessions using %s were not idle within %s; the update did not run", target, idleUpdateMaxWait))
			return
		}
		select {
		case <-ctx.Done():
			s.finishIdleUpdate(job, run, StatusInterrupted, "daemon shutdown interrupted the update")
			return
		case <-run.cancelled:
			s.finishIdleUpdate(job, run, StatusInterrupted, "Update cancelled; sessions were left as they were.")
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) idleUpdateReady(ctx context.Context, coordinator IdleUpdateCoordinator, harness domain.AgentHarness, job *Job) bool {
	busy, err := coordinator.BusyHarnessSessions(ctx, harness)
	if err != nil {
		return false
	}
	if _, reviewers, err := s.harnessInUse(ctx, Target(harness)); err == nil {
		for _, id := range reviewers {
			busy = append(busy, domain.SessionWait{SessionID: id, Reason: domain.SessionWaitReviewer})
		}
	} else {
		return false
	}
	now := time.Now().UTC()
	s.mu.Lock()
	job.WaitingOn = slices.Clone(busy)
	job.UpdatedAt = &now
	s.mu.Unlock()
	return len(busy) == 0
}

func (s *Service) runIdleBatch(ctx context.Context, coordinator IdleUpdateCoordinator, plan Plan, baseline *installedBaseline, job *Job, run *idleUpdateRun) ([]domain.SessionID, bool) {
	target := plan.Target
	gate := s.harnessGate(domain.AgentHarness(target))
	if !tryLockFor(gate, 2*time.Second) {
		return nil, false
	}
	s.mu.Lock()
	select {
	case <-run.cancelled:
		s.mu.Unlock()
		gate.Unlock()
		return nil, false
	default:
	}
	run.running = true
	s.mu.Unlock()
	s.setHarnessChanging(target, true)
	release := func() {
		s.mu.Lock()
		run.running = false
		s.mu.Unlock()
		s.setHarnessChanging(target, false)
		gate.Unlock()
	}
	stopped, err := coordinator.StopHarnessSessions(ctx, domain.AgentHarness(target))
	if err == nil {
		if sessions, reviewers, inUseErr := s.harnessInUse(ctx, target); inUseErr != nil || len(sessions)+len(reviewers) != 0 {
			err = errors.Join(inUseErr, errors.New("sessions are still running"))
		}
	}
	if err != nil {
		release()
		s.resumeAfterIdleBatch(target, coordinator, stopped)
		return nil, false
	}
	if transitionErr := s.transitionAgentJob(job, StatusInstalling, "", "", ""); transitionErr != nil {
		release()
		s.resumeAfterIdleBatch(target, coordinator, stopped)
		s.finishIdleUpdate(job, run, StatusFailed, fmt.Sprintf("persist installing state: %v", transitionErr))
		return nil, true
	}
	s.mu.Lock()
	job.WaitingOn = nil
	if s.updateLevels == nil {
		s.updateLevels = make(map[Target]UpdateWhileRunning)
	}
	s.updateLevels[target] = UpdateWhileRunningIdle
	waited := time.Since(*job.StartedAt)
	s.mu.Unlock()
	s.emit(EventHarnessIdleWait, map[string]any{"harness": string(target), "wait_ms": waited.Milliseconds(), "sessions": len(stopped)})
	s.runAgentOperation(ctx, plan, AgentOperationUpdate, job, baseline)
	s.mu.Lock()
	if s.idleUpdates[target] == run {
		delete(s.idleUpdates, target)
	}
	s.mu.Unlock()
	release()
	return stopped, true
}

func (s *Service) resumeAfterIdleBatch(target Target, coordinator IdleUpdateCoordinator, stopped []domain.SessionID) {
	if len(stopped) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.backgroundContext), 5*time.Minute)
	defer cancel()
	if err := coordinator.ResumeHarnessSessions(ctx, stopped); err != nil {
		s.emit(EventHarnessResumeFailed, map[string]any{"harness": string(target), "sessions": len(stopped), "error": err.Error()})
	}
}

func (s *Service) finishIdleUpdate(job *Job, run *idleUpdateRun, status Status, message string) {
	now := time.Now().UTC()
	s.mu.Lock()
	job.Status = status
	job.Error = message
	job.WaitingOn = nil
	job.FinishedAt = &now
	job.UpdatedAt = &now
	if s.idleUpdates[job.Target] == run {
		delete(s.idleUpdates, job.Target)
	}
	s.mu.Unlock()
}

func (s *Service) CancelAgentOperation(ctx context.Context, target Target) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	run := s.idleUpdates[target]
	job := s.jobs[target]
	if run == nil || job == nil || job.Status != StatusWaitingForIdle || run.running {
		s.mu.Unlock()
		return Job{}, ErrNotWaiting
	}
	run.cancel.Do(func() { close(run.cancelled) })
	s.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		snapshot := *job
		s.mu.Unlock()
		if snapshot.Status != StatusWaitingForIdle {
			return snapshot, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return *job, nil
}

func (s *Service) setHarnessChanging(target Target, changing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changing == nil {
		s.changing = make(map[Target]bool)
	}
	if changing {
		s.changing[target] = true
	} else {
		delete(s.changing, target)
	}
}

func (s *Service) waitForHarnessChange(target Target, gate *sync.RWMutex) bool {
	s.mu.Lock()
	waiting := s.changing[target]
	s.mu.Unlock()
	if !waiting {
		return false
	}
	deadline := time.Now().Add(harnessUseWait)
	for time.Now().Before(deadline) {
		if gate.TryRLock() {
			return true
		}
		select {
		case <-s.backgroundContext.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
	return false
}

func (s *Service) holdOtherHarnesses(targets []Target) (release func(), err error) {
	var releases []func()
	release = func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	for _, target := range targets {
		gate := s.harnessGate(domain.AgentHarness(target))
		if !tryLockFor(gate, otherHarnessHoldWait) {
			release()
			return func() {}, &HarnessActiveError{Target: target, Starting: true, Level: UpdateWhileRunningBlocked}
		}
		s.mu.Lock()
		job, ok := s.jobs[target]
		busy := ok && activeStatus(job.Status)
		if !busy {
			if s.changing == nil {
				s.changing = make(map[Target]bool)
			}
			s.changing[target] = true
		}
		s.mu.Unlock()
		if busy {
			gate.Unlock()
			release()
			return func() {}, fmt.Errorf("%w: %s is being installed or updated; retry once it finishes", ErrInstallActive, target)
		}
		releases = append(releases, func() {
			s.setHarnessChanging(target, false)
			gate.Unlock()
		})
	}
	return release, nil
}

func (s *Service) harnessChanging(target Target) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changing[target]
}

func (s *Service) cleanupStepTimeout() time.Duration {
	if s.cleanupStepLimit > 0 {
		return s.cleanupStepLimit
	}
	return cleanupTimeout
}

func tryLockFor(gate *sync.RWMutex, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if gate.TryLock() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}
