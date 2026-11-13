package review

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// supervisedEngine is an engine over a real SQLite store with a movable clock,
// so every test exercises the store's guarded transitions.
type supervisedEngine struct {
	*Engine
	store  *sqlite.Store
	worker domain.SessionRecord
	now    *time.Time
}

func newSupervisedEngine(t *testing.T, launcher *fakeLauncher) supervisedEngine {
	t.Helper()
	st := newSQLiteReviewStore(t)
	worker := liveWorker()
	seedReviewWorker(t, st, worker)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	ids := 0
	eng := New(Deps{
		Store: st, Sessions: st, PRs: prAt("sha1"), Launcher: launcher,
		Clock: func() time.Time { return now },
		NewID: func() string { ids++; return fmt.Sprintf("id-%d", ids) },
	})
	return supervisedEngine{Engine: eng, store: st, worker: worker, now: &now}
}

func (s supervisedEngine) advance(d time.Duration) { *s.now = s.now.Add(d) }

func (s supervisedEngine) trigger(t *testing.T) domain.ReviewRun {
	t.Helper()
	res, err := s.TriggerWithOptions(context.Background(), s.worker.ID, TriggerOptions{})
	if err != nil {
		t.Fatalf("TriggerWithOptions: %v", err)
	}
	if !res.Created || len(res.CreatedRuns) != 1 {
		t.Fatalf("trigger result = %+v", res)
	}
	return res.CreatedRuns[0]
}

func (s supervisedEngine) run(t *testing.T, id string) domain.ReviewRun {
	t.Helper()
	run, ok, err := s.store.GetReviewRun(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("get run %s: ok=%v err=%v", id, ok, err)
	}
	return run
}

func (s supervisedEngine) review(t *testing.T) domain.Review {
	t.Helper()
	review, ok, err := s.store.GetReviewBySessionAndHarness(context.Background(), s.worker.ID, domain.ReviewerClaudeCode)
	if err != nil || !ok {
		t.Fatalf("get review: ok=%v err=%v", ok, err)
	}
	return review
}

func (s supervisedEngine) evaluate(t *testing.T) {
	t.Helper()
	if err := s.EvaluateSession(context.Background(), s.worker.ID); err != nil {
		t.Fatalf("EvaluateSession: %v", err)
	}
}

// observe feeds a reviewer hook signal through the same path the daemon's
// activity endpoint uses.
func (s supervisedEngine) observe(t *testing.T, launchID string, state domain.ActivityState) {
	t.Helper()
	review := s.review(t)
	if _, err := s.store.UpdateReviewActivity(context.Background(), review.ID, state, "", launchID); err != nil {
		t.Fatalf("UpdateReviewActivity: %v", err)
	}
	review.ReviewerActivityState = state
	if err := s.ObserveReviewActivity(context.Background(), review, launchID, state); err != nil {
		t.Fatalf("ObserveReviewActivity: %v", err)
	}
}

func wantRun(t *testing.T, run domain.ReviewRun, status domain.ReviewRunStatus, reason domain.ReviewFailureReason) {
	t.Helper()
	if run.Status != status || run.FailureReason != reason {
		t.Fatalf("run = %s (%q), want %s (%q): %+v", run.Status, run.FailureReason, status, reason, run)
	}
	if status.IsFinal() && run.EndedAt == nil {
		t.Fatalf("final run has no ended_at: %+v", run)
	}
}

// insertLaunchingRun records a run left launching at the current clock, as a
// trigger call that died mid-launch leaves it.
func (s supervisedEngine) insertLaunchingRun(t *testing.T, review domain.Review) domain.ReviewRun {
	t.Helper()
	ctx := context.Background()
	run := domain.ReviewRun{ID: "run-launching", ReviewID: review.ID, SessionID: s.worker.ID, Harness: review.Harness, PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunQueued, CreatedAt: *s.now}
	if err := s.store.InsertReviewRun(ctx, run); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	at := *s.now
	if ok, err := s.store.TransitionReviewRun(ctx, run.ID, []domain.ReviewRunStatus{domain.ReviewRunQueued}, domain.ReviewRunLaunching, domain.ReviewRunPatch{LaunchID: review.ReviewerLaunchID, LaunchingAt: &at}); err != nil || !ok {
		t.Fatalf("mark launching: ok=%v err=%v", ok, err)
	}
	return s.run(t, run.ID)
}

func TestTriggerMovesARunThroughQueuedLaunchingRunning(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newSupervisedEngine(t, launcher)
	var during domain.ReviewRun
	launcher.onSpawn = func(spec LaunchSpec) {
		during = s.run(t, spec.RunID)
	}
	created := s.trigger(t)

	if during.Status != domain.ReviewRunLaunching || during.LaunchingAt == nil || during.LaunchID == "" {
		t.Fatalf("run while the reviewer starts = %+v, want launching with a launch id", during)
	}
	got := s.run(t, created.ID)
	if got.Status != domain.ReviewRunRunning || got.StartedAt == nil || got.LaunchID != s.review(t).ReviewerLaunchID {
		t.Fatalf("run after the prompt was delivered = %+v, want running on the reviewer's launch", got)
	}
	if created.Status != domain.ReviewRunRunning {
		t.Fatalf("trigger reported %s, want running", created.Status)
	}
}

func TestTriggerRecordsPreflightAndLaunchFailureCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		launcher *fakeLauncher
		want     domain.ReviewFailureReason
	}{
		{"preflight", &fakeLauncher{handle: "review-mer-1", preflightErr: errors.New("claude not on PATH")}, domain.ReviewFailurePreflightFailed},
		{"spawn", &fakeLauncher{handle: "review-mer-1", spawnErr: errors.New("pty exhausted")}, domain.ReviewFailureLaunchFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSupervisedEngine(t, tc.launcher)
			if _, err := s.TriggerWithOptions(context.Background(), s.worker.ID, TriggerOptions{}); err == nil {
				t.Fatal("trigger succeeded, want the launch error")
			}
			runs, err := s.store.ListReviewRunsBySession(context.Background(), s.worker.ID)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs = %+v err=%v", runs, err)
			}
			wantRun(t, runs[0], domain.ReviewRunFailed, tc.want)
		})
	}
}

// §6.1 row 8 (#7718): a launch that never finishes fails after the launch
// timeout, even while the trigger call that owns it still holds the worker.
func TestSupervisorFailsALaunchHungPastTheLaunchTimeout(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1", alive: true}
	s := newSupervisedEngine(t, launcher)
	review, err := s.upsertReview(context.Background(), s.worker, domain.ReviewerClaudeCode, "", "", "launch-1", domain.ActivityActive, *s.now)
	if err != nil {
		t.Fatal(err)
	}
	run := s.insertLaunchingRun(t, review)

	unlock := s.lockWorker(s.worker.ID) // the hung trigger call
	defer unlock()
	s.advance(DefaultSupervisorConfig().LaunchTimeout - time.Second)
	if err := s.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRun(t, s.run(t, run.ID), domain.ReviewRunLaunching, "")
	s.advance(time.Second)
	if err := s.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureNeverStarted)
}

// §6.1 row 9 (#6642): the reviewer process is gone while launching.
func TestSupervisorFailsALaunchWhoseReviewerExited(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newSupervisedEngine(t, launcher)
	review, err := s.upsertReview(context.Background(), s.worker, domain.ReviewerClaudeCode, "review-mer-1", "", "launch-1", domain.ActivityActive, *s.now)
	if err != nil {
		t.Fatal(err)
	}
	run := s.insertLaunchingRun(t, review)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureExitedDuringLaunch)
	if got := s.review(t).ReviewerActivityState; got != domain.ActivityExited {
		t.Fatalf("reviewer activity = %q, want exited", got)
	}
}

// §6.1 row 12 (#7718): no activity from this launch within the window.
func TestSupervisorFailsARunWithNoFirstActivityFromItsLaunch(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
	s := newSupervisedEngine(t, launcher)
	run := s.trigger(t)

	// Activity from an older launch never counts as this run's first activity.
	s.observe(t, "launch-from-an-older-reviewer", domain.ActivityActive)
	s.advance(DefaultSupervisorConfig().FirstActivityWindow)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureNeverStarted)
	if !launcher.cancelled {
		t.Fatal("a reviewer that never started was not interrupted")
	}
}

// §6.1 row 13 (#7739): the reviewer went idle and did not submit within the
// idle grace; waiting for input is not idle.
func TestSupervisorFailsARunWhoseTurnEndedWithoutSubmit(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
	s := newSupervisedEngine(t, launcher)
	run := s.trigger(t)
	launchID := s.run(t, run.ID).LaunchID

	s.observe(t, launchID, domain.ActivityActive)
	if got := s.run(t, run.ID); got.FirstActivityAt == nil {
		t.Fatalf("first activity not recorded: %+v", got)
	}
	s.observe(t, launchID, domain.ActivityWaitingInput)
	s.advance(10 * time.Minute)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")

	s.observe(t, launchID, domain.ActivityIdle)
	s.advance(DefaultSupervisorConfig().IdleGrace - time.Second)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
	s.advance(time.Second)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureTurnEndedWithoutSubmit)
}

// §6.1 row 14 (#6642, #5948): the process is definitively gone; no List poll
// is needed for the run to end.
func TestSupervisorFailsARunWhoseReviewerProcessExited(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newSupervisedEngine(t, launcher)
	run := s.trigger(t)
	launcher.spawned = false
	if err := s.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureProcessExited)
}

// A failed or unavailable probe is unknown, not dead: only max_duration
// bounds such a run.
func TestSupervisorNeverFailsARunOnAProbeError(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newSupervisedEngine(t, launcher)
	run := s.trigger(t)
	launcher.spawned = false
	launcher.aliveErr = errors.New("runtime probe unavailable")
	s.advance(DefaultSupervisorConfig().MaxDuration - time.Second)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
	s.advance(time.Second)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureTimedOut)
}

// §6.1 row 15: max_duration counts time spent waiting for input too.
func TestSupervisorTimesOutARunPastItsMaxDuration(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
	s := newSupervisedEngine(t, launcher)
	run := s.trigger(t)
	launchID := s.run(t, run.ID).LaunchID
	s.observe(t, launchID, domain.ActivityActive)
	s.observe(t, launchID, domain.ActivityWaitingInput)
	s.advance(DefaultSupervisorConfig().MaxDuration)
	s.evaluate(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureTimedOut)
	if !launcher.cancelled {
		t.Fatal("a timed-out reviewer was not interrupted")
	}
}

// The first final state wins: a submit after a timeout changes nothing, and a
// late supervisor pass after a submit does nothing.
func TestFirstFinalStateWinsBetweenSubmitAndTimeout(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newSupervisedEngine(t, launcher)
	ctx := context.Background()
	timedOut := s.trigger(t)
	s.advance(DefaultSupervisorConfig().MaxDuration)
	s.evaluate(t)
	if ok, err := s.store.TransitionReviewRun(ctx, timedOut.ID, []domain.ReviewRunStatus{domain.ReviewRunLaunching, domain.ReviewRunRunning}, domain.ReviewRunCompleted, domain.ReviewRunPatch{Verdict: domain.VerdictApproved}); err != nil || ok {
		t.Fatalf("late submit accepted: ok=%v err=%v", ok, err)
	}
	wantRun(t, s.run(t, timedOut.ID), domain.ReviewRunFailed, domain.ReviewFailureTimedOut)

	s2 := newSupervisedEngine(t, &fakeLauncher{handle: "review-mer-1"})
	submitted := s2.trigger(t)
	if ok, err := s2.store.TransitionReviewRun(ctx, submitted.ID, []domain.ReviewRunStatus{domain.ReviewRunLaunching, domain.ReviewRunRunning}, domain.ReviewRunCompleted, domain.ReviewRunPatch{Verdict: domain.VerdictApproved}); err != nil || !ok {
		t.Fatalf("submit: ok=%v err=%v", ok, err)
	}
	s2.advance(DefaultSupervisorConfig().MaxDuration)
	s2.evaluate(t)
	if got := s2.run(t, submitted.ID); got.Status != domain.ReviewRunCompleted || got.Verdict != domain.VerdictApproved {
		t.Fatalf("late timeout overwrote a submit: %+v", got)
	}
}

// §6.1 rows 17-19 (#4850): cancel waits for the stop to be confirmed and
// force-kills a reviewer that ignores the interrupt.
func TestCancelIsConfirmedOrForceKilled(t *testing.T) {
	t.Run("turn stops after the interrupt", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
		s := newSupervisedEngine(t, launcher)
		run := s.trigger(t)
		launchID := s.run(t, run.ID).LaunchID
		s.observe(t, launchID, domain.ActivityActive)
		if _, err := s.Cancel(context.Background(), s.worker.ID); err != nil {
			t.Fatal(err)
		}
		wantRun(t, s.run(t, run.ID), domain.ReviewRunCancelling, "")
		s.observe(t, launchID, domain.ActivityIdle)
		s.evaluate(t)
		if got := s.run(t, run.ID); got.Status != domain.ReviewRunCancelled || got.CancelledBy != domain.ReviewCancelledByUser {
			t.Fatalf("run = %+v, want cancelled by user", got)
		}
		if launcher.destroyed {
			t.Fatal("a reviewer that stopped was killed")
		}
	})
	t.Run("interrupt ignored, kill confirmed", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1", killOnDestroy: true}
		s := newSupervisedEngine(t, launcher)
		run := s.trigger(t)
		if _, err := s.Cancel(context.Background(), s.worker.ID); err != nil {
			t.Fatal(err)
		}
		s.advance(DefaultSupervisorConfig().CancelTimeout - time.Second)
		s.evaluate(t)
		wantRun(t, s.run(t, run.ID), domain.ReviewRunCancelling, "")
		s.advance(time.Second)
		s.evaluate(t)
		wantRun(t, s.run(t, run.ID), domain.ReviewRunCancelled, "")
		if !launcher.destroyed || launcher.destroyedHandle != "review-mer-1" {
			t.Fatalf("reviewer was not force-killed: %+v", launcher)
		}
		if s.review(t).ReviewerHandleID != "" {
			t.Fatal("killed reviewer's handle was kept")
		}
	})
	t.Run("interrupt ignored, kill unconfirmed", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1"}
		s := newSupervisedEngine(t, launcher)
		run := s.trigger(t)
		if _, err := s.Cancel(context.Background(), s.worker.ID); err != nil {
			t.Fatal(err)
		}
		s.advance(DefaultSupervisorConfig().CancelTimeout)
		s.evaluate(t)
		wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureCancelUnconfirmed)
		if domain.ReviewFailureCancelUnconfirmed.AutoRetryable() {
			t.Fatal("cancel_unconfirmed must not be retried automatically")
		}
	})
}

// §8.3 (#5948): after a restart, a reviewer whose exact launch is still alive
// keeps its run; every other active run fails and its orphaned reviewer is
// destroyed. A run that never reached a reviewer always fails.
func TestStartupSweepKeepsReadoptableReviewersAndReapsTheRest(t *testing.T) {
	t.Run("live reviewer kept", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1"}
		s := newSupervisedEngine(t, launcher)
		run := s.trigger(t)
		restarted := New(Deps{Store: s.store, Sessions: s.store, PRs: prAt("sha1"), Launcher: launcher, Clock: func() time.Time { return *s.now }})
		if err := restarted.SweepActiveRunsAfterRestart(context.Background()); err != nil {
			t.Fatal(err)
		}
		wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
		if launcher.destroyed {
			t.Fatal("a re-adoptable reviewer was destroyed")
		}
	})
	t.Run("orphan reaped", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1"}
		s := newSupervisedEngine(t, launcher)
		run := s.trigger(t)
		// The new daemon cannot re-adopt the old launch.
		after := &fakeLauncher{handle: "review-mer-1"}
		restarted := New(Deps{Store: s.store, Sessions: s.store, PRs: prAt("sha1"), Launcher: after, Clock: func() time.Time { return *s.now }})
		if err := restarted.SweepActiveRunsAfterRestart(context.Background()); err != nil {
			t.Fatal(err)
		}
		wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureDaemonRestarted)
		if !after.destroyed || after.destroyedHandle != "review-mer-1" {
			t.Fatalf("orphaned reviewer not destroyed: %+v", after)
		}
		if s.review(t).ReviewerHandleID != "" {
			t.Fatal("orphaned reviewer handle kept")
		}
	})
	t.Run("launch interrupted by the restart", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1", alive: true}
		s := newSupervisedEngine(t, launcher)
		review, err := s.upsertReview(context.Background(), s.worker, domain.ReviewerClaudeCode, "review-mer-1", "", "launch-1", domain.ActivityActive, *s.now)
		if err != nil {
			t.Fatal(err)
		}
		run := s.insertLaunchingRun(t, review)
		if err := s.SweepActiveRunsAfterRestart(context.Background()); err != nil {
			t.Fatal(err)
		}
		wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureDaemonRestarted)
	})
}

// The supervisor loop owns runs on its own: nobody calls List or Trigger.
func TestRunSupervisorSettlesRunsOnItsTick(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newSupervisedEngine(t, launcher)
	run := s.trigger(t)
	launcher.spawned = false
	s.supervisor.config.TickInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunSupervisor(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := s.store.GetReviewRun(context.Background(), run.ID); got.Status.IsFinal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureProcessExited)
}

// Repro tests for the row-1 issues: each drives the scenario that used to
// leave a run "running" forever (or "cancelled" while the reviewer kept
// working) and checks the run now reaches a final state on its own.
func TestRowOneReviewRunsAlwaysReachAFinalState(t *testing.T) {
	cfg := DefaultSupervisorConfig()
	for _, tc := range []struct {
		issue, name, want string
		run               func(t *testing.T) string
	}{
		{"#7718", "reviewer never starts working", "failed (never_started)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
			s := newSupervisedEngine(t, launcher)
			run := s.trigger(t)
			s.advance(cfg.FirstActivityWindow)
			s.evaluate(t)
			return describeRun(s.run(t, run.ID))
		}},
		{"#6642", "reviewer exits right after launch (no List poll)", "failed (process_exited)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1"}
			s := newSupervisedEngine(t, launcher)
			run := s.trigger(t)
			launcher.spawned = false
			if err := s.EvaluateAll(context.Background()); err != nil {
				t.Fatal(err)
			}
			return describeRun(s.run(t, run.ID))
		}},
		{"#7739", "reviewer turn ends idle without submit (List polled)", "failed (turn_ended_without_submit)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
			s := newSupervisedEngine(t, launcher)
			run := s.trigger(t)
			launchID := s.run(t, run.ID).LaunchID
			s.observe(t, launchID, domain.ActivityActive)
			s.observe(t, launchID, domain.ActivityIdle)
			s.advance(cfg.IdleGrace)
			if _, err := s.List(context.Background(), s.worker.ID); err != nil {
				t.Fatal(err)
			}
			return describeRun(s.run(t, run.ID))
		}},
		{"#5948", "daemon restarts while an orphaned reviewer is alive", "failed (daemon_restarted) (orphan destroyed=true)", func(t *testing.T) string {
			s := newSupervisedEngine(t, &fakeLauncher{handle: "review-mer-1"})
			run := s.trigger(t)
			after := &fakeLauncher{handle: "review-mer-1"}
			restarted := New(Deps{Store: s.store, Sessions: s.store, PRs: prAt("sha1"), Launcher: after, Clock: func() time.Time { return *s.now }})
			if err := restarted.SweepActiveRunsAfterRestart(context.Background()); err != nil {
				t.Fatal(err)
			}
			return fmt.Sprintf("%s (orphan destroyed=%v)", describeRun(s.run(t, run.ID)), after.destroyed)
		}},
		{"#4850", "cancel while the reviewer ignores the interrupt", "cancelling -> cancelled (reviewer process still alive=false, force killed=true)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1", killOnDestroy: true}
			s := newSupervisedEngine(t, launcher)
			run := s.trigger(t)
			if _, err := s.Cancel(context.Background(), s.worker.ID); err != nil {
				t.Fatal(err)
			}
			afterCancel := s.run(t, run.ID).Status
			s.advance(cfg.CancelTimeout)
			s.evaluate(t)
			alive, _ := launcher.Alive(context.Background(), "review-mer-1", "")
			return fmt.Sprintf("%s -> %s (reviewer process still alive=%v, force killed=%v)", afterCancel, describeRun(s.run(t, run.ID)), alive, launcher.destroyed)
		}},
	} {
		t.Run(tc.issue, func(t *testing.T) {
			got := tc.run(t)
			t.Logf("AFTER %s %-55s -> %s", tc.issue, tc.name, got)
			if got != tc.want {
				t.Fatalf("%s: got %q, want %q", tc.issue, got, tc.want)
			}
		})
	}
}

func describeRun(run domain.ReviewRun) string {
	if !run.Status.IsFinal() {
		return string(run.Status) + " (NOT FINAL)"
	}
	if run.FailureReason != "" {
		return fmt.Sprintf("%s (%s)", run.Status, run.FailureReason)
	}
	return string(run.Status)
}
