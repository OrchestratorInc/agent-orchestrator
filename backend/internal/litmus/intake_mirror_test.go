package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// pr6588 is the real #6588 as recorded: 298 changed files, which gh lists 100 of and GitHub refuses to diff. The file
// list and counts were taken once from the pulls API and shaped as git prints them (-z).
const (
	head6588 = "200160b490e25a3fe4d36b0eaca8fb4df41faba2"
	base6588 = "f22e74d270f7877a63d0fe00695e9d72baff4cd5"
	// fork6588 is where the PR branched, 9 commits before main's tip: the base build is this commit.
	fork6588 = "fca1b72c00000000000000000000000000000000"
)

func replay6588(t *testing.T) (*Service, *fakeGH, *fakeRunner) {
	t.Helper()
	svc, _ := newSvc(t)
	gh, f := svc.gh.(*fakeGH), svc.runner.(*fakeRunner)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	gh.out["pr view"] = []byte(read("pr6588.json"))
	gh.err["pr diff"] = errors.New("HTTP 406: Sorry, the diff exceeded the maximum number of files (300)")
	patch := func(name string, added int, extra string) string {
		return fmt.Sprintf("diff --git a/%[1]s b/%[1]s\n%[2]s--- a/%[1]s\n+++ b/%[1]s\n@@ -1 +1 @@\n", name, extra) + strings.Repeat("+line\n", added)
	}
	f.gitOut["merge-base "+base6588] = fork6588 + "\n"
	f.gitOut["rev-list --count "+fork6588+".."+base6588] = "9\n"
	f.gitOut["diff --numstat"], f.gitOut["diff --name-status"] = read("pr6588.numstat"), read("pr6588.names")
	f.gitOut["diff --patch"] = patch("frontend/package-lock.json", 400, "") + patch("backend/internal/adapters/agent/codex/codex.go", 20, "") +
		patch("frontend/src/renderer/components/chat/AgentSwitchPicker.tsx", 30, "new file mode 100644\n") + patch("backend/internal/old.go", 0, "deleted file mode 100644\n")
	return svc, gh, f
}

func TestIntakeReplays6588WithoutAskingGitHubForTheDiff(t *testing.T) {
	svc, gh, f := replay6588(t)
	run, err := svc.Intake(ctx, "s1", "https://github.com/OrchestratorInc/agent-orchestrator/pull/6588")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(gh.calls, func(c string) bool { return strings.HasPrefix(c, "pr diff") }) {
		t.Fatalf("gh was asked for the diff: %q", gh.calls)
	}
	if run.HeadSHA != head6588 || run.BaseSHA != fork6588 || f.count("update-ref refs/litmus/keep/"+head6588) != 1 || f.count("update-ref refs/litmus/keep/"+base6588) != 1 || f.count("update-ref refs/litmus/keep/"+fork6588) != 1 {
		t.Fatalf("run %+v (pins: %d %d %d)", run, f.count("update-ref refs/litmus/keep/"+head6588), f.count("update-ref refs/litmus/keep/"+base6588), f.count("update-ref refs/litmus/keep/"+fork6588))
	}
	if want := []string{"The PR branched from main 9 commits ago; 'main' in this report is that branch point (fca1b72c), not today's main."}; !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q, want %q", run.Gaps, want)
	}
	if files := readRunFile(t, svc, run, "files.txt"); strings.Count(files, "\n") != 299 || !strings.HasPrefix(files, "# status\tadded\tdeleted\tpath") { // a header and 298 files
		t.Fatalf("files.txt has %d lines", strings.Count(files, "\n"))
	}
}

func TestIntakeTakesTheMovedHeadFromThePullRef(t *testing.T) {
	svc, _ := newSvc(t)
	f := svc.runner.(*fakeRunner)
	moved := strings.Repeat("ab", 20)
	f.gitErr["fetch --no-tags origin "+head6394+" "+base6394] = errors.New("fatal: remote error: upload-pack: not our ref " + head6394)
	f.gitOut["rev-parse FETCH_HEAD"] = moved + "\n"
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || run.HeadSHA != moved || run.BaseSHA != base6394 {
		t.Fatalf("run %+v err %v", run, err)
	}
	if f.count("fetch --no-tags origin refs/pull/6394/head") != 1 || f.count("fetch --no-tags origin "+base6394) != 1 || f.count("update-ref refs/litmus/keep/"+moved+" "+moved) != 1 {
		t.Fatalf("calls %q", f.calls)
	}
	if want := []string{"The PR's commit changed while I was reading it; I tested abababab."}; !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q", run.Gaps)
	}
}

func TestIntakeStartsFromGitAloneWhenGitHubIsDown(t *testing.T) {
	fails := map[string]error{"timeout": context.DeadlineExceeded}
	for name, msg := range map[string]string{ // gh's words, as it prints them
		"rate limit": "HTTP 403: API rate limit exceeded for user ID 1.",
		"logged out": "To get started with GitHub CLI, please run:  gh auth login",
		"no gh":      `exec: "gh": executable file not found in $PATH`,
		"504":        "HTTP 504: Gateway Timeout",
	} {
		fails[name] = errors.New(msg)
	}
	for name, fail := range fails {
		t.Run(name, func(t *testing.T) {
			svc, _ := newSvc(t)
			gh, f := svc.gh.(*fakeGH), svc.runner.(*fakeRunner)
			gh.err["pr view"] = fail
			f.gitOut["ls-remote --symref"] = "ref: refs/heads/main\tHEAD\n" + base6394 + "\tHEAD\n" + head6394 + "\trefs/pull/6394/head\n"
			run, err := svc.Intake(ctx, "s1", prURL)
			if err != nil || run.HeadSHA != head6394 || run.BaseSHA != base6394 || run.BaseRef != "main" || run.Title != "" {
				t.Fatalf("run %+v err %v", run, err)
			}
			if len(run.Gaps) != 1 || !strings.HasPrefix(run.Gaps[0], gapNoWords+" (") || !strings.HasSuffix(run.Gaps[0], "), so I tested without its title, body, comments or linked issue and compared against the tip of main.") || !run.noWords() {
				t.Fatalf("gaps %q", run.Gaps)
			}
			if ask, err := svc.mustAsk(run, Verdict{Result: "pass"}); err != nil || !ask {
				t.Fatalf("a run without PR text must hold its comment: ask=%t error=%v", ask, err)
			}
			if ask, err := svc.mustAsk(run, Verdict{Result: "blocked"}); err != nil || ask {
				t.Fatalf("blocked has no comment to hold: ask=%t error=%v", ask, err)
			}
		})
	}
	// no pull ref on GitHub: the PR does not exist
	svc, _ := newSvc(t)
	svc.gh.(*fakeGH).err["pr view"] = errors.New("HTTP 502")
	svc.runner.(*fakeRunner).gitOut["ls-remote --symref"] = "ref: refs/heads/main\tHEAD\n" + base6394 + "\tHEAD\n"
	is(t, "TESTING_GH_NOT_FOUND")(svc.Intake(ctx, "s1", prURL))
	// and git cannot reach GitHub either: nothing can be tested
	svc, _ = newSvc(t)
	svc.gh.(*fakeGH).err["pr view"] = errors.New("HTTP 502")
	svc.runner.(*fakeRunner).gitErr["ls-remote --symref"] = errors.New("fatal: unable to access")
	is(t, "TESTING_COMMITS_MISSING")(svc.Intake(ctx, "s1", prURL))
}

func TestIntakeCutsThePatchAtTheCapAndKeepsTheFileList(t *testing.T) {
	svc, _ := newSvc(t)
	f := svc.runner.(*fakeRunner)
	line := "+" + strings.Repeat("x", 99) + "\n"
	f.gitOut["diff --patch"] = "diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@ -1 +1 @@\n" + strings.Repeat(line, maxPatch/len(line)+100)
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil {
		t.Fatal(err)
	}
	patch := readRunFile(t, svc, run, "diff.patch")
	if len(patch) > maxPatch+300 || !strings.HasSuffix(patch, " -- <path>` in the PR build checkout shows the rest]\n") || !strings.Contains(patch, "\n[diff.patch cut at 5.0 MB; `git diff ") {
		t.Fatalf("diff.patch: %d bytes, ends %q", len(patch), patch[max(len(patch)-200, 0):])
	}
	if want := []string{"The PR's patch is longer than 5.0 MB; I was shown the first 5.0 MB of it and read the rest with git (files.txt lists every file)."}; !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q", run.Gaps)
	}
}

func TestIntakeReadsTheLinkedIssueAsAGapOnTheClosingIssuesPath(t *testing.T) {
	svc, _ := newSvc(t)
	gh := svc.gh.(*fakeGH)
	var pr map[string]any
	_ = json.Unmarshal(gh.out["pr view"], &pr)
	pr["closingIssuesReferences"] = []map[string]any{{"number": 7, "repository": map[string]any{"name": "agent-orchestrator", "owner": map[string]any{"login": "OrchestratorInc"}}}, {"number": 8, "repository": map[string]any{"name": "other", "owner": map[string]any{"login": "acme"}}}}
	gh.out["pr view"], _ = json.Marshal(pr)
	notFound := "HTTP 404: missing reference"
	gh.err["api repos/"+DefaultRepo+"/issues/7"] = errors.New(notFound)
	gh.err["api repos/acme/other/issues/8"] = errors.New(notFound)
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || len(run.Issues) != 0 {
		t.Fatalf("run %+v err %v", run, err)
	}
	if want := []string{"Could not read reference https://github.com/" + DefaultRepo + "/issues/7: " + notFound, "Could not read reference https://github.com/acme/other/issues/8: " + notFound}; !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q", run.Gaps)
	}
}

func TestIntakeFileListFailureIsOneGapNotTwo(t *testing.T) {
	svc, _ := newSvc(t)
	svc.runner.(*fakeRunner).gitErr["diff --numstat"] = errors.New("fatal: boom")
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"I could not read the PR's file list with git (fatal: boom), so the areas it changes are unknown."}; !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q, want %q", run.Gaps, want)
	}
	if patch := readRunFile(t, svc, run, "diff.patch"); patch == "" {
		t.Fatal("the patch call succeeded, diff.patch is empty")
	}
}

func (f *fakeGH) setPR(t *testing.T, fields map[string]any) {
	t.Helper()
	var pr map[string]any
	if err := json.Unmarshal(f.out["pr view"], &pr); err != nil {
		t.Fatal(err)
	}
	maps.Copy(pr, fields)
	f.out["pr view"], _ = json.Marshal(pr)
}

func TestIntakeRefusesACommitIdThatIsNotOne(t *testing.T) {
	bad := "--upload-pack=touch /tmp/x"
	cases := map[string]func(gh *fakeGH, f *fakeRunner){
		"gh head": func(gh *fakeGH, _ *fakeRunner) { gh.setPR(t, map[string]any{"headRefOid": bad}) },
		"gh base": func(gh *fakeGH, _ *fakeRunner) { gh.setPR(t, map[string]any{"baseRefOid": "main"}) },
		"path":    func(gh *fakeGH, _ *fakeRunner) { gh.setPR(t, map[string]any{"headRefOid": "../../../" + head6394}) },
		"ls-remote": func(gh *fakeGH, f *fakeRunner) {
			gh.err["pr view"] = errors.New("HTTP 502")
			f.gitOut["ls-remote --symref"] = "ref: refs/heads/main\tHEAD\n" + base6394 + "\tHEAD\n" + bad + "\trefs/pull/6394/head\n"
		},
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := newSvc(t)
			gh, f := svc.gh.(*fakeGH), svc.runner.(*fakeRunner)
			break_(gh, f)
			run, err := svc.Intake(ctx, "s1", prURL)
			is(t, "TESTING_COMMITS_MISSING")(run, err)
			if run.HeadSHA != "" || !strings.Contains(run.Error, "is not a commit id") {
				t.Fatalf("run %+v", run)
			}
			for _, c := range f.calls {
				if strings.Contains(c, "upload-pack") || strings.Contains(c, "touch") || strings.Contains(c, "fetch") || strings.Contains(c, "update-ref") {
					t.Errorf("git was called with the bad id: %s", c)
				}
			}
			svc.gh = fixtureGH(t) // GitHub answers again, with real ids
			if run, err := svc.Intake(ctx, "s1", prURL); err != nil || run.HeadSHA != head6394 {
				t.Fatalf("retry: run %+v err %v", run, err)
			}
		})
	}
}

func TestIntakeGapsHideLocalPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	// The gap text is clipped, so each path gets its own run.
	for _, c := range []struct {
		path          func(dataDir string) string
		hidden, shown string
	}{
		{func(d string) string { return d + "/litmus/mirrors/o__r.git" }, "", "<data dir>/litmus/mirrors/o__r.git"},
		{func(string) string { return home + "/.gitconfig" }, home, "~/.gitconfig"},
	} {
		svc, _ := newSvc(t)
		f := svc.runner.(*fakeRunner)
		f.gitErr["diff --numstat"] = errors.New("fatal: cannot open " + c.path(svc.store.dataDir()))
		run, err := svc.Intake(ctx, "s1", prURL)
		if err != nil || len(run.Gaps) != 1 {
			t.Fatalf("run gaps %q err %v", run.Gaps, err)
		}
		g := run.Gaps[0]
		if strings.Contains(g, svc.store.dataDir()) || (c.hidden != "" && strings.Contains(g, c.hidden)) || !strings.Contains(g, c.shown) {
			t.Errorf("gap %q lacks %q or still names a local path", g, c.shown)
		}
	}
}

func TestIntakeFatalCodesAreAClosedList(t *testing.T) {
	svc0, _ := newSvc(t)
	is(t, "SESSION_ID_REQUIRED")(svc0.Intake(ctx, " ", prURL)) // the codes before any gh or git call
	is(t, "TESTING_PR_URL_INVALID")(svc0.Intake(ctx, "s1", "not a pr"))
	// Every gh call and every git call intake makes fails on its own; the outcome is a run with gaps or one of the codes.
	ghKeys := []string{"pr view", "api repos/" + DefaultRepo + "/issues/7"}
	gitKeys := []string{"clone --bare", "config gc.auto", "fetch --no-tags", "update-ref refs/litmus/keep/" + head6394, "update-ref refs/litmus/keep/" + base6394,
		"merge-base " + base6394, "diff --numstat", "diff --name-status", "diff --patch", "ls-remote --symref", "rev-parse FETCH_HEAD"}
	for _, key := range append(ghKeys, gitKeys...) {
		svc, _ := newSvc(t)
		gh, f := svc.gh.(*fakeGH), svc.runner.(*fakeRunner)
		var pr map[string]any
		_ = json.Unmarshal(gh.out["pr view"], &pr)
		pr["body"] = "Closes #7"
		gh.out["pr view"], _ = json.Marshal(pr)
		gh.out["api repos/"+DefaultRepo+"/issues/7"], _ = json.Marshal(map[string]any{"title": "t", "state": "open"})
		if slices.Contains(ghKeys, key) {
			gh.err[key] = errors.New("HTTP 500: boom")
		} else {
			f.gitErr[key] = errors.New("fatal: boom")
		}
		run, err := svc.Intake(ctx, "s1", prURL)
		var e *apierr.Error
		switch {
		case err == nil && run.HeadSHA == "":
			t.Errorf("%s: no error and no pinned run", key)
		case err == nil && !slices.Contains([]string{"pr view", "ls-remote --symref", "rev-parse FETCH_HEAD"}, key) && len(run.Gaps) == 0:
			t.Errorf("%s: the failure left no gap", key) // the two fallback calls run only after gh fails, so failing them alone changes nothing
		case err != nil && (!errors.As(err, &e) || !slices.Contains(fatalIntakeCodes, e.Code)):
			t.Errorf("%s: intake returned %v, not one of %v", key, err, fatalIntakeCodes)
		}
	}

	// And the source: Intake and the pull functions name no other code.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "intake.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !slices.Contains([]string{"Intake", "claimIntake", "claim", "pullWords", "pullIDs", "pullCode", "changedFiles"}, fn.Name.Name) {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, _ := strconv.Unquote(lit.Value); strings.HasPrefix(s, "TESTING_") && !slices.Contains(fatalIntakeCodes, s) {
					t.Errorf("%s names %s, which is not a fatal intake code; make it a gap", fn.Name.Name, s)
				}
			}
			return true
		})
	}
}

func TestIntakeChangedFilesParsesRenamesCopiesAndBinaries(t *testing.T) {
	svc, _ := newSvc(t)
	f := svc.runner.(*fakeRunner)
	f.gitOut["diff --numstat"] = "3\t1\tsrc/mod.go\x00" + "1\t1\t\x00old/a.test.ts\x00new/a.test.ts\x00" + "0\t0\t\x00src/base.go\x00src/copy.go\x00" +
		"-\t-\timg.png\x00" + "12\t0\tadded.go\x00" + "0\t7\tgone.test.ts\x00"
	f.gitOut["diff --name-status"] = "M\x00src/mod.go\x00" + "R095\x00old/a.test.ts\x00new/a.test.ts\x00" + "C100\x00src/base.go\x00src/copy.go\x00" +
		"M\x00img.png\x00" + "A\x00added.go\x00" + "D\x00gone.test.ts\x00"
	files, err := svc.changedFiles(ctx, "/mirror", "fork", "head")
	if err != nil {
		t.Fatal(err)
	}
	want := []changedFile{
		{Status: "M", Path: "src/mod.go", Added: 3, Deleted: 1},
		{Status: "R", Path: "new/a.test.ts", From: "old/a.test.ts", Added: 1, Deleted: 1},
		{Status: "C", Path: "src/copy.go", From: "src/base.go"},
		{Status: "M", Path: "img.png", Binary: true},
		{Status: "A", Path: "added.go", Added: 12},
		{Status: "D", Path: "gone.test.ts", Deleted: 7},
	}
	if !slices.Equal(files, want) {
		t.Fatalf("files\n got %+v\nwant %+v", files, want)
	}
	text := filesText(files)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	wantLines := []string{
		"M\t3\t1\tsrc/mod.go",
		"R\t1\t1\tnew/a.test.ts\told/a.test.ts",
		"C\t0\t0\tsrc/copy.go\tsrc/base.go",
		"M\t-\t-\timg.png",
		"A\t12\t0\tadded.go",
		"D\t0\t7\tgone.test.ts",
	}
	if len(lines) != len(wantLines)+1 || !strings.HasPrefix(lines[0], "# status\tadded\tdeleted\tpath\t[from]") || !slices.Equal(lines[1:], wantLines) {
		t.Fatalf("files.txt:\n%s", text)
	}
}

func TestIntakeRunOutKeepsStderrOutOfStdout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r := execRunner{}
	out, err := r.RunOut(ctx, "", nil, "git", "-C", t.TempDir(), "rev-parse", "--verify", "--quiet", "nothing^{commit}")
	if err == nil || len(out) != 0 {
		t.Fatalf("a missing commit gave out %q, err %v", out, err)
	}
	out, err = r.RunOut(ctx, "", nil, "sh", "-c", "echo out; echo warn >&2")
	if err != nil || string(out) != "out\n" {
		t.Fatalf("stdout %q, err %v", out, err)
	}
	_, err = r.RunOut(ctx, "", nil, "sh", "-c", "echo out; echo warn >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "warn") || strings.Contains(err.Error(), "out\n") {
		t.Fatalf("a failing command's error should carry stderr only: %v", err)
	}
}

func TestIntakeRunCappedStopsTheCommandAtTheLimit(t *testing.T) {
	r := execRunner{}
	start := time.Now()
	out, cut, err := r.RunCapped(ctx, "", nil, 1<<20, "sh", "-c", "while :; do echo xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx; done") // never ends on its own
	if err != nil || !cut || len(out) != 1<<20 || time.Since(start) > 20*time.Second {
		t.Fatalf("an endless command: %d bytes, cut %v, err %v, after %v", len(out), cut, err, time.Since(start))
	}
	for _, tc := range []struct {
		limit int
		want  string
		cut   bool
	}{{6, "abcdef", false}, {7, "abcdef", false}, {5, "abcde", true}, {0, "", true}} {
		out, cut, err := r.RunCapped(ctx, "", nil, tc.limit, "printf", "abcdef")
		if err != nil || string(out) != tc.want || cut != tc.cut {
			t.Errorf("limit %d: got %q cut %v err %v, want %q cut %v", tc.limit, out, cut, err, tc.want, tc.cut)
		}
	}
	out, cut, err = r.RunCapped(ctx, "", nil, 100, "sh", "-c", "echo out; echo warn >&2; exit 3")
	if err == nil || cut || !strings.Contains(err.Error(), "warn") || strings.Contains(err.Error(), "out\n") {
		t.Fatalf("a failing command: out %q, cut %v, err %v", out, cut, err)
	}
}

var fatalIntakeCodes = []string{"TESTING_PR_URL_INVALID", "SESSION_ID_REQUIRED", "TESTING_RUN_EXISTS", "TESTING_GH_NOT_FOUND", "TESTING_COMMITS_MISSING"}

func TestIntakeReadsClosingIssuesReferences(t *testing.T) {
	svc, _ := newSvc(t)
	gh := svc.gh.(*fakeGH)
	gh.setPR(t, map[string]any{"body": "", "closingIssuesReferences": []map[string]any{{"number": 7, "repository": map[string]any{"name": "agent-orchestrator", "owner": map[string]any{"login": "OrchestratorInc"}}}}})
	body := "<!-- full <body> & text -->\n" + strings.Repeat("Steps ", 12000) + "Nested #999"
	gh.out["api repos/"+DefaultRepo+"/issues/7"], _ = json.Marshal(map[string]any{"title": "Linked issue", "state": "open", "body": body})
	gh.out["api --paginate repos/"+DefaultRepo+"/issues/7/comments?per_page=100"] = []byte(`[{"body":"first page","user":{"login":"reporter"}}][{"body":"second page #998"}]`)
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	want := "api repos/" + DefaultRepo + "/issues/7"
	if !slices.Contains(gh.calls, want) || len(run.Issues) != 1 || run.Issues[0].URL != "https://github.com/orchestratorinc/agent-orchestrator/issues/7" || run.Issues[0].Title != "Linked issue" {
		t.Fatalf("run %+v calls %v", run, gh.calls)
	}
	var refs []targetReference
	is(t, "")(nil, svc.store.read(run.ID, "refs.json", &refs))
	if len(refs) != 1 || refs[0].Number != 7 || refs[0].Body != body || refs[0].Title != "Linked issue" || refs[0].State != "open" || len(refs[0].Comments) != 2 || refs[0].Error != "" {
		t.Fatalf("sidebar-linked issue context was not saved in full: %+v", refs)
	}
	if raw := readRunFile(t, svc, run, "refs.json"); !strings.Contains(raw, "<!-- full <body> & text -->") || !strings.Contains(raw, "second page #998") {
		t.Fatal("linked issue text was escaped or comments were lost")
	}
	for _, call := range gh.calls {
		if strings.Contains(call, "/issues/999") || strings.Contains(call, "/issues/998") {
			t.Fatalf("followed a linked issue's body or comment: %s", call)
		}
	}
}
