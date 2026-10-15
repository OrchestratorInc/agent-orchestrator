package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// fakeMedia stands in for ffmpeg: it records the arguments and creates the output file (the last argument).
type fakeMedia struct {
	calls [][]string
	err   error
}

func (f *fakeMedia) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, args)
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(args[len(args)-1], []byte("mp4"), 0o600)
}

func TestChecksPlanRules(t *testing.T) {
	svc, _ := newSvc(t)
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	run, _ := svc.store.Get("run_s1")
	run.State, run.Legs = RunPrepared, []Leg{{Name: "head", State: "prepared"}}
	is(t, "")(nil, svc.store.Save(run))
	refused := func() bool { // launch is refused for want of checks (anything else it fails on is beyond this test)
		_, err := svc.Launch(ctx, "s1", "head")
		var e *apierr.Error
		return errors.As(err, &e) && e.Code == "TESTING_CHECKS_REQUIRED"
	}
	c, err := svc.CheckAdd(ctx, "s1", "menu opens", "list shown", "base")
	if !refused() || err != nil || c.Leg != "base" || c.Name != "menu opens" {
		t.Fatalf("one check: refused %t, %+v, %v", refused(), c, err)
	}
	is(t, "TESTING_LEG_INVALID")(svc.CheckAdd(ctx, "s1", "menu opens", "list shown", "main"))
	is(t, "")(svc.CheckAdd(ctx, "s1", "menu opens", "list shown", ""))
	if refused() {
		t.Fatal("launch with two checks still refused")
	}
	for i := 2; i <= 20; i++ { // there is no cap: the run's budget is the only limit
		is(t, "")(svc.CheckAdd(ctx, "s1", fmt.Sprint("check ", i), "x", ""))
	}
}

func TestVerdictListsSeedGapsAsLimits(t *testing.T) {
	prGap := "The PR build started without its sample content (seed step 2 failed: no project)."
	mainGap := "The main build started without its sample content (seed step 2 failed: no project)."
	intakeGap := "I could not read the linked issue #1 (not found)."
	for _, intake := range []bool{false, true} {
		run := commentRun
		run.Gaps = []string{prGap, mainGap}
		if intake {
			run.Gaps = append(run.Gaps, intakeGap)
		}
		md := renderVerdict(run, commentVerdict(commentChecks(t)), false, nil, t.TempDir(), run.CreatedAt.Add(time.Minute), nil)
		before, limits, ok := strings.Cut(md, "## Limits\n")
		if !ok || strings.Contains(before, prGap) || strings.Contains(before, mainGap) || !strings.Contains(limits, "- "+prGap) || !strings.Contains(limits, "- "+mainGap) {
			t.Fatalf("seed gaps are not separate limits:\n%s", md)
		}
		if strings.Contains(before, "What intake could not get") != intake || strings.Contains(before, intakeGap) != intake {
			t.Fatalf("intake heading or gap is wrong:\n%s", md)
		}
	}
}

func TestDeriveOverall(t *testing.T) {
	head := func(id string, st CheckState) Check { return Check{ID: id, Name: id, State: st} }
	base := func(id string, st CheckState) Check {
		return Check{ID: "b" + id, Name: strings.ToUpper(id), State: st, Leg: "base", Receipts: []Receipt{{Leg: "base", Action: "click", AfterPath: evidenceFile(t)}}}
	} // same name as head(id), any case, with a screenshot receipt like every ended check
	same := func(c Check) Check { c.SameFault = true; return c }
	for i, c := range []struct {
		checks []Check
		want   CheckState
	}{
		{[]Check{head("x", CheckPass), base("x", CheckFail)}, CheckPass},                                     // the base failing is the bug being fixed
		{[]Check{head("x", CheckPass), base("x", CheckFail), head("y", CheckPass)}, CheckPass},               // y was never compared; the difference on x carries it
		{[]Check{head("x", CheckPass)}, CheckPartial},                                                        // no base result
		{[]Check{head("x", CheckPass), base("x", CheckPass)}, CheckPartial},                                  // base is the same: no difference shown
		{[]Check{head("x", CheckPass), base("x", CheckPartial)}, CheckPartial},                               // only a base fail counts
		{[]Check{{ID: "c1", Name: "x", State: CheckPass, Receipts: []Receipt{{Leg: "base"}}}}, CheckPartial}, // base observed, no outcome
		{[]Check{head("x", CheckPartial), base("x", CheckFail)}, CheckPartial},                               // a partial head never passes
		{[]Check{head("x", CheckPass), base("x", CheckFail), head("y", CheckPartial)}, CheckPartial},
		{[]Check{head("x", CheckFail), base("x", CheckPass), head("y", CheckPartial)}, CheckFail}, // any head fail wins
		{[]Check{head("x", CheckPass), base("x", CheckFail), head("y", CheckSkipped)}, CheckPass}, // could not reach: left out
		{[]Check{head("x", CheckSkipped), head("y", CheckPass), base("y", CheckFail)}, CheckPass},
		{[]Check{head("x", CheckPass), head("y", CheckSkipped)}, CheckPartial},                                               // nothing differs
		{[]Check{head("x", CheckSkipped)}, CheckPartial},                                                                     // nothing was reached
		{[]Check{base("x", CheckFail)}, CheckPartial},                                                                        // a base-only check never decides
		{[]Check{head("x", CheckPass), base("x", CheckSkipped)}, CheckPartial},                                               // the base could not be reached either
		{[]Check{head("x", CheckPartial), base("x", CheckPartial), head("y", CheckPass), base("y", CheckFail)}, CheckFail},   // matching partial labels do not prove the same fault
		{[]Check{head("x", CheckFail), base("x", CheckFail), head("y", CheckPass), base("y", CheckFail)}, CheckPartial},      // an unproven matching failure stays in the finding but does not block
		{[]Check{head("x", CheckFail), {ID: "bx", Name: "X", State: CheckFail, Leg: "base"}}, CheckFail},                     // main's failure has no evidence and cannot make the head failure partial
		{[]Check{head("x", CheckFail), base("x", CheckPass), head("y", CheckPass), base("y", CheckFail)}, CheckFail},         // a base pass makes it the PR's regression
		{[]Check{head("x", CheckPartial), base("x", CheckSkipped)}, CheckPartial},                                            // the base was not reached: not pre-existing
		{[]Check{head("x", CheckFail), base("x", CheckFail)}, CheckPartial},                                                  // an unproven matching failure does not block
		{[]Check{head("x", CheckPass), {ID: "bx", Name: "x", State: CheckFail, Leg: "base"}}, CheckPartial},                  // nor one with no receipt at all
		{[]Check{head("x", CheckFail), base("x", CheckSkipped), head("y", CheckPass), base("y", CheckFail)}, CheckFail},      // a head fail beside a base skip is not pre-existing
		{[]Check{head("x", CheckFail), base("x", CheckPartial), head("y", CheckPass), base("y", CheckFail)}, CheckFail},      // nor beside a base partial
		{[]Check{head("x", CheckPass), base("x", CheckAbsent)}, CheckPass},                                                   // a feature main lacks is a difference in the PR's favour
		{[]Check{head("x", CheckFail), base("x", CheckAbsent)}, CheckFail},                                                   // a head fail beside an absent main is the PR's own
		{[]Check{head("x", CheckFail), base("x", CheckAbsent), head("y", CheckPass), base("y", CheckFail)}, CheckFail},       // it is never pre-existing, whatever else passes
		{[]Check{head("x", CheckPartial), base("x", CheckAbsent)}, CheckPartial},                                             // a head partial still counts against the PR
		{[]Check{head("x", CheckPartial), base("x", CheckAbsent), head("y", CheckPass), base("y", CheckFail)}, CheckPartial}, // and is not pre-existing
		{[]Check{head("x", CheckAbsent), base("x", CheckAbsent)}, CheckFail},                                                 // a promised feature absent on head failed
		{[]Check{head("x", CheckAbsent)}, CheckFail},                                                                         // a head absent alone is a failure
		{[]Check{head("x", CheckAbsent), head("y", CheckPass), base("y", CheckFail)}, CheckFail},                             // absence on the PR build counts as a failure
		{[]Check{head("x", CheckAbsent), base("x", CheckPass), head("y", CheckPass), base("y", CheckAbsent)}, CheckFail},     // a head absent beside a base pass is a failure
		{[]Check{head("x", CheckPass), base("x", CheckAbsent), head("y", CheckPass)}, CheckPass},                             // y was never compared; the difference on x carries it
		{[]Check{head("x", CheckFail), same(base("x", CheckFail)), head("y", CheckPass), base("y", CheckFail)}, CheckPass},
		{[]Check{head("x", CheckPartial), same(base("x", CheckPartial)), head("y", CheckPass), base("y", CheckFail)}, CheckPass},
		{[]Check{head("x", CheckFail), same(base("x", CheckFail))}, CheckPartial},
		{[]Check{head("x", CheckPass), {Name: "x", State: CheckFail, Leg: "base", Receipts: []Receipt{{Action: "click", Leg: "base"}}}}, CheckPartial},
		{[]Check{base("x", CheckAbsent)}, CheckPartial}, // a base-only check never decides
	} {
		if got, _ := derive(c.checks); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
	for i, c := range []struct { // the reason says why a headline stayed partial
		checks []Check
		want   string
	}{
		{[]Check{head("x", CheckSkipped), head("y", CheckSkipped)}, "no head check could be reached"},
		{[]Check{base("x", CheckFail)}, "no head check could be reached"},
		{[]Check{head("x", CheckPass), head("y", CheckSkipped)}, "no check passed on the head while failing on the base"},
		{[]Check{head("x", CheckFail), base("x", CheckFail)}, "x x: fails on main too; not shown to be the same fault"},
		{[]Check{head("x", CheckPartial), base("x", CheckPartial)}, "x x: is partial on main too; not shown to be the same fault"},
		{[]Check{head("x", CheckAbsent), base("x", CheckAbsent)}, "the PR build does not have this"},
		{[]Check{head("x", CheckPass), base("x", CheckPass), head("y", CheckAbsent)}, "the PR build does not have this"},
	} {
		if _, reasons := derive(c.checks); !slices.ContainsFunc(reasons, func(r string) bool { return strings.Contains(r, c.want) }) {
			t.Errorf("reason case %d: %q lacks %q", i, reasons, c.want)
		}
	}
	dir := t.TempDir()
	for title, want := range map[string]bool{"fix(chat): x": true, "Update stuff": true, "CI(x): y": false} {
		_ = os.WriteFile(filepath.Join(dir, "context-pack.md"), []byte("## 1. PR\n\n- #1 "+title+"\n"), 0o600)
		if claimsChange(dir) != want {
			t.Errorf("claimsChange(%q) = %t", title, !want)
		}
	}
}

// submitSvc is a run with evidence on disk: head check c1 with its base twin c2, and head check c3 with no base.
func submitSvc(t *testing.T) (*Service, *fakeMedia, string, string) {
	t.Helper()
	svc, now := newSvc(t)
	fm := &fakeMedia{}
	svc.media = fm
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	runDir := filepath.Join(svc.store.root, "run_s1")
	var actions []byte
	rcpt := func(seq int, leg string) Receipt {
		p := filepath.Join(runDir, "receipts", fmt.Sprintf("%d-after.png", seq))
		is(t, "")(nil, os.WriteFile(p, []byte("png"), 0o600))
		r := Receipt{Seq: seq, Leg: leg, Action: "click", Args: []string{"@e1"}, AfterPath: p, At: now.Add(time.Duration(seq*10) * time.Second)}
		line, _ := json.Marshal(actionRec{Seq: seq, Leg: leg, Action: "click", Args: r.Args, After: p, At: r.At, ConsoleTail: "ok\nUncaught TypeError: boom"})
		actions = append(actions, append(line, '\n')...)
		return r
	}
	is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{
		{ID: "c1", Name: "menu opens", Expect: "list | shown", State: CheckPass, Receipts: []Receipt{rcpt(1, "head")}},
		{ID: "c2", Name: "menu opens", Expect: "list shown", State: CheckFail, Leg: "base", Receipts: []Receipt{rcpt(2, "base")}},
		{ID: "c3", Name: "last model", Expect: "scrolls", State: CheckPass, Receipts: []Receipt{rcpt(3, "head")}},
	}))
	is(t, "")(nil, os.WriteFile(filepath.Join(runDir, "actions.jsonl"), actions, 0o600))
	is(t, "")(nil, os.MkdirAll(filepath.Join(runDir, "head", "logs"), 0o750))
	is(t, "")(nil, os.WriteFile(filepath.Join(runDir, "head", "logs", "dev.log"), []byte("boot\nlistening on :3200\n"), 0o600))
	_, err := svc.update("run_s1", func(r *Run) { r.Legs, r.Evidence = []Leg{{Name: "head", State: "running"}}, EvidenceOff }) // evidence has tests of its own
	is(t, "")(nil, err)
	return svc, fm, runDir, filepath.Join(svc.store.dataDir(), "artifacts", "s1")
}

// skipLastModelOnBase gives submitSvc's c3 (a head pass with no base result) the base result a running base needs.
func skipLastModelOnBase(t *testing.T, svc *Service) {
	t.Helper()
	checks, err := svc.store.Checks("run_s1")
	is(t, "")(nil, err)
	is(t, "")(nil, svc.store.SaveChecks("run_s1", append(checks, Check{ID: "c4", Name: "last model", Expect: "scrolls", State: CheckSkipped, Leg: "base", Note: "main has no such setting"})))
}

func exists(t *testing.T, p string) {
	t.Helper()
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		t.Errorf("%s missing or empty: %v", p, err)
	}
}

func TestSubmitWritesVerdictAndPublishesArtifacts(t *testing.T) {
	svc, fm, runDir, art := submitSvc(t)
	v, err := svc.Submit(ctx, "s1", "looks right on head", "")
	if err != nil || v.Overall != CheckPass { // c1 passes on head and fails on base; c3 has no base result and does not matter
		t.Fatalf("verdict %+v err %v", v, err)
	}
	if len(fm.calls) != 0 {
		t.Fatalf("video encoded without a recording: %v", fm.calls)
	}
	md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md"))
	for _, want := range []string{"# Verdict: pass", "| check | expect | head result | base result | receipts |", `| c1 menu opens | list \| shown | pass | fail | [1](receipts/1-after.png) |`,
		"| c3 last model | scrolls | pass | not compared on base |", "Uncaught TypeError: boom", "listening on :3200", "## Limits", "The base leg was not launched",
		"- c3 last model: not compared on base", "- The result is fail if any head check was absent or failed, except a head fail beside an unmarked base fail that ran and has evidence; that finding stays in the comment but leaves the result partial",
		"- The PR title claims a behavior change.", "Video: none (recording was not available)", "looks right on head"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("verdict.md lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(string(md), "| c2 ") {
		t.Errorf("the base twin got its own row:\n%s", md)
	}
	for _, d := range []string{runDir, art} { // the run dir and the session's artifact dir hold the same evidence
		for _, f := range []string{"verdict.md", "receipts/1-after.png", "receipts/3-after.png"} {
			exists(t, filepath.Join(d, f))
		}
	}
	index, _ := os.ReadFile(filepath.Join(art, "index.md"))
	for _, want := range []string{`<img src="receipts/1-after.png"`} {
		if !strings.Contains(string(index), want) {
			t.Fatalf("index.md lacks %q:\n%s", want, index)
		}
	}
	is(t, "TESTING_RUN_CLOSED")(svc.Submit(ctx, "s1", "", ""))
}

func TestSubmitCutsEachLegFromItsOwnRecordingAndSurvivesFFmpegFailure(t *testing.T) {
	svc, fm, runDir, art := submitSvc(t)
	svc.runner = &videoProbeRunner{duration: "120"}
	headMP4, baseMP4 := recordingPath(runDir, "head"), recordingPath(runDir, "base")
	for _, p := range []string{headMP4, baseMP4} {
		is(t, "")(nil, os.MkdirAll(filepath.Dir(p), 0o750))
		is(t, "")(nil, os.WriteFile(p, []byte("webm"), 0o600)) // what `record stop` left behind
	}
	t0 := svc.clock() // actions: head +10 s, base +20 s, head +30 s; the base recording began 15 s after the head's
	_, err := svc.update("run_s1", func(r *Run) {
		r.Legs = []Leg{{Name: "head", State: "running", RecordStartedAt: t0, RecordStoppedAt: t0.Add(time.Minute)}, {Name: "base", State: "running", RecordStartedAt: t0.Add(15 * time.Second), RecordStoppedAt: t0.Add(time.Minute)}}
	})
	is(t, "")(nil, err)
	skipLastModelOnBase(t, svc)
	is(t, "")(svc.Submit(ctx, "s1", "", ""))
	findCall := func(name string) string {
		for _, call := range fm.calls {
			if strings.HasSuffix(call[len(call)-1], name) {
				return strings.Join(call, " ")
			}
		}
		return ""
	}
	head, base := findCall(cutPath(runDir, "head")), findCall(cutPath(runDir, "base"))
	if head == "" || base == "" {
		t.Fatalf("want one cut per leg: %v", fm.calls)
	}
	if !strings.Contains(head, "-ss 9.000 -t 2.000 -i "+headMP4+" -ss 29.000 -t 2.000 -i "+headMP4+" -i ") || !strings.Contains(head, "concat=n=2") || !strings.HasSuffix(head, " "+cutPath(runDir, "head")) {
		t.Errorf("head cut: %s", head)
	}
	if !strings.Contains(base, "-ss 4.000 -t 2.000 -i "+baseMP4+" -i ") || !strings.Contains(base, "concat=n=1") || !strings.HasSuffix(base, " "+cutPath(runDir, "base")) {
		t.Errorf("base cut: %s", base)
	}
	headSRT, _ := os.ReadFile(filepath.Join(runDir, "head-cut-captions.srt"))
	baseSRT, _ := os.ReadFile(filepath.Join(runDir, "base-cut-captions.srt"))
	if string(headSRT) != "1\n00:00:00,000 --> 00:00:02,000\nStep 1: click an element\n\n2\n00:00:02,000 --> 00:00:04,000\nStep 3: click an element\n\n" ||
		string(baseSRT) != "1\n00:00:00,000 --> 00:00:02,000\nStep 2: click an element\n\n" {
		t.Fatalf("captions:\n%s\n%s", headSRT, baseSRT)
	}
	for _, f := range []string{"head.mp4", "base.mp4", "head-cut.mp4", "base-cut.mp4"} { // both videos reach the session's artifact dir
		exists(t, filepath.Join(art, f))
	}
	if _, err := os.Stat(filepath.Join(art, "run.mp4")); !os.IsNotExist(err) { // the single mislabelled video is gone
		t.Errorf("run.mp4 in the artifact dir: %v", err)
	}
	if md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md")); !strings.Contains(string(md), "Video: head: recorded (head.mp4; head-cut.mp4 keeps a 2 s window around each action); base: recorded (base.mp4; base-cut.mp4") {
		t.Fatalf("verdict.md:\n%s", md)
	}
	if index, _ := os.ReadFile(filepath.Join(art, "index.md")); !strings.Contains(string(index), "Video: [head.mp4](head.mp4), [head-cut.mp4](head-cut.mp4), [base.mp4](base.mp4), [base-cut.mp4](base-cut.mp4).") {
		t.Fatalf("index.md:\n%s", index)
	}

	svc, fm, runDir, art = submitSvc(t) // a failing ffmpeg never blocks a verdict
	fm.err = errors.New("no ffmpeg")
	is(t, "")(svc.Submit(ctx, "s1", "", ""))
	if md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md")); !strings.Contains(string(md), "Video: none") {
		t.Fatalf("verdict.md:\n%s", md)
	}
	exists(t, filepath.Join(art, "verdict.md"))
}

// Submit, cleanup and launch exclude each other, whichever started first, so a recording is never stopped before
// its launch has stored it.
func TestLaunchAndCloseRefuseEachOther(t *testing.T) {
	svc, _, _, _ := submitSvc(t)
	_, launching, err := svc.begin("s1", "launch base")
	is(t, "")(nil, err)
	is(t, "TESTING_RUN_BUSY")(svc.Submit(ctx, "s1", "", ""))
	is(t, "TESTING_RUN_BUSY")(svc.SubmitBlocked(ctx, "s1", "x"))
	is(t, "TESTING_RUN_BUSY")(svc.Cleanup(ctx, "s1"))
	_, other, err := svc.begin("s1", "launch head") // the two legs may still launch at once
	is(t, "")(nil, err)
	other()
	launching()

	for _, verb := range []string{"submit", "cleanup"} {
		svc.mu.Lock()
		closing, err := svc.markBusy("run_s1", verb)
		svc.mu.Unlock()
		is(t, "")(nil, err)
		is(t, "TESTING_RUN_BUSY")(svc.Launch(ctx, "s1", "base"))
		is(t, "TESTING_RUN_BUSY")(svc.Launch(ctx, "s1", "head"))
		closing()
	}
	is(t, "")(svc.Submit(ctx, "s1", "", "")) // nothing stays marked busy
}

func TestNotesAreKeptWholeAndNeverRefused(t *testing.T) {
	svc, _ := newSvc(t)
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	is(t, "")(svc.CheckAdd(ctx, "s1", "reach the menu", "head shows the c2 receipt", ""))
	is(t, "")(svc.CheckStart(ctx, "s1", "c1"))
	long := "the base has none, the snapshot lacks it. " + strings.Repeat("word ", 150) // over 600 characters, with the tester's words
	got, err := svc.CheckEnd(ctx, "s1", "c1", CheckSkipped, long, false, 0)
	if err != nil || got.Note != strings.TrimSpace(long) {
		t.Fatalf("note not kept whole: %q, %v", got.Note, err)
	}
	if saved, _ := svc.store.Checks("run_s1"); saved[0].Note != strings.TrimSpace(long) {
		t.Errorf("checks.json holds %d characters of the note, want %d", len(saved[0].Note), len(strings.TrimSpace(long)))
	}
	body := renderComment(Run{Repo: "o/r", PRNumber: 1, BaseRef: "main"}, Verdict{Overall: CheckPartial, Checks: []Check{got}}, evidence{}, time.Now())
	if strings.Contains(body, "the base has none") || !strings.Contains(body, "the main has none") && !strings.Contains(body, "main has none") {
		t.Errorf("the comment did not rewrite the tester's words:\n%s", body)
	}
	if !strings.Contains(body, "...") || strings.Contains(body, strings.Repeat("word ", 150)) {
		t.Errorf("the comment did not cut the long note:\n%s", body)
	}
	is(t, "")(svc.CheckAdd(ctx, "s1", "x", strings.Repeat("a", 500), ""))
}

func TestSkippedCheckNeedsNoReceiptAndLeavesTheHeadline(t *testing.T) {
	svc, _ := newSvc(t)
	svc.media = &fakeMedia{}
	is(t, "")(svc.Intake(ctx, "s1", prURL))
	is(t, "")(svc.CheckAdd(ctx, "s1", "reach the menu", "opens", ""))
	is(t, "")(svc.CheckAdd(ctx, "s1", "last model", "scrolls", ""))
	is(t, "")(svc.CheckStart(ctx, "s1", "c1"))
	is(t, "")(svc.CheckEnd(ctx, "s1", "c1", CheckSkipped, "needs a paid plan", false, 0))
	is(t, "")(svc.CheckStart(ctx, "s1", "c2"))
	is(t, "TESTING_CHECK_NO_RECEIPT")(svc.CheckEnd(ctx, "s1", "c2", CheckPass, "", false, 0)) // only a skip is exempt
	checks, _ := svc.store.Checks("run_s1")
	checks[1].Receipts = []Receipt{{Seq: 1, Action: "click", AfterPath: evidenceFile(t)}}
	is(t, "")(nil, svc.store.SaveChecks("run_s1", checks))
	is(t, "")(svc.CheckEnd(ctx, "s1", "c2", CheckPass, "", false, 0))
	v, err := svc.Submit(ctx, "s1", "", "")
	if err != nil || v.Overall != CheckPartial { // c2 passed, but nothing differs from the base
		t.Fatalf("verdict %+v err %v", v, err)
	}
	if md, _ := os.ReadFile(filepath.Join(svc.store.root, "run_s1", "verdict.md")); !strings.Contains(string(md), "- c1 reach the menu (head leg): could not reach. needs a paid plan") || strings.Contains(string(md), "- c1 reach the menu: not compared") {
		t.Fatalf("verdict.md:\n%s", md)
	}
}

func TestCompareDiffsTheBaseShotAgainstHead(t *testing.T) {
	svc, f, _, runDir := driveSvc(t)
	shot := func(n string) string { return filepath.Join(runDir, "receipts", n) }
	is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{
		{ID: "c1", Name: "menu", State: CheckPass, Receipts: []Receipt{{Seq: 1, AfterPath: shot("1-after.png")}, {Seq: 4, AfterPath: shot("4-after.png")}}},
		{ID: "c2", Name: "Menu", Leg: "base", Receipts: []Receipt{{Seq: 2, Leg: "base", AfterPath: shot("2-after.png")}}},
		{ID: "c3", Name: "other", Receipts: []Receipt{{Seq: 3, AfterPath: shot("3-after.png")}}},
	}))
	is(t, "TESTING_COMPARE_INCOMPLETE")(svc.Compare(ctx, "s1", "c3")) // no base shot
	is(t, "TESTING_CHECK_NOT_FOUND")(svc.Compare(ctx, "s1", "c9"))
	is(t, "TESTING_ARGS_INVALID")(svc.Compare(ctx, "s1", " "))
	if len(f.calls) != 0 {
		t.Fatalf("a refused compare reached the engine: %v", f.calls)
	}
	f.out["diff"] = "3.2% differ"
	res, err := svc.Compare(ctx, "s1", "c1")
	if err != nil || res["diff"] != shot("diff-c1.png") || res["output"] != "3.2% differ" {
		t.Fatalf("compare %v err %v", res, err)
	}
	if c := f.find("diff"); !reflect.DeepEqual(c, []string{"diff", "screenshot", "--baseline", shot("2-after.png"), "--output", shot("diff-c1.png")}) {
		t.Fatalf("engine ran %v", c)
	}
	if got, _ := svc.store.Checks("run_s1"); got[0].Diff != shot("diff-c1.png") {
		t.Fatalf("diff path not kept on the check: %+v", got[0])
	}
}

func TestCompareKeepsNoDiffPathForAnImageTheEngineDidNotWrite(t *testing.T) {
	svc, f, _, runDir := driveSvc(t)
	shot := func(n string) string { return filepath.Join(runDir, "receipts", n) }
	is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{
		{ID: "c1", Name: "menu", Receipts: []Receipt{{Seq: 1, AfterPath: shot("1-after.png")}}},
		{ID: "c2", Name: "menu", Leg: "base", Receipts: []Receipt{{Seq: 2, Leg: "base", AfterPath: shot("2-after.png")}}},
	}))
	is(t, "")(nil, os.MkdirAll(shot(""), 0o700))
	is(t, "")(nil, os.WriteFile(shot("diff-c1.png"), []byte("old"), 0o600)) // a leftover must not pass for this run's image
	f.noImg = true
	res, err := svc.Compare(ctx, "s1", "c1")
	if err != nil || res["diff"] != "" || res["warning"] == nil {
		t.Fatalf("compare %v err %v", res, err)
	}
	if got, _ := svc.store.Checks("run_s1"); got[0].Diff != "" {
		t.Fatalf("a diff path was kept for a file that is not there: %q", got[0].Diff)
	}
}

// leaveLeg gives the head leg of submitSvc's run a live process group, a registered clone and an owned.json, as a launch would.
func leaveLeg(t *testing.T, svc *Service) (*fakeSys, *fakeRunner, chan string) {
	t.Helper()
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	f, r, killed := &fakeSys{started: map[int]time.Time{100: t0}}, &fakeRunner{}, make(chan string, 4)
	svc.sys, svc.runner = f.ops(), r
	svc.closeEngine = func(id string) { f.log = append(f.log, "engine "+id) }
	svc.WithKiller(func(_ context.Context, id string) error { killed <- id; return nil })
	clone := t.TempDir()
	is(t, "")(nil, os.MkdirAll(filepath.Join(clone, ".git"), 0o750))
	_, err := svc.update("run_s1", func(r *Run) { r.Legs = []Leg{{Name: "head", State: "running", Worktree: clone}} })
	is(t, "")(nil, err)
	dir := svc.store.legDir("run_s1", "head")
	is(t, "")(nil, os.MkdirAll(filepath.Join(dir, "data", "worktrees"), 0o750))
	is(t, "")(nil, writeOwned(dir, Owned{RunID: "run_s1", Leg: "head", PGID: Proc{100, t0}, State: "running"}))
	return f, r, killed
}

func TestEverySubmitCleansItsLegAndLeavesTheCloneClean(t *testing.T) {
	endGrace = 0
	t.Cleanup(func() { endGrace = 10 * time.Second })
	for name, submit := range map[string]func(*Service) (Verdict, error){
		"pass":    func(s *Service) (Verdict, error) { return s.Submit(ctx, "s1", "", "") },
		"blocked": func(s *Service) (Verdict, error) { return s.SubmitBlocked(ctx, "s1", "seed failed") },
	} {
		t.Run(name, func(t *testing.T) {
			svc, _, runDir, _ := submitSvc(t)
			f, r, killed := leaveLeg(t, svc)
			if _, err := submit(svc); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(f.log, "|"); !strings.Contains(got, "engine run_s1") || !strings.Contains(got, "killGroup 100") {
				t.Fatalf("submit left the leg running: %q", got)
			}
			if _, err := os.Stat(filepath.Join(svc.store.legDir("run_s1", "head"), "data")); err == nil {
				t.Fatal("the leg's private data survived the submit")
			}
			if r.count("worktree prune") != 1 {
				t.Fatalf("the shared clone was not pruned: %q", r.calls)
			}
			if run, _ := svc.store.Get("run_s1"); run.State != RunSubmitted || run.Legs[0].State != "cleaned" {
				t.Fatalf("run %+v", run)
			}
			if md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md")); strings.Contains(string(md), "Cleanup after submit failed") {
				t.Fatalf("a clean submit reported a cleanup failure:\n%s", md)
			}
			select { // the tester ends after its grace, through the daemon's kill
			case id := <-killed:
				if id != "s1" {
					t.Fatalf("killed %q", id)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the tester was not stopped after its run closed")
			}
		})
	}
}

func TestSubmitSurvivesAFailedCleanupAndSaysSo(t *testing.T) {
	svc, _, runDir, _ := submitSvc(t)
	_, r, _ := leaveLeg(t, svc)
	r.gitErr = map[string]error{"worktree prune": errors.New("fatal: not a git repository")}
	v, err := svc.Submit(ctx, "s1", "", "")
	if err != nil || v.Overall == "" {
		t.Fatalf("verdict %+v err %v", v, err)
	}
	md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md"))
	if !strings.Contains(string(md), "- Cleanup after submit failed, stop leftovers with `ao testing cleanup`: ") || !strings.Contains(string(md), "not a git repository") {
		t.Fatalf("verdict.md hides the cleanup failure:\n%s", md)
	}
	if run, _ := svc.store.Get("run_s1"); run.State != RunSubmitted || run.Legs[0].State == "cleaned" {
		t.Fatalf("run %+v", run)
	}
}

func TestVerdictLinksOnlyFilesThatExist(t *testing.T) {
	svc, _, runDir, art := submitSvc(t)
	is(t, "")(nil, os.Remove(filepath.Join(runDir, "receipts", "3-after.png")))
	checks, _ := svc.store.Checks("run_s1")
	checks[0].Diff = filepath.Join(runDir, "receipts", "diff-c1.png") // recorded, never written
	is(t, "")(nil, svc.store.SaveChecks("run_s1", checks))
	is(t, "")(svc.Submit(ctx, "s1", "", ""))
	md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md"))
	index, _ := os.ReadFile(filepath.Join(art, "index.md"))
	for _, doc := range []string{string(md), string(index)} {
		if strings.Contains(doc, "3-after.png") || strings.Contains(doc, "diff-c1.png") {
			t.Errorf("a missing file is referenced:\n%s", doc)
		}
	}
	for _, want := range []string{"c3 receipt 3: its after-shot file is missing, so it is not linked.", "c1 pixel diff: the image file is missing, so it is not linked."} {
		if !strings.Contains(string(md), want) {
			t.Errorf("Limits lack %q:\n%s", want, md)
		}
	}
	if !strings.Contains(string(md), "(receipts/1-after.png)") {
		t.Errorf("the receipt that exists lost its link:\n%s", md)
	}
}

func TestSubmitBlockedClosesTheRunWithoutReceipts(t *testing.T) {
	svc, _ := newSvc(t)
	var reported []string
	fail := errors.New("report store down")
	svc.WithReporter(func(_ context.Context, id, note string) error { reported = append(reported, id+": "+note); return fail })
	svc.WithRepos(func(context.Context, ...string) (string, error) {
		return "", errors.New("no registered AO project matches x")
	})
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	is(t, "")(svc.CheckAdd(ctx, "s1", "menu opens", "list shown", "")) // planned, never driven: a normal submit would refuse it
	is(t, "TESTING_CHECKS_INCOMPLETE")(svc.Submit(ctx, "s1", "", ""))
	is(t, "TESTING_REASON_REQUIRED")(svc.SubmitBlocked(ctx, "s1", "  "))
	if len(reported) != 0 {
		t.Fatalf("refused submits reported: %v", reported)
	}

	v, err := svc.SubmitBlocked(ctx, "s1", " prepare refused: no checkout ") // a failing report does not fail the submit
	if err != nil || v.Overall != CheckBlocked || !reflect.DeepEqual(v.Reasons, []string{"prepare refused: no checkout"}) || len(v.Checks) != 1 {
		t.Fatalf("verdict %+v err %v", v, err)
	}
	md := readRunFile(t, svc, run, "verdict.md")
	for _, want := range []string{"# Verdict: blocked", "## Blocked\n\nprepare refused: no checkout", "Daemon: blocked at prepare: no registered AO project matches x", "c1 menu opens"} {
		if !strings.Contains(md, want) {
			t.Errorf("verdict.md lacks %q:\n%s", want, md)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(sessionartifacts.Dir(svc.store.dataDir(), domain.SessionID("s1")), "verdict.md")); string(got) != md {
		t.Fatal("verdict.md was not copied to the session artifacts")
	}
	if saved, err := svc.store.Get(run.ID); err != nil || saved.State != RunSubmitted {
		t.Fatalf("saved %+v err %v", saved, err)
	}
	if want := "s1: Litmus verdict blocked for orchestratorinc/agent-orchestrator#6394: see verdict.md in the session artifacts."; !reflect.DeepEqual(reported, []string{want}) {
		t.Fatalf("reported %q", reported)
	}
	is(t, "TESTING_RUN_CLOSED")(svc.SubmitBlocked(ctx, "s1", "again"))
}

func TestSubmitBlockedKeepsRecordedFailure(t *testing.T) {
	svc, now := newSvc(t)
	svc.media = &fakeMedia{}
	run := Run{ID: "run_s1", SessionID: "s1", Repo: "o/r", PRNumber: 7, State: RunRunning, CreatedAt: *now, Deadline: now.Add(RunBudget), Comment: CommentOff, Evidence: EvidenceOff}
	is(t, "")(nil, svc.store.Save(run))
	checks := []Check{
		{ID: "c1", Name: "menu", State: CheckFail, Note: "click closed the menu", Receipts: []Receipt{{Action: "click", AfterPath: evidenceFile(t)}}},
		{ID: "c2", Name: "unfinished", State: CheckRunning},
	}
	is(t, "")(nil, svc.store.SaveChecks(run.ID, checks))
	v, err := svc.SubmitBlocked(ctx, "s1", " recorder stopped ")
	if err != nil || v.Overall != CheckFail || !slices.Equal(v.Reasons, []string{"recorder stopped", "c1 menu: click closed the menu"}) || v.StoppedEarly != "recorder stopped" {
		t.Fatalf("verdict %+v, %v", v, err)
	}
	md := readRunFile(t, svc, run, "verdict.md")
	for _, want := range []string{"# Verdict: fail", "Stopped early: recorder stopped", "c1 menu", "click closed the menu"} {
		if !strings.Contains(md, want) {
			t.Errorf("verdict.md lacks %q:\n%s", want, md)
		}
	}
	if saved, err := svc.store.Get(run.ID); err != nil || saved.State != RunSubmitted {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	for _, file := range []string{"comment.md", "comments/1.md"} {
		body := readRunFile(t, svc, run, file)
		for _, want := range []string{"**Result: something broke; do not merge yet", "click closed the menu", "recorder stopped"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lost %q:\n%s", file, want, body)
			}
		}
	}
}

func TestSubmitBlockedKeepsPerBugFailure(t *testing.T) {
	svc, now := newSvc(t)
	svc.media = &fakeMedia{}
	run := Run{ID: "run_s1", SessionID: "s1", Repo: "o/r", PRNumber: 7, State: RunRunning, CreatedAt: *now, Deadline: now.Add(RunBudget), Comment: CommentOff, Evidence: EvidenceOff}
	is(t, "")(nil, svc.store.Save(run))
	checks := []Check{
		{ID: "c1", Name: "menu", State: CheckFail, Note: "click closed the menu", Receipts: []Receipt{{Action: "click", AfterPath: evidenceFile(t)}}},
		{ID: "c2", Name: "menu", Leg: "base", State: CheckPass},
		{ID: "c3", Name: "unfinished", State: CheckRunning},
	}
	is(t, "")(nil, svc.store.SaveChecks(run.ID, checks))
	v, err := svc.SubmitBlocked(ctx, "s1", "recorder stopped")
	if err != nil || v.Overall != CheckFail || v.StoppedEarly != "recorder stopped" {
		t.Fatalf("verdict %+v, %v", v, err)
	}
	for _, file := range []string{"comment.md", "comments/1.md"} {
		body := readRunFile(t, svc, run, file)
		for _, want := range []string{"Bug 1 of 1:", "click closed the menu", "Stopped early: recorder stopped.", litmusCommentMarker()} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lost %q:\n%s", file, want, body)
			}
		}
	}
}

func TestSubmitRefusesAnUnlaunchedBaseUnlessSkipped(t *testing.T) {
	head := Leg{Name: "head", State: "running"}
	prepared := []Leg{head, {Name: "base", State: "prepared"}}
	for _, c := range []struct {
		name, want, skip string
		legs             []Leg
		left             time.Duration
	}{
		{"prepared base, time left", "TESTING_BASE_NOT_RUN", "", prepared, 10 * time.Minute},
		{"exactly the guard window left", "TESTING_BASE_NOT_RUN", "", prepared, baseGuardLeft},
		{"skipped with a reason while time is left", "", "a comparison would not change the verdict", prepared, 10 * time.Minute},
		{"skipped with a reason, base running", "", "main is slow", []Leg{head, {Name: "base", State: "running"}}, 10 * time.Minute},
		{"skipped with a reason, under the guard window", "", "the main build\nis broken", prepared, baseGuardLeft - time.Second},
		{"skipped with a reason, base failed to launch", "", "the main build\nis broken", []Leg{head, {Name: "base", State: "failed"}}, 10 * time.Minute},
		{"skipped without a reason", "TESTING_REASON_REQUIRED", "  ", prepared, 10 * time.Minute},
		{"too little time left", "", "", prepared, baseGuardLeft - time.Second},
		{"no base leg", "", "", []Leg{head}, 10 * time.Minute},
		{"base launch failed, no reason", "TESTING_BASE_NOT_RUN", "", []Leg{head, {Name: "base", State: "failed"}}, 10 * time.Minute},
		{"base launch failed, no reason, under the guard window", "TESTING_BASE_NOT_RUN", "", []Leg{head, {Name: "base", State: "failed"}}, baseGuardLeft - time.Second},
	} {
		svc, _, runDir, _ := submitSvc(t)
		_, err := svc.update("run_s1", func(r *Run) { r.Legs, r.Deadline = c.legs, svc.clock().Add(c.left) })
		is(t, "")(nil, err)
		v, err := svc.Submit(ctx, "s1", "x", c.skip)
		is(t, c.want)(v, err)
		md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md"))
		switch {
		case c.want != "":
			if len(md) != 0 {
				t.Errorf("%s: a refused submit wrote verdict.md", c.name)
			}
			way := "repeat each check there in the app and record its visible result"
			if strings.Contains(c.name, "failed") {
				way = "submit with `--skip-base --reason"
			}
			if c.want == "TESTING_BASE_NOT_RUN" && !strings.Contains(err.Error(), way) {
				t.Errorf("%s: message %q does not teach the way out", c.name, err)
			}
		case c.skip != "":
			// submitSvc's c1 has a base result and c3 has none: main ran for one check, so the line says which was not
			if want := "- base compared on 1 of 2 checks; not run there: c3: " + strings.Join(strings.Fields(c.skip), " "); !strings.Contains(string(md), want) || strings.Contains(string(md), "base not run:") || !strings.Contains(string(md), "## Summary\n\nx\n") {
				t.Errorf("%s: verdict.md:\n%s", c.name, md)
			}
		case strings.Contains(string(md), "base not run:"):
			t.Errorf("%s: verdict.md claims a skip nobody asked for", c.name)
		}
	}
	svc, _, _, _ := submitSvc(t) // a blocked submit is never held back by the base
	_, err := svc.update("run_s1", func(r *Run) { r.Legs = prepared })
	is(t, "")(nil, err)
	is(t, "")(svc.SubmitBlocked(ctx, "s1", "prepare refused"))
}

func TestSubmitRefusesHeadChecksNotComparedOnBase(t *testing.T) {
	legs := []Leg{{Name: "head", State: "running"}, {Name: "base", State: "running"}}
	rc := func(leg string) []Receipt { return []Receipt{{Seq: 9, Leg: leg, Action: "click", AfterPath: "x.png"}} }
	pass := Check{ID: "c1", Name: "a", State: CheckPass, Receipts: rc("head")}
	partial := Check{ID: "c2", Name: "b", State: CheckPartial, Receipts: rc("head")}
	baseSkip := func(id, name, note string) Check {
		return Check{ID: id, Name: name, State: CheckSkipped, Leg: "base", Note: note}
	}
	for _, c := range []struct {
		name, want, skip, ids string
		checks                []Check
		legs                  []Leg
		left                  time.Duration
	}{
		{"partial head check, no base result", "TESTING_BASE_INCOMPLETE", "", "checks c1, c2 have", []Check{pass, partial}, legs, 10 * time.Minute},
		{"passing head check, no base result", "TESTING_BASE_INCOMPLETE", "", "check c1 has", []Check{pass}, legs, 10 * time.Minute},
		{"base skipped without a note", "TESTING_BASE_INCOMPLETE", "", "check c1 has", []Check{pass, baseSkip("c2", "a", " ")}, legs, 10 * time.Minute},
		{"base skipped with a note", "", "", "", []Check{pass, baseSkip("c2", "A", "main has no such menu")}, legs, 10 * time.Minute},
		{"a head check the tester could not reach needs no base result", "", "", "", []Check{pass, baseSkip("c2", "a", "no menu on main"), {ID: "c3", Name: "z", State: CheckSkipped, Note: "needs a key"}}, legs, 10 * time.Minute},
		{"base check of the same name ended", "", "", "", []Check{pass, partial, {ID: "c3", Name: "B", State: CheckFail, Leg: "base", Receipts: rc("base")}, baseSkip("c4", "A", "not repeated")}, legs, 10 * time.Minute},
		{"base receipts on the head check", "", "", "", []Check{pass, {ID: "c2", Name: "b", State: CheckFail, Receipts: append(rc("head"), rc("base")...)}, baseSkip("c3", "a", "not repeated")}, legs, 10 * time.Minute},
		{"skip-base after the base failed to launch", "", "main would not start", "", []Check{pass, partial}, []Leg{legs[0], {Name: "base", State: "failed"}}, 10 * time.Minute},
		{"too little time left", "", "", "", []Check{pass, partial}, legs, baseGuardLeft - time.Second},
		{"no base leg", "", "", "", []Check{pass, partial}, legs[:1], 10 * time.Minute},
	} {
		svc, _, runDir, _ := submitSvc(t)
		is(t, "")(nil, svc.store.SaveChecks("run_s1", c.checks))
		_, err := svc.update("run_s1", func(r *Run) { r.Legs, r.Deadline = c.legs, svc.clock().Add(c.left) })
		is(t, "")(nil, err)
		v, err := svc.Submit(ctx, "s1", "x", c.skip)
		if c.want == "" && err != nil && strings.Contains(err.Error(), "TESTING_BASE_INCOMPLETE") {
			t.Errorf("%s: refused: %v", c.name, err)
			continue
		}
		is(t, c.want)(v, err)
		if c.want != "" {
			if want := c.ids + " no result on main; repeat it there or record it with --result skipped and a note saying why"; !strings.Contains(err.Error(), want) {
				t.Errorf("%s: message %q", c.name, err)
			}
			if md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md")); len(md) != 0 {
				t.Errorf("%s: a refused submit wrote verdict.md", c.name)
			}
		}
	}
	svc, _, _, _ := submitSvc(t) // a blocked submit is never held back
	is(t, "")(nil, svc.store.SaveChecks("run_s1", []Check{partial}))
	_, err := svc.update("run_s1", func(r *Run) { r.Legs = legs })
	is(t, "")(nil, err)
	is(t, "")(svc.SubmitBlocked(ctx, "s1", "prepare refused"))
}

func TestPreExistingHeadFailureIsLimitedNotBlocking(t *testing.T) {
	long := strings.Repeat("n", 200)
	checks := []Check{
		{ID: "c1", Name: "x", State: CheckFail, Note: long, Receipts: []Receipt{{Leg: "head"}}},
		{ID: "c2", Name: "X", State: CheckFail, Leg: "base", SameFault: true, Receipts: []Receipt{{Leg: "base", Action: "click", AfterPath: evidenceFile(t)}}},
		{ID: "c3", Name: "y", State: CheckFail, Note: "regressed"},
		{ID: "c4", Name: "y", State: CheckPass, Leg: "base"},
	}
	if got := checkLimit(checks, checks[0]); got != "c1 x: pre-existing, same on base: "+strings.Repeat("n", 120)+"..." {
		t.Errorf("limit %q", got)
	}
	if got := checkLimit(checks, checks[2]); got != "" { // it passes on the base: the PR broke it
		t.Errorf("a regression got the limit %q", got)
	}
	checks[1].State = CheckPartial // a base partial is not the same thing: the head fail may be the PR's
	if got := checkLimit(checks, checks[0]); strings.Contains(got, "pre-existing") {
		t.Errorf("a head fail beside a base partial got the limit %q", got)
	}
	if !strings.Contains(headlineRule, "pre-existing") {
		t.Error("the result rule does not mention pre-existing checks")
	}
}

// --skip-base is honoured at any time. Where main ran on some checks, each check without a main result gets a skipped
// main side carrying the tester's reason; where main ran on none, the reason stays run-wide.
func TestSubmitSkipBaseRecordsTheMainSideOfUnrepeatedChecks(t *testing.T) {
	legs := []Leg{{Name: "head", State: "running"}, {Name: "base", State: "running"}}
	rc := func(leg string) []Receipt { return []Receipt{{Seq: 9, Leg: leg, Action: "click", AfterPath: "x.png"}} }
	a := Check{ID: "c1", Name: "a", State: CheckPass, Receipts: rc("head")}
	aMain := Check{ID: "c2", Name: "a", State: CheckFail, Leg: "base", Receipts: rc("base")}
	b := Check{ID: "c3", Name: "b", State: CheckPartial, Receipts: rc("head")}
	for _, c := range []struct {
		name         string
		checks       []Check
		wantChecks   int
		wantLimit    string
		wantTwinNote string
	}{
		{"main ran on one of two", []Check{a, aMain, b}, 4, "- base compared on 1 of 2 checks; not run there: c3: main took too long", "main took too long"},
		{"main ran on none", []Check{a, b}, 2, "- base not run: main took too long", ""},
	} {
		svc, _, runDir, _ := submitSvc(t)
		is(t, "")(nil, svc.store.SaveChecks("run_s1", c.checks))
		_, err := svc.update("run_s1", func(r *Run) { r.Legs, r.Deadline = legs, svc.clock().Add(20*time.Minute) }) // far from the deadline
		is(t, "")(nil, err)
		is(t, "TESTING_BASE_INCOMPLETE")(svc.Submit(ctx, "s1", "x", "")) // without the flag the missing main result is still refused
		v, err := svc.Submit(ctx, "s1", "x", "main took too long")
		is(t, "")(nil, err)
		if len(v.Checks) != c.wantChecks {
			t.Fatalf("%s: %d checks, want %d: %+v", c.name, len(v.Checks), c.wantChecks, v.Checks)
		}
		if c.wantTwinNote != "" {
			twin := v.Checks[len(v.Checks)-1]
			if twin.Name != "b" || twin.Leg != "base" || twin.State != CheckSkipped || twin.Note != c.wantTwinNote || twin.ID != "c4" {
				t.Errorf("%s: twin %+v", c.name, twin)
			}
		}
		md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md"))
		if !strings.Contains(string(md), c.wantLimit) {
			t.Errorf("%s: verdict.md lacks %q:\n%s", c.name, c.wantLimit, md)
		}
	}
}

// A head partial beside a base fail is not "pre-existing": the feature may exist on the PR build and not on main, so
// neither the Limits line nor the comment says it happens on main too.
func TestHeadPartialBesideBaseFailIsNeverPreExisting(t *testing.T) {
	rc := func(leg string) []Receipt { return []Receipt{{Seq: 1, Leg: leg, Action: "click", AfterPath: "x.png"}} }
	checks := []Check{
		{ID: "c1", Name: "picker", Expect: "the picker lists agents", State: CheckPartial, Note: "the list shows, the Agent row does nothing", Receipts: rc("head")},
		{ID: "c2", Name: "picker", Expect: "the picker lists agents", State: CheckFail, Leg: "base", Note: "there is no agent list", Receipts: rc("base")},
	}
	if preExisting(checks, checks[0]) {
		t.Fatal("a head partial beside a base fail is pre-existing")
	}
	if got := checkLimit(checks, checks[0]); strings.Contains(got, "pre-existing") {
		t.Errorf("limit %q", got)
	}
	overall, reasons := derive(checks)
	if overall != CheckPartial || slices.Contains(reasons, reasonPrePartial) || slices.Contains(reasons, reasonPreOnly) || slices.Contains(reasons, reasonPreBoth) {
		t.Errorf("derive = %s %q", overall, reasons)
	}
	got := renderComment(commentRun, commentVerdict(checks), evidence{}, commentRun.CreatedAt)
	for _, banned := range []string{"happens on main", "on main too", "on main either", "did not cause it"} {
		if strings.Contains(got, banned) {
			t.Errorf("the comment says %q:\n%s", banned, got)
		}
	}
}

func TestAbsentIsNeverTheEqualState(t *testing.T) {
	rc := func(leg string) []Receipt { return []Receipt{{Seq: 1, Leg: leg, Action: "click", AfterPath: "x.png"}} }
	for _, st := range []CheckState{CheckFail, CheckPartial, CheckAbsent, CheckPass} {
		checks := []Check{
			{ID: "c1", Name: "picker", State: st, Note: "no row", Receipts: rc("head")},
			{ID: "c2", Name: "picker", State: CheckAbsent, Leg: "base", Note: "no picker", Receipts: rc("base")},
		}
		if preExisting(checks, checks[0]) {
			t.Errorf("a head %s beside an absent base is pre-existing", st)
		}
		if got := checkLimit(checks, checks[0]); strings.Contains(got, "pre-existing") {
			t.Errorf("head %s: limit %q", st, got)
		}
	}
	// An absent main is not a failure that ran: only the difference rule reads it.
	if ranAndFailed(Check{State: CheckAbsent, Receipts: rc("base")}) {
		t.Error("absent ran and failed")
	}
	if !failedOnBase([]Check{{Name: "x"}, {Name: "x", State: CheckAbsent, Leg: "base", Receipts: rc("base")}}, Check{Name: "x"}) {
		t.Error("an absent base is no difference")
	}
	if !strings.Contains(headlineRule, "absent") {
		t.Error("verdict.md does not state the absent rule")
	}
}
