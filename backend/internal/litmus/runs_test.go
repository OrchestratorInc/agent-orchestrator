package litmus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// askSvc is submitSvc for a project that asks before posting: the run belongs to project p1, and p1 asks.
func askSvc(t *testing.T, ask bool) (*Service, string, *commentGH) {
	t.Helper()
	svc, _, runDir, _ := submitSvc(t)
	g := &commentGH{t: t, login: "litmus-bot"}
	svc.gh = g
	_, err := svc.update("run_s1", func(r *Run) { r.ProjectID, r.Comment = "p1", CommentPost })
	is(t, "")(nil, err)
	_, err = svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: ask})
	is(t, "")(nil, err)
	return svc, runDir, g
}

func TestAskBeforePostingStopsAtAwaitingApprovalThenPosts(t *testing.T) {
	svc, runDir, g := askSvc(t, true)
	v, err := svc.Submit(ctx, "s1", "", "")
	run, _ := svc.store.Get("run_s1")
	if err != nil || v.Overall != CheckPass || v.CommentState != CommentAwaiting || v.CommentURL != "" {
		t.Fatalf("verdict %+v err %v", v, err)
	}
	if run.State != RunAwaitingApproval || run.CommentState != CommentAwaiting || run.CommentURL != "" || len(g.calls) != 0 {
		t.Fatalf("run %+v gh calls %q: nothing may be published or posted yet", run, g.calls)
	}
	body, _ := os.ReadFile(filepath.Join(runDir, "comment.md"))
	exists(t, filepath.Join(runDir, "verdict.json"))
	if !strings.HasPrefix(string(body), "Litmus tried #6394 on the PR build and on main (") {
		t.Fatalf("comment.md = %s", body)
	}

	// The run is closed to the tester and the 25 minute budget does not touch it.
	is(t, "TESTING_RUN_CLOSED")(svc.Status(ctx, "s1"))
	svc.clock = func() time.Time { return run.Deadline.Add(time.Hour) }
	if killed := sweep(svc, nil); len(killed) != 0 {
		t.Fatalf("the sweep stopped %v", killed)
	}
	if got, _ := svc.store.Get("run_s1"); got.State != RunAwaitingApproval {
		t.Fatalf("state after the sweep = %s", got.State)
	}
	// The retention pass keeps its folder even at keep-runs 0.
	if _, err := svc.Prune(ctx, 0, 0); err != nil || !pathExists(runDir) {
		t.Fatalf("prune removed the awaiting run: %v", err)
	}

	posted, err := svc.Post(ctx, "run_s1")
	if err != nil || posted.State != RunSubmitted || posted.CommentState != "posted" || posted.CommentURL == "" {
		t.Fatalf("post: %+v err %v", posted, err)
	}
	if !g.has("api --method POST repos/"+run.Repo+"/issues/6394/comments ") || g.field(0, "body") != string(body) {
		t.Fatalf("the posted comment is not comment.md: calls %q", g.calls)
	}
	is(t, "TESTING_NOT_AWAITING")(svc.Post(ctx, "run_s1")) // a second post is refused
	is(t, "TESTING_NOT_AWAITING")(svc.Discard(ctx, "run_s1"))
	if n := strings.Count(strings.Join(g.calls, "\n"), "--method POST"); n != 1 {
		t.Fatalf("the comment was posted %d times", n)
	}
}

func TestDiscardCleansUpWithoutPosting(t *testing.T) {
	svc, runDir, g := askSvc(t, true)
	_, err := svc.Submit(ctx, "s1", "", "")
	is(t, "")(nil, err)
	run, err := svc.Discard(ctx, "run_s1")
	if err != nil || run.State != RunCleaned || run.CommentState != CommentDiscarded || len(g.calls) != 0 {
		t.Fatalf("discard: %+v err %v calls %q", run, err, g.calls)
	}
	exists(t, filepath.Join(runDir, "comment.md")) // the unposted comment stays for the record
	is(t, "TESTING_NOT_AWAITING")(svc.Post(ctx, "run_s1"))
	is(t, "TESTING_NOT_AWAITING")(svc.Discard(ctx, "run_s1"))
	is(t, "TESTING_RUN_NOT_FOUND")(svc.Post(ctx, "run_nope"))
}

func TestFailedPostLeavesTheRunAwaitingForAnotherTry(t *testing.T) {
	svc, _, g := askSvc(t, true)
	_, err := svc.Submit(ctx, "s1", "", "")
	is(t, "")(nil, err)
	g.fail = "api user"
	run, err := svc.Post(ctx, "run_s1")
	if err != nil || run.State != RunAwaitingApproval || !strings.HasPrefix(run.CommentState, "failed:") {
		t.Fatalf("failed post: %+v err %v", run, err)
	}
	g.fail = ""
	if run, err = svc.Post(ctx, "run_s1"); err != nil || run.State != RunSubmitted || run.CommentState != "posted" {
		t.Fatalf("retry: %+v err %v", run, err)
	}
}

func TestAskBeforePostingOnlyAppliesToAComment(t *testing.T) {
	for name, c := range map[string]struct {
		ask     bool
		mode    string
		blocked bool
		want    RunState
		state   string
	}{
		"flag off":     {false, CommentPost, false, RunSubmitted, "posted"},
		"no comment":   {true, CommentOff, false, RunSubmitted, "off"},
		"blocked":      {true, CommentPost, true, RunSubmitted, "off"},
		"flag on":      {true, CommentPost, false, RunAwaitingApproval, CommentAwaiting},
		"other flag":   {false, CommentPost, true, RunSubmitted, "off"},
		"older run":    {true, "", false, RunSubmitted, "off"},
		"flag on blkd": {true, CommentOff, true, RunSubmitted, "off"},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _, _ := askSvc(t, c.ask)
			_, err := svc.update("run_s1", func(r *Run) { r.Comment = c.mode })
			is(t, "")(nil, err)
			if c.blocked {
				_, err = svc.SubmitBlocked(ctx, "s1", "prepare refused")
			} else {
				_, err = svc.Submit(ctx, "s1", "", "")
			}
			run, _ := svc.store.Get("run_s1")
			if err != nil || run.State != c.want || run.CommentState != c.state {
				t.Fatalf("run %s %q err %v, want %s %q", run.State, run.CommentState, err, c.want, c.state)
			}
		})
	}
	// A run that recorded no project never asks, whatever any project says.
	svc, _, _ := askSvc(t, true)
	_, err := svc.update("run_s1", func(r *Run) { r.ProjectID = "" })
	is(t, "")(nil, err)
	_, err = svc.Submit(ctx, "s1", "", "")
	if run, _ := svc.store.Get("run_s1"); err != nil || run.State != RunSubmitted {
		t.Fatalf("run %+v err %v", run, err)
	}
}

func TestProjectSettingsRoundTrip(t *testing.T) {
	svc, _ := newSvc(t)
	if got, err := svc.ProjectSettings(ctx, "p1"); err != nil || got.AskBeforePosting {
		t.Fatalf("default = %+v err %v", got, err)
	}
	is(t, "")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{AskBeforePosting: true}))
	is(t, "")(svc.SetProjectSettings(ctx, "p2", ProjectSettings{}))
	is(t, "")(svc.SetProjectSettings(ctx, "p3", ProjectSettings{Model: "opus", Effort: "max"}))
	if got, _ := svc.ProjectSettings(ctx, "p1"); !got.AskBeforePosting || got.Model != "" || got.Effort != "" {
		t.Fatalf("p1 = %+v", got)
	}
	if got, _ := svc.ProjectSettings(ctx, "p2"); got.AskBeforePosting {
		t.Fatalf("p2 = %+v", got)
	}
	if got, _ := svc.ProjectSettings(ctx, "p3"); got != (ProjectSettings{Model: "opus", Effort: "max"}) {
		t.Fatalf("p3 = %+v", got)
	}
	dataDir := svc.store.dataDir() // the session manager reads the same file without a Service
	if got, err := LoadProjectSettings(dataDir, "p3"); err != nil || got.Model != "opus" || got.Effort != "max" {
		t.Fatalf("LoadProjectSettings = %+v err %v", got, err)
	}
	is(t, "")(svc.SetProjectSettings(ctx, "p3", ProjectSettings{})) // empty clears
	if got, _ := svc.ProjectSettings(ctx, "p3"); got != (ProjectSettings{}) {
		t.Fatalf("p3 after clearing = %+v", got)
	}
	for _, bad := range []string{"", "..", "a/b", " "} {
		is(t, "TESTING_PROJECT_INVALID")(svc.ProjectSettings(ctx, bad))
		is(t, "TESTING_PROJECT_INVALID")(svc.SetProjectSettings(ctx, bad, ProjectSettings{}))
	}
}

func TestStartRecordsTheProjectOnTheRun(t *testing.T) {
	svc, _ := newSvc(t)
	svc.WithSpawner(func(context.Context, SpawnRequest) (string, error) { return "s7", nil })
	started, err := svc.Start(ctx, StartRequest{ProjectID: "p1", PRURL: prURL})
	is(t, "")(nil, err)
	run, err := svc.Intake(ctx, started.SessionID, prURL)
	if err != nil || run.ProjectID != "p1" {
		t.Fatalf("run %+v err %v", run, err)
	}
}

func TestRerunStartsTheSamePRInTheSameProject(t *testing.T) {
	svc, _ := newSvc(t)
	var got []SpawnRequest
	ids := []string{"s2", "s3"}
	svc.WithSpawner(func(_ context.Context, req SpawnRequest) (string, error) {
		got = append(got, req)
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	_, err := svc.update("run_s1", func(r *Run) { r.ProjectID, r.Comment, r.Evidence = "p1", CommentOff, EvidencePost })
	is(t, "")(nil, err)

	run, _ := svc.store.Get("run_s1")
	re, err := svc.Rerun(ctx, "run_s1", "")
	if err != nil || re.SessionID != "s2" || re.RunID != "run_s2" || len(got) != 1 || got[0].ProjectID != "p1" || !strings.Contains(got[0].Prompt, "https://github.com/"+run.Repo+"/pull/6394") {
		t.Fatalf("rerun %+v err %v spawn %+v", re, err, got)
	}
	again, err := svc.Intake(ctx, re.SessionID, prURL)
	if err != nil || again.ProjectID != "p1" || again.Comment != CommentOff || again.Evidence != EvidencePost {
		t.Fatalf("the rerun's run %+v err %v: it keeps the project and the modes", again, err)
	}

	is(t, "TESTING_RUN_NOT_FOUND")(svc.Rerun(ctx, "run_none", ""))
	_, err = svc.update("run_s1", func(r *Run) { r.ProjectID = "" })
	is(t, "")(nil, err)
	is(t, "TESTING_PROJECT_REQUIRED")(svc.Rerun(ctx, "run_s1", ""))
	if _, err = svc.Rerun(ctx, "run_s1", "p9"); err != nil || got[1].ProjectID != "p9" {
		t.Fatalf("explicit project: %+v err %v", got, err)
	}
}

func TestRunsListAndDetail(t *testing.T) {
	svc, runDir, _ := askSvc(t, false)

	_, err := svc.Submit(ctx, "s1", "", "")
	is(t, "")(nil, err)
	end := svc.clock().Add(7 * time.Minute)
	is(t, "")(nil, os.Chtimes(filepath.Join(runDir, "verdict.md"), end, end))

	// A newer run of another project, one that never ran, and one with no project at all.
	other := Run{ID: "run_s2", SessionID: "s2", ProjectID: "p2", Repo: "o/r", PRNumber: 9, State: RunRunning, CreatedAt: svc.clock().Add(time.Hour), Deadline: svc.clock().Add(2 * time.Hour)}
	old := Run{ID: "run_s0", SessionID: "s0", Repo: "o/r", PRNumber: 3, State: RunExpired, CreatedAt: svc.clock().Add(-time.Hour), Error: ReasonNoIntake}
	is(t, "")(nil, svc.store.Save(other))
	is(t, "")(nil, svc.store.Save(old))
	expiredAt := old.CreatedAt.Add(90 * time.Second) // an expired run ends when run.json last changed
	is(t, "")(nil, os.Chtimes(filepath.Join(svc.store.root, "run_s0", "run.json"), expiredAt, expiredAt))

	list, err := svc.Runs(ctx, "p1", 0)
	if err != nil || len(list.Runs) != 1 {
		t.Fatalf("p1 list = %+v err %v", list, err)
	}
	row := list.Runs[0]
	if row.ID != "run_s1" || row.State != RunSubmitted || row.Verdict != "pass" || row.DurationSeconds != 420 ||
		row.PR.Number != 6394 || row.PR.URL != "https://github.com/"+row.Repo+"/pull/6394" || row.PR.Title == "" ||
		row.ProjectID != "p1" || row.CommentState != "posted" || row.CommentURL == "" {
		t.Fatalf("row = %+v", row)
	}
	all, _ := svc.Runs(ctx, "", 0)
	if len(all.Runs) != 3 || all.Runs[0].ID != "run_s2" || all.Runs[0].Verdict != "none" || all.Runs[0].DurationSeconds != 0 {
		t.Fatalf("all = %+v", all.Runs)
	}
	if exp := all.Runs[2]; exp.ID != "run_s0" || exp.Verdict != "expired" || exp.Error != ReasonNoIntake || exp.DurationSeconds != 90 {
		t.Fatalf("expired row = %+v", exp)
	}
	if one, _ := svc.Runs(ctx, "", 1); len(one.Runs) != 1 || one.Runs[0].ID != "run_s2" {
		t.Fatalf("limit 1 = %+v", one.Runs)
	}
	if none, err := svc.Runs(ctx, "p3", 0); err != nil || none.Runs == nil || len(none.Runs) != 0 {
		t.Fatalf("p3 sees no run of another project, nor one without a project: %+v err %v", none, err)
	}

	d, err := svc.RunDetail(ctx, "run_s1")
	if err != nil || d.ID != "run_s1" || d.Run.PRNumber != 6394 || len(d.Checks) != 3 || !strings.HasPrefix(d.VerdictMD, "# Verdict: pass") || !strings.HasPrefix(d.CommentMD, "Litmus tried") {
		t.Fatalf("detail = %+v err %v", d, err)
	}
	raw, _ := json.Marshal(d)
	for _, key := range []string{`"verdictMd"`, `"commentMd"`, `"checks"`, `"durationSeconds":420`, `"run":{`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("detail JSON lacks %s: %.300s", key, raw)
		}
	}
	is(t, "TESTING_RUN_NOT_FOUND")(svc.RunDetail(ctx, "run_nope"))
	is(t, "TESTING_ID_INVALID")(svc.RunDetail(ctx, ".."))
	if no, err := svc.RunDetail(ctx, "run_s2"); err != nil || no.VerdictMD != "" || no.CommentMD != "" || no.Checks == nil {
		t.Fatalf("a run with no files: %+v err %v", no, err)
	}
}

func TestPruneIgnoresARunThatIsStarting(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	now := time.Now()
	svc.clock = func() time.Time { return now }
	// run_ao-25's tester was spawned a minute ago and has not run intake: its dir has no run.json yet.
	is(t, "")(nil, svc.store.markSpawned("ao-25", spawned{At: now.Add(-time.Minute), Repo: "o/r", PR: 7}))
	writeFile(t, filepath.Join(svc.store.root, "run_ao-25", "claude-settings.json"), "{}")

	rep, err := svc.Prune(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rep.Lines, "\n"), "skipped") || len(rep.Checkouts) != 1 {
		t.Fatalf("a starting run blocked the checkout prune: %+v", rep)
	}
	if !pathExists(filepath.Join(svc.store.root, "run_ao-25")) || !pathExists(filepath.Join(root, "repos", "aoagents__agent-orchestrator", strings.Repeat("3", 40))) {
		t.Fatalf("the starting run dir or an unfinished run's checkout was removed: %+v", rep)
	}

	// Past SpawnBudget with still no run.json it is an unreadable run dir again.
	svc.clock = func() time.Time { return now.Add(SpawnBudget + time.Second) }
	rep, err = svc.Prune(ctx, 1, 1)
	if err != nil || !strings.Contains(strings.Join(rep.Lines, "\n"), "skipped checkouts: 1 run dirs have no readable run.json (run_ao-25)") {
		t.Fatalf("an old spawn marker must not excuse a missing run.json: %+v err %v", rep, err)
	}
	// A run dir with no marker at all is unreadable too.
	svc.store.clearSpawned("ao-25")
	svc.clock = func() time.Time { return now }
	rep, err = svc.Prune(ctx, 1, 1)
	if err != nil || !strings.Contains(strings.Join(rep.Lines, "\n"), "(run_ao-25)") {
		t.Fatalf("no marker: %+v err %v", rep, err)
	}
}

func TestRunsListsOnlyTheProjectsOwnRuns(t *testing.T) {
	svc, _ := newSvc(t)
	svc.WithProjectRepo(func(_ context.Context, id string) string {
		return map[string]string{"p1": "acme/one", "p2": "acme/two"}[id]
	})
	at := svc.clock()
	save := func(id, project string, n int) {
		is(t, "")(nil, svc.store.Save(Run{ID: "run_" + id, SessionID: id, ProjectID: project, Repo: "acme/" + id, PRNumber: n, State: RunSubmitted, CreatedAt: at.Add(time.Duration(n) * time.Minute)}))
	}
	save("a", "p1", 1)
	save("b", "p2", 2)
	save("marked", "", 3) // started before runs recorded their project, but its session was marked
	save("lost", "", 4)   // no project anywhere
	is(t, "")(nil, svc.store.setSessionProject("marked", "p2"))

	ids := func(project string) (out []string) {
		list, err := svc.Runs(ctx, project, 0)
		is(t, "")(nil, err)
		for _, r := range list.Runs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids("p1"); !slices.Equal(got, []string{"run_a"}) {
		t.Fatalf("p1 = %v", got)
	}
	if got := ids("p2"); !slices.Equal(got, []string{"run_marked", "run_b"}) {
		t.Fatalf("p2 = %v", got)
	}
	if got := ids("p3"); len(got) != 0 {
		t.Fatalf("p3 = %v", got)
	}
	if run, _ := svc.store.Get("run_marked"); run.ProjectID != "p2" {
		t.Fatalf("the marker was not written into run.json: %+v", run)
	}
	if run, _ := svc.store.Get("run_lost"); run.ProjectID != "" {
		t.Fatalf("a run with no marker gained a project: %+v", run)
	}
	if list, _ := svc.Runs(ctx, "p1", 0); list.Repo != "acme/one" {
		t.Fatalf("repo = %q: the list names the project's own repo, not a run's", list.Repo)
	}
	if list, _ := svc.Runs(ctx, "", 0); list.Repo != "" || len(list.Runs) != 4 {
		t.Fatalf("an unscoped list has no repo and every run: %+v", list)
	}
}

func TestRunTitleComesFromRunJSON(t *testing.T) {
	svc, runDir, _ := askSvc(t, false)
	run, _ := svc.store.Get("run_s1")
	if run.Title == "" {
		t.Fatalf("intake did not store the title: %+v", run)
	}
	is(t, "")(nil, os.WriteFile(filepath.Join(runDir, "context-pack.md"), []byte("- #6394 changed in the pack\n"), 0o600))
	if row := svc.summarize(run); row.PR.Title != run.Title {
		t.Fatalf("title = %q, want the stored %q (not a re-read of the pack)", row.PR.Title, run.Title)
	}
	// A run from before titles were stored takes it from the pack once, and keeps it.
	_, err := svc.update(run.ID, func(r *Run) { r.Title, r.State = "", RunSubmitted })
	is(t, "")(nil, err)
	old, _ := svc.store.Get(run.ID)
	if row := svc.summarize(old); row.PR.Title != "changed in the pack" {
		t.Fatalf("legacy title = %q", row.PR.Title)
	}
	is(t, "")(nil, os.Remove(filepath.Join(runDir, "context-pack.md")))
	again, _ := svc.store.Get(run.ID)
	if row := svc.summarize(again); row.PR.Title != "changed in the pack" || again.Title != "changed in the pack" {
		t.Fatalf("the backfilled title was lost: %q", row.PR.Title)
	}

}

func TestDiscardThatCannotCleanUpDoesNotMarkTheCommentDiscarded(t *testing.T) {
	svc, runDir, g := askSvc(t, true)
	_, err := svc.Submit(ctx, "s1", "", "")
	is(t, "")(nil, err)
	is(t, "")(nil, os.MkdirAll(filepath.Join(runDir, "head"), 0o750))
	is(t, "")(nil, os.WriteFile(filepath.Join(runDir, "head", "owned.json"), []byte("{broken"), 0o600)) // teardown cannot read it
	run, err := svc.Discard(ctx, "run_s1")
	if err == nil {
		t.Fatalf("discard succeeded: %+v", run)
	}
	got, _ := svc.store.Get("run_s1")
	if got.State != RunAwaitingApproval || got.CommentState != CommentAwaiting {
		t.Fatalf("after a failed discard: %s %q", got.State, got.CommentState)
	}
	// The comment can still be posted once the leftovers are gone.
	is(t, "")(nil, os.Remove(filepath.Join(runDir, "head", "owned.json")))
	if posted, err := svc.Post(ctx, "run_s1"); err != nil || posted.CommentState != "posted" || !g.has("api --method POST") {
		t.Fatalf("post after a failed discard: %+v err %v", posted, err)
	}
}

func TestADiscardedCommentIsNeverPosted(t *testing.T) {
	svc, _, g := askSvc(t, true)
	_, err := svc.Submit(ctx, "s1", "", "")
	is(t, "")(nil, err)
	_, err = svc.update("run_s1", func(r *Run) { r.CommentState = CommentDiscarded }) // state still awaiting: a discard that stopped half way
	is(t, "")(nil, err)
	is(t, "TESTING_DISCARDED")(svc.Post(ctx, "run_s1"))
	is(t, "TESTING_DISCARDED")(svc.Discard(ctx, "run_s1"))
	if len(g.calls) != 0 {
		t.Fatalf("gh was called: %q", g.calls)
	}
}

func TestRerunPostsOnlyWhenTheRunPosted(t *testing.T) {
	for mode, wantNoComment := range map[string]bool{CommentPost: false, CommentOff: true, "": true} {
		svc, _ := newSvc(t)
		svc.WithSpawner(func(_ context.Context, req SpawnRequest) (string, error) { return "s2", nil })
		is(t, "")(svc.Intake(ctx, "s1", prURL))
		_, err := svc.update("run_s1", func(r *Run) { r.ProjectID, r.Comment = "p1", mode })
		is(t, "")(nil, err)
		if _, err := svc.Rerun(ctx, "run_s1", ""); err != nil {
			t.Fatal(err)
		}
		if got := svc.store.commentMode("s2") == CommentOff; got != wantNoComment {
			t.Errorf("comment mode %q: the rerun's no-comment = %v, want %v", mode, got, wantNoComment)
		}
	}
}

func TestPostSurvivesAHungUpClient(t *testing.T) {
	svc, _, g := askSvc(t, true)
	_, err := svc.Submit(ctx, "s1", "", "")
	is(t, "")(nil, err)
	gone, cancel := context.WithCancel(ctx)
	cancel()
	posted, err := svc.Post(gone, "run_s1")
	if err != nil || posted.State != RunSubmitted || posted.CommentState != "posted" || !g.has("api --method POST") {
		t.Fatalf("post with a cancelled request: %+v err %v", posted, err)
	}
}
