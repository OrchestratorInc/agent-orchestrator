package reaper

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeReviewerObserver struct {
	targets  []ports.ReviewerTarget
	listErr  error
	alive    map[string]bool
	probeErr map[string]error
	probed   []string
	applyErr error
	passes   []ports.ReviewerObservationPass
}

func (f *fakeReviewerObserver) ListObservableReviewers(context.Context) ([]ports.ReviewerTarget, error) {
	return f.targets, f.listErr
}

func (f *fakeReviewerObserver) ProbeReviewer(_ context.Context, target ports.ReviewerTarget) (bool, error) {
	f.probed = append(f.probed, target.ReviewID)
	return f.alive[target.ReviewID], f.probeErr[target.ReviewID]
}

func (f *fakeReviewerObserver) ApplyReviewerObservations(_ context.Context, pass ports.ReviewerObservationPass) error {
	f.passes = append(f.passes, pass)
	return f.applyErr
}

type failingSessions struct{}

func (failingSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return nil, errors.New("sessions unavailable")
}

func reviewerTarget(id string) ports.ReviewerTarget {
	return ports.ReviewerTarget{ReviewID: id, SessionID: domain.SessionID("w-" + id), HandleID: "pane-" + id, LaunchID: "launch"}
}

func TestReaperReportsReviewerProbesToTheBoundObserver(t *testing.T) {
	obs := &fakeReviewerObserver{
		targets:  []ports.ReviewerTarget{reviewerTarget("alive"), reviewerTarget("dead"), reviewerTarget("unknown"), {SessionID: "w-orphan"}},
		alive:    map[string]bool{"alive": true},
		probeErr: map[string]error{"unknown": errors.New("probe timed out")},
	}
	r := New(&fakeLCM{}, fakeSessions{}, fakeRuntime{}, Config{Logger: quietLogger()})
	r.BindReviewers(obs)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(obs.passes) != 1 || !obs.passes[0].Boot {
		t.Fatalf("passes = %+v, want one boot pass", obs.passes)
	}
	got := map[string]ports.ProbeResult{}
	for _, o := range obs.passes[0].Observations {
		got[o.Target.ReviewID] = o.Reviewer
	}
	want := map[string]ports.ProbeResult{"alive": ports.ProbeAlive, "dead": ports.ProbeDead, "unknown": ports.ProbeFailed, "": ports.ProbeDead}
	for id, result := range want {
		if got[id] != result {
			t.Fatalf("reviewer %q = %q, want %q (all: %v)", id, got[id], result, got)
		}
	}
	if len(obs.probed) != 3 {
		t.Fatalf("probed = %v, want the three reviewers with a handle", obs.probed)
	}

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(obs.passes) != 2 || obs.passes[1].Boot {
		t.Fatalf("second pass = %+v, want a normal pass", obs.passes)
	}
}

// With no review in progress a cycle lists reviewers and stops: no probes and
// nothing to apply once the boot pass is done.
func TestReaperDoesNothingForReviewersWithNoReviewInProgress(t *testing.T) {
	obs := &fakeReviewerObserver{}
	r := New(&fakeLCM{}, fakeSessions{}, fakeRuntime{}, Config{Logger: quietLogger()})
	r.BindReviewers(obs)
	for range 3 {
		if err := r.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(obs.passes) != 1 || !obs.passes[0].Boot || len(obs.probed) != 0 {
		t.Fatalf("passes = %+v probed = %v, want only the empty boot pass", obs.passes, obs.probed)
	}
}

func TestReaperRetriesAFailedReviewerBootPass(t *testing.T) {
	obs := &fakeReviewerObserver{applyErr: errors.New("store busy")}
	r := New(&fakeLCM{}, fakeSessions{}, fakeRuntime{}, Config{Logger: quietLogger()})
	r.BindReviewers(obs)
	if err := r.Tick(context.Background()); err == nil {
		t.Fatal("boot pass error was swallowed")
	}
	obs.applyErr = nil
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(obs.passes) != 2 || !obs.passes[1].Boot {
		t.Fatalf("passes = %+v, want the boot pass retried", obs.passes)
	}
}

// Reviewers have their own breaker: a pass where most reviewers look dead is
// an outage, reported as inconclusive, and a handle-less reviewer (nothing
// was probed) never counts towards it.
func TestReaperReviewerBreakerIsPerKind(t *testing.T) {
	obs := &fakeReviewerObserver{alive: map[string]bool{"r6": true}}
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5", "r6"} {
		obs.targets = append(obs.targets, reviewerTarget(id))
	}
	obs.targets = append(obs.targets, ports.ReviewerTarget{SessionID: "w-orphan"})
	// Every worker is alive, so only the reviewer ratio can trip the breaker.
	workers := fakeSessions{rows: []domain.SessionRecord{probableSession("s1"), probableSession("s2")}}
	lcm := &fakeLCM{}
	r := New(lcm, workers, fakeRuntime{alive: true, workloadAlive: true}, Config{Logger: quietLogger()})
	r.BindReviewers(obs)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, o := range obs.passes[0].Observations {
		switch {
		case o.Target.HandleID == "" && o.Reviewer != ports.ProbeDead:
			t.Fatalf("handle-less reviewer = %q, want dead", o.Reviewer)
		case o.Target.HandleID != "" && o.Target.ReviewID != "r6" && o.Reviewer != ports.ProbeFailed:
			t.Fatalf("reviewer %s = %q, want downgraded to failed by the breaker", o.Target.ReviewID, o.Reviewer)
		}
	}
	for id, facts := range lcm.observed {
		if facts.Runtime != ports.ProbeAlive {
			t.Fatalf("worker %s = %q, reviewers' breaker must not touch workers", id, facts.Runtime)
		}
	}
}

// Workers and reviewers are independent: a failed session listing still lets
// reviewers be observed.
func TestReaperObservesReviewersWhenSessionListingFails(t *testing.T) {
	obs := &fakeReviewerObserver{targets: []ports.ReviewerTarget{reviewerTarget("r1")}, alive: map[string]bool{"r1": true}}
	r := New(&fakeLCM{}, failingSessions{}, fakeRuntime{}, Config{Logger: quietLogger()})
	r.BindReviewers(obs)
	if err := r.Tick(context.Background()); err == nil {
		t.Fatal("session listing error was swallowed")
	}
	if len(obs.passes) != 1 {
		t.Fatalf("reviewer passes = %d, want 1 despite the session listing failure", len(obs.passes))
	}
}
