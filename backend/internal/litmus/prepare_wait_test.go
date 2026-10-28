package litmus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedRunner blocks the first runner call until gate closes, so a prepare can be caught in flight. entered, when set,
// is closed as that call begins to wait.
type gatedRunner struct {
	inner   *fakeRunner
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (g *gatedRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	wait := false
	g.once.Do(func() {
		wait = true
		if g.entered != nil {
			close(g.entered)
		}
	})
	if wait {
		<-g.gate
	}
	return g.inner.Run(ctx, dir, env, name, args...)
}

func TestPrepareFinishingAfterRunClosedKeepsRunClosed(t *testing.T) {
	svc, fake := prepSvc(t, map[string]string{"v24": "v24.21.0"})
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	gate := &gatedRunner{inner: fake, gate: make(chan struct{}), entered: make(chan struct{})}
	svc.runner = gate
	release := sync.OnceFunc(func() { close(gate.gate) })
	defer release()
	type result struct {
		run Run
		err error
	}
	finished := make(chan result, 1)
	go func() {
		run, err := svc.Prepare(ctx, "s1")
		finished <- result{run, err}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare did not reach the paused build")
	}

	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, Verdict{Result: "blocked", BlockedOn: "stopped while prepare was in flight"}))
	is(t, "")(nil, err)
	closed, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	assertBlockedPrepareVerdict(t, svc, closed, "stopped while prepare was in flight")
	release()
	select {
	case got := <-finished:
		if got.err != nil || !reflect.DeepEqual(got.run, closed) {
			t.Fatalf("prepare changed the closed run: state %s err %v", got.run.State, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("prepare did not finish after its build was released")
	}
	saved, err := svc.store.Get(closed.ID)
	if err != nil || !reflect.DeepEqual(saved, closed) {
		t.Fatalf("prepare overwrote the closed run on disk: state %s err %v", saved.State, err)
	}

	is(t, "TESTING_RUN_CLOSED")(svc.Status(ctx, "s1"))
}

func TestPrepareRetryWaitsForTheBuildInFlight(t *testing.T) {
	svc, f := prepSvc(t, map[string]string{"v24": "v24.21.0\n"})
	if _, err := svc.Intake(ctx, "s1", prURL); err != nil {
		t.Fatal(err)
	}
	gate := &gatedRunner{inner: f, gate: make(chan struct{})}
	svc.runner = gate
	type res struct {
		run Run
		err error
	}
	first := make(chan res, 1)
	go func() { r, err := svc.Prepare(ctx, "s1"); first <- res{r, err} }()
	deadline := time.Now().Add(5 * time.Second)
	for { // status shows the first prepare in flight once it has begun
		st, err := svc.Status(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if len(st.InFlight) == 1 && st.InFlight[0].Verb == "prepare" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("prepare never showed in flight: %+v", st.InFlight)
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := make(chan res, 1)
	go func() { r, err := svc.Prepare(ctx, "s1"); second <- res{r, err} }()
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.Prepare(cctx, "s1"); !errors.Is(err, context.Canceled) { // a waiter that gives up gets its own error, not busy
		t.Fatalf("cancelled waiter err = %v", err)
	}
	close(gate.gate)
	for _, c := range []chan res{first, second} {
		select {
		case r := <-c:
			if r.err != nil || r.run.State != RunPrepared {
				t.Fatalf("run state %q err %v", r.run.State, r.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("prepare did not finish")
		}
	}
	if n := f.count("clone --shared"); n != 2 { // the second prepare reused the clones the first made
		t.Fatalf("clones = %d, want 2 (head and base once)", n)
	}
	if st, err := svc.Status(ctx, "s1"); err != nil || len(st.InFlight) != 0 {
		t.Fatalf("after prepare: in flight %+v err %v", st.InFlight, err)
	}
}

// A build step that fails reaches the tester as TESTING_PREPARE_FAILED with the step's last lines, not a bare 500.
func TestPrepareReportsAFailedBuildStepWithItsOutput(t *testing.T) {
	svc, f := prepSvc(t, map[string]string{"v24": "v24.21.0\n"})
	if _, err := svc.Intake(ctx, "s1", prURL); err != nil {
		t.Fatal(err)
	}
	f.sh, f.shOut = func(string) error { return errors.New("exit status 1") }, "vite: out of memory"
	_, err := svc.Prepare(ctx, "s1")
	is(t, "TESTING_PREPARE_FAILED")(nil, err)
	if !strings.Contains(err.Error(), "head build at ") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v", err)
	}
}

// The tester's budget starts when the builds are ready: intake's deadline gives way to prepare's, and a repeat
// prepare does not extend it.
func TestPrepareStartsTheRunBudgetWhenTheBuildsAreReady(t *testing.T) {
	svc, _ := prepSvc(t, map[string]string{"v24": "v24.21.0\n"})
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc.clock = func() time.Time { return at }
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || !run.Deadline.Equal(at.Add(RunBudget)) {
		t.Fatalf("intake deadline %v err %v", run.Deadline, err)
	}
	at = at.Add(9 * time.Minute) // a slow build on a loaded Mac
	run, err = svc.Prepare(ctx, "s1")
	if want := at.Add(RunBudget); err != nil || run.State != RunPrepared || !run.Deadline.Equal(want) {
		t.Fatalf("prepared run state %q deadline %v err %v, want deadline %v", run.State, run.Deadline, err, want)
	}
	if saved, err := svc.store.Get(run.ID); err != nil || !saved.Deadline.Equal(run.Deadline) {
		t.Fatalf("saved deadline %v err %v", saved.Deadline, err)
	}
	at = at.Add(time.Minute)
	if again, err := svc.Prepare(ctx, "s1"); err != nil || !again.Deadline.Equal(run.Deadline) {
		t.Fatalf("a second prepare moved the deadline to %v (err %v)", again.Deadline, err)
	}
	// The builds moved the app deadline. Submit stays open for five minutes afterward.
	at = run.Deadline.Add(-time.Second)
	if st, err := svc.Status(ctx, "s1"); err != nil || st.Run.State != RunPrepared {
		t.Fatalf("status just before the moved deadline: %+v err %v", st.Run, err)
	}
	at = run.Deadline.Add(writeupGrace)
	is(t, "TESTING_RUN_CLOSED")(svc.Status(ctx, "s1"))
	if saved, _ := svc.store.Get(run.ID); saved.State != RunExpired {
		t.Fatalf("state %q after the moved deadline", saved.State)
	}
}

// stuckRunner hangs every build step until the call's context ends, as an install that never finishes does.
type stuckRunner struct{ *fakeRunner }

func (s stuckRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "sh" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.fakeRunner.Run(ctx, dir, env, name, args...)
}

// A prepare that outlasts its budget ends the run as blocked with that reason, not as a failed build to retry.
func TestPrepareOverBudgetClosesTheRunAsBlocked(t *testing.T) {
	svc, f := prepSvc(t, map[string]string{"v24": "v24.21.0\n"})
	if _, err := svc.Intake(ctx, "s1", prURL); err != nil {
		t.Fatal(err)
	}
	svc.runner, svc.prepareBudget = stuckRunner{f}, 100*time.Millisecond
	_, err := svc.Prepare(ctx, "s1")
	is(t, "TESTING_PREPARE_TIMEOUT")(nil, err)
	run, gerr := svc.store.Get("run_s1")
	if gerr != nil || run.State != RunSubmitted {
		t.Fatalf("run %+v err %v", run, gerr)
	}
	assertBlockedPrepareVerdict(t, svc, run, "the head build was still going")
	is(t, "TESTING_RUN_CLOSED")(svc.Status(ctx, "s1"))
}

// stuckBase lets the head build finish and hangs the base build's steps.
type stuckBase struct{ *fakeRunner }

func (s stuckBase) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "sh" && strings.Contains(dir, base6394) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.fakeRunner.Run(ctx, dir, env, name, args...)
}

// When the base build is the one still going, the run records that the head build was ready.
func TestPrepareOverBudgetRecordsTheBuildThatWasReady(t *testing.T) {
	svc, f := prepSvc(t, map[string]string{"v24": "v24.21.0\n"})
	if _, err := svc.Intake(ctx, "s1", prURL); err != nil {
		t.Fatal(err)
	}
	svc.runner, svc.prepareBudget = stuckBase{f}, 2*time.Second
	_, err := svc.Prepare(ctx, "s1")
	is(t, "TESTING_PREPARE_TIMEOUT")(nil, err)
	if run, gerr := svc.store.Get("run_s1"); gerr != nil || !slices.Equal(run.Ready, []string{"head"}) {
		t.Fatalf("ready %v err %v", run.Ready, gerr)
	}
}

// A failed timeout-state write must not claim that the run was closed.
func TestPrepareTimeoutReturnsPersistenceFailure(t *testing.T) {
	svc, _ := newSvc(t)
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	if err := os.Remove(filepath.Join(svc.store.root, run.ID, "run.json")); err != nil {
		t.Fatal(err)
	}
	err = svc.blockStuckPrepare(ctx, run, "head", nil)
	is(t, "TESTING_PREPARE_TIMEOUT")(nil, err)
	if !errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "the run is closed as blocked") {
		t.Fatalf("timeout hid a persistence failure: %v", err)
	}
}

func assertBlockedPrepareVerdict(t *testing.T, svc *Service, run Run, reason string) {
	t.Helper()
	if run.Error != "" {
		t.Fatalf("blocked verdict became Run.Error: %q", run.Error)
	}
	var v Verdict
	is(t, "")(nil, svc.store.read(run.ID, verdictFile, &v))
	if v.Result != "blocked" || !strings.Contains(v.BlockedOn, reason) {
		t.Fatalf("verdict %+v", v)
	}
	md := readRunFile(t, svc, run, "verdict.md")
	if !strings.Contains(md, "# Verdict: blocked") || !strings.Contains(md, reason) {
		t.Fatal(md)
	}
}

func TestPrepareTimeoutPreservesAlreadySubmittedVerdict(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, Verdict{Result: "blocked", BlockedOn: "tester supplied reason"}))
	is(t, "")(nil, err)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	before := map[string]string{}
	for _, name := range []string{"run.json", "verdict.json", "verdict.md"} {
		before[name] = readRunFile(t, svc, run, name)
	}
	reported, killed := 0, 0
	svc.WithReporter(func(context.Context, string, string) error { reported++; return nil })
	svc.WithKiller(func(context.Context, string) error { killed++; return nil })
	err = svc.blockStuckPrepare(ctx, run, "head", nil)
	is(t, "TESTING_PREPARE_TIMEOUT")(nil, err)
	if !strings.Contains(err.Error(), "already closed") || reported != 0 || killed != 0 {
		t.Fatalf("err %v reports %d kills %d", err, reported, killed)
	}
	for name, want := range before {
		if got := readRunFile(t, svc, run, name); got != want {
			t.Fatalf("%s overwritten in %s", name, dir)
		}
	}
}
