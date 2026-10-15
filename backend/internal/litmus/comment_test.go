package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeComment struct {
	ID          int64
	Login, Body string
}

// commentGH serves the comment endpoints from memory, records every argv, and reads each --input file when it is
// used. The listing comes back one JSON array per comment, as `gh --paginate` prints pages.
type commentGH struct {
	t             *testing.T
	login, author string
	private       string // "true" or "false": the repo's visibility
	comments      []fakeComment
	fail          string // an argv containing this fails
	notFound      int    // the next this many reads of an uploaded video answer 404
	calls, sent   []string
	argv          [][]string // every call's argv as gh got it
	uploads       int
}

const upload = "api --method POST -H Content-Type: video/mp4 https://uploads.github.com/"

func (g *commentGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil { // like gh, which is killed with its context
		return nil, err
	}
	line := strings.Join(args, " ")
	g.calls = append(g.calls, line)
	g.argv = append(g.argv, args)
	var in struct{ Body string }
	if i := slices.Index(args, "--input"); i >= 0 && !strings.HasPrefix(line, upload) { // a video upload sends a file, not a JSON body
		raw, err := os.ReadFile(args[i+1])
		if err != nil {
			g.t.Fatal(err)
		}
		g.sent = append(g.sent, string(raw))
		_ = json.Unmarshal(raw, &in)
	}
	if g.fail != "" && strings.Contains(line, g.fail) {
		return nil, errors.New("HTTP 403: Resource not accessible\nsecond line")
	}
	switch {
	case line == "api user --jq .login":
		return []byte(g.login + "\n"), nil
	case strings.HasPrefix(line, "repo view "):
		return []byte(g.private + "\n"), nil
	case line == "auth setup-git":
		return nil, nil
	case line == "api repos/o/r --jq .id":
		return []byte("1234\n"), nil
	case strings.HasPrefix(line, upload):
		g.uploads++
		return []byte(fmt.Sprintf(`{"url":"https://github.com/user-attachments/assets/%08d-aaaa-4bbb-8ccc-dddddddddddd"}`, g.uploads)), nil
	case strings.HasPrefix(line, "api -i https://github.com/user-attachments/assets/"):
		if g.notFound > 0 {
			g.notFound--
			return nil, errors.New("gh: Not Found (HTTP 404)")
		}
		return []byte("HTTP/2.0 200 OK\r\nContent-Length: 3\r\nContent-Type: video/mp4\r\n\r\nmp4"), nil
	case strings.HasPrefix(line, "pr view ") && strings.Contains(line, " --json author "):
		return []byte(g.author + "\n"), nil
	case strings.HasSuffix(line, "/comments --paginate"):
		out := "[]"
		if len(g.comments) > 0 {
			out = ""
		}
		for _, c := range g.comments {
			page, _ := json.Marshal([]map[string]any{{"id": c.ID, "body": c.Body, "user": map[string]string{"login": c.Login}}})
			out += string(page)
		}
		return []byte(out), nil
	case strings.HasPrefix(line, "api --method POST ") && strings.Contains(line, "/issues/") && strings.Contains(line, "/comments --input "):
		g.comments = append(g.comments, fakeComment{900, g.login, in.Body})
		return []byte(`{"id":900,"html_url":"https://github.com/o/r/pull/7#issuecomment-900"}`), nil
	case strings.HasPrefix(line, "api --method PATCH ") && strings.Contains(line, "/issues/comments/"):
		_, rest, _ := strings.Cut(line, "/issues/comments/")
		id := strings.Fields(rest)[0]
		return []byte(`{"html_url":"https://github.com/o/r/pull/7#issuecomment-` + id + `"}`), nil
	case strings.HasPrefix(line, "api --method POST ") && strings.Contains(line, "/reviews --input "):
		return []byte(`{"id":5}`), nil
	}
	g.t.Fatalf("unexpected gh call %q", line)
	return nil, nil
}

func (g *commentGH) has(prefix string) bool {
	return slices.ContainsFunc(g.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

var commentRun = Run{ID: "run_s1", SessionID: "s1", Repo: "o/r", PRNumber: 7, BaseSHA: "0ca80cfc370b21443c4a2b9d22199c047b8c74e7", HeadSHA: "e2204e0f361867a28310e71421a4227ab1d09544",
	State: RunSubmitted, CreatedAt: time.Date(2026, 10, 11, 8, 56, 0, 0, time.UTC), Comment: CommentPost}

// commentChecks: c1 passes on the head and fails on the base, c3 has no base result, c4 could not be reached.
func commentChecks(t *testing.T) []Check {
	r := func(leg string) []Receipt {
		return []Receipt{{Seq: 1, Leg: leg, Action: "click", AfterPath: evidenceFile(t)}}
	}
	return []Check{
		{ID: "c1", Name: "menu opens", Expect: "the menu stays open", State: CheckPass, Note: "Head: the menu stayed open and grew to 14 rows (ref @e12 | focus kept) <b> ![x](u) &commat;", Receipts: append(r("head"), r("head")...)},
		{ID: "c2", Name: "menu opens", Expect: "the menu stays open", State: CheckFail, Leg: "base", Note: "Base: closed the submenu.", Receipts: r("base")},
		{ID: "c3", Name: "last model", Expect: "the last model is remembered", State: CheckPass, Note: strings.Repeat("long note ", 20), Receipts: r("head")},
		{ID: "c4", Name: "effort slider", Expect: "the effort slider moves", State: CheckSkipped, Note: "needs a paid key"},
	}
}

func commentVerdict(checks []Check) Verdict {
	overall, reasons := derive(checks)
	return Verdict{Overall: overall, Checks: checks, Reasons: reasons}
}

// slop is what a comment must never say: our own words (legs, receipts, ids, engine refs, file names) and the
// machine-written tells. It is checked on every line but the image links and the marker.
var slop = regexp.MustCompile(`(?i)\b(legs?|heads?|base|receipts?|cuts?|mp4|run_\w+|c[0-9]+|@e[0-9]+|observables?|exercised|artifacts?|headline|snapshots?|refs?|ao session|additionally|ensuring|leverage|robust|seamless|not just)\b|\.(png|gif)\b|\x{2014}|[\x{201c}\x{201d}\x{2018}\x{2019}]`)

func noSlop(t *testing.T, body string) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "![") && !strings.HasPrefix(line, "<!--") && slop.MatchString(line) {
			t.Errorf("%q in %q", slop.FindString(line), line)
		}
	}
}

// The builds finish before the tester's clock starts: the first line gives the build time and the testing time apart,
// and the testing time is what the budget is measured against.
func TestCommentSplitsBuildTimeFromTestingTime(t *testing.T) {
	run := commentRun
	run.Deadline = run.CreatedAt.Add(3*time.Minute + RunBudget) // prepare finished 3 minutes in
	now := run.CreatedAt.Add(10 * time.Minute)
	if got := renderComment(run, commentVerdict(commentChecks(t)), evidence{}, now); !strings.HasPrefix(got, "Litmus tried #7 on the PR build and on main (builds 3 minutes, testing 7 minutes).") {
		t.Errorf("comment: %q", got[:min(len(got), 120)])
	}
	if md := renderVerdict(run, commentVerdict(commentChecks(t)), false, nil, t.TempDir(), now, nil); !strings.Contains(md, "Duration: builds 3m0s, testing 7m0s of a 25m0s budget.") {
		t.Errorf("verdict.md: %q", md[:min(len(md), 300)])
	}
}

func TestRenderCommentGolden(t *testing.T) {
	now := commentRun.CreatedAt.Add(4*time.Minute + 3*time.Second)
	got := renderComment(commentRun, commentVerdict(commentChecks(t)), evidence{}, now)
	const want = `Litmus tried #7 on the PR build and on main (4 minutes).

**Result: the PR does what it says in the 2 checks I finished. One check only ran on the PR build and one could not be done.**

I checked that the menu stays open. On the PR build, the menu stayed open and grew to 14 rows (that element \| focus kept) &lt;b> !\[x](u) &amp;commat;. On main, closed the submenu.

Next I checked that the last model is remembered. On the PR build, long note long note long note long note long note long note long note long note long note long note long note long note long note long note long note long note long note long note long note long note. I could not compare it with main.

I could not check that the effort slider moves. Needs a paid key.

<!-- litmus:comment o/r#7 -->`
	if got != want {
		t.Fatalf("comment:\n%s\nwant:\n%s", got, want)
	}
	noSlop(t, got)
}

func TestRenderCommentFitsTheCapAndClipsTableRowsLast(t *testing.T) {
	long := strings.Repeat("n", 300)
	var checks []Check
	for i := 1; i <= 6; i++ {
		checks = append(checks, Check{ID: fmt.Sprint("c", i), Name: "check " + fmt.Sprint(i), State: CheckPartial, Note: long, Receipts: []Receipt{{Seq: i}}})
	}
	v := commentVerdict(checks)
	for i := 0; i < 30; i++ { // far more Limits text than 6,000 characters
		v.Reasons = append(v.Reasons, fmt.Sprintf("reason %d %s", i, long))
	}
	got := renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
	if len(got) > commentCap || !strings.HasSuffix(got, "\n"+commentMarker("o/r", 7)) || !strings.HasPrefix(got, "Litmus tried #7 on the PR build (under a minute).\n\n**Result: nothing failed, but I could not confirm the change from the app; read the diff before merging.**\n") {
		t.Fatalf("len %d, ends %q", len(got), got[len(got)-60:])
	}
	rows := 0
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "| check ") && strings.HasSuffix(l, strings.Repeat("n", 80)+"... |") {
			rows++
		}
	}
	if rows != 6 { // the Limits gave way; every table note kept its full clip
		t.Fatalf("%d of 6 table notes kept the full clip:\n%s", rows, got)
	}
	if !strings.Contains(got, "\nAnd ") || strings.Contains(got, "AO session") {
		t.Fatalf("squeezed Limits do not say so:\n%s", got)
	}

	for i := 0; i < 300; i++ { // nothing fits: the body is cut, the marker survives
		checks = append(checks, checks[0])
	}
	got = renderComment(commentRun, commentVerdict(checks), evidence{}, commentRun.CreatedAt)
	if len(got) > commentCap || !strings.HasSuffix(got, commentMarker("o/r", 7)) {
		t.Fatalf("hard cap: len %d", len(got))
	}
}

// After a prepare timeout the comment says which build was ready, from the legs that finished.
func TestBlockedCommentNamesTheBuildThatWasReady(t *testing.T) {
	v := Verdict{Overall: CheckBlocked, Reasons: []string{"the builds were not ready after 20 minutes"}}
	run := commentRun
	run.Ready = []string{"head"}
	if got := renderComment(run, v, evidence{}, run.CreatedAt); !strings.Contains(got, "The PR build was ready; main was not. Nothing was checked.") || strings.Contains(got, "Nothing was built") {
		t.Errorf("head ready:\n%s", got)
	}
	run.Ready = []string{"base"}
	if got := renderComment(run, v, evidence{}, run.CreatedAt); !strings.Contains(got, "Main was ready; the PR build was not. Nothing was checked.") {
		t.Errorf("base ready:\n%s", got)
	}
}

func TestBlockedCommentCarriesTheReason(t *testing.T) {
	v := Verdict{Overall: CheckBlocked, Reasons: []string{"prepare refused: no checkout"}}
	got := renderComment(commentRun, v, evidence{}, commentRun.CreatedAt) // no leg and no receipt: nothing was built
	if !strings.HasPrefix(got, "Litmus could not start on #7 (prepare refused: no checkout). Nothing was built or checked.\n\n**Result: could not test this one.**\n") || strings.Contains(got, "I could not run the checks") || strings.Contains(got, "| what was checked |") {
		t.Fatalf("blocked comment:\n%s", got)
	}
	built := commentRun
	built.Legs = []Leg{{Name: "head", State: "cleaned"}}
	got = renderComment(built, v, evidence{}, commentRun.CreatedAt)
	if !strings.HasPrefix(got, "Litmus tried #7 on the PR build (under a minute).\n\n**Result: could not test this one.**\n") || !strings.Contains(got, "\nI could not run the checks. Prepare refused: no checkout.\n") {
		t.Fatalf("blocked comment after a build:\n%s", got)
	}
}

func TestBlockedRunKeepsTheReasonsTheFirstLineLeavesOut(t *testing.T) {
	long := strings.Repeat("word ", 40)
	v := Verdict{Overall: CheckBlocked, Reasons: []string{"prepare refused: no checkout", strings.TrimSpace(long)}}
	got := renderComment(commentRun, v, evidence{}, commentRun.CreatedAt) // no leg and no receipt: nothing was built
	if !strings.HasPrefix(got, "Litmus could not start on #7 (prepare refused: no checkout). Nothing was built or checked.\n\n") || strings.Count(got, "I could not run the checks.") != 1 || !strings.Contains(got, "\nI could not run the checks. Word word ") {
		t.Fatalf("second reason is not the one Limits bullet:\n%s", got)
	}
	v.Reasons = []string{strings.TrimSpace(long)} // a first reason the first line clips keeps its full text as a bullet
	got = renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
	if strings.Count(got, "I could not run the checks.") != 1 {
		t.Fatalf("a clipped first reason is lost:\n%s", got)
	}
}

func TestCommentSaysTheGapsAsLimitsWithoutChangingTheResult(t *testing.T) {
	now := commentRun.CreatedAt.Add(4 * time.Minute)
	plain := renderComment(commentRun, commentVerdict(commentChecks(t)), evidence{}, now)
	run := commentRun
	run.Gaps = []string{"I could not read the linked issue #123 (not found).", "The PR's patch is longer than 5.0 MB; I was shown the first 5.0 MB of it and read the rest with git (files.txt lists every file)."}
	got := renderComment(run, commentVerdict(commentChecks(t)), evidence{}, now)
	result := func(s string) string { return strings.SplitN(s, "\n", 4)[2] }
	if result(got) != result(plain) || !strings.HasPrefix(result(got), "**Result: ") {
		t.Fatalf("the result line changed: %q vs %q", result(got), result(plain))
	}
	if !strings.Contains(got, "\n"+run.Gaps[0]+"\n"+run.Gaps[1]+"\n\n<!-- litmus:comment") {
		t.Fatalf("gaps are not the Limits bullets:\n%s", got)
	}
	noSlop(t, got)

	v := commentVerdict(commentChecks(t)) // with Limits text far over the cap, the gaps are not what gets folded
	for i := 0; i < 30; i++ {
		v.Reasons = append(v.Reasons, fmt.Sprintf("reason %d %s", i, strings.Repeat("n", 300)))
	}
	got = renderComment(run, v, evidence{}, now)
	if !strings.Contains(got, run.Gaps[0]) || !strings.Contains(got, run.Gaps[1]) || !strings.Contains(got, "\nAnd ") {
		t.Fatalf("the cap folded the gaps:\n%s", got)
	}
}

// A gap is never clipped or folded into "And N more." at any fit, the tightest included: the other limits give way.
func TestCommentKeepsEveryGapWholeAtEveryFit(t *testing.T) {
	run := commentRun
	long := "I could not read the PR's file list with git (" + strings.Repeat("fatal: the object is missing ", 6) + "), so the areas it changes are unknown."
	run.Gaps = []string{long, "I could not read the linked issue #123 (not found)."}
	v := commentVerdict(commentChecks(t))
	for i := 0; i < 5; i++ {
		v.Reasons = append(v.Reasons, fmt.Sprintf("reason %d %s", i, strings.Repeat("n", 200)))
	}
	for _, f := range commentFits {
		got := commentBody(run, v, evidence{}, run.CreatedAt, f)
		if !strings.Contains(got, "\n"+mdText(long, len(long))+"\n"+run.Gaps[1]) {
			t.Errorf("fit %+v lost or clipped a gap:\n%s", f, got)
		}
		if f.bullets <= len(run.Gaps) && !strings.HasSuffix(got, "\nAnd 5 more.") {
			t.Errorf("fit %+v: the other limits are not folded into one line:\n%s", f, got[max(len(got)-400, 0):])
		}
	}
}

func TestBlockedRunSkipsTheCheckParagraphs(t *testing.T) {
	run := commentRun
	run.Areas = []Area{{Name: "Tests", Files: 4}}
	checks := []Check{{ID: "c1", Name: "artifactversions-tests", Expect: "the artifactversions package tests pass: files are captured after two stable ticks", Receipts: nil}}
	v := Verdict{Overall: CheckBlocked, Checks: checks, Reasons: []string{"prepare refused: no checkout"}}
	got := renderComment(run, v, evidence{}, run.CreatedAt.Add(7*time.Minute))
	const want = `Litmus could not start on #7 (prepare refused: no checkout). Nothing was built or checked.

**Result: could not test this one.**

Not checked by this run: tests (4 files).

<!-- litmus:comment o/r#7 -->`
	if got != want {
		t.Fatalf("blocked comment:\n%s\nwant:\n%s", got, want)
	}
	noSlop(t, got)

	v.Overall = CheckPartial // not blocked: a check with nothing seen says so in one sentence, not an empty note
	if got := checkPara(run, v, checks[0], 80, 0); got != "Artifactversions tests could not be run on the PR build." {
		t.Fatalf("unobserved check: %q", got)
	}
}

func TestSubmitCommentModesAndFailure(t *testing.T) {
	svc, _, runDir, _ := submitSvc(t) // checks: c1 head pass, c2 base fail, c3 head pass: a PASS
	g := &commentGH{t: t, login: "litmus-bot"}
	svc.gh = g
	v, err := svc.Submit(ctx, "s1", "", "")
	run, _ := svc.store.Get("run_s1")
	body, _ := os.ReadFile(filepath.Join(runDir, "comment.md"))
	if err != nil || v.CommentState != "posted" || run.CommentState != "posted" || run.CommentURL != v.CommentURL || v.CommentURL == "" {
		t.Fatalf("posted: verdict %+v run %+v err %v", v, run, err)
	}
	if g.field(0, "body") != string(body) || !strings.HasPrefix(string(body), "Litmus tried #6394 on the PR build and on main (") {
		t.Fatalf("comment.md is not the posted body: %s", body)
	}

	for name, c := range map[string]struct {
		mode  string
		fail  string
		state string
	}{
		"off":         {CommentOff, "", "off"},
		"older run":   {"", "", "off"}, // a run from before comments existed never posts
		"login fails": {CommentPost, "api user", "failed:HTTP 403: Resource not accessible"},
		"list fails":  {CommentPost, "--paginate", "failed:HTTP 403: Resource not accessible"},
		"write fails": {CommentPost, "--method POST", "failed:HTTP 403: Resource not accessible"},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _, runDir, _ := submitSvc(t)
			g := &commentGH{t: t, login: "litmus-bot", fail: c.fail}
			svc.gh = g
			_, err := svc.update("run_s1", func(r *Run) { r.Comment = c.mode })
			is(t, "")(nil, err)
			v, err := svc.Submit(ctx, "s1", "", "") // a failed post is not a failed submit
			run, _ := svc.store.Get("run_s1")
			if err != nil || v.CommentState != c.state || run.CommentState != c.state || run.State != RunSubmitted || v.CommentURL != "" {
				t.Fatalf("verdict %+v run %+v err %v", v, run, err)
			}
			exists(t, filepath.Join(runDir, "comment.md"))
			log, _ := os.ReadFile(filepath.Join(runDir, "comment.log"))
			log = regexp.MustCompile(`(?m)^\S+ pruned: .*\n`).ReplaceAll(log, nil) // the retention pass logs there too
			if (c.fail == "") != (len(g.calls) == 0 && len(log) == 0) || (c.fail != "" && !strings.Contains(string(log), "Resource not accessible")) {
				t.Fatalf("off must touch neither gh nor the log; failed must log: calls %q log %q", g.calls, log)
			}
		})
	}

	blocked, _, runDir, _ := submitSvc(t)
	bg := &commentGH{t: t, login: "litmus-bot"}
	blocked.gh = bg
	v, err = blocked.SubmitBlocked(ctx, "s1", "prepare refused")
	if body, _ = os.ReadFile(filepath.Join(runDir, "comment.md")); err != nil || v.CommentState != "off" || len(bg.calls) != 0 || !strings.Contains(string(body), "**Result: could not test this one.**") {
		t.Fatalf("blocked: %+v %v\n%s", v, err, body)
	}
}

func TestStartNoCommentSetsTheRunsMode(t *testing.T) {
	ids := []string{"s8", "s9"}
	svc, _ := newSvc(t)
	svc.WithSpawner(func(context.Context, SpawnRequest) (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	for _, c := range []struct{ session, mode string }{{"s8", CommentPost}, {"s9", CommentOff}} {
		started, err := svc.Start(ctx, StartRequest{PRURL: prURL, NoComment: c.mode == CommentOff})
		run, ierr := svc.Intake(ctx, started.SessionID, prURL)
		if err != nil || ierr != nil || started.SessionID != c.session || run.Comment != c.mode {
			t.Fatalf("%s: started %+v run %+v errs %v %v", c.session, started, run, err, ierr)
		}
	}
}

// field is one string of the i-th request body that was sent.
func (g *commentGH) field(i int, name string) string {
	var m map[string]string
	_ = json.Unmarshal([]byte(g.sent[i]), &m)
	return m[name]
}

func TestMdTextDoublesBackslashes(t *testing.T) {
	// a trailing backslash must not escape the pipe that cell() adds, nor a note's own backslash the markup after it
	if got, want := mdText(`a\ | b\[x]`, 50), `a\\ \| b\\\[x]`; got != want {
		t.Fatalf("mdText = %q, want %q", got, want)
	}
}

func TestVerdictLineSentences(t *testing.T) {
	counted := count(commentChecks(t)) // passed 2, one shown against main, one not compared, one unreached
	for _, c := range []struct {
		overall CheckState
		t       tally
		want    string
	}{
		{CheckPass, counted, "the PR does what it says in the 2 checks I finished. One check only ran on the PR build and one could not be done"},
		{CheckPass, tally{passed: 3, shown: 1, notCompared: 2, unreached: 3}, "the PR does what it says in the 3 checks I finished. A few checks only ran on the PR build and a few could not be done"},
		{CheckPass, tally{passed: 1, shown: 1, unreached: 1}, "the PR does what it says in the 1 check I finished. One check could not be done"},
		{CheckPass, tally{passed: 1, shown: 1, unreached: 2}, "the PR does what it says in the 1 check I finished. A few checks could not be done"},
		{CheckPass, tally{passed: 3, shown: 1, notCompared: 2}, "the PR does what it says in the 3 checks I finished. A few checks only ran on the PR build"},
		{CheckPass, tally{passed: 1, shown: 1, notCompared: 1}, "the PR does what it says in the 1 check I finished. One check only ran on the PR build"},
		{CheckPass, tally{passed: 1, shown: 1}, "the PR does what it says in the 1 check I finished"},
		{CheckPass, tally{passed: 2, pre: 1}, "the PR does what it says in the 2 checks I finished"},
		{CheckPartial, tally{passed: 2}, "the PR build works, but nothing differed from main; read the diff before merging"},
		{CheckPartial, tally{partial: 2}, "nothing failed, but I could not confirm the change from the app; read the diff before merging"},
		{CheckPartial, tally{pre: 1}, "nothing failed, but I could not confirm the change from the app; read the diff before merging"},
		{CheckPartial, tally{unreached: 4}, "could not test this from the app"},
		{CheckPartial, tally{unreached: 1, partial: 1}, "nothing failed, but I could not confirm the change from the app; read the diff before merging"},
		{CheckFail, tally{failed: 1, passed: 2}, "something broke; do not merge yet"},
		{CheckFail, tally{failed: 3}, "something broke; do not merge yet"},
		{CheckBlocked, tally{}, "could not test this one"},
	} {
		if got := verdictLine(c.overall, c.t, "main"); got != c.want {
			t.Errorf("%s %+v:\n got %q\nwant %q", c.overall, c.t, got, c.want)
		}
	}
	if got := verdictLine(CheckPartial, tally{passed: 1}, "develop"); got != "the PR build works, but nothing differed from develop; read the diff before merging" {
		t.Errorf("a run against another base branch names it: %q", got)
	}
}

func TestAreaLineSaysWhichChangedAreasTheChecksDidNotCover(t *testing.T) {
	// run 11: three e2e specs, two unit tests and one component. The component's file name shares the stem "nav" with
	// "navigate", and it is the only area that is not tests, so it counts as covered either way. Tests are never run.
	pr := []Area{{Name: "Tests", Files: 5}, {Name: "Renderer components", Files: 1, Stems: []string{"TitlebarNav"}}}
	navigate := []Check{{Name: "history", Expect: "back and forward navigate", Note: "the arrows stayed", State: CheckPass}}
	if got, want := areaLine(pr, navigate), "Not checked by this run: tests (5 files)."; got != want {
		t.Fatalf("line %q, want %q", got, want)
	}
	if got, want := areaLine(pr, []Check{{Name: "colors", Expect: "the page is red", State: CheckFail}}), "Not checked by this run: tests (5 files)."; got != want {
		t.Fatalf("the only area besides tests is about what the checks claim: %q, want %q", got, want)
	}
	checks := []Check{{Name: "history", Expect: "going back works", Note: "the sidebar shows the arrows", State: CheckPass}, {Name: "x", Note: "the latest build happens often; Project files changed", State: CheckPartial}}
	areas := []Area{{Name: "Shell sidebar", Files: 3, Stems: []string{"Sidebar", "useHistory"}}, {Name: "Tests", Files: 1}, {Name: "Project files", Files: 2, Stems: []string{"package", "lockfile"}}, {Name: "App", Files: 4, Stems: []string{"index"}}}
	want := "Not checked by this run: tests (1 file), project files (2 files), app (4 files)."
	if got := areaLine(areas, checks); got != want { // a file word must start a word the checks use; "index" and "use" are too common to count
		t.Fatalf("line %q, want %q", got, want)
	}
	if got, want := areaLine([]Area{{Name: "Shell sidebar", Files: 2}, {Name: "Chat", Files: 1}}, checks), "Not checked by this run: chat (1 file)."; got != want { // older runs kept no file names: the area's own words stand in
		t.Fatalf("older run: %q, want %q", got, want)
	}
	if got := areaLine([]Area{{Name: "Shell sidebar", Files: 2}}, checks); got != "" {
		t.Fatalf("everything covered, no line: %q", got)
	}
	if got := areaLine(nil, checks); got != "" {
		t.Fatalf("no areas, no line: %q", got)
	}
	var many []Area
	for i := 1; i <= 8; i++ {
		many = append(many, Area{Name: fmt.Sprint("Area", i), Files: i})
	}
	if got := areaLine(many[:7], nil); !strings.HasSuffix(got, " area6 (6 files), and 1 more area.") || strings.Contains(got, "area7") {
		t.Fatalf("seven areas: %q", got)
	}
	if got := areaLine(many, nil); !strings.HasSuffix(got, ", and 2 more areas.") {
		t.Fatalf("eight areas: %q", got)
	}
}

func TestAreaIsCoveredOnlyWhenACheckNamingItRan(t *testing.T) {
	areas := []Area{{Name: "Shell sidebar", Files: 3, Stems: []string{"Sidebar"}}, {Name: "Chat", Files: 2, Stems: []string{"Chat"}}}
	about := func(state CheckState, name string) Check {
		return Check{Name: name, Expect: "the " + name + " works", Note: "seen", State: state}
	}
	want := "Not checked by this run: shell sidebar (3 files)."
	if got := areaLine(areas, []Check{about(CheckSkipped, "sidebar"), about(CheckPass, "chat")}); got != want { // could not be done: not checked
		t.Errorf("skipped check: %q, want %q", got, want)
	}
	for _, state := range []CheckState{CheckPass, CheckFail, CheckPartial} {
		if got := areaLine(areas, []Check{about(state, "sidebar"), about(CheckPass, "chat")}); got != "" {
			t.Errorf("%s check: %q, want no line", state, got)
		}
	}
	// a skipped check alone covers nothing, even for the only non-test area
	if got, want := areaLine(areas[:1], []Check{about(CheckSkipped, "sidebar")}), "Not checked by this run: shell sidebar (3 files)."; got != want {
		t.Errorf("only area, skipped: %q, want %q", got, want)
	}
	// a check on main alone does not cover the PR build's area
	base := about(CheckPass, "sidebar")
	base.Leg = "base"
	if got := areaLine(areas[:1], []Check{base}); got == "" {
		t.Errorf("a main-only check covered an area")
	}
}

func TestClauseCutsAtAWordAndHasNoStopOfItsOwn(t *testing.T) {
	long := "With sidebar expanded and brand shown, Collapse, Go back and Go forward buttons are all visible"
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{long, 160, long},
		{"Ends with a stop...", 160, "Ends with a stop"},
		{strings.Repeat("alpha ", 40), 160, strings.TrimSpace(strings.Repeat("alpha ", 26))},
		{"it shows Go back and the", 20, "it shows Go back"},                           // a cut never ends on "and" or "the" // the 160th character is inside a word
		{"alpha beta gamma delta", 16, "alpha beta gamma"},                             // the cut falls on a space: no word is lost
		{"keep (open bracket and then a long tail", 20, "keep"},                        // an unclosed bracket goes with the cut
		{"keep (both closed) and more words after", 30, "keep (both closed) and more"}, //
	} {
		if got := clause(c.in, c.n); got != c.want {
			t.Errorf("clause(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestCommentSaysWholeSentencesInTheReadersWords(t *testing.T) {
	long := "With sidebar expanded and brand shown, Collapse, Go back and Go forward buttons are all visible"
	over := strings.Repeat("the sidebar keeps every button ", 8) + "and then some"
	rc := func(leg string) []Receipt { return []Receipt{{Seq: 1, Leg: leg}} }
	checks := []Check{
		{ID: "c1", Name: "history-visible", Expect: long, State: CheckPass, Note: "Head shows the arrows", Receipts: rc("head")},
		{ID: "c2", Name: "history-visible", Expect: long, State: CheckFail, Leg: "base", Note: "Base: expanded sidebar shows only Collapse", Receipts: rc("base")},
		{ID: "c3", Name: "history-navigates", Expect: "Back/forward navigate and stay visible", State: CheckPartial, Note: "Forward did not land. C2 needs the arrows to navigate, so it cannot run there (see c1).", Receipts: rc("head")},
		{ID: "c4", Name: "long", Expect: over, State: CheckPass, Note: "fine", Receipts: rc("head")},
	}
	v := commentVerdict(checks)
	v.SkipBase = "C2 has no arrows; base has none when expanded, head is slow"
	got := renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
	for _, want := range []string{
		"I checked that with sidebar expanded and brand shown, Collapse, Go back and Go forward buttons are all visible. The PR build shows the arrows. On main, expanded sidebar shows only Collapse.",
		"Next I checked that Back/forward navigate and stay visible. On the PR build, forward did not land. History visible needs the arrows to navigate, so it cannot run there (see history visible).\n",
		"I also checked that " + strings.TrimSuffix(strings.Repeat("the sidebar keeps every button ", 5), " ") + ". On the PR build, fine.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "....") || strings.Contains(got, "Worth a look") || strings.Contains(got, "could not compare this") || strings.Contains(got, "I did not run") {
		t.Errorf("clipped sentence, the old closing line, or a run-wide skip over a run that compared a check:\n%s", got)
	}
	noSlop(t, got)

	checks[1].State = CheckSkipped // nothing ran on main: the reason stands for the whole run, said once
	v = commentVerdict(checks)
	v.SkipBase = "C2 has no arrows; base has none when expanded, head is slow"
	got = renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
	if !strings.Contains(got, "\n\nI did not run main: history visible has no arrows; main has none when expanded, the PR build is slow.\n\n") || strings.Count(got, "I did not run") != 1 {
		t.Errorf("skip reason not said exactly once:\n%s", got)
	}
	noSlop(t, got)
}

// A run that compared some checks on main says which, and never claims it did not run main.
func TestCommentSaysWhichChecksWereComparedOnMain(t *testing.T) {
	checks := commentChecks(t) // c1 compared, c3 has no main result, c4 could not be reached
	v := commentVerdict(checks)
	v.SkipBase = "main took too long to start"
	got := renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
	if want := "\n\nI compared 1 check on main; 1 was not run there: last model.\n\n"; !strings.Contains(got, want) {
		t.Errorf("no %q in\n%s", want, got)
	}
	if strings.Contains(got, "I did not run") {
		t.Errorf("the comment says main was not run:\n%s", got)
	}
	noSlop(t, got)

	checks = append(checks, Check{ID: "c5", Name: "last model", Expect: "the last model is remembered", State: CheckSkipped, Leg: "base", Note: "main took too long to start"}, Check{ID: "c6", Name: "pick", Expect: "the picker opens", State: CheckPass, Receipts: []Receipt{{Seq: 3, Leg: "head"}}})
	v = commentVerdict(checks)
	v.SkipBase = "main took too long to start"
	got = renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
	if want := "I compared 1 check on main; 2 were not run there: last model, pick."; !strings.Contains(got, want) {
		t.Errorf("no %q in\n%s", want, got)
	}
	if !strings.Contains(got, "I did not repeat this on main: main took too long to start.") {
		t.Errorf("the skipped twin's reason is not on its row:\n%s", got)
	}
}

// The result line says how many checks ran on main when some did, and keeps "nothing was compared" for when none did.
func TestVerdictLineSaysHowManyChecksWereComparedOnMain(t *testing.T) {
	if got := count(commentChecks(t)); got.compared != 1 || got.notRun != 1 {
		t.Errorf("commentChecks compared %d, not run %d on main, want 1 and 1", got.compared, got.notRun)
	}
	for _, c := range []struct {
		t    tally
		want string
	}{
		{tally{passed: 7, notCompared: 2, compared: 5, notRun: 2}, "the PR build works as far as I could check; 5 checks were compared on main and showed no difference, 2 were not; read the diff before merging"},
		{tally{passed: 2, notCompared: 1, compared: 1, notRun: 1}, "the PR build works as far as I could check; 1 check was compared on main and showed no difference, 1 was not; read the diff before merging"},
		{tally{passed: 2, notCompared: 2}, "the PR build works as far as I could check; nothing was compared against main, so read the diff before merging"},
	} {
		got := verdictLine(CheckPartial, c.t, "main")
		if got != c.want {
			t.Errorf("verdictLine(%+v) = %q, want %q", c.t, got, c.want)
		}
		noSlop(t, got)
	}
}

func TestHeroCaptionSaysWhatEachSideShowedInNinetyCharacters(t *testing.T) {
	both := hero{pr: &shot{}, base: &shot{}}
	if got := heroCaption(commentRun, both); got != "Main on the left, PR build on the right." {
		t.Errorf("no notes: %q", got)
	}
	both.baseNote, both.prNote = "No Go back or Go forward buttons", "Expanded sidebar shows Collapse, Go back, Go forward (both disabled on Home) and Orchestrator.inc brand; still present after toggling a project row"
	want := "Main on the left: no Go back or Go forward buttons. PR build on the right: expanded sidebar shows Collapse, Go back, Go forward (both disabled on Home)."
	if got := heroCaption(commentRun, both); got != want {
		t.Errorf("caption %q, want %q", got, want)
	}
	both.baseNote = ""
	if got := heroCaption(commentRun, both); got != "Main on the left, PR build on the right." {
		t.Errorf("one note missing: %q", got)
	}
}

func TestPlainTesterRewritesTheTestersVocabulary(t *testing.T) {
	checks := []Check{{ID: "c1", Name: "history-visible"}, {ID: "c3", Name: "menu opens"}}
	run := commentRun
	for in, want := range map[string]string{
		"Head: both arrows show":                            "both arrows show",
		"head leg shows it, the headline and database stay": "the PR build leg shows it, the headline and database stay",
		"Base has none; BASE too":                           "Main has none; Main too",
		"C1 needs it, shown by c3, and c9 too":              "History visible needs it, shown by menu opens, and that check too",
		"nothing to change here":                            "nothing to change here",
	} {
		if got := plainTester(in, run, checks); got != want {
			t.Errorf("plainTester(%q) = %q, want %q", in, got, want)
		}
	}
	run.BaseRef = "develop"
	if got := plainTester("base differs", run, checks); got != "develop differs" {
		t.Errorf("another base branch is named: %q", got)
	}
}

func TestChecksTableAppearsOnlyBeyondFourChecks(t *testing.T) {
	var checks []Check
	for i := 1; i <= 5; i++ {
		checks = append(checks, Check{ID: fmt.Sprint("c", i), Name: fmt.Sprint("check ", i), Expect: fmt.Sprint("thing ", i, " works"), State: CheckPass, Note: "fine", Receipts: []Receipt{{Seq: i}}})
	}
	const header = "| what was checked | PR build | main | note |"
	if got := renderComment(commentRun, commentVerdict(checks), evidence{}, commentRun.CreatedAt); !strings.Contains(got, "\n\n"+header+"\n|---|---|---|---|\n| thing 1 works | works | not compared | fine |\n") || strings.Count(got, "checked that thing") != 5 {
		t.Fatalf("five checks get a table and a paragraph each:\n%s", got)
	}
	if got := renderComment(commentRun, commentVerdict(checks[:4]), evidence{}, commentRun.CreatedAt); strings.Contains(got, header) {
		t.Fatalf("four checks read as paragraphs:\n%s", got)
	}
}

func TestPlainTextKeepsQuestionsAndEmailAddresses(t *testing.T) {
	if got := plainSentence("does it save?", 80); got != "does it save?" {
		t.Errorf("question: %q", got)
	}
	if got, want := mdText("mail a@e2e.com, ref @e12 and @e7", 80), "mail a@\u200be2e.com, that element and that element"; got != want {
		t.Errorf("refs: %q, want %q", got, want)
	}
}

func TestNoteSentencesScrubsReceiptClauses(t *testing.T) {
	run := Run{BaseRef: "main"}
	for _, tc := range []struct{ note, want string }{
		{
			"On first opening the board, the collapsed Agent updates available notice covered New task, Orchestrator and Notifications. Clicking New task was refused because the notice covered it, captured in receipts/11-after.png during the model-filter check. After Dismiss all updates, New task opened normally. The final screenshots show the workaround; the failure screenshot is receipt 11.",
			"On first opening the board, the collapsed Agent updates available notice covered New task, Orchestrator and Notifications. Clicking New task was refused because the notice covered it. After Dismiss all updates, New task opened normally. The final screenshots show the workaround.",
		},
		{"Receipt 11 shows the failure. The menu stayed closed.", "The menu stayed closed."},
		{"The menu stayed closed, see RECEIPTS/ 11-after.png! It reopened?", "The menu stayed closed! It reopened?"},
		{"The menu stayed closed; see receipt 11 and receipts/12-after.png", "The menu stayed closed."},
		{"The menu, when clicked, stayed closed; see receipt 11. It reopened.", "The menu, when clicked, stayed closed. It reopened."},
	} {
		if got := noteSentences(run, Verdict{}, Check{Note: tc.note}); got != tc.want {
			t.Errorf("noteSentences(%q) = %q, want %q", tc.note, got, tc.want)
		}
	}
}

func TestPlainSentenceTrimsTrailingSeparators(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"shows the workaround;", "shows the workaround."},
		{"shows the workaround:", "shows the workaround."},
		{"shows the workaround,", "shows the workaround."},
		{"&commat;", "&amp;commat;."},
		{"&#64;", "&amp;#64;."},
	} {
		if got := plainSentence(tc.in, 80); got != tc.want {
			t.Errorf("plainSentence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCommentFirstLineNamesTheClosedIssues(t *testing.T) {
	url := func(n int) string { return fmt.Sprintf("https://github.com/o/r/issues/%d", n) }
	run := commentRun
	run.PRNumber = 6403
	for _, c := range []struct {
		issues []IssueRef
		want   string
	}{
		{nil, "Litmus tried #6403 on the PR build and on main (1 minute)."},
		{[]IssueRef{{Number: 6390, URL: url(6390)}}, "Litmus tried #6403 (for [#6390](" + url(6390) + ")) on the PR build and on main (1 minute)."},
		{[]IssueRef{{Number: 6390, URL: url(6390)}, {Number: 6391, URL: url(6391)}}, "Litmus tried #6403 (for [#6390](" + url(6390) + ") and [#6391](" + url(6391) + ")) on the PR build and on main (1 minute)."},
		{[]IssueRef{{Number: 1}, {Number: 2}, {Number: 3}}, "Litmus tried #6403 (for #1, #2 and #3) on the PR build and on main (1 minute)."},
	} {
		run.Issues = c.issues
		got := renderComment(run, commentVerdict(commentChecks(t)), evidence{}, run.CreatedAt.Add(time.Minute))
		if first, _, _ := strings.Cut(got, "\n"); first != c.want {
			t.Errorf("first line %q, want %q", first, c.want)
		}
		noSlop(t, got)
	}
	run.Issues = []IssueRef{{Number: 6390, URL: url(6390)}}
	one := plainVerdict(run, commentVerdict(commentChecks(t)))
	for i := range one.Checks {
		one.Checks[i].Leg = "head"
		one.Checks[i].Receipts = nil
	}
	if first, _, _ := strings.Cut(commentBody(run, one, evidence{}, run.CreatedAt, commentFits[0]), "\n"); first != "Litmus tried #6403 (for [#6390]("+url(6390)+")) on the PR build (under a minute)." {
		t.Errorf("head-only first line %q", first)
	}
}

// A run whose base build is main at the PR's branch point says so where the comment introduces the two builds; every
// other mention of main stays, and a PR build alone has no main to qualify.
func TestCommentFirstLineSaysMainAtTheBranchPoint(t *testing.T) {
	run := commentRun
	run.PRNumber = 6403
	run.Gaps = []string{forkGapPrefix + "main 9 commits ago; 'main' in this report is that branch point (fca1b72c), not today's main."}
	got := renderComment(run, commentVerdict(commentChecks(t)), evidence{}, run.CreatedAt.Add(time.Minute))
	if first, _, _ := strings.Cut(got, "\n"); first != "Litmus tried #6403 on the PR build and on main at the PR's branch point (1 minute)." {
		t.Errorf("first line %q", first)
	}
	if !strings.Contains(got, "not today's main") {
		t.Errorf("the gap is missing from the comment:\n%s", got)
	}
	run.Gaps = []string{"GitHub counts 3 changed files and git found 2 from the fork point."}
	got = renderComment(run, commentVerdict(commentChecks(t)), evidence{}, run.CreatedAt.Add(time.Minute))
	if first, _, _ := strings.Cut(got, "\n"); first != "Litmus tried #6403 on the PR build and on main (1 minute)." {
		t.Errorf("first line without the fork gap %q", first)
	}
}

func TestMainAbsentIsNewNotFailing(t *testing.T) {
	rc := func(leg string) []Receipt { return []Receipt{{Seq: 1, Leg: leg, Action: "click", AfterPath: "x.png"}} }
	for _, c := range []struct {
		name    string
		head    CheckState
		note    string
		result  string // the result line
		para    []string
		banned  []string
		partial bool
	}{
		{"head fail", CheckFail, "Clicking the Agent row closed the picker without switching.", "something broke; do not merge yet",
			[]string{"clicking the Agent row closed the picker without switching.", "main does not have this: no agent picker there", "Since main has no such feature, this is the PR's own problem."},
			[]string{"happens on main", "did not cause it", "nothing differed", "fails on main", "On main, it did not work"}, false},
		{"head partial", CheckPartial, "The list showed but the Agent row did nothing.", "",
			[]string{"the list showed but the Agent row did nothing.", "main does not have this", "Since main has no such feature, this is the PR's own problem."},
			[]string{"happens on main", "did not cause it", "fails on main"}, true},
		{"head pass", CheckPass, "The picker switched agents.", "the PR does what it says in the 1 check I finished",
			[]string{"the picker switched agents.", "This is new in the PR; main does not have this: no agent picker there."},
			[]string{"happens on main", "did not cause it", "nothing differed", "fails on main", "own problem"}, false},
	} {
		checks := []Check{
			{ID: "c1", Name: "agent-row", Expect: "clicking the Agent row switches the agent", State: c.head, Note: c.note, Receipts: rc("head")},
			{ID: "c2", Name: "agent-row", Expect: "clicking the Agent row switches the agent", State: CheckAbsent, Leg: "base", Note: "no agent picker there", Receipts: rc("base")},
		}
		got := renderComment(commentRun, commentVerdict(checks), evidence{}, commentRun.CreatedAt)
		if c.result != "" && !strings.Contains(got, "**Result: "+c.result) {
			t.Errorf("%s: result line lacks %q:\n%s", c.name, c.result, got)
		}
		for _, want := range c.para {
			if !strings.Contains(got, want) {
				t.Errorf("%s: comment lacks %q:\n%s", c.name, want, got)
			}
		}
		for _, banned := range c.banned {
			if strings.Contains(got, banned) {
				t.Errorf("%s: comment says %q:\n%s", c.name, banned, got)
			}
		}
		noSlop(t, got)
		if cellWord(string(CheckAbsent)) == "fails" {
			t.Errorf("%s: an absent cell reads as a failure", c.name)
		}
	}
}

func TestHeadAbsentReadsAsFailure(t *testing.T) {
	checks := []Check{
		{ID: "c1", Name: "agent-row", Expect: "the Agent row is there", State: CheckAbsent, Note: "The picker has no Agent row.", Receipts: []Receipt{{Seq: 1, Action: "click", AfterPath: "x.png"}}},
		{ID: "c2", Name: "agent-row", Expect: "the Agent row is there", State: CheckAbsent, Leg: "base", Note: "No picker.", Receipts: []Receipt{{Seq: 1, Leg: "base", Action: "click", AfterPath: "x.png"}}},
	}
	got := renderComment(commentRun, commentVerdict(checks), evidence{}, commentRun.CreatedAt)
	if !strings.Contains(got, "The PR build does not have this.") || !strings.Contains(got, "the picker has no Agent row.") || !strings.Contains(got, "**Result: something broke; do not merge yet") || strings.Contains(got, "This is new in the PR") {
		t.Errorf("head absent is not told as a failure:\n%s", got)
	}
}

func TestEqualFailuresNeedSameFaultInComment(t *testing.T) {
	for _, state := range []CheckState{CheckFail, CheckPartial} {
		checks := []Check{
			{ID: "c1", Name: "menu", State: state, Note: "row does nothing", Receipts: []Receipt{{Action: "click", AfterPath: evidenceFile(t)}}},
			{ID: "c2", Name: "menu", State: state, Leg: "base", Receipts: []Receipt{{Action: "click", Leg: "base", AfterPath: evidenceFile(t)}}},
		}
		v := commentVerdict(checks)
		wantOverall := CheckFail
		if state == CheckFail {
			wantOverall = CheckPartial
		}
		if v.Overall != wantOverall {
			t.Fatalf("equal %s labels: %+v", state, v)
		}
		got := renderComment(commentRun, v, evidence{}, commentRun.CreatedAt)
		want := "It also fails on main; I could not tell whether it is the same fault."
		if state == CheckPartial {
			want = "It is also partial on main; I could not tell whether it is the same fault."
		}
		if !strings.Contains(got, want) || strings.Contains(got, "the PR did not cause it") || (state == CheckFail && (strings.Contains(got, "do not merge yet") || strings.Contains(got, "fine to merge"))) {
			t.Errorf("unproven %s sameness:\n%s", state, got)
		}
		checks[1].SameFault = true
		v = commentVerdict(checks)
		if v.Overall != CheckPartial || !preExisting(checks, checks[0]) {
			t.Fatalf("same %s fault: %+v", state, v)
		}
	}
}

func TestBugCommentConsoleErrors(t *testing.T) {
	const section = "<details><summary>App console errors during this check</summary>"
	for _, tc := range []struct {
		name     string
		state    CheckState
		receipts []Receipt
		want     []string
		absent   []string
	}{
		{name: "distinct lines across receipts", state: CheckFail, receipts: []Receipt{
			{ConsoleTail: "  Error: menu failed  \n\nError: menu failed"},
			{Leg: "head", ConsoleTail: "Error: menu failed\nError: save failed"},
		}, want: []string{"Error: menu failed", "Error: save failed"}},
		{name: "no console tails", state: CheckFail, receipts: []Receipt{{}, {Leg: "head"}}},
		{name: "base receipts excluded", state: CheckFail, receipts: []Receipt{
			{Leg: "head", ConsoleTail: "Error: head failed"},
			{Leg: "base", ConsoleTail: "Error: base failed"},
		}, want: []string{"Error: head failed"}, absent: []string{"Error: base failed"}},
		{name: "only base console tail", state: CheckFail, receipts: []Receipt{{Leg: "base", ConsoleTail: "Error: base failed"}}, absent: []string{"Error: base failed"}},
		{name: "local paths reduced", state: CheckPartial, receipts: []Receipt{{ConsoleTail: "Error at /Users/x/.ao/data/run/app.js:12:3 and file:///private/tmp/source.js"}},
			want: []string{"Error at app.js:12:3 and source.js"}, absent: []string{"/Users/", "/private/"}},
		{name: "five lines at most", state: CheckFail, receipts: []Receipt{{ConsoleTail: "one\ntwo\nthree\nfour\nfive\nsix"}},
			want: []string{"one", "two", "three", "four", "five"}, absent: []string{"six"}},
		{name: "two hundred characters", state: CheckFail, receipts: []Receipt{{ConsoleTail: strings.Repeat("界", 201)}}, want: []string{strings.Repeat("界", 200)}, absent: []string{strings.Repeat("界", 201)}},
		{name: "remote URL kept", state: CheckFail, receipts: []Receipt{{ConsoleTail: "Failed https://example.com/api/request"}}, want: []string{"Failed https://example.com/api/request"}},
		{name: "windows local path", state: CheckFail, receipts: []Receipt{{ConsoleTail: `Error at C:\Users\x\app.js`}}, want: []string{"Error at app.js"}, absent: []string{`C:\Users`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := Check{ID: "head", Name: "menu opens", State: tc.state, Note: "The menu closed.", Receipts: tc.receipts}
			checks := []Check{head, {Name: head.Name, Leg: "base", State: CheckPass, Receipts: []Receipt{{Leg: "base", ConsoleTail: "other base error"}}}}
			bug := bugComment{Check: head, Number: 1, Total: 1}
			body := bugCommentBody(commentRun, Verdict{Checks: checks}, bug, "bug.png", "clip.mp4", false)
			if len(tc.want) == 0 {
				if strings.Contains(body, section) {
					t.Fatalf("unexpected console section: %s", body)
				}
			} else {
				want := section + "\n\n```\n" + strings.Join(tc.want, "\n") + "\n```\n\n</details>"
				if strings.Count(body, section) != 1 || !strings.Contains(body, want) {
					t.Fatalf("console section mismatch: %s", body)
				}
				if strings.Index(body, section) < strings.Index(body, "The menu closed.") || strings.Index(body, section) > strings.Index(body, "![") {
					t.Fatalf("console section must follow bug text and precede picture: %s", body)
				}
			}
			for _, absent := range append(tc.absent, "other base error") {
				if strings.Contains(body, absent) {
					t.Errorf("unexpected %q in %s", absent, body)
				}
			}
		})
	}
}
