package review

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// lifecycleEngine is an engine over a real SQLite store with a movable clock,
// driven by a real runtime reaper, so every test exercises the store's
// guarded transitions and the reaper's observe -> update path.
type lifecycleEngine struct {
	*Engine
	store  *sqlite.Store
	worker domain.SessionRecord
	now    *time.Time
	reaper *reaper.Reaper
	// ids continues across restarts, as real ids never repeat.
	ids *int
}

func newLifecycleEngine(t *testing.T, launcher *fakeLauncher) lifecycleEngine {
	t.Helper()
	return newLifecycleEngineWith(t, launcher, RunLifecycleConfig{})
}

func TestRelaunchPersistsReviewerModel(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		fail        bool
	}{{"override", "gpt-5-mini", false}, {"default_model", "", false}, {"failed_replacement", "gpt-5-mini", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			launcher := &fakeLauncher{alive: true, handle: "reviewer"}
			s := newLifecycleEngine(t, launcher)
			res, err := s.TriggerWithOptions(ctx, s.worker.ID, TriggerOptions{Source: domain.ReviewTriggerAgent, RequesterSessionID: s.worker.ID, Config: domain.AgentConfig{Model: "gpt-5"}})
			if err != nil {
				t.Fatal(err)
			}
			launcher.launchID = ""
			if tc.fail {
				launcher.spawnErr = errors.New("replacement failed")
			}
			_, err = s.TriggerWithOptions(ctx, s.worker.ID, TriggerOptions{Config: domain.AgentConfig{Model: tc.model, Effort: "high"}})
			if (err != nil) != tc.fail {
				t.Fatalf("relaunch error = %v, want failure %v", err, tc.fail)
			}
			want := tc.model
			if tc.fail {
				want = "gpt-5"
			}
			run := s.run(t, res.Run.ID)
			if run.Status != domain.ReviewRunRunning || run.ReviewerModel != want {
				t.Fatalf("relaunch: status=%s model=%q, want running model=%q", run.Status, run.ReviewerModel, want)
			}
			if _, err := s.store.TransitionReviewRun(ctx, run.ID, []domain.ReviewRunStatus{domain.ReviewRunRunning}, domain.ReviewRunCompleted, domain.ReviewRunPatch{Verdict: domain.VerdictApproved}); err != nil {
				t.Fatal(err)
			}
			notices, err := s.store.ListPendingReviewRunNotices(ctx, s.worker.ID)
			if err != nil || len(notices) != 1 || notices[0].ReviewerModel != want {
				t.Fatalf("completion notices = %+v, %v; want model %q", notices, err, want)
			}
		})
	}
}

func TestCancelledRelaunchRestoresRunningReview(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	launcher := &fakeLauncher{alive: true, handle: "reviewer"}
	s := newLifecycleEngine(t, launcher)
	res, err := s.TriggerWithOptions(ctx, s.worker.ID, TriggerOptions{Config: domain.AgentConfig{Model: "gpt-5"}})
	if err != nil {
		t.Fatal(err)
	}
	original := s.run(t, res.Run.ID)
	launcher.onSpawn = func(LaunchSpec) {
		run := s.run(t, original.ID)
		if run.Status != domain.ReviewRunLaunching || run.ReviewerModel != "gpt-5-mini" {
			t.Fatalf("before cancellation: status=%s model=%q, want launching with replacement model", run.Status, run.ReviewerModel)
		}
		cancel()
	}
	launcher.spawnErr = context.Canceled
	_, err = s.TriggerWithOptions(ctx, s.worker.ID, TriggerOptions{Config: domain.AgentConfig{Model: "gpt-5-mini"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("relaunch error = %v, want context canceled", err)
	}
	run := s.run(t, original.ID)
	if run.Status != domain.ReviewRunRunning || run.ReviewerModel != original.ReviewerModel || run.LaunchID != original.LaunchID {
		t.Fatalf("after cancellation: status=%s model=%q launch=%q, want running model=%q launch=%q",
			run.Status, run.ReviewerModel, run.LaunchID, original.ReviewerModel, original.LaunchID)
	}
	if got := s.review(t).ReviewerLaunchID; got != original.LaunchID {
		t.Fatalf("reviewer launch = %q, want %q", got, original.LaunchID)
	}
}

func newLifecycleEngineWith(t *testing.T, launcher *fakeLauncher, cfg RunLifecycleConfig) lifecycleEngine {
	t.Helper()
	st := newSQLiteReviewStore(t)
	worker := liveWorker()
	seedReviewWorker(t, st, worker)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	s := lifecycleEngine{store: st, worker: worker, now: &now, ids: new(int)}
	s.Engine = s.restart(t, launcher, cfg)
	return s
}

// restart builds a new engine and reaper over the same store, as a daemon
// restart does, and runs the reaper's boot pass.
func (s *lifecycleEngine) restart(t *testing.T, launcher *fakeLauncher, cfg RunLifecycleConfig) *Engine {
	t.Helper()
	eng := s.restartBeforeBoot(t, launcher, cfg)
	s.pass(t)
	return eng
}

// restartBeforeBoot is restart without the reaper's boot pass, as the daemon
// is between Chat reviewer recovery and ReconcileRuntime. Like the daemon,
// the engine reserves a previous daemon's runs for that pass.
func (s *lifecycleEngine) restartBeforeBoot(t *testing.T, launcher *fakeLauncher, cfg RunLifecycleConfig) *Engine {
	t.Helper()
	ids := s.ids
	now := s.now
	eng := New(Deps{
		Store: s.store, Sessions: s.store, PRs: prAt("sha1"), Launcher: launcher, RunLifecycle: cfg,
		AwaitBootPass: true,
		Clock:         func() time.Time { return *now },
		NewID:         func() string { *ids++; return fmt.Sprintf("id-%d", *ids) },
	})
	s.Engine = eng
	s.reaper = reaper.New(noWorkers{}, noWorkers{}, noWorkers{}, reaper.Config{Clock: func() time.Time { return *now }})
	s.reaper.BindReviewers(eng)
	return eng
}

// noWorkers stands in for the worker side of the reaper: no sessions.
type noWorkers struct{}

func (noWorkers) ListAllSessions(context.Context) ([]domain.SessionRecord, error) { return nil, nil }
func (noWorkers) ApplyRuntimeObservation(context.Context, domain.SessionID, ports.RuntimeFacts) error {
	return nil
}
func (noWorkers) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) { return true, nil }

// pass runs one reaper cycle.
func (s lifecycleEngine) pass(t *testing.T) {
	t.Helper()
	if err := s.reaper.Tick(context.Background()); err != nil {
		t.Fatalf("reaper Tick: %v", err)
	}
}

// wait lets d pass the way the daemon does: a reaper cycle every 5 s.
func (s lifecycleEngine) wait(t *testing.T, d time.Duration) {
	t.Helper()
	for d > 0 {
		step := min(d, reaper.DefaultTickInterval)
		s.advance(step)
		d -= step
		s.pass(t)
	}
}

func (s lifecycleEngine) advance(d time.Duration) { *s.now = s.now.Add(d) }

func (s lifecycleEngine) trigger(t *testing.T) domain.ReviewRun {
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

func (s lifecycleEngine) run(t *testing.T, id string) domain.ReviewRun {
	t.Helper()
	run, ok, err := s.store.GetReviewRun(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("get run %s: ok=%v err=%v", id, ok, err)
	}
	return run
}

func (s lifecycleEngine) review(t *testing.T) domain.Review {
	t.Helper()
	review, ok, err := s.store.GetReviewBySessionAndHarness(context.Background(), s.worker.ID, domain.ReviewerClaudeCode)
	if err != nil || !ok {
		t.Fatalf("get review: ok=%v err=%v", ok, err)
	}
	return review
}

// observe feeds a reviewer hook signal through the same path the daemon's
// activity endpoint uses.
func (s lifecycleEngine) observe(t *testing.T, launchID string, state domain.ActivityState) {
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
func (s lifecycleEngine) insertLaunchingRun(t *testing.T, review domain.Review) domain.ReviewRun {
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

// A failed or unavailable probe is unknown, not dead: only max_duration
// bounds such a run.
func TestRunLifecycleNeverFailsARunOnAProbeError(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newLifecycleEngine(t, launcher)
	run := s.trigger(t)
	launcher.spawned = false
	launcher.aliveErr = errors.New("runtime probe unavailable")
	s.wait(t, DefaultRunLifecycleConfig().MaxDuration-time.Second)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
	s.wait(t, time.Second)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureTimedOut)
}

// §8.3 (#5948): the reaper's boot pass keeps a reviewer whose exact launch is
// still alive; every other active run fails and its orphaned reviewer is
// destroyed. A run that never reached a reviewer always fails.
func TestReaperBootPassKeepsReadoptableReviewersAndReapsTheRest(t *testing.T) {
	t.Run("live reviewer kept", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1"}
		s := newLifecycleEngine(t, launcher)
		run := s.trigger(t)
		s.restart(t, launcher, RunLifecycleConfig{})
		wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
		if launcher.destroyed {
			t.Fatal("a re-adoptable reviewer was destroyed")
		}
	})
	t.Run("launch interrupted by the restart", func(t *testing.T) {
		launcher := &fakeLauncher{handle: "review-mer-1", alive: true, killOnDestroy: true}
		s := newLifecycleEngine(t, launcher)
		review, err := s.upsertReview(context.Background(), s.worker, domain.ReviewerClaudeCode, "review-mer-1", "", "launch-1", domain.ActivityActive, *s.now)
		if err != nil {
			t.Fatal(err)
		}
		run := s.insertLaunchingRun(t, review)
		s.restart(t, launcher, RunLifecycleConfig{})
		wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureDaemonRestarted)
	})
}

// The reaper probes a reviewer only while it serves an in-progress run: once
// the run ends, a pass lists nothing and probes nothing.
func TestReaperObservesReviewersOnlyDuringAReview(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newLifecycleEngine(t, launcher)
	run := s.trigger(t)

	launcher.aliveChecked = false
	s.pass(t)
	if !launcher.aliveChecked {
		t.Fatal("reaper did not probe the reviewer of an in-progress run")
	}
	if ok, err := s.store.TransitionReviewRun(context.Background(), run.ID, []domain.ReviewRunStatus{domain.ReviewRunRunning}, domain.ReviewRunCompleted, domain.ReviewRunPatch{Verdict: domain.VerdictApproved}); err != nil || !ok {
		t.Fatalf("submit: ok=%v err=%v", ok, err)
	}
	targets, err := s.ListObservableReviewers(context.Background())
	if err != nil || len(targets) != 0 {
		t.Fatalf("observable reviewers after the review ended = %+v err=%v", targets, err)
	}
	launcher.aliveChecked = false
	s.pass(t)
	if launcher.aliveChecked {
		t.Fatal("reaper probed a reviewer with no review in progress")
	}
}

// Time the daemon did not observe (the machine slept) never counts against a
// run's limits; time it did observe does.
func TestSleepGapIsNotCountedAgainstARun(t *testing.T) {
	cfg := RunLifecycleConfig{MaxDuration: 2 * time.Minute, SuspendGap: 30 * time.Second}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newLifecycleEngineWith(t, launcher, cfg)
	run := s.trigger(t)

	s.wait(t, time.Minute)
	s.advance(3 * time.Hour) // the laptop sleeps
	s.pass(t)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
	s.wait(t, time.Minute-reaper.DefaultTickInterval)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
	s.wait(t, reaper.DefaultTickInterval)
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureTimedOut)
}

// Repro tests for the row-1 issues: each drives the scenario that used to
// leave a run "running" forever (or "cancelled" while the reviewer kept
// working) and checks the run now reaches a final state on its own.
func TestRowOneReviewRunsAlwaysReachAFinalState(t *testing.T) {
	cfg := DefaultRunLifecycleConfig()
	for _, tc := range []struct {
		issue, name, want string
		run               func(t *testing.T) string
	}{
		{"#7718", "reviewer never starts working", "failed (never_started)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
			s := newLifecycleEngine(t, launcher)
			run := s.trigger(t)
			s.wait(t, cfg.FirstActivityWindow)
			return describeRun(s.run(t, run.ID))
		}},
		{"#6642", "reviewer exits right after launch (no List poll)", "failed (process_exited)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1"}
			s := newLifecycleEngine(t, launcher)
			run := s.trigger(t)
			launcher.spawned = false
			s.pass(t)
			return describeRun(s.run(t, run.ID))
		}},
		{"#7739", "reviewer turn ends idle without submit (List polled)", "failed (turn_ended_without_submit)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1", reportsActivity: true}
			s := newLifecycleEngine(t, launcher)
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
			s := newLifecycleEngine(t, &fakeLauncher{handle: "review-mer-1"})
			run := s.trigger(t)
			after := &fakeLauncher{handle: "review-mer-1"}
			s.restart(t, after, RunLifecycleConfig{})
			return fmt.Sprintf("%s (orphan destroyed=%v)", describeRun(s.run(t, run.ID)), after.destroyed)
		}},
		{"#4850", "cancel while the reviewer ignores the interrupt", "cancelling -> cancelled (reviewer process still alive=false, force killed=true)", func(t *testing.T) string {
			launcher := &fakeLauncher{handle: "review-mer-1", killOnDestroy: true}
			s := newLifecycleEngine(t, launcher)
			run := s.trigger(t)
			if _, err := s.Cancel(context.Background(), s.worker.ID); err != nil {
				t.Fatal(err)
			}
			afterCancel := s.run(t, run.ID).Status
			s.wait(t, cfg.CancelTimeout)
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

// --- review feedback on PR #8353 ---

// The boot pass settles only what a previous daemon left. A run a trigger in
// this daemon created before the boot pass reached its worker is left to the
// normal rules, even when the boot rules would reject it: its reviewer is not
// reaped and the run is never recorded as daemon_restarted.
func TestBootPassLeavesARunATriggerRacedInAlone(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	s := newLifecycleEngine(t, launcher)
	s.restartBeforeBoot(t, launcher, RunLifecycleConfig{})
	s.advance(time.Second)
	run := s.trigger(t)      // the trigger wins the worker lock before the boot pass
	launcher.spawned = false // its reviewer looks gone when the boot pass probes
	launcher.destroyed = false
	s.pass(t) // boot pass
	wantRun(t, s.run(t, run.ID), domain.ReviewRunRunning, "")
	if launcher.destroyed {
		t.Fatalf("the boot pass reaped a reviewer this daemon launched: %+v", launcher)
	}
	s.pass(t) // the normal rules settle it
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureProcessExited)
}

// --- second round of review feedback on PR #8353 ---

// The boot pass never waits on a worker lock: a trigger hung in Spawn must not
// stall startup reconciliation, the reaper loop, or anyone's launch timeout.
// The hung worker's previous-daemon runs stay reserved and are settled by a
// later pass once the lock frees.
func TestBootPassDoesNotBlockBehindAHungTrigger(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1", alive: true}
	s := newLifecycleEngine(t, launcher)
	review, err := s.upsertReview(context.Background(), s.worker, domain.ReviewerClaudeCode, "review-mer-1", "", "launch-1", domain.ActivityActive, *s.now)
	if err != nil {
		t.Fatal(err)
	}
	leftover := s.insertLaunchingRun(t, review)
	s.advance(10 * time.Minute)
	after := &fakeLauncher{handle: "review-mer-1", alive: true, killOnDestroy: true}
	s.restartBeforeBoot(t, after, RunLifecycleConfig{})
	s.advance(time.Second)

	started, unblock := make(chan struct{}), make(chan struct{})
	var startOnce sync.Once
	after.unblockSpawn = unblock
	after.onSpawn = func(LaunchSpec) { startOnce.Do(func() { close(started) }) }
	done := make(chan TriggerResult, 1)
	go func() {
		res, err := s.TriggerWithOptions(context.Background(), s.worker.ID, TriggerOptions{Harness: domain.ReviewerCodex})
		if err != nil || !res.Created {
			t.Errorf("trigger did not launch: %+v err=%v", res, err)
			startOnce.Do(func() { close(started) })
		}
		done <- res
	}()
	<-started
	if t.Failed() {
		t.FailNow()
	}

	booted := make(chan error, 1)
	go func() { booted <- s.reaper.Tick(context.Background()) }()
	select {
	case err := <-booted:
		if err != nil {
			t.Fatalf("boot pass: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(unblock)
		t.Fatal("the boot pass blocked behind the hung trigger")
	}
	wantRun(t, s.run(t, leftover.ID), domain.ReviewRunLaunching, "")

	// The hung launch is this daemon's and still times out.
	runs, err := s.store.ListActiveReviewRunsBySession(context.Background(), s.worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	var hung domain.ReviewRun
	for _, run := range runs {
		if run.ID != leftover.ID {
			hung = run
		}
	}
	s.wait(t, DefaultRunLifecycleConfig().LaunchTimeout)
	wantRun(t, s.run(t, hung.ID), domain.ReviewRunFailed, domain.ReviewFailureNeverStarted)
	wantRun(t, s.run(t, leftover.ID), domain.ReviewRunLaunching, "")

	close(unblock)
	<-done
	s.pass(t)
	wantRun(t, s.run(t, leftover.ID), domain.ReviewRunFailed, domain.ReviewFailureDaemonRestarted)
}

// A reviewer whose current launch serves a run this daemon created is not an
// orphan, even when an older run on its previous launch is failed at boot.
func TestBootPassKeepsAReviewerServingThisDaemonsRun(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1", alive: true}
	s := newLifecycleEngine(t, launcher)
	ctx := context.Background()
	review, err := s.upsertReview(ctx, s.worker, domain.ReviewerClaudeCode, "review-mer-1", "", "launch-1", domain.ActivityActive, *s.now)
	if err != nil {
		t.Fatal(err)
	}
	old := domain.ReviewRun{ID: "run-old", ReviewID: review.ID, SessionID: s.worker.ID, Harness: review.Harness, PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning, CreatedAt: *s.now}
	if err := s.store.InsertReviewRun(ctx, old); err != nil {
		t.Fatal(err)
	}
	after := &fakeLauncher{handle: "review-mer-1", alive: true, killOnDestroy: true}
	s.restartBeforeBoot(t, after, RunLifecycleConfig{})
	s.advance(time.Second)
	// This daemon replaces the reviewer (launch-2) for another PR.
	if _, err := s.upsertReview(ctx, s.worker, domain.ReviewerClaudeCode, "review-mer-1", "", "launch-2", domain.ActivityActive, *s.now); err != nil {
		t.Fatal(err)
	}
	current := domain.ReviewRun{ID: "run-current", ReviewID: review.ID, SessionID: s.worker.ID, Harness: review.Harness, PRURL: "https://github.com/o/r/pull/2", TargetSHA: "sha2", Status: domain.ReviewRunRunning, CreatedAt: *s.now}
	if err := s.store.InsertReviewRun(ctx, current); err != nil {
		t.Fatal(err)
	}
	s.pass(t) // boot pass
	wantRun(t, s.run(t, old.ID), domain.ReviewRunFailed, domain.ReviewFailureDaemonRestarted)
	wantRun(t, s.run(t, current.ID), domain.ReviewRunRunning, "")
	if after.destroyed {
		t.Fatal("the boot pass destroyed the reviewer serving this daemon's run")
	}
}

// failingForceStopLauncher's force-stop is unconfirmed until survive is false,
// like a captured agent that cannot be killed yet.
type failingForceStopLauncher struct {
	*fakeLauncher
	survive bool
	calls   int
}

func (f *failingForceStopLauncher) ForceStop(context.Context, string) (bool, error) {
	f.calls++
	if f.survive {
		return false, errors.New("reviewer processes still alive after force-stop: [44493]")
	}
	return true, nil
}

// An unconfirmed force-kill after a cancel ends the run cancel_unconfirmed,
// but the reviewer is still retried on later passes until its death is
// confirmed; only then is its handle released.
func TestUnconfirmedCancelKillIsRetriedUntilConfirmed(t *testing.T) {
	launcher := &failingForceStopLauncher{fakeLauncher: &fakeLauncher{handle: "review-mer-1"}, survive: true}
	s := newLifecycleEngine(t, launcher.fakeLauncher)
	s.restart(t, launcher.fakeLauncher, RunLifecycleConfig{})
	s.launcher = launcher
	run := s.trigger(t)
	if _, err := s.Cancel(context.Background(), s.worker.ID); err != nil {
		t.Fatal(err)
	}
	// A failed force-stop is reported as a reaper error, so step the passes
	// by hand rather than through wait, which treats any error as fatal.
	tick := func() { _ = s.reaper.Tick(context.Background()) }
	for elapsed := time.Duration(0); elapsed < DefaultRunLifecycleConfig().CancelTimeout; elapsed += reaper.DefaultTickInterval {
		s.advance(reaper.DefaultTickInterval)
		tick()
	}
	wantRun(t, s.run(t, run.ID), domain.ReviewRunFailed, domain.ReviewFailureCancelUnconfirmed)
	if s.review(t).ReviewerHandleID == "" {
		t.Fatal("handle released while the reviewer may still be alive")
	}
	before := launcher.calls
	tick()
	if launcher.calls <= before {
		t.Fatal("the unconfirmed kill was not retried")
	}
	launcher.survive = false
	s.pass(t)
	if s.review(t).ReviewerHandleID != "" || len(s.pendingStops()) != 0 {
		t.Fatalf("confirmed retry did not release the reviewer: handle=%q stops=%+v", s.review(t).ReviewerHandleID, s.pendingStops())
	}
}
