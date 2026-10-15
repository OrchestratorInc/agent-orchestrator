package litmus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const fakeSHA = "0123456789abcdef0123456789abcdef01234567"

// evGit stands in for git: it records "dir|git argv", makes a clone dir on `clone`, answers ls-remote with remote
// and rev-parse with fakeSHA, notes the files staged at `add` time, and fails any argv containing a key of fail
// (once per entry in failOnce, else always).
type evGit struct {
	t        *testing.T
	remote   string
	fail     map[string]error
	failOnce map[string]error
	calls    []string
	added    []string
}

func (g *evGit) Run(_ context.Context, dir string, _ []string, name string, args ...string) ([]byte, error) {
	if filepath.Base(name) == "ffprobe" {
		return []byte("120\n"), nil
	}
	if name != "git" {
		g.t.Fatalf("unexpected command %s", name)
	}
	line := strings.Join(args, " ")
	if len(args) > 2 && args[0] == "-C" && args[2] == "for-each-ref" { // the retention pass lists the mirror's pins (`git -C <mirror> for-each-ref ...`); it is not evidence work
		return nil, nil
	}
	g.calls = append(g.calls, dir+"|"+line)
	for k, err := range g.failOnce {
		if strings.Contains(line, k) {
			delete(g.failOnce, k)
			return nil, err
		}
	}
	for k, err := range g.fail {
		if strings.Contains(line, k) {
			return nil, err
		}
	}
	switch args[0] {
	case "clone":
		dest := args[len(args)-1]
		_ = os.MkdirAll(filepath.Join(dest, ".git"), 0o750)
		_ = os.WriteFile(filepath.Join(dest, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600)
	case "ls-remote":
		return []byte(g.remote), nil
	case "add":
		files, _ := filepath.Glob(filepath.Join(dir, args[len(args)-1], "*"))
		for _, f := range files {
			g.added = append(g.added, filepath.Base(f))
		}
	case "rev-parse":
		return []byte(fakeSHA + "\n"), nil
	}
	return nil, nil
}

func (g *evGit) argv() []string { // the git argv alone, without the dir
	out := make([]string, len(g.calls))
	for i, c := range g.calls {
		_, out[i], _ = strings.Cut(c, "|")
	}
	return out
}

// sizedMedia stands in for ffmpeg: every output is a few bytes, except a GIF, whose size depends on the fps asked for.
type sizedMedia struct {
	gif   map[string]int // "fps=8" -> bytes
	err   error
	calls [][]string
}

func (m *sizedMedia) Run(_ context.Context, args ...string) error {
	m.calls = append(m.calls, args)
	if m.err != nil {
		return m.err
	}
	out, size := args[len(args)-1], 3
	if strings.HasSuffix(out, ".gif") {
		for k, n := range m.gif {
			if strings.Contains(strings.Join(args, " "), k+",") {
				size = n
			}
		}
	}
	return os.WriteFile(out, make([]byte, size), 0o600)
}

// shotCheck is a check whose receipts have after-shot files on disk, except those in missing.
func shotChecks(t *testing.T, dir string) []Check {
	t.Helper()
	rc := func(seq int, leg string, missing bool) Receipt {
		p := filepath.Join(dir, fmt.Sprintf("%d-after.png", seq))
		if !missing {
			is(t, "")(nil, os.WriteFile(p, []byte("png"), 0o600))
		}
		return Receipt{Seq: seq, Leg: leg, AfterPath: p}
	}
	return []Check{
		{ID: "c1", Name: "menu opens", State: CheckPass, Receipts: []Receipt{rc(1, "head", false), rc(2, "head", false)}},
		{ID: "c2", Name: "menu opens", State: CheckFail, Leg: "base", Receipts: []Receipt{rc(3, "base", false), rc(4, "base", true)}}, // its last shot is gone: seq 3
		{ID: "c3", Name: "last model", State: CheckPass, Receipts: []Receipt{rc(5, "head", false), rc(6, "base", false)}},             // looked at the base itself: observed
		{ID: "c4", Name: "no base", State: CheckPartial, Receipts: []Receipt{rc(7, "head", false)}},                                   // nothing on the base
		{ID: "c5", Name: "unreachable", State: CheckSkipped, Note: "needs a key"},
		{ID: "c6", Name: "base only", State: CheckFail, Leg: "base", Receipts: []Receipt{rc(8, "base", false)}},
		{ID: "c7", Name: "past the cap", State: CheckPass, Receipts: []Receipt{rc(9, "head", false)}},
	}
}

func TestPlanShotsPairsSidesAndCaps(t *testing.T) {
	heroes, extras := planShots(shotChecks(t, t.TempDir()))
	side := func(s *shot) string {
		if s == nil {
			return "-"
		}
		return fmt.Sprint(s.rcpt.Seq)
	}
	var got []string
	for _, h := range heroes {
		got = append(got, fmt.Sprintf("%s %s %s", h.label, side(h.pr), side(h.base)))
	}
	for _, x := range extras {
		got = append(got, fmt.Sprintf("extra %s %d", x.label, x.rcpt.Seq))
	}
	want := []string{"menu opens 2 3", "last model 5 6", "no base 7 -", "base only - 8", "past the cap 9 -", "extra menu opens 1"} // the unreachable check has no image; seq 4's file is gone
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planned %q\nwant %q", got, want)
	}
	var many []Check // 6 checks with a base twin each: 12 candidates, and the cap holds at 6 images
	dir := t.TempDir()
	for i := 0; i < 6; i++ {
		c := shotChecks(t, dir)[0]
		c.ID, c.Name = fmt.Sprint("c", i), fmt.Sprint("n", i)
		b := shotChecks(t, dir)[1]
		b.ID, b.Name = fmt.Sprint("b", i), c.Name
		many = append(many, c, b)
	}
	if h, x := planShots(many); len(h)+len(x) != maxShots {
		t.Fatalf("planned %d images, want the cap %d", len(h)+len(x), maxShots)
	}
}

func TestGIFFallsBackToFourFpsThenSkips(t *testing.T) {
	for name, c := range map[string]struct {
		sizes map[string]int
		made  bool
		fps   []string // the fps asked for, in order
	}{
		"8 fps fits":         {map[string]int{"fps=8": 100, "fps=4": 50}, true, []string{"fps=8"}},
		"4 fps fits":         {map[string]int{"fps=8": gifCap + 1, "fps=4": gifCap}, true, []string{"fps=8", "fps=4"}},
		"nothing fits":       {map[string]int{"fps=8": gifCap + 1, "fps=4": gifCap + 1}, false, []string{"fps=8", "fps=4"}},
		"exactly at the cap": {map[string]int{"fps=8": gifCap}, true, []string{"fps=8"}},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newSvc(t)
			m := &sizedMedia{gif: c.sizes}
			svc.media = m
			out := filepath.Join(t.TempDir(), "head.gif")
			made, err := svc.makeGIF(ctx, "in.mp4", out)
			var fps []string
			for _, a := range m.calls {
				fps = append(fps, strings.Split(a[slices.Index(a, "-filter_complex")+1], ",")[0])
			}
			if err != nil || made != c.made || !reflect.DeepEqual(fps, c.fps) || fileExists(out) != c.made {
				t.Fatalf("made %t err %v fps %q file %t", made, err, fps, fileExists(out))
			}
			a := m.calls[0]
			if !strings.Contains(a[slices.Index(a, "-filter_complex")+1], "scale=640:-2") || !strings.Contains(a[slices.Index(a, "-filter_complex")+1], "palettegen") || !slices.Contains(a, "-loop") {
				t.Fatalf("recipe: %q", a)
			}
		})
	}
}

func evRun(t *testing.T, mode string) (*Service, *evGit, Run, string) {
	t.Helper()
	svc, _ := newSvc(t)
	g := &evGit{t: t}
	svc.runner = g
	run := commentRun
	run.Evidence = mode
	is(t, "")(nil, svc.store.Save(run))
	stage := t.TempDir()
	is(t, "")(nil, os.WriteFile(filepath.Join(stage, "c1-head-1.png"), []byte("png"), 0o600))
	is(t, "")(nil, os.WriteFile(filepath.Join(stage, "head.gif"), []byte("gif"), 0o600))
	return svc, g, run, stage
}

func TestPushEvidenceCreatesAnOrphanBranchOrContinuesTheRemoteOne(t *testing.T) {
	svc, g, run, stage := evRun(t, EvidencePost)
	clone := filepath.Join(svc.store.dataDir(), "litmus", "evidence", "o__r")
	names := []string{"c1-head-1.png", "head.gif"}
	msg := "litmus: evidence for PR 7 run run_s1"

	sha, err := svc.pushEvidence(ctx, run, stage, names) // the remote has no litmus/evidence: an orphan
	wantNew := []string{
		"clone --no-checkout --depth 1 --filter=blob:none https://github.com/o/r.git " + clone,
		"ls-remote --heads origin refs/heads/litmus/evidence",
		"symbolic-ref HEAD refs/heads/litmus/evidence", "update-ref -d refs/heads/litmus/evidence", "read-tree --empty",
		"add -f -- runs/7/20261011-085600", "commit -q --no-gpg-sign --no-verify -m " + msg, "rev-parse HEAD", "push origin litmus/evidence",
	}
	if err != nil || sha != fakeSHA || !reflect.DeepEqual(g.argv(), wantNew) || !reflect.DeepEqual(g.added, names) {
		t.Fatalf("sha %q err %v\ngit: %q\nadded %q", sha, err, g.argv(), g.added)
	}
	if !strings.HasPrefix(g.calls[0], filepath.Dir(clone)+"|") || !strings.HasPrefix(g.calls[1], clone+"|") {
		t.Fatalf("the clone is made beside, and git runs in, the dedicated dir: %q", g.calls[:2])
	}
	if _, err := os.Stat(filepath.Join(clone, "runs")); !os.IsNotExist(err) {
		t.Fatalf("the staged copy stays in the clone: %v", err)
	}

	g.calls, g.added, g.remote = nil, nil, "abc\trefs/heads/litmus/evidence\n" // a later run: the clone exists and the branch is on the remote
	if _, err := svc.pushEvidence(ctx, run, stage, names); err != nil {
		t.Fatal(err)
	}
	wantOld := []string{
		"ls-remote --heads origin refs/heads/litmus/evidence",
		"fetch --depth 1 origin refs/heads/litmus/evidence", "symbolic-ref HEAD refs/heads/litmus/evidence", "reset -q --mixed FETCH_HEAD",
		"add -f -- runs/7/20261011-085600", "commit -q --no-gpg-sign --no-verify -m " + msg, "rev-parse HEAD", "push origin litmus/evidence",
	}
	if !reflect.DeepEqual(g.argv(), wantOld) {
		t.Fatalf("git: %q", g.argv())
	}
	for _, c := range g.calls {
		if strings.Contains(c, "checkout") || strings.Contains(c, "push -f") || strings.Contains(c, "--force") {
			t.Fatalf("evidence must never check files out or force-push: %q", c)
		}
	}
}

func TestPushEvidenceRetriesOnceAfterGhAuthSetupGit(t *testing.T) {
	svc, g, run, stage := evRun(t, EvidencePost)
	gh := &commentGH{t: t, login: "bot"}
	svc.gh = gh
	g.failOnce = map[string]error{"push": errors.New("git push: exit status 128\nfatal: could not read Username for 'https://github.com': terminal prompts disabled")}
	if _, err := svc.pushEvidence(ctx, run, stage, []string{"head.gif"}); err != nil {
		t.Fatal(err)
	}
	if pushes := strings.Count(strings.Join(g.argv(), "\n"), "push origin"); pushes != 2 || !gh.has("auth setup-git") {
		t.Fatalf("pushes %d, gh %q", pushes, gh.calls)
	}

	svc, g, run, stage = evRun(t, EvidencePost) // a permission failure is not an auth failure: no setup, no retry
	svc.gh = &commentGH{t: t, login: "bot"}
	g.fail = map[string]error{"push": errors.New("git push: exit status 128\nremote: Permission to o/r.git denied to bot.\nfatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 403")}
	_, err := svc.pushEvidence(ctx, run, stage, []string{"head.gif"})
	if err == nil || err.Error() != "git push: fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 403" || strings.Count(strings.Join(g.argv(), "\n"), "push origin") != 1 {
		t.Fatalf("err %v, git %q", err, g.argv())
	}
}

// evSubmit is a submit with after-shots and no recordings.
func evSubmit(t *testing.T, mode, private string) (*Service, *evGit, *commentGH, string) {
	t.Helper()
	svc, _, runDir, _ := submitSvc(t)
	svc.media = &sizedMedia{}
	g, gh := &evGit{t: t}, &commentGH{t: t, login: "bot", private: private}
	svc.runner, svc.gh = g, gh
	_, err := svc.update("run_s1", func(r *Run) { r.Evidence, r.Repo, r.PRNumber = mode, "o/r", 7 })
	is(t, "")(nil, err)
	return svc, g, gh, runDir
}

// evidenceBug turns the menu comparison into a PR regression and supplies real
// PNGs so the crop is exercised without ffmpeg or a browser.
func evidenceBug(t *testing.T, svc *Service) {
	t.Helper()
	checks, err := svc.store.Checks("run_s1")
	is(t, "")(nil, err)
	checks[0].State, checks[0].Note = CheckFail, "The menu closed after the click."
	checks[1].State, checks[1].Note = CheckPass, "The menu stayed open."
	for _, check := range checks {
		for _, receipt := range check.Receipts {
			if receipt.AfterPath != "" {
				whitePNG(t, receipt.AfterPath, 800, 600)
			}
		}
	}
	is(t, "")(nil, svc.store.SaveChecks("run_s1", checks))
}

func TestSubmitPublishesEvidenceAndEmbedsIt(t *testing.T) {
	for _, private := range []string{"false", "true"} {
		t.Run("private="+private, func(t *testing.T) {
			svc, g, gh, runDir := evSubmit(t, EvidencePost, private)
			// A published check clip now requires a recording, rather than stills.
			_, err := svc.update("run_s1", func(run *Run) {
				run.Legs[0].RecordStartedAt = svc.clock()
				run.Legs[0].RecordStoppedAt = svc.clock().Add(time.Minute)
			})
			is(t, "")(nil, err)
			path := recordingPath(runDir, "head")
			is(t, "")(nil, os.MkdirAll(filepath.Dir(path), 0o750))
			is(t, "")(nil, os.WriteFile(path, []byte("mp4"), 0o600))
			evidenceBug(t, svc)
			v, err := svc.Submit(ctx, "s1", "", "")
			run, _ := svc.store.Get("run_s1")
			if err != nil || v.Overall != CheckFail || v.CommentState != "posted" || v.EvidenceState != "published" || v.EvidenceCommit != fakeSHA || run.EvidenceState != "published" {
				t.Fatalf("verdict %+v run %+v err %v", v, run, err)
			}
			wantVis := map[string]string{"false": "public", "true": "private"}[private]
			if run.RepoVisibility != wantVis || !gh.has("repo view o/r --json isPrivate") {
				t.Fatalf("visibility %q gh %q", run.RepoVisibility, gh.calls)
			}
			body, _ := os.ReadFile(filepath.Join(runDir, "comment.md"))
			imageURL := evidenceURL(run.Repo, fakeSHA, evidencePath(run)+"/bug-1.png", private == "true")
			for _, want := range []string{"Bug 1 of 1:", "The menu closed after the click.", "(" + imageURL + ")", "<details><summary>Recording for menu opens</summary>", vid1, litmusCommentMarker()} {
				if !strings.Contains(string(body), want) {
					t.Errorf("comment lacks %q:\n%s", want, body)
				}
			}
			if gh.field(0, "body") != string(body) || strings.Count(string(body), "![") != 1 || gh.uploads != 1 {
				t.Fatalf("comment or clip count wrong: %s, uploads %d", body, gh.uploads)
			}
			if !slices.Contains(g.added, "bug-1.png") {
				t.Fatalf("crop not published: %q", g.added)
			}
			for _, f := range g.added {
				exists(t, filepath.Join(runDir, "evidence", f))
			}
			if log, _ := os.ReadFile(filepath.Join(runDir, "comment.log")); !strings.Contains(string(log), "evidence published at "+fakeSHA) {
				t.Fatalf("comment.log = %q", log)
			}
		})
	}
}

func TestSubmitPostsWithoutImagesWhenPublishingFails(t *testing.T) {
	svc, g, gh, runDir := evSubmit(t, EvidencePost, "false")
	evidenceBug(t, svc)
	clone := filepath.Join(filepath.Dir(svc.store.root), "evidence", "o__r")
	g.fail = map[string]error{"push": errors.New("git push origin litmus/evidence: exit status 128\nremote: Permission to o/r.git denied to bot.\nfatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 403")}
	v, err := svc.Submit(ctx, "s1", "", "")
	run, _ := svc.store.Get("run_s1")
	const reason = "git push: fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 403"
	if err != nil || v.CommentState != "posted" || v.EvidenceState != "failed:"+reason || v.EvidenceCommit != "" || run.State != RunSubmitted {
		t.Fatalf("a push failure failed the submit: %+v, %v", v, err)
	}
	body := gh.field(0, "body")
	if strings.Contains(body, "![") || strings.Contains(body, "(bug-1.png)") || !strings.Contains(body, "The comparison image was not available in the published evidence.") {
		t.Fatalf("comment: %s", body)
	}
	if log, _ := os.ReadFile(filepath.Join(runDir, "comment.log")); !strings.Contains(string(log), "Permission to o/r.git denied") {
		t.Fatalf("comment.log = %q", log)
	}
	if _, err := os.Stat(clone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed publish retained its clone: %v", err)
	}

	// Crops use Go image processing, so they remain useful when ffmpeg is absent.
	svc, _, gh, _ = evSubmit(t, EvidencePost, "false")
	evidenceBug(t, svc)
	svc.media = &sizedMedia{err: errors.New("ffmpeg: not found")}
	v, err = svc.Submit(ctx, "s1", "", "")
	if err != nil || v.CommentState != "posted" || v.EvidenceState != "published" || gh.uploads != 0 || !strings.Contains(gh.field(0, "body"), "The check recording was not available.") {
		t.Fatalf("verdict %+v err %v uploads %d", v, err, gh.uploads)
	}
}

func TestNoEvidenceAndOlderRunsPublishNothing(t *testing.T) {
	for name, mode := range map[string]string{"--no-evidence": EvidenceOff, "older run": ""} {
		t.Run(name, func(t *testing.T) {
			svc, g, gh, _ := evSubmit(t, mode, "false")
			v, err := svc.Submit(ctx, "s1", "", "")
			run, _ := svc.store.Get("run_s1")
			body := gh.field(0, "body")
			if err != nil || v.CommentState != "posted" || v.EvidenceState != "off" || run.EvidenceState != "off" || len(g.calls) != 0 || gh.has("repo view") ||
				strings.Contains(body, "![") || strings.Contains(body, "**Notes**") || strings.Contains(body, "evidence files") {
				t.Fatalf("verdict %+v git %q gh %q\n%s", v, g.calls, gh.calls, body)
			}
		})
	}
	svc, g, _, _ := evSubmit(t, EvidencePost, "false") // no comment is posted, so no evidence is published either
	_, err := svc.update("run_s1", func(r *Run) { r.Comment = CommentOff })
	is(t, "")(nil, err)
	if v, err := svc.Submit(ctx, "s1", "", ""); err != nil || v.EvidenceState != "off" || len(g.calls) != 0 {
		t.Fatalf("verdict %+v err %v git %q", v, err, g.calls)
	}
}

func TestStartNoEvidenceSetsTheRunsMode(t *testing.T) {
	ids := []string{"s8", "s9"}
	svc, _ := newSvc(t)
	svc.WithSpawner(func(context.Context, SpawnRequest) (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	for _, c := range []struct{ session, mode string }{{"s8", EvidencePost}, {"s9", EvidenceOff}} {
		started, err := svc.Start(ctx, StartRequest{PRURL: prURL, NoEvidence: c.mode == EvidenceOff})
		run, ierr := svc.Intake(ctx, started.SessionID, prURL)
		if err != nil || ierr != nil || started.SessionID != c.session || run.Evidence != c.mode || run.Comment != CommentPost {
			t.Fatalf("%s: started %+v run %+v errs %v %v", c.session, started, run, err, ierr)
		}
	}
}

// goldenRun is a two-check run like run 10's: the arrows check has a shot on each side (one composite and one extra
// screenshot), the model check only on the PR build (one single image), and the tester skipped main for it.
func goldenRun(t *testing.T, dir string) (Run, Verdict) {
	t.Helper()
	shot := func(name string) string {
		p := filepath.Join(dir, name)
		is(t, "")(nil, os.WriteFile(p, []byte("png"), 0o600))
		return p
	}
	click := func(seq int, leg, name, png string) Receipt {
		return Receipt{Seq: seq, Leg: leg, Action: "click", Args: []string{"@e4"}, TargetRole: "button", TargetName: name, AfterPath: shot(png)}
	}
	run := commentRun
	run.SessionID, run.Areas = "ao-8", []Area{{Name: "Shell sidebar", Files: 3, Stems: []string{"Sidebar", "useHistory"}}, {Name: "Tests", Files: 2}}
	checks := []Check{
		{ID: "c1", Name: "arrows-visible", Expect: "the Go back and Go forward arrows show next to the brand", State: CheckPass, Note: "Head: both buttons are there, and Go back works",
			Receipts: []Receipt{click(2, "head", "Go back", "2.png"), click(4, "head", "Go back", "4.png")}},
		{ID: "c2", Name: "arrows-visible", Expect: "the Go back and Go forward arrows show next to the brand", State: CheckFail, Leg: "base", Note: "Base: there are no arrows at all",
			Receipts: []Receipt{click(5, "base", "Brand", "5.png")}},
		{ID: "c3", Name: "last-model", Expect: "the last model picked is remembered after reopening", State: CheckPass, Note: "Head: the model picker in the sidebar still showed the last model",
			Receipts: []Receipt{click(6, "head", "", "6.png")}},
	}
	v := commentVerdict(checks)
	v.SkipBase = "main has no setting for the last model"
	return run, v
}

func TestRenderCommentWithImagesGolden(t *testing.T) {
	const base = "https://raw.githubusercontent.com/o/r/" + fakeSHA + "/runs/7/20261011-090000/"
	svc, _ := newSvc(t)
	media := &sizedMedia{gif: map[string]int{}}
	svc.media = media
	dir := filepath.Join(svc.store.root, "run_s1", "evidence")
	is(t, "")(nil, os.MkdirAll(dir, 0o750))
	for _, leg := range []string{"head", "base"} {
		is(t, "")(nil, os.WriteFile(cutPath(filepath.Dir(dir), leg), []byte("mp4"), 0o600))
	}
	shots := t.TempDir()
	run, v := goldenRun(t, shots)
	items, limits, err := svc.stageEvidence(ctx, run, v, dir)
	if err != nil || len(limits) != 0 {
		t.Fatalf("stage: limits %q err %v", limits, err)
	}
	for i := range items {
		items[i].url = evidenceURL("o/r", fakeSHA, "runs/7/20261011-090000/"+items[i].file, false)
	}
	now := run.CreatedAt.Add(2*time.Minute + 11*time.Second)
	videos := []vidItem{{leg: "head", url: "https://github.com/user-attachments/assets/11111111-2222-4333-8444-555555555555", span: 79 * time.Second},
		{leg: "base", url: "https://github.com/user-attachments/assets/66666666-7777-4888-8999-aaaaaaaaaaaa", span: 15 * time.Second}}
	got := renderComment(run, v, evidence{state: "published", commit: fakeSHA, items: items, videos: videos}, now)
	want := strings.ReplaceAll(`Litmus tried #7 on the PR build and on main (2 minutes).

**Result: the PR does what it says in the 2 checks I finished. One check only ran on the PR build.**

I compared 1 check on main; 1 was not run there: last model.

I checked that the Go back and Go forward arrows show next to the brand. On the PR build, both buttons are there, and Go back works. On main, there are no arrows at all.

Next I checked that the last model picked is remembered after reopening. On the PR build, the model picker in the sidebar still showed the last model.

![base branch on the left, PR build on the right]({base}the-go-back-and-go-forward-arrows-show-main-vs-pr.png)
Main on the left: there are no arrows at all. PR build on the right: both buttons are there, and Go back works.

![PR build]({base}the-last-model-picked-is-remembered-pr.png)
PR build: the model picker in the sidebar still showed the last model.

Not checked by this run: tests (2 files).

<details><summary>Recordings and every screenshot</summary>

PR build, 1 min 19 s

https://github.com/user-attachments/assets/11111111-2222-4333-8444-555555555555

Main, 15 s

https://github.com/user-attachments/assets/66666666-7777-4888-8999-aaaaaaaaaaaa

PR build, the moments around each step
![PR build recording]({base}pr.gif)

Main, the moments around each step
![base branch recording]({base}main.gif)

Step 2, PR build: click "Go back"
![]({base}the-go-back-and-go-forward-arrows-show-pr-step-2.png)

</details>

<!-- litmus:comment o/r#7 -->`, "{base}", base)
	if got != want {
		t.Fatalf("comment:\n%s\nwant:\n%s", got, want)
	}
	noSlop(t, got)
	composite := []string{"-y", "-i", filepath.Join(shots, "5.png"), "-i", filepath.Join(shots, "4.png"),
		"-filter_complex", "[0:v]scale=640:-2[l];[1:v]scale=640:-2[r];[l][r]xstack=inputs=2:layout=0_0|w0_0:fill=white", "-frames:v", "1", filepath.Join(dir, "the-go-back-and-go-forward-arrows-show-main-vs-pr.png")}
	if !reflect.DeepEqual(media.calls[0], composite) {
		t.Fatalf("composite argv:\n%q\nwant\n%q", media.calls[0], composite)
	}
}

func TestImageLinesDoNotCountAgainstTheCap(t *testing.T) {
	long := strings.Repeat("n", 700)
	checks := []Check{{ID: "c1", Name: "a", State: CheckPartial, Note: long, Receipts: []Receipt{{Seq: 1}}}}
	var items []evItem
	for i := 0; i < 6; i++ {
		items = append(items, evItem{alt: "c1 head partial", caption: "*c1 a*", url: "https://raw.githubusercontent.com/o/r/" + fakeSHA + "/runs/7/20261011-090000/" + strings.Repeat("x", 1000) + ".png"})
	}
	ev := evidence{state: "published", items: items}
	got := renderComment(commentRun, commentVerdict(checks), ev, commentRun.CreatedAt)
	if len(got) <= commentCap || len(got)-ev.imageChars() > commentCap || strings.Count(got, "![") != 6 || !strings.Contains(got, "On the PR build, "+strings.Repeat("n", 600)+"...") {
		t.Fatalf("len %d, images %d: the images must stay and the text fit the cap", len(got), ev.imageChars())
	}
	for i := 0; i < 400; i++ { // text alone cannot fit: the images go, the cap and the marker hold
		checks = append(checks, checks[0])
	}
	got = renderComment(commentRun, commentVerdict(checks), ev, commentRun.CreatedAt)
	if len(got) > commentCap || strings.Contains(got, "![") || !strings.HasSuffix(got, commentMarker("o/r", 7)) {
		t.Fatalf("hard cap: len %d", len(got))
	}
}

func TestEvidenceFailureReasonHidesLocalPaths(t *testing.T) {
	svc := &Service{store: NewStore("/data/ao")}
	ev := svc.evidenceFailed(Run{ID: "run_s1"}, errors.New("git clone: fatal: could not create work tree dir '/data/ao/litmus/evidence/o__r': denied"))
	if strings.Contains(ev.state, "/data/ao") || strings.Contains(strings.Join(ev.limits, "\n"), "/data/ao") {
		t.Fatalf("path leaked: %q %q", ev.state, ev.limits)
	}
	if !strings.Contains(ev.state, "<data dir>/litmus/evidence/o__r") {
		t.Fatalf("state = %q", ev.state)
	}
}

type mediaFunc func(args []string) error

func (f mediaFunc) Run(_ context.Context, args ...string) error { return f(args) }

// stageGolden stages goldenRun's evidence with media and returns the run, the verdict and what was staged.
func stageGolden(t *testing.T, media mediaFunc, edit func(*Run, *Verdict)) (Run, Verdict, []evItem, []string, error) {
	t.Helper()
	svc, _ := newSvc(t)
	svc.media = media
	dir := filepath.Join(svc.store.root, "run_s1", "evidence")
	is(t, "")(nil, os.MkdirAll(dir, 0o750))
	run, v := goldenRun(t, t.TempDir())
	if edit != nil {
		edit(&run, &v)
	}
	items, limits, err := svc.stageEvidence(ctx, run, v, dir)
	return run, v, items, limits, err
}

func TestCompositeFallsBackToThePRBuildShot(t *testing.T) {
	_, _, items, limits, err := stageGolden(t, func(a []string) error {
		if strings.Contains(strings.Join(a, " "), "xstack") {
			return errors.New("ffmpeg: Input 1 height 378 does not match input 0 height 250")
		}
		return os.WriteFile(a[len(a)-1], []byte("png"), 0o600)
	}, nil)
	if err == nil || len(limits) != 1 || !strings.HasPrefix(limits[0], "I could not build the side-by-side image for the Go back and Go forward") || !strings.HasSuffix(limits[0], ", so it shows the PR build only.") {
		t.Fatalf("limits %q err %v", limits, err)
	}
	if items[0].file != "the-go-back-and-go-forward-arrows-show-pr.png" || items[0].alt != "PR build" || items[0].caption != "PR build: both buttons are there, and Go back works." {
		t.Fatalf("the lead image is the PR build shot: %+v", items[0])
	}
}

func TestTesterAndPageTextCannotAddImagesOrMentionsOrLeakTypedText(t *testing.T) {
	run, v, items, _, err := stageGolden(t, func(a []string) error { return os.WriteFile(a[len(a)-1], []byte("png"), 0o600) }, func(r *Run, v *Verdict) {
		r.BaseRef = "@evil"
		hostile := "x](https://evil.example/p.png) @user <b>"
		v.Checks[0].Expect, v.Checks[1].Expect = hostile, hostile
		v.Checks[0].Receipts[0].Action, v.Checks[0].Receipts[0].Args = "fill", []string{"@e9", "hunter2"} // the extra screenshot
		v.Checks[0].Receipts[0].TargetName = "![y](https://evil.example/q.png) @user"
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		items[i].url = evidenceURL("o/r", fakeSHA, "runs/7/20261011-090000/"+items[i].file, false)
	}
	got := renderComment(run, v, evidence{state: "published", commit: fakeSHA, items: items}, run.CreatedAt)
	safe := regexp.MustCompile(`^!\[[A-Za-z ,]*\]\(https://raw\.githubusercontent\.com/o/r/[0-9a-f]{40}/runs/7/20261011-090000/[a-z0-9.-]+\)$`)
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "![") && !safe.MatchString(line) {
			t.Errorf("an image line carries tester or page text: %q", line)
		}
	}
	if strings.Count(got, "![") != len(items) || strings.Contains(got, "hunter2") || strings.Contains(got, "@user") || strings.Contains(got, "@evil") || strings.Contains(got, "<b>") {
		t.Fatalf("%d images for %d items:\n%s", strings.Count(got, "!["), len(items), got)
	}
	if !strings.Contains(got, "Step 2, PR build: type text into ") {
		t.Fatalf("the typed value is not replaced:\n%s", got)
	}
}

func TestSideSlugNeverCollidesOrComesUpEmpty(t *testing.T) {
	for base, want := range map[string]string{"": "main", "release/1.0": "release-1-0", "pr": "base", "!!!": "base"} {
		if got := sideSlug(Run{BaseRef: base}, "base"); got != want || sideSlug(Run{BaseRef: base}, "head") != "pr" {
			t.Errorf("base %q: %q, want %q", base, got, want)
		}
	}
}

const (
	vid1 = "https://github.com/user-attachments/assets/00000001-aaaa-4bbb-8ccc-dddddddddddd"
	vid2 = "https://github.com/user-attachments/assets/00000002-aaaa-4bbb-8ccc-dddddddddddd"
)

// recordLegs gives run a finished recording per leg on disk: the PR build's 1 min 19 s and main's 15 s.
func recordLegs(t *testing.T, svc *Service, runID string) string {
	t.Helper()
	t0 := svc.clock()
	_, err := svc.update(runID, func(r *Run) {
		r.Legs = []Leg{{Name: "head", State: "running", RecordStartedAt: t0, RecordStoppedAt: t0.Add(79 * time.Second)},
			{Name: "base", State: "running", RecordStartedAt: t0, RecordStoppedAt: t0.Add(15 * time.Second)}}
	})
	is(t, "")(nil, err)
	runDir := filepath.Join(svc.store.root, runID)
	for _, leg := range []string{"head", "base"} {
		p := recordingPath(runDir, leg)
		is(t, "")(nil, os.MkdirAll(filepath.Dir(p), 0o750))
		is(t, "")(nil, os.WriteFile(p, []byte("mp4"), 0o600))
	}
	return runDir
}

func TestSpanText(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "", 400 * time.Millisecond: "", 15 * time.Second: "15 s", time.Minute: "1 min", 79 * time.Second: "1 min 19 s", 119600 * time.Millisecond: "2 min"} {
		if got := spanText(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestUnattachedVideoSaysSoAndPointsAtTheGIFWhenThereIsOne(t *testing.T) {
	gif := evItem{file: "pr.gif", kind: kindGIF, alt: "PR build recording", caption: "PR build, the moments around each step", url: "https://raw.githubusercontent.com/o/r/" + fakeSHA + "/runs/7/20261011-090000/pr.gif"}
	videos := []vidItem{{leg: "head"}, {leg: "base"}}
	got := renderComment(commentRun, commentVerdict(commentChecks(t)), evidence{state: "published", items: []evItem{gif}, videos: videos}, commentRun.CreatedAt)
	const want = "<details><summary>Recordings and every screenshot</summary>\n\nPR build video could not be attached; the GIF below shows the steps.\n\nMain video could not be attached.\n\nPR build, the moments around each step\n![PR build recording]("
	if !strings.Contains(got, want) {
		t.Fatalf("comment:\n%s", got)
	}
	noSlop(t, got)
	if got = renderComment(commentRun, commentVerdict(commentChecks(t)), evidence{videos: videos[:1]}, commentRun.CreatedAt); !strings.Contains(got, "PR build video could not be attached.\n\n</details>") || strings.Contains(got, "GIF") {
		t.Fatalf("no GIF to point at:\n%s", got)
	}
}

// recSubmit is evSubmit with a finished recording per leg, so the submit cuts, makes GIFs and uploads the videos.
func recSubmit(t *testing.T) (*Service, *commentGH, string) {
	t.Helper()
	svc, _, gh, _ := evSubmit(t, EvidencePost, "false")
	runDir := recordLegs(t, svc, "run_s1") // the base is running, so every head check needs a base result: c3 is recorded as skipped there
	checks, err := svc.store.Checks("run_s1")
	is(t, "")(nil, err)
	is(t, "")(nil, svc.store.SaveChecks("run_s1", append(checks, Check{ID: "c4", Name: "last model", Expect: "scrolls", State: CheckSkipped, Leg: "base", Note: "main has no such setting"})))
	return svc, gh, runDir
}

func TestSubmitUploadsOnlyBugClipsAndKeepsPassingSummaryShort(t *testing.T) {
	svc, gh, _ := recSubmit(t)
	v, err := svc.Submit(ctx, "s1", "", "")
	run, _ := svc.store.Get("run_s1")
	body := gh.field(0, "body")
	if err != nil || v.CommentState != "posted" || run.VideoState != "off" || gh.uploads != 0 || strings.Contains(body, "<details>") || strings.Contains(body, "![") {
		t.Fatalf("passing run uploaded evidence: %+v err %v body %s", run, err, body)
	}

	svc, gh, runDir := recSubmit(t)
	evidenceBug(t, svc)
	v, err = svc.Submit(ctx, "s1", "", "")
	run, _ = svc.store.Get("run_s1")
	body = gh.field(0, "body")
	if err != nil || v.CommentState != "posted" || run.VideoState != "attached" || !reflect.DeepEqual(run.VideoURLs, map[string]string{"menu opens": vid1}) || gh.uploads != 1 {
		t.Fatalf("verdict %+v run %+v err %v", v, run, err)
	}
	if !strings.Contains(body, vid1) || strings.Contains(body, vid2) || strings.Contains(body, ".gif") {
		t.Fatalf("comment includes another check's video: %s", body)
	}
	for _, args := range gh.argv {
		if strings.HasPrefix(strings.Join(args, " "), upload) {
			if i := slices.Index(args, "--input"); i < 0 || args[i+1] != checkClipPath(runDir, "c1") {
				t.Fatalf("uploaded the full recording instead of the check clip: %q", args)
			}
		}
	}

	svc, gh, _ = recSubmit(t)
	evidenceBug(t, svc)
	gh.fail = "uploads.github.com"
	_, err = svc.Submit(ctx, "s1", "", "")
	run, _ = svc.store.Get("run_s1")
	body = gh.field(0, "body")
	if err != nil || run.CommentState != "posted" || run.VideoState != "failed:HTTP 403: Resource not accessible" || len(run.VideoURLs) != 0 || !strings.Contains(body, "The check recording was not available.") || strings.Contains(body, "../clips/") {
		t.Fatalf("run %+v err %v body %s", run, err, body)
	}

	svc, gh, _ = recSubmit(t)
	evidenceBug(t, svc)
	_, err = svc.update("run_s1", func(r *Run) { r.Evidence = EvidenceOff })
	is(t, "")(nil, err)
	_, err = svc.Submit(ctx, "s1", "", "")
	run, _ = svc.store.Get("run_s1")
	body = gh.field(0, "body")
	if err != nil || run.VideoState != "off" || gh.uploads != 0 || strings.Contains(body, "user-attachments") || strings.Contains(body, "../clips/") || strings.Contains(body, "![") {
		t.Fatalf("run %+v err %v uploads %d body %s", run, err, gh.uploads, body)
	}
}
