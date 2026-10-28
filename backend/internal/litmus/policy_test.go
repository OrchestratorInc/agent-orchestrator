package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	for _, d := range []string{"Bash(git push:*)", "Bash(gh pr comment:*)", "Bash(gh pr review:*)", "Bash(gh api:*)", "Bash(gh pr update-branch:*)", "Bash(ao testing start:*)", "Bash(ao testing rerun:*)", "Bash(ao spawn:*)", "Bash(ao session:*)", "Bash(ao send:*)", "Bash(ao pr:*)", "Bash(ao stop:*)", "Bash(gh auth:*)", "Bash(ao testing post:*)", "Bash(ao testing discard:*)", "Bash(ao testing prune:*)", "Bash(ao testing settings:*)", "Read(**/litmus/runs/run_*/comment.md)", "Read(**/litmus/runs/run_*/verdict.md)", "Read(**/litmus/runs/run_*/verdict.json)"} {
		if !slices.Contains(denied, d) {
			t.Errorf("%s is not denied", d)
		}
	}
	for _, d := range denied {
		if !strings.HasPrefix(d, "Bash(") && !slices.ContainsFunc([]string{"Read(", "Write(", "Edit(", "MultiEdit("}, func(prefix string) bool { return strings.HasPrefix(d, prefix+"**/litmus/runs/run_*/") }) {
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

func TestSweepSalvagesExitedTesterAfterGrace(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprintf("valid=%t", valid), func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			path := verdictInput(t, svc, passVerdict())
			if !valid {
				writeFile(t, filepath.Join(dir, path), `{`)
			}
			original, err := os.ReadFile(filepath.Join(dir, path))
			is(t, "")(nil, err)
			run, err := svc.store.Get("run_s1")
			is(t, "")(nil, err)
			now := run.Deadline.Add(-time.Minute)
			svc.clock = func() time.Time { return now }
			svc.WithActivity(func(context.Context, string) (string, time.Time, bool) {
				return "exited", now, true
			})
			for _, at := range []time.Time{now, run.Deadline, run.Deadline.Add(writeupGrace - time.Nanosecond)} {
				now = at
				if killed := sweep(svc, nil); len(killed) != 0 {
					t.Fatalf("exited tester reaped during grace: %v", killed)
				}
				got, err := svc.store.Get(run.ID)
				if err != nil || got.State != run.State || svc.reaped(run.ID) || fileExists(filepath.Join(dir, verdictFile)) {
					t.Fatalf("exited tester closed during grace: %+v, %v", got, err)
				}
			}
			now = run.Deadline.Add(writeupGrace)
			killed := sweep(svc, nil) // no expired Submit call before the sweep
			got, err := svc.store.Get(run.ID)
			is(t, "")(nil, err)
			if valid {
				var v Verdict
				is(t, "")(nil, svc.store.read(run.ID, verdictFile, &v))
				if got.State != RunSubmitted || v.Result != "pass" || len(v.Coverage) != 1 || len(killed) != 0 {
					t.Fatalf("valid scratch verdict lost: run %+v, verdict %+v, killed %v", got, v, killed)
				}
				saved, err := os.ReadFile(filepath.Join(dir, verdictFile))
				if err != nil || string(saved) != string(original) {
					t.Fatalf("salvage changed tester JSON: %v", err)
				}
				if report := readRunFile(t, svc, run, "verdict.md"); !strings.Contains(report, "submitted by Litmus at the deadline from scratch/verdict.json") || !strings.Contains(report, "Receipts: 3\n") {
					t.Errorf("verdict.md lacks salvage provenance or receipt count: %s", report)
				}
				if comment := readRunFile(t, svc, run, "comment.md"); !strings.HasPrefix(comment, "Litmus: pass at "+run.HeadSHA[:7]+"\nThe menu opens.\n") || !strings.HasSuffix(comment, "\n"+commentMarker(run, "summary")+"\n") || strings.Count(comment, "<!-- litmus:") != 1 {
					t.Errorf("salvaged summary lost its result or marker: %s", comment)
				}
			} else {
				if got.State != RunExpired || !svc.reaped(run.ID) || !strings.Contains(got.Error, "no valid scratch/verdict.json: invalid verdict JSON") || !slices.Equal(killed, []string{"s1"}) {
					t.Fatalf("invalid scratch verdict not explained: %+v, killed %v", got, killed)
				}
				for _, name := range []string{verdictFile, "verdict.md", "comment.md"} {
					if fileExists(filepath.Join(dir, name)) {
						t.Errorf("invalid scratch verdict submitted as %s", name)
					}
				}
			}
			if got.Legs[0].State != "cleaned" {
				t.Fatalf("exited tester's targets not cleaned: %+v", got.Legs)
			}
			if killed := sweep(svc, nil); len(killed) != 0 {
				t.Fatalf("closed run reaped again: %v", killed)
			}
		})
	}
}

func TestSweepNeverSalvagesADiscardedRun(t *testing.T) {
	svc, runDir, g := askSvc(t, true)
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict())))
	is(t, "")(svc.Discard(ctx, "run_s1"))
	before, err := os.ReadFile(filepath.Join(runDir, verdictFile))
	is(t, "")(nil, err)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	svc.clock = func() time.Time { return run.Deadline.Add(writeupGrace) }
	svc.WithActivity(func(context.Context, string) (string, time.Time, bool) { return "exited", time.Time{}, true })
	if killed := sweep(svc, nil); len(killed) != 0 {
		t.Fatalf("discarded run reaped: %v", killed)
	}
	got, err := svc.store.Get(run.ID)
	if err != nil || got.State != RunCleaned || got.CommentState != CommentDiscarded || len(g.calls) != 0 {
		t.Fatalf("discarded run changed: %+v, gh calls %q, err %v", got, g.calls, err)
	}
	after, err := os.ReadFile(filepath.Join(runDir, verdictFile))
	if err != nil || string(after) != string(before) {
		t.Fatalf("discarded verdict changed: %q, %v", after, err)
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
	svc := policySvc(t, dir, func() time.Time { return now }).
		WithActivity(func(_ context.Context, id string) (string, time.Time, bool) {
			st, ok := act[id]
			return st, since, ok
		})
	for _, sid := range []string{"idle", "gone", "slow", "worked", "busy"} {
		spawnTester(t, svc, sid)
	}
	is(t, "")(svc.Intake(ctx, "worked", prURL)) // ran intake and planned a check
	svc.attach("run_worked", &actionRec{Seq: 1, Leg: "head", Action: "click"})
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
	svc := policySvc(t, t.TempDir(), func() time.Time { return now })
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
func TestDeadlineCloseLeavesARunSubmittedAfterListingAlone(t *testing.T) {
	svc, runDir, g := askSvc(t, false)
	runs, err := svc.store.List()
	is(t, "")(nil, err)
	closed := 0
	svc.closeEngine = func(string) { closed++ }
	is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict())))
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

func TestSweepKeepsPinnedIntakeWithoutReceipts(t *testing.T) {
	for _, kind := range []string{"pr", "issue"} {
		t.Run(kind, func(t *testing.T) {
			svc, _ := newSvc(t)
			spawnTester(t, svc, "s1")
			run, err := svc.Intake(ctx, "s1", prURL)
			is(t, "")(nil, err)
			if kind == "issue" {
				run, err = svc.update(run.ID, func(r *Run) { r.Kind, r.TargetSHA = "issue", r.HeadSHA })
				is(t, "")(nil, err)
			}
			now := run.CreatedAt.Add(SpawnBudget + time.Second)
			svc.clock = func() time.Time { return now }
			svc.WithActivity(func(context.Context, string) (string, time.Time, bool) { return "exited", now, true })
			input, err := json.Marshal(Verdict{Result: "blocked", BlockedOn: "No account for the required setup."})
			is(t, "")(nil, err)
			writeFile(t, filepath.Join(svc.store.root, run.ID, "scratch", "verdict.json"), string(input))
			for _, at := range []time.Time{now, run.Deadline, run.Deadline.Add(writeupGrace - time.Nanosecond)} {
				now = at
				if killed := sweep(svc, nil); len(killed) != 0 {
					t.Fatalf("pinned intake killed during grace: %v", killed)
				}
				saved, err := svc.store.Get(run.ID)
				if err != nil || saved.State != RunIntake || saved.Error == ReasonNoIntake {
					t.Fatalf("lost completed intake: %+v, %v", saved, err)
				}
			}
			now = run.Deadline.Add(writeupGrace)
			if killed := sweep(svc, nil); len(killed) != 0 {
				t.Fatalf("salvage killed tester: %v", killed)
			}
			var v Verdict
			is(t, "")(nil, svc.store.read(run.ID, verdictFile, &v))
			saved, err := svc.store.Get(run.ID)
			if err != nil || saved.State != RunSubmitted || v.Result != "blocked" || v.ReceiptCount != 0 {
				t.Fatalf("blocked verdict not salvaged: %+v, %+v, %v", saved, v, err)
			}
		})
	}
}

func TestSweepInvalidExitedVerdictRetriesCleanupOnly(t *testing.T) {
	for _, failure := range []string{"kill", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			svc, _, dir, _ := submitSvc(t)
			writeFile(t, filepath.Join(dir, "scratch", "verdict.json"), "{")
			run, err := svc.store.Get("run_s1")
			is(t, "")(nil, err)
			svc.clock = func() time.Time { return run.Deadline.Add(writeupGrace) }
			svc.WithActivity(func(context.Context, string) (string, time.Time, bool) { return "exited", time.Time{}, true })
			var killErr error
			owned := filepath.Join(dir, "head", "owned.json")
			if failure == "kill" {
				killErr = errors.New("kill unavailable")
			} else {
				writeFile(t, owned, "{")
			}
			sweep(svc, killErr)
			saved, err := svc.store.Get(run.ID)
			if svc.reaped(run.ID) {
				t.Fatal("failed cleanup was marked complete")
			}
			if failure == "cleanup" {
				is(t, "")(nil, os.Remove(owned))
			}
			if err != nil || !strings.Contains(saved.Error, "no valid scratch/verdict.json") {
				t.Fatalf("missing decision: %+v, %v", saved, err)
			}
			svc.activity = nil
			sweep(svc, nil)
			for _, name := range []string{verdictFile, "verdict.md", "comment.md"} {
				if fileExists(filepath.Join(dir, name)) {
					t.Errorf("cleanup retry created %s", name)
				}
			}
			if !svc.reaped(run.ID) {
				t.Fatal("cleanup retry did not finish")
			}
		})
	}
}

func TestSweepDiscardsARunThatWaitedAWeekForApproval(t *testing.T) {
	svc, runDir, g := askSvc(t, true)
	_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict()))
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

func policySvc(t *testing.T, dir string, clock func() time.Time) *Service {
	t.Helper()
	svc := New(NewStore(dir), clock).WithGH(fixtureGH(t))
	svc.runner, svc.retryPause, svc.spawnMarkerWait = gitFixture(t), 0, time.Millisecond
	return svc
}
