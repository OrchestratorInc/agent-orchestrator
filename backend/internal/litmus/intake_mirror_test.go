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
	total := 0
	for _, a := range run.Areas {
		total += a.Files
	}
	if total != 298 {
		t.Fatalf("areas cover %d files: %+v", total, run.Areas)
	}
	pack := readRunFile(t, svc, run, "context-pack.md")
	for _, want := range []string{"- Size: 298 files, +", "| area | files | added | deleted |", "| frontend/src/renderer |", "files with the most churn", "more files: files.txt",
		"### Reading a big PR", "Do not try to cover the diff.", "`git diff " + fork6588 + "..." + head6588 + " --stat`", "## Gaps in this pack", "The PR branched from main 9 commits ago", "Base: main@" + fork6588[:12], "- Smoke: the app opens and a chat runs.",
		"UI files first, then files the PR text names, deletions and generated files last"} {
		if !strings.Contains(pack, want) {
			t.Errorf("context-pack.md lacks %q", want)
		}
	}
	if strings.Count(pack, "- Something visible in") > 0 || strings.Count(strings.SplitN(strings.SplitN(pack, "## Suggested checks", 2)[1], "##", 2)[0], "\n- ") != 4 {
		t.Errorf("a big PR gets the headline, the author's words, one nearby thing and a smoke check:\n%s", pack[:min(len(pack), 1500)])
	}
	// the excerpt opens with the UI file, not the lockfile that has the most added lines
	if excerpt := strings.SplitN(strings.SplitN(pack, "```diff\n", 2)[1], "\n", 2)[0]; excerpt != "diff --git a/frontend/src/renderer/components/chat/AgentSwitchPicker.tsx b/frontend/src/renderer/components/chat/AgentSwitchPicker.tsx" {
		t.Errorf("excerpt opens with %q", excerpt)
	}
	if n := len(strings.Fields(pack)); n > maxPackWords {
		t.Fatalf("pack has %d words", n)
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
	if pack := readRunFile(t, svc, run, "context-pack.md"); !strings.Contains(pack, "Head: fix/model-menu-closes-on-selection@"+moved) {
		t.Fatal("the pack does not name the moved head")
	}
}

// With gh down (any failure but "not found") the ids come from git and the run starts with the gap written; such a
// run finishes at awaiting-approval instead of posting, whatever its project says.
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
			pack := readRunFile(t, svc, run, "context-pack.md")
			if strings.Contains(pack, "### Body") || !strings.Contains(pack, "## Gaps in this pack") || !strings.Contains(pack, "title not available") || !strings.Contains(pack, "TurnSettingsBar.tsx +7/-0") {
				t.Fatalf("pack:\n%s", pack[:min(len(pack), 2000)])
			}
			if !svc.mustAsk(run, Verdict{Overall: CheckPass}) || svc.mustAsk(run, Verdict{Overall: CheckBlocked}) {
				t.Fatal("a run without the PR's words must wait for approval, unless it is blocked")
			}
			run.Comment = CommentOff
			if svc.mustAsk(run, Verdict{Overall: CheckPass}) {
				t.Fatal("a --no-comment run has nothing to hold")
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

func TestRunWithoutThePRsWordsStopsAtAwaitingApproval(t *testing.T) {
	svc, _, runDir, _ := submitSvc(t)
	_, err := svc.update("run_s1", func(r *Run) { r.Gaps = []string{gapNoWords + " (gh timed out), so I tested without its title."} })
	is(t, "")(nil, err)
	v, err := svc.Submit(ctx, "s1", "", "")
	run, _ := svc.store.Get("run_s1")
	if err != nil || v.Overall != CheckPass || run.State != RunAwaitingApproval || run.CommentState != CommentAwaiting {
		t.Fatalf("verdict %+v run %+v err %v", v, run, err)
	}
	md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md"))
	if !strings.Contains(string(md), "## Gaps\n\nWhat intake could not get; none of it changes the result.\n\n- "+gapNoWords) {
		t.Fatalf("verdict.md lacks the gaps:\n%s", md)
	}
	body, _ := os.ReadFile(filepath.Join(runDir, "comment.md"))
	if !strings.Contains(string(body), "\n"+gapNoWords+" (gh timed out), so I tested without its title.\n") {
		t.Fatalf("comment.md lacks the gap:\n%s", body)
	}
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
	notFound := "GraphQL: Could not resolve to an Issue with the number of 7." // gh's words
	gh.err["issue view"] = errors.New(notFound)
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || len(run.Issues) != 0 {
		t.Fatalf("run %+v err %v", run, err)
	}
	if want := []string{"I could not read the linked issue #7 (not found).", "I could not read the linked issue acme/other#8 (not found)."}; !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q", run.Gaps)
	}
	if pack := readRunFile(t, svc, run, "context-pack.md"); !strings.Contains(pack, "None linked (could not read the issue the PR names, see Gaps).") || !strings.Contains(pack, "- I could not read the linked issue #7 (not found).") {
		t.Fatalf("pack:\n%s", pack[:min(len(pack), 1500)])
	}
}

// When git cannot give the file list but gives the patch, the file list is a gap and nothing else: the count check
// compares GitHub's number with a list that was never read, so it must stay silent.
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

// splitDiff reads paths from "diff --git a/<path> b/<path>" lines as the pinned patch format prints them: not quoted,
// whatever the characters in the path.
func TestSplitDiffReadsNonASCIIAndSpacedPaths(t *testing.T) {
	diff := "diff --git a/docs/r\u00e9sum\u00e9-\u65e5\u672c.txt b/docs/r\u00e9sum\u00e9-\u65e5\u672c.txt\nindex 1111111..2222222 100644\n--- a/docs/r\u00e9sum\u00e9-\u65e5\u672c.txt\n+++ b/docs/r\u00e9sum\u00e9-\u65e5\u672c.txt\n@@ -1 +1,2 @@\n a\n+b\n" +
		"diff --git a/my dir/with space.txt b/my dir/with space.txt\nnew file mode 100644\nindex 0000000..3333333\n--- /dev/null\n+++ b/my dir/with space.txt\n@@ -0,0 +1 @@\n+x\n" +
		"diff --git a/src/old.go b/src/renamed.go\nsimilarity index 90%\nrename from src/old.go\nrename to src/renamed.go\n" +
		"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\nindex 4444444..0000000\n--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-y\n"
	var got []string
	for _, f := range splitDiff(diff) {
		got = append(got, fmt.Sprintf("%s +%d deleted=%t lines=%d", f.path, f.added, f.deleted, len(f.lines)))
	}
	want := []string{"docs/r\u00e9sum\u00e9-\u65e5\u672c.txt +1 deleted=false lines=7", "my dir/with space.txt +1 deleted=false lines=7", "src/renamed.go +0 deleted=false lines=4", "gone.txt +0 deleted=true lines=7"}
	if !slices.Equal(got, want) {
		t.Fatalf("files\n got %q\nwant %q", got, want)
	}
}

// setPR changes top-level fields of the PR that gh view answers.
func (f *fakeGH) setPR(t *testing.T, fields map[string]any) {
	t.Helper()
	var pr map[string]any
	if err := json.Unmarshal(f.out["pr view"], &pr); err != nil {
		t.Fatal(err)
	}
	maps.Copy(pr, fields)
	f.out["pr view"], _ = json.Marshal(pr)
}

// A commit id from gh or from ls-remote goes into git's argv, a ref name and a path: one that is not hex is the
// same fatal "cannot get both commits", before any git call is made with it, and the run can be retried.
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

// fatalIntakeCodes is the closed list of errors intake may return today. Anything else GitHub or git fails to give must
// become a gap: this test fails when a new failure is returned instead. TESTING_REPO_UNRESOLVED is not here on purpose:
// a repo no registered project holds pins the run with Error "blocked at prepare: ..." and returns nil (see
// TestIntakeRecordsRequestedRepoAndBlocksWhenUnresolved); prepare is what returns that code.
var fatalIntakeCodes = []string{"TESTING_PR_URL_INVALID", "SESSION_ID_REQUIRED", "TESTING_RUN_EXISTS", "TESTING_GH_NOT_FOUND", "TESTING_COMMITS_MISSING"}

// gateFile is the UI file of the fixture PR that the gate scan reads from the head commit.
const gateFile = "frontend/src/renderer/components/chat/TurnSettingsBar.tsx"

func TestIntakeSaysWhenTheGateScanCouldNotRun(t *testing.T) {
	for _, key := range []string{"show " + head6394 + ":" + gateFile, "grep -n"} {
		svc, _ := newSvc(t)
		f := svc.runner.(*fakeRunner)
		f.gitOut["show "+head6394+":"+gateFile] = "const settingsEnabled = true\n"
		f.gitErr[key] = errors.New("fatal: bad object")
		run, err := svc.Intake(ctx, "s1", prURL)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		want := []string{"The gate scan could not run: fatal: bad object, so the pack may lack notes on how the touched features are gated."}
		if !slices.Equal(run.Gaps, want) {
			t.Errorf("%s: gaps %q, want %q", key, run.Gaps, want)
		}
		if pack := readRunFile(t, svc, run, "context-pack.md"); !strings.Contains(pack, "- "+want[0]) {
			t.Errorf("%s: the pack does not say it:\n%s", key, pack[:min(len(pack), 1500)])
		}
	}
}

// A gap goes into the pack and the public comment, so git's messages lose the user's home dir and the data dir.
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
		f.gitOut["show "+head6394+":"+gateFile] = "const settingsEnabled = true\n"
		f.gitErr["grep -n"] = errors.New("fatal: cannot open " + c.path(svc.store.dataDir()))
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

// git grep exits 1 when nothing matches: that is an answer, not a failed scan. Any other failure is a gap.
func TestIntakeGateScanTreatsGrepExit1AsNoMatch(t *testing.T) {
	exit := func(code string) error {
		err := exec.Command("sh", "-c", "exit "+code).Run()
		var e *exec.ExitError
		if !errors.As(err, &e) {
			t.Fatalf("sh -c exit %s: %v", code, err)
		}
		return fmt.Errorf("git grep: %w", err)
	}
	for code, want := range map[string][]string{"1": nil, "2": {"The gate scan could not run: git grep: exit status 2, so the pack may lack notes on how the touched features are gated."}} {
		svc, _ := newSvc(t)
		f := svc.runner.(*fakeRunner)
		f.gitOut["show "+head6394+":"+gateFile] = "const settingsEnabled = true\n"
		f.gitErr["grep -n"] = exit(code)
		run, err := svc.Intake(ctx, "s1", prURL)
		if err != nil || !slices.Equal(run.Gaps, want) {
			t.Errorf("grep exit %s: gaps %q err %v, want %q", code, run.Gaps, err, want)
		}
	}
}

func TestIntakeFatalCodesAreAClosedList(t *testing.T) {
	svc0, _ := newSvc(t)
	is(t, "SESSION_ID_REQUIRED")(svc0.Intake(ctx, " ", prURL)) // the codes before any gh or git call
	is(t, "TESTING_PR_URL_INVALID")(svc0.Intake(ctx, "s1", "not a pr"))
	// Every gh call and every git call intake makes fails on its own; the outcome is a run with gaps or one of the codes.
	ghKeys := []string{"pr view", "issue view"}
	gitKeys := []string{"clone --bare", "config gc.auto", "fetch --no-tags", "update-ref refs/litmus/keep/" + head6394, "update-ref refs/litmus/keep/" + base6394,
		"merge-base " + base6394, "diff --numstat", "diff --name-status", "diff --patch", "ls-remote --symref", "rev-parse FETCH_HEAD", "show " + head6394 + ":" + gateFile, "grep -n"}
	for _, key := range append(ghKeys, gitKeys...) {
		svc, _ := newSvc(t)
		gh, f := svc.gh.(*fakeGH), svc.runner.(*fakeRunner)
		var pr map[string]any
		_ = json.Unmarshal(gh.out["pr view"], &pr)
		pr["body"] = "Closes #7"
		gh.out["pr view"], _ = json.Marshal(pr)
		gh.out["issue view"], _ = json.Marshal(map[string]any{"title": "t"})
		f.gitOut["show "+head6394+":"+gateFile] = "const settingsEnabled = true\n" // a gate name, so the scan goes on to grep for it
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

// changedFiles reads git's -z shapes: a rename or copy is "added\tdeleted\t" then two path tokens in numstat, and
// status, old, new in name-status; a binary file has "-" for both counts.
func TestChangedFilesParsesRenamesCopiesAndBinaries(t *testing.T) {
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

// execRunner.RunOut keeps git's stderr out of the output that is parsed, and puts it in the error instead.
func TestRunOutKeepsStderrOutOfStdout(t *testing.T) {
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

// execRunner.RunCapped keeps the first limit bytes and stops a command that has more: that is a cut, not a failure, and
// memory stays at the limit however much the command would have written.
func TestRunCappedStopsTheCommandAtTheLimit(t *testing.T) {
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
