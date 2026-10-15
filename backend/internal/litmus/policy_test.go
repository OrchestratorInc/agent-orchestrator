package litmus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestToolPolicyListsAndRunDir(t *testing.T) {
	allowed, denied := ToolPolicy()
	for _, a := range []string{"Bash", "Read", "Edit", "Write", "Grep", "Glob", "WebFetch", "WebSearch"} {
		if !slices.Contains(allowed, a) {
			t.Errorf("%s is not allowed", a)
		}
	}
	for _, d := range []string{"Bash(git push:*)", "Bash(gh pr comment:*)", "Bash(gh pr review:*)", "Bash(gh api:*)", "Bash(gh pr update-branch:*)", "Bash(ao testing start:*)", "Bash(ao spawn:*)", "Bash(ao session:*)", "Bash(ao send:*)", "Bash(ao pr:*)", "Bash(ao stop:*)", "Bash(gh auth:*)", "Bash(ao testing post:*)", "Bash(ao testing prune:*)", "Read(**/litmus/runs/run_*/comment.md)", "Read(**/litmus/runs/run_*/verdict.md)"} {
		if !slices.Contains(denied, d) {
			t.Errorf("%s is not denied", d)
		}
	}
	for _, d := range denied {
		if !strings.HasPrefix(d, "Bash(") && !strings.HasPrefix(d, "Read(**/litmus/runs/run_*/") {
			t.Errorf("a whole tool is denied: %s", d)
		}
	}
	allowed[0], denied[0] = "x", "x" // callers get copies
	if a, d := ToolPolicy(); a[0] == "x" || d[0] == "x" {
		t.Fatal("ToolPolicy shares its backing lists")
	}

	dir := RunDir("/data", "proj-7")
	if dir != filepath.Join("/data", "litmus", "runs", "run_proj-7") || !IsRunDir("/data", dir) {
		t.Fatalf("run dir %q", dir)
	}
	for _, not := range []string{"", "/data/litmus/runs", "/data/litmus/runs/run_a/receipts", "/data/worktrees/run_a", "/other/litmus/runs/run_a", "/data/litmus/runs/x"} {
		if IsRunDir("/data", not) {
			t.Errorf("IsRunDir(%q) = true", not)
		}
	}
}

// sweep runs one deadline sweep and returns the sessions kill was asked to stop.
func sweep(svc *Service, killErr error) (killed []string) {
	svc.SweepExpired(context.Background(), func(_ context.Context, id string) error {
		killed = append(killed, id)
		return killErr
	}, quiet)
	slices.Sort(killed) // List order among equal creation times is not defined
	return killed
}

func TestSweepExpiredTransitionsAndRestart(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc := New(NewStore(dir), func() time.Time { return now }).WithGH(fixtureGH(t))
	for _, sid := range []string{"old", "lazy", "done"} {
		is(t, "")(svc.Intake(ctx, sid, prURL))
	}
	now = now.Add(time.Minute)
	is(t, "")(svc.Intake(ctx, "young", prURL)) // its deadline is a minute after the others
	is(t, "")(nil, svc.store.Save(Run{ID: "run_gone", SessionID: "gone", State: RunSubmitted, Deadline: now.Add(-time.Hour)}))
	is(t, "")(svc.update("run_done", func(r *Run) { r.State = RunSubmitted }))

	if got := sweep(svc, nil); got != nil {
		t.Fatalf("killed %v before any deadline", got)
	}

	now = now.Add(RunBudget - time.Minute + time.Second) // old, lazy and done are past the deadline, young is not
	is(t, "TESTING_RUN_CLOSED")(svc.Status(ctx, "lazy")) // a verb flips lazy to expired before the sweep sees it
	if got := sweep(svc, errors.New("kill failed")); !slices.Equal(got, []string{"lazy", "old"}) {
		t.Fatalf("killed %v, want lazy and old (a submitted run is left alone)", got)
	}
	if got := sweep(svc, nil); !slices.Equal(got, []string{"lazy", "old"}) { // the failed kill is retried
		t.Fatalf("retry killed %v", got)
	}
	if got := sweep(svc, nil); got != nil { // reaped runs are not touched again
		t.Fatalf("killed %v after reaping", got)
	}
	states := map[string]RunState{}
	runs, _ := svc.store.List()
	for _, r := range runs {
		states[r.SessionID] = r.State
	}
	if states["old"] != RunExpired || states["lazy"] != RunExpired || states["young"] != RunIntake || states["done"] != RunSubmitted {
		t.Fatalf("states = %v", states)
	}
	is(t, "TESTING_RUN_CLOSED")(svc.CheckAdd(ctx, "old", "late", "x", "")) // every verb refuses once expired
	is(t, "TESTING_RUN_CLOSED")(svc.Submit(ctx, "old", "", ""))
	is(t, "")(svc.CheckAdd(ctx, "young", "still open", "x", ""))

	// A new daemon over the same data dir knows what was reaped from disk alone: it leaves old and lazy be, and closes
	// young (it has a check) with that check skipped, without killing anything itself.
	again := New(NewStore(dir), func() time.Time { return now.Add(time.Hour) }).WithGH(fixtureGH(t))
	if got := sweep(again, nil); got != nil {
		t.Fatalf("restart killed %v", got)
	}
	if run, _ := again.store.Get("run_young"); run.State != RunSubmitted {
		t.Fatalf("young = %s, want it closed at its deadline", run.State)
	}
	if checks, _ := again.store.Checks("run_young"); len(checks) != 1 || checks[0].State != CheckSkipped || checks[0].Note != ReasonOutOfTime {
		t.Fatalf("young's checks = %+v", checks)
	}
}

func TestSweepExpiresAStoppedRunWithChecks(t *testing.T) {
	for _, ask := range []bool{false, true} {
		for _, pastDeadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("ask=%t/past-deadline=%t", ask, pastDeadline), func(t *testing.T) {
				svc, runDir, g := askSvc(t, ask)
				_, err := svc.update("run_s1", func(r *Run) { r.State = RunRunning })
				is(t, "")(nil, err)
				is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{{ID: "c1", Name: "unfinished", State: CheckRunning}}))
				run, err := svc.store.Get("run_s1")
				is(t, "")(nil, err)
				now := run.Deadline.Add(-time.Minute)
				if pastDeadline {
					now = run.Deadline.Add(time.Second)
				}
				svc.clock = func() time.Time { return now }
				svc.WithActivity(func(_ context.Context, id string) (string, time.Time, bool) {
					return "exited", now, id == run.SessionID
				})
				var cleaned []string
				svc.closeEngine = func(id string) { cleaned = append(cleaned, id) }
				if killed := sweep(svc, nil); !slices.Equal(killed, []string{"s1"}) || !slices.Equal(cleaned, []string{run.ID}) {
					t.Fatalf("killed %v, cleaned %v", killed, cleaned)
				}
				got, err := svc.store.Get(run.ID)
				if err != nil || got.State != RunExpired || got.Error != ReasonStopped || !svc.reaped(run.ID) || got.Legs[0].State != "cleaned" {
					t.Fatalf("run %+v, reaped %t, err %v", got, svc.reaped(run.ID), err)
				}
				if got.CommentState != "" || got.CommentURL != "" || len(g.calls) != 0 {
					t.Fatalf("comment state %q, URL %q, gh calls %q", got.CommentState, got.CommentURL, g.calls)
				}
				for _, name := range []string{"verdict.md", "verdict.json", "comment.md"} {
					if _, err := os.Stat(filepath.Join(runDir, name)); !os.IsNotExist(err) {
						t.Errorf("%s exists or could not be checked: %v", name, err)
					}
				}
				checks, err := svc.store.Checks(run.ID)
				if err != nil || len(checks) != 1 || checks[0].State != CheckRunning || checks[0].Note != "" {
					t.Fatalf("checks %+v, err %v", checks, err)
				}
				detail, err := svc.RunDetail(ctx, run.ID)
				if err != nil || detail.Error != ReasonStopped || detail.Verdict != verdictExpired || detail.CommentMD != "" {
					t.Fatalf("detail %+v, err %v", detail, err)
				}
				if killed := sweep(svc, nil); len(killed) != 0 || len(cleaned) != 1 {
					t.Fatalf("reaped again: killed %v, cleaned %v", killed, cleaned)
				}
			})
		}
	}
}

func TestSweepNeverSalvagesAStoppedRunLater(t *testing.T) {
	svc, runDir, g := askSvc(t, true)
	_, err := svc.update("run_s1", func(r *Run) { r.State, r.Error = RunExpired, ReasonStopped })
	is(t, "")(nil, err)
	is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{{ID: "c1", Name: "unfinished", State: CheckRunning}}))
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	svc.clock = func() time.Time { return run.Deadline.Add(time.Second) }
	// an earlier tick expired it as stopped but did not reap it, and the session row is gone by now
	svc.WithActivity(func(context.Context, string) (string, time.Time, bool) { return "", time.Time{}, false })
	if killed := sweep(svc, nil); !slices.Equal(killed, []string{"s1"}) {
		t.Fatalf("killed %v", killed)
	}
	got, err := svc.store.Get(run.ID)
	if err != nil || got.State != RunExpired || got.Error != ReasonStopped || got.CommentState != "" || len(g.calls) != 0 {
		t.Fatalf("run %+v, gh calls %q, err %v", got, g.calls, err)
	}
	for _, name := range []string{"verdict.md", "comment.md"} {
		if _, err := os.Stat(filepath.Join(runDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s exists or could not be checked: %v", name, err)
		}
	}
	if checks, err := svc.store.Checks(run.ID); err != nil || len(checks) != 1 || checks[0].Note != "" {
		t.Fatalf("checks %+v, err %v", checks, err)
	}
}

func TestSweepRetriesTheDeadlineCloseOfAnExpiredRun(t *testing.T) {
	svc, _, _ := askSvc(t, true)
	_, err := svc.update("run_s1", func(r *Run) { r.State = RunExpired }) // its deadline close failed on an earlier tick
	is(t, "")(nil, err)
	is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{{ID: "c1", Name: "unfinished", State: CheckRunning}}))
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	svc.clock = func() time.Time { return run.Deadline.Add(time.Second) }
	svc.WithActivity(func(_ context.Context, id string) (string, time.Time, bool) { // the sweep killed its session
		return "exited", run.Deadline, id == run.SessionID
	})
	sweep(svc, nil)
	got, err := svc.store.Get(run.ID)
	if err != nil || got.State != RunAwaitingApproval || got.Error == ReasonStopped {
		t.Fatalf("run %+v, err %v", got, err)
	}
	if checks, err := svc.store.Checks(run.ID); err != nil || len(checks) != 1 || checks[0].Note != ReasonOutOfTime {
		t.Fatalf("checks %+v, err %v", checks, err)
	}
}

func TestSweepSparesARunBeforeItsDeadline(t *testing.T) {
	for _, c := range []struct {
		name, state string
		found, busy bool
	}{
		{"active", "active", true, false},
		{"unknown", "exited", false, false},
		{"in flight", "exited", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, _, g := askSvc(t, true)
			run, err := svc.store.Get("run_s1")
			is(t, "")(nil, err)
			svc.clock = func() time.Time { return run.Deadline.Add(-time.Minute) }
			svc.WithActivity(func(context.Context, string) (string, time.Time, bool) {
				return c.state, time.Time{}, c.found
			})
			if c.busy {
				svc.busy[run.ID+" prepare"] = busyVerb{since: svc.clock(), done: make(chan struct{})}
			}
			if killed := sweep(svc, nil); len(killed) != 0 {
				t.Fatalf("killed %v before the deadline", killed)
			}
			got, err := svc.store.Get(run.ID)
			if err != nil || got.State != run.State || got.Error != run.Error || svc.reaped(run.ID) || len(g.calls) != 0 {
				t.Fatalf("run %+v, err %v, gh calls %q", got, err, g.calls)
			}
		})
	}
}

func TestSweepWaitsForInFlightVerbBeforeReaping(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc := New(NewStore(dir), func() time.Time { return now }).WithGH(fixtureGH(t))
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	now = now.Add(RunBudget + time.Second)

	svc.busy["run_s1 launch"] = busyVerb{since: now, done: make(chan struct{})} // a launch is still building when the deadline passes
	if got := sweep(svc, nil); !slices.Equal(got, []string{"s1"}) {
		t.Fatalf("the session of an expired run is stopped at once, killed %v", got)
	}
	if run, _ := svc.store.Get("run_s1"); run.State != RunExpired || svc.reaped("run_s1") {
		t.Fatalf("state %s reaped %t while the launch was still running", run.State, svc.reaped("run_s1"))
	}
	delete(svc.busy, "run_s1 launch")
	if got := sweep(svc, nil); !slices.Equal(got, []string{"s1"}) || !svc.reaped("run_s1") {
		t.Fatalf("after the launch: killed %v, reaped %t", got, svc.reaped("run_s1"))
	}
}

// spawnTester starts a tester through Start with a spawner that returns sid.
func spawnTester(t *testing.T, svc *Service, sid string) {
	t.Helper()
	svc.WithSpawner(func(context.Context, SpawnRequest) (string, error) { return sid, nil })
	is(t, "")(svc.Start(ctx, StartRequest{PRURL: prURL}))
}

func TestSweepReapsATesterThatNeverRanIntake(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	act := map[string]string{} // session -> activity state
	since := now
	svc := New(NewStore(dir), func() time.Time { return now }).WithGH(fixtureGH(t)).
		WithActivity(func(_ context.Context, id string) (string, time.Time, bool) {
			st, ok := act[id]
			return st, since, ok
		})
	for _, sid := range []string{"idle", "gone", "slow", "worked", "busy"} {
		spawnTester(t, svc, sid)
	}
	is(t, "")(svc.Intake(ctx, "worked", prURL)) // ran intake and planned a check
	is(t, "")(svc.CheckAdd(ctx, "worked", "a check", "x", ""))
	act["idle"], act["gone"], act["worked"], act["busy"] = "idle", "active", "idle", "active"

	now = now.Add(time.Minute)
	if got := sweep(svc, nil); got != nil {
		t.Fatalf("killed %v a minute in", got)
	}
	act["gone"] = "exited"                     // exited counts at once
	since = now.Add(-IdleBudget - time.Second) // idle has now lasted over 2 minutes
	now = now.Add(time.Minute)
	if got := sweep(svc, nil); !slices.Equal(got, []string{"gone", "idle"}) {
		t.Fatalf("killed %v, want gone (exited) and idle (idle over 2 minutes); worked has checks, busy is active", got)
	}
	run, err := svc.store.Get("run_idle")
	if err != nil || run.State != RunExpired || run.Error != ReasonNoIntake || run.Repo != "aoagents/agent-orchestrator" || run.PRNumber != 6394 {
		t.Fatalf("run.json = %+v, %v", run, err)
	}
	_, err = svc.Status(ctx, "idle")
	if !strings.Contains(fmt.Sprint(err), ReasonNoIntake) {
		t.Fatalf("status does not explain: %v", err)
	}
	if got := sweep(svc, nil); got != nil { // reaped once, not again
		t.Fatalf("killed %v again", got)
	}

	// Past 5 minutes from spawn the rest go whatever their session says, except a run that has checks.
	now = now.Add(SpawnBudget)
	if got := sweep(svc, nil); !slices.Equal(got, []string{"busy", "slow"}) {
		t.Fatalf("killed %v", got)
	}
	if run, _ := svc.store.Get("run_worked"); run.Error == ReasonNoIntake {
		t.Fatalf("a tester with checks was reaped as never having run intake: %+v", run)
	}
	if run, _ := svc.store.Get("run_slow"); run.State != RunExpired || run.Error != ReasonNoIntake {
		t.Fatalf("slow = %+v", run)
	}
}

func TestSweepSparesATesterWithIntakeOrPrepareInFlight(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc := New(NewStore(t.TempDir()), func() time.Time { return now }).WithGH(fixtureGH(t))
	spawnTester(t, svc, "s1")
	for _, verb := range []string{"intake", "prepare"} {
		svc.busy["run_s1 "+verb] = busyVerb{since: now, done: make(chan struct{})}
		now = now.Add(SpawnBudget + time.Second)
		if got := sweep(svc, nil); got != nil {
			t.Fatalf("killed %v while %s was in flight", got, verb)
		}
		delete(svc.busy, "run_s1 "+verb)
	}
	if got := sweep(svc, nil); !slices.Equal(got, []string{"s1"}) {
		t.Fatalf("killed %v once nothing was in flight", got)
	}
}

// A run with checks that reaches its deadline is finished with what exists, and then follows its comment mode.
func TestSweepClosesARunWithChecksAtItsDeadline(t *testing.T) {
	for name, c := range map[string]struct {
		ask  bool
		mode string
		want RunState
		sent bool
	}{
		"post":       {false, CommentPost, RunSubmitted, true},
		"ask":        {true, CommentPost, RunAwaitingApproval, false},
		"no comment": {false, CommentOff, RunSubmitted, false},
	} {
		t.Run(name, func(t *testing.T) {
			svc, runDir, g := askSvc(t, c.ask)
			svc.WithActivity(func(context.Context, string) (string, time.Time, bool) {
				return "active", time.Time{}, true
			})
			_, err := svc.update("run_s1", func(r *Run) { r.Comment = c.mode })
			is(t, "")(nil, err)
			checks, _ := svc.store.Checks("run_s1")
			is(t, "")(nil, svc.store.SaveChecks("run_s1", append(checks,
				Check{ID: "c4", Name: "late check", Expect: "opens", State: CheckRunning},
				Check{ID: "c5", Name: "late check", Expect: "opens", State: CheckPending, Leg: "base"})))
			run, _ := svc.store.Get("run_s1")
			svc.clock = func() time.Time { return run.Deadline.Add(time.Second) }
			sweep(svc, nil)
			got, _ := svc.store.Get("run_s1")
			if got.State != c.want || (len(g.sent) > 0) != c.sent {
				t.Fatalf("state %s (want %s), comments sent %q", got.State, c.want, g.sent)
			}
			saved, _ := svc.store.Checks("run_s1")
			for _, i := range []int{3, 4} {
				if saved[i].State != CheckSkipped || saved[i].Note != ReasonOutOfTime {
					t.Errorf("%s = %+v, want skipped %q", saved[i].ID, saved[i], ReasonOutOfTime)
				}
			}
			for _, f := range []string{"verdict.md", "comment.md"} {
				exists(t, filepath.Join(runDir, f))
			}
			if md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md")); !strings.Contains(string(md), "# Verdict: pass") {
				t.Errorf("verdict.md:\n%s", md)
			}
			if sweep(svc, nil); len(g.sent) > 1 {
				t.Errorf("the next sweep posted again: %q", g.sent)
			}
		})
	}
}

func TestDeadlineCloseLeavesARunSubmittedAfterListingAlone(t *testing.T) {
	svc, runDir, g := askSvc(t, false)
	runs, err := svc.store.List()
	is(t, "")(nil, err)
	closed := 0
	svc.closeEngine = func(string) { closed++ }
	is(t, "")(svc.Submit(ctx, "s1", "original verdict", ""))
	before, err := os.ReadFile(filepath.Join(runDir, "verdict.md"))
	is(t, "")(nil, err)
	if len(g.sent) != 1 || closed != 1 {
		t.Fatalf("submit: comments %d, teardowns %d", len(g.sent), closed)
	}
	svc.clock = func() time.Time { return runs[0].Deadline.Add(time.Second) }
	if !svc.closeAtDeadline(ctx, runs[0], quiet) {
		t.Fatal("a run submitted after listing was not handled")
	}
	after, err := os.ReadFile(filepath.Join(runDir, "verdict.md"))
	is(t, "")(nil, err)
	if len(g.sent) != 1 || closed != 1 || string(after) != string(before) {
		t.Fatalf("closed again: comments %d, teardowns %d, verdict changed %t", len(g.sent), closed, string(after) != string(before))
	}
	if svc.inFlight(runs[0].ID) {
		t.Fatal("the skipped close did not release its busy marker")
	}
}

func TestDeadlineCloseLeavesClosedOrReapedRunsAlone(t *testing.T) {
	for _, state := range []RunState{RunSubmitted, RunAwaitingApproval, RunCleaned, RunExpired} {
		t.Run(string(state), func(t *testing.T) {
			svc, runDir, g := askSvc(t, false)
			run, err := svc.store.Get("run_s1")
			is(t, "")(nil, err)
			is(t, "")(svc.update(run.ID, func(cur *Run) { cur.State = state }))
			if state == RunExpired {
				is(t, "")(nil, svc.store.WriteFile(run.ID, reapedFile, []byte("already reaped")))
			}
			is(t, "")(nil, svc.store.WriteFile(run.ID, "verdict.md", []byte("original")))
			svc.closeEngine = func(string) { t.Error("closed a finished run's engines") }
			if !svc.closeAtDeadline(ctx, run, quiet) || svc.inFlight(run.ID) {
				t.Fatal("finished run was not handled or busy marker was retained")
			}
			md, err := os.ReadFile(filepath.Join(runDir, "verdict.md"))
			if err != nil || string(md) != "original" || len(g.sent) != 0 {
				t.Fatalf("finished run changed: verdict %q, error %v, comments %d", md, err, len(g.sent))
			}
		})
	}
}

func TestDeadlineCloseKeepsAReceiptThatArrivedBeforeMarkBusy(t *testing.T) {
	svc, _, _ := askSvc(t, false)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	var once sync.Once
	svc.clock = func() time.Time {
		once.Do(func() { // markBusy runs after the first ledger read
			checks, err := svc.store.Checks(run.ID)
			is(t, "")(nil, err)
			checks[0].Receipts = append(checks[0].Receipts, Receipt{Seq: 99, Leg: "head", Action: "click"})
			is(t, "")(nil, svc.store.SaveChecks(run.ID, checks))
		})
		return run.Deadline.Add(time.Second)
	}
	if !svc.closeAtDeadline(ctx, run, quiet) {
		t.Fatal("deadline close was not handled")
	}
	checks, err := svc.store.Checks(run.ID)
	is(t, "")(nil, err)
	if !slices.ContainsFunc(checks[0].Receipts, func(r Receipt) bool { return r.Seq == 99 }) {
		t.Fatalf("the new receipt was overwritten: %+v", checks[0].Receipts)
	}
}

func TestSweepReapsAnIntakeOnlyRunAfterSpawnBudgetWithoutActivitySeam(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc := New(NewStore(t.TempDir()), func() time.Time { return now }).WithGH(fixtureGH(t))
	spawnTester(t, svc, "s1")
	is(t, "")(svc.Intake(ctx, "s1", prURL)) // intake ran, no checks followed
	now = now.Add(SpawnBudget - time.Second)
	if got := sweep(svc, nil); got != nil {
		t.Fatalf("killed %v before 5 minutes", got)
	}
	now = now.Add(2 * time.Second)
	if got := sweep(svc, nil); !slices.Equal(got, []string{"s1"}) {
		t.Fatalf("killed %v", got)
	}
	if run, _ := svc.store.Get("run_s1"); run.State != RunExpired || run.Error != ReasonNoIntake || run.HeadSHA == "" {
		t.Fatalf("an existing intake run lost its pins or missed the reason: %+v", run)
	}
}

func TestSweepDiscardsARunThatWaitedAWeekForApproval(t *testing.T) {
	svc, runDir, g := askSvc(t, true)
	_, err := svc.Submit(ctx, "s1", "", "")
	is(t, "")(nil, err)
	run, _ := svc.store.Get("run_s1")

	svc.clock = func() time.Time { return run.CreatedAt.Add(AwaitingTTL) }
	if killed := sweep(svc, nil); len(killed) != 0 {
		t.Fatalf("killed %v", killed)
	}
	if got, _ := svc.store.Get("run_s1"); got.State != RunAwaitingApproval {
		t.Fatalf("a run exactly at the limit was discarded: %s", got.State)
	}

	svc.clock = func() time.Time { return run.CreatedAt.Add(AwaitingTTL + time.Second) }
	sweep(svc, nil)
	got, _ := svc.store.Get("run_s1")
	if got.State != RunCleaned || got.CommentState != CommentDiscarded || len(g.calls) != 0 {
		t.Fatalf("after a week: %s %q, gh calls %q", got.State, got.CommentState, g.calls)
	}
	log, _ := os.ReadFile(filepath.Join(runDir, "comment.log"))
	if !strings.Contains(string(log), "discarded: nobody approved or discarded this comment within 7 days") {
		t.Fatalf("comment.log = %q", log)
	}
	is(t, "TESTING_NOT_AWAITING")(svc.Post(ctx, "run_s1"))
}

// RefuseTester names the verbs only the user may run; a session with a run or a spawn marker is a tester.
func TestRefuseTesterRefusesARunsOwnSession(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(Run{ID: "run_ao-1", SessionID: "ao-1"}); err != nil {
		t.Fatal(err)
	}
	s := New(store, nil)
	err := s.RefuseTester("ao-1", "post")
	if err == nil || !strings.Contains(err.Error(), "post") {
		t.Fatalf("tester session not refused: %v", err)
	}
	for _, id := range []string{"", "ao-orch"} {
		if err := s.RefuseTester(id, "post"); err != nil {
			t.Errorf("session %q refused: %v", id, err)
		}
	}
}
