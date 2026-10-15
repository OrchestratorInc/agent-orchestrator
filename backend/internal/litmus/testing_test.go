package litmus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const prURL = "https://github.com/aoagents/agent-orchestrator/pull/6394"

var ctx = context.Background()

// newSvc is a service over a temp data dir whose gh and git answer from the recorded PR 6394, so no network is reached.
func newSvc(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc := New(NewStore(t.TempDir()), func() time.Time { return now }).WithGH(fixtureGH(t))
	svc.runner, svc.retryPause = gitFixture(t), 0
	svc.spawnMarkerWait = time.Millisecond
	is(t, "")(nil, svc.store.markSpawned("s1", spawned{At: now, Repo: "aoagents/agent-orchestrator", PR: 6394}))
	return svc, &now
}

// evidenceFile gives a receipt a saved file, as the drive and test verbs do.
func evidenceFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "evidence")
	if err := os.WriteFile(p, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// is returns a checker for a call's outcome: is(t, "CODE")(svc.Verb(...)) wants
// that apierr code, is(t, "")(...) wants success.
func is(t *testing.T, want string) func(any, error) {
	return func(_ any, err error) {
		t.Helper()
		var e *apierr.Error
		got := ""
		if errors.As(err, &e) {
			got = e.Code
		} else if err != nil {
			got = err.Error()
		}
		if got != want {
			t.Fatalf("err = %v, want code %q", err, want)
		}
	}
}

func TestTestingStoreRoundTripIsAtomic(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	run := Run{ID: "run_abc", SessionID: "s1", State: RunIntake, Deadline: time.Unix(910, 0).UTC()}
	for _, state := range []RunState{RunIntake, RunPrepared} {
		run.State = state
		is(t, "")(nil, st.Save(run))
	}
	if got, err := st.Get(run.ID); err != nil || got.State != RunPrepared || !got.Deadline.Equal(run.Deadline) {
		t.Fatalf("got %+v err %v", got, err)
	}
	is(t, "")(nil, st.SaveChecks(run.ID, []Check{{ID: "c1", Receipts: []Receipt{{Seq: 1}}}}))
	if checks, err := st.Checks(run.ID); err != nil || checks[0].Receipts[0].Seq != 1 {
		t.Fatalf("checks %+v err %v", checks, err)
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, "litmus", "runs", run.ID, ".tmp-*")); len(tmp) != 0 { // a write leaves no temp file behind
		t.Fatalf("temp files left: %v", tmp)
	}
	if runs, err := st.List(); err != nil || len(runs) != 1 {
		t.Fatalf("list %+v err %v", runs, err)
	}
	if _, err := st.Get("run_none"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing run err = %v", err)
	}
	if st.Save(Run{ID: "../escape"}) == nil || st.WriteFile(run.ID, "../x", nil) == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestTestingSubmitRequiresReceipts(t *testing.T) {
	svc, now := newSvc(t)
	for _, bad := range []string{"", "https://github.com/o/r/issues/1", "https://gitlab.com/g/p/-/merge_requests/3"} {
		is(t, "TESTING_PR_URL_INVALID")(svc.Intake(ctx, "s1", bad))
	}
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || run.State != RunIntake || run.PRNumber != 6394 || !run.Deadline.Equal(now.Add(25*time.Minute)) {
		t.Fatalf("run %+v err %v", run, err)
	}
	is(t, "TESTING_RUN_EXISTS")(svc.Intake(ctx, "s1", prURL))
	is(t, "TESTING_NO_CHECKS")(svc.Submit(ctx, "s1", "", ""))
	is(t, "")(svc.CheckAdd(ctx, "s1", "menu opens", "list shown", ""))
	is(t, "")(svc.CheckAdd(ctx, "s1", "last model reachable", "scrolls", ""))
	is(t, "")(svc.CheckAdd(ctx, "s1", "never started", "x", ""))
	addReceipt := func(i int, leg string) { // receipts normally arrive from the drive stage
		checks, _ := svc.store.Checks(run.ID)
		checks[i].Receipts = []Receipt{{Seq: i + 1, Action: "click", Leg: leg, AfterPath: evidenceFile(t)}}
		is(t, "")(nil, svc.store.SaveChecks(run.ID, checks))
	}
	_, err = svc.Submit(ctx, "s1", "", "")
	if !strings.Contains(err.Error(), "c1 menu opens; c2 last model reachable; c3 never started") {
		t.Fatalf("err = %v", err)
	}
	for i, result := range []CheckState{CheckPass, CheckPartial} {
		id := fmt.Sprintf("c%d", i+1)
		is(t, "")(svc.CheckStart(ctx, "s1", id))
		is(t, "TESTING_CHECK_NO_RECEIPT")(svc.CheckEnd(ctx, "s1", id, result, "clipped", false, 0))
		addReceipt(i, "base") // a receipt on the other leg does not count
		is(t, "TESTING_CHECK_NO_RECEIPT")(svc.CheckEnd(ctx, "s1", id, result, "clipped", false, 0))
		addReceipt(i, "")
		is(t, "")(svc.CheckEnd(ctx, "s1", id, result, "clipped", false, 0))
	}
	is(t, "TESTING_CHECKS_INCOMPLETE")(svc.Submit(ctx, "s1", "", ""))
	addReceipt(2, "head") // a receipt does not substitute for starting and ending a check
	is(t, "TESTING_CHECKS_INCOMPLETE")(svc.Submit(ctx, "s1", "", ""))
	is(t, "")(svc.CheckStart(ctx, "s1", "c3"))
	is(t, "")(svc.CheckEnd(ctx, "s1", "c3", CheckPass, "", false, 0))
	var reported []string
	svc.WithReporter(func(_ context.Context, id, note string) error { reported = append(reported, id+": "+note); return nil })
	v, err := svc.Submit(ctx, "s1", "head only", "")
	if len(reported) != 1 || !strings.HasPrefix(reported[0], "s1: Litmus verdict partial for ") {
		t.Fatalf("done report %q", reported)
	}
	if err != nil || v.Overall != CheckPartial || len(v.Reasons) != 1 || v.Summary != "head only" { // c2's note; c1 and c3 passed
		t.Fatalf("verdict %+v err %v", v, err)
	}
	if md, err := os.ReadFile(filepath.Join(svc.store.root, run.ID, "verdict.md")); err != nil || !strings.HasPrefix(string(md), "# Verdict: partial") {
		t.Fatalf("verdict.md %q err %v", md, err)
	}
	is(t, "TESTING_RUN_CLOSED")(svc.CheckAdd(ctx, "s1", "late", "x", ""))
}

func TestTestingVerbsAndExpiry(t *testing.T) {
	svc, now := newSvc(t)
	is(t, "TESTING_RUN_NOT_FOUND")(svc.Prepare(ctx, "s1"))
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	is(t, "TESTING_REPO_UNRESOLVED")(svc.Prepare(ctx, "s1")) // no resolver wired
	is(t, "TESTING_LEG_INVALID")(svc.Launch(ctx, "s1", "main"))
	is(t, "TESTING_NOT_PREPARED")(svc.Launch(ctx, "s1", "head"))
	is(t, "TESTING_ACTION_UNSUPPORTED")(svc.Drive(ctx, "s1", "open", nil))
	is(t, "TESTING_RUN_NOT_RUNNING")(svc.Drive(ctx, "s1", "click", []string{"e1"})) // intake only: nothing is launched
	is(t, "TESTING_RECIPE_UNREADABLE")(svc.RecipeValidate(ctx, "recipe.yaml"))

	*now = now.Add(RunBudget + time.Second) // past the deadline every verb refuses and the run flips to expired
	is(t, "TESTING_RUN_CLOSED")(svc.Status(ctx, "s1"))
	is(t, "")(svc.Cleanup(ctx, "s1")) // cleanup still works on a closed run, and keeps its state
	if runs, _ := svc.store.List(); len(runs) != 1 || runs[0].State != RunExpired {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestCheckAddRefusesDuplicateNamesOnTheSameLeg(t *testing.T) {
	svc, _ := newSvc(t)
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	is(t, "")(svc.CheckAdd(ctx, "s1", " menu opens ", "list shown", ""))
	is(t, "TESTING_CHECK_DUPLICATE")(svc.CheckAdd(ctx, "s1", "MENU opens", "list shown", "head"))
	is(t, "")(svc.CheckAdd(ctx, "s1", "menu opens", "list shown", "base"))
	is(t, "TESTING_CHECK_DUPLICATE")(svc.CheckAdd(ctx, "s1", "menu opens ", "list shown", "base"))
	checks, err := svc.store.Checks("run_s1")
	if err != nil || len(checks) != 2 || checks[0].Name != "menu opens" || checks[1].ID != "c2" {
		t.Fatalf("checks %+v, %v", checks, err)
	}
}
