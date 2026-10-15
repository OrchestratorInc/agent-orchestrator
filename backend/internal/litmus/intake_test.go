package litmus

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGH answers gh calls from canned output keyed by the first two args.
type fakeGH struct {
	t     *testing.T
	out   map[string][]byte
	err   map[string]error
	calls []string
}

func (f *fakeGH) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	key := args[0] + " " + args[1]
	if err := f.err[key]; err != nil {
		return nil, err
	}
	out, ok := f.out[key]
	if !ok && args[0] == "api" { // a submit tries to post its comment; these tests have no GitHub to post to
		return nil, errors.New("gh: no network in tests")
	}
	if !ok {
		f.t.Fatalf("unexpected gh call %v", args)
	}
	return out, nil
}

// fixtureGH serves the recorded output of the real gh commands for PR 6394.
func fixtureGH(t *testing.T) *fakeGH {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return &fakeGH{t: t, err: map[string]error{}, out: map[string][]byte{"pr view": read("pr6394.json")}}
}

const (
	base6394 = "0ca80cfc370b21443c4a2b9d22199c047b8c74e7"
	head6394 = "e2204e0f361867a28310e71421a4227ab1d09544"
)

// gitFixture answers the git calls intake makes in the mirror for PR 6394: its two changed files, from the fork point
// (the base commit itself), and the recorded patch.
func gitFixture(t *testing.T) *fakeRunner {
	t.Helper()
	patch, err := os.ReadFile(filepath.Join("testdata", "pr6394.diff"))
	if err != nil {
		t.Fatal(err)
	}
	test, src := "frontend/src/renderer/components/chat/TurnSettingsBar.test.tsx", "frontend/src/renderer/components/chat/TurnSettingsBar.tsx"
	return &fakeRunner{node: map[string]string{}, heads: map[string]string{}, missing: map[string]bool{}, gitErr: map[string]error{}, gitOut: map[string]string{
		"merge-base " + base6394: base6394 + "\n",
		"diff --numstat":         "36\t16\t" + test + "\x007\t0\t" + src + "\x00",
		"diff --name-status":     "M\x00" + test + "\x00M\x00" + src + "\x00",
		"diff --patch":           string(patch),
	}}
}

func readRunFile(t *testing.T, svc *Service, run Run, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(svc.store.root, run.ID, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParsePR(t *testing.T) {
	cases := map[string]string{ // input -> "repo#number", or "" when invalid
		"https://github.com/OrchestratorInc/agent-orchestrator/pull/6394": "orchestratorinc/agent-orchestrator#6394",
		"https://www.github.com/o/r/pull/7/":                              "o/r#7",
		" Untrivial-ai/Agent-Orchestrator#12 ":                            "untrivial-ai/agent-orchestrator#12",
	}
	for _, in := range []string{"", "o/r", "o/r#", "o/r#0", "o/r#x", "r#5", "#5", "https://github.com/o/r/issues/1", "https://github.com/o/r/pull/0", "https://github.com/o/r/pull/3/files",
		"https://github.com/o/pull/3", "https://github.com/o/r/pull/3?x=1", "https://gitlab.com/g/p/-/merge_requests/3", "https://acme.ghe.io/o/r/pull/3", "ftp://github.com/o/r/pull/3"} {
		cases[in] = ""
	}
	for in, want := range cases {
		got := ""
		if repo, n, err := ParsePR(in); err == nil {
			got = fmt.Sprintf("%s#%d", repo, n)
		}
		if got != want {
			t.Errorf("ParsePR(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIntakeWritesContextPack(t *testing.T) {
	svc, now := newSvc(t)
	gh := svc.gh.(*fakeGH)
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil {
		t.Fatal(err)
	}
	if run.BaseSHA != "0ca80cfc370b21443c4a2b9d22199c047b8c74e7" || run.HeadSHA != "e2204e0f361867a28310e71421a4227ab1d09544" ||
		run.Repo != "orchestratorinc/agent-orchestrator" || run.State != RunIntake || !run.Deadline.Equal(now.Add(25*time.Minute)) {
		t.Fatalf("run %+v", run)
	}
	if want := []Area{{Name: "Renderer chat", Files: 1, Stems: []string{"TurnSettingsBar"}}, {Name: "Tests", Files: 1}}; run.BaseRef != "main" || !reflect.DeepEqual(run.Areas, want) {
		t.Fatalf("base %q areas %+v, want %+v", run.BaseRef, run.Areas, want)
	}
	// gh is asked for the PR's words only: no file list (gh cuts it at 100) and no diff (GitHub refuses big ones)
	if want := []string{"pr view 6394 --repo aoagents/agent-orchestrator --json " + prFields}; !slices.Equal(gh.calls, want) {
		t.Fatalf("gh calls %q", gh.calls)
	}
	// the code comes from a mirror named as gh reported the repo, not the renamed one from the URL: made once, main
	// only, with git's cleanup off, both commits fetched and pinned, the changes read from the fork point
	f, mirror := svc.runner.(*fakeRunner), svc.store.mirrorDir("orchestratorinc/agent-orchestrator")
	for _, want := range []string{"clone --bare --single-branch https://github.com/orchestratorinc/agent-orchestrator.git " + mirror + ".tmp", "config gc.auto 0", "fetch --no-tags origin " + head6394 + " " + base6394,
		"update-ref refs/litmus/keep/" + head6394 + " " + head6394, "update-ref refs/litmus/keep/" + base6394 + " " + base6394, "merge-base " + base6394 + " " + head6394, "diff --numstat -z -M " + base6394 + " " + head6394} {
		if f.count(want) != 1 {
			t.Errorf("git call %q made %d times:\n%s", want, f.count(want), strings.Join(f.calls, "\n"))
		}
	}
	if saved, err := svc.store.Get(run.ID); err != nil || saved.HeadSHA != run.HeadSHA || saved.Repo != run.Repo || saved.Error != "" || len(saved.Gaps) != 0 {
		t.Fatalf("saved %+v err %v", saved, err)
	}
	if len(run.Issues) != 0 {
		t.Fatalf("a PR that closes nothing has issues %+v", run.Issues)
	}
	if want, _ := os.ReadFile("testdata/pr6394.diff"); readRunFile(t, svc, run, "diff.patch") != string(want) {
		t.Fatal("diff.patch is not the whole diff")
	}
	if files := readRunFile(t, svc, run, "files.txt"); !strings.Contains(files, "M\t7\t0\tfrontend/src/renderer/components/chat/TurnSettingsBar.tsx\n") || strings.Count(files, "\n") != 3 {
		t.Fatalf("files.txt:\n%s", files)
	}
	if _, err := svc.Intake(ctx, "s2", prURL); err != nil || f.count("clone --bare") != 1 || f.count("fetch --no-tags") != 2 { // the second intake finds the mirror
		t.Fatalf("second intake: err %v, %d bare clones", err, f.count("clone --bare"))
	}
	pack, last := readRunFile(t, svc, run, "context-pack.md"), -1
	for _, h := range []string{"## 1. PR", "## 2. What the PR says", "## 3. Linked issue", "## 4. Changed files", "### Diff excerpt", "## 5. CI status", "## 6. Recipe", "## 7. What to do next"} {
		if i := strings.Index(pack, h); i <= last {
			t.Fatalf("heading %q out of order (at %d, previous %d)", h, i, last)
		} else {
			last = i
		}
	}
	for _, want := range []string{"keep model menu open on Other models", "Author: ronishrohan | State: MERGED", "Base: main@0ca80cfc370b", "Head: fix/model-menu-closes-on-selection@e2204e0f3618",
		"Size: 2 files, +43/-16", "### frontend/src/renderer", "components/chat/TurnSettingsBar.tsx +7/-0", "full diff: diff.patch", "- test -> SUCCESS", "None linked.", "none yet", "ao testing launch head", "the next changed area from section 4", "every check is something a user would do in the app, in the order of Suggested checks", "## Where the code is", "/repos/orchestratorinc__agent-orchestrator/e2204e0f361867a28310e71421a4227ab1d09544", "/repos/orchestratorinc__agent-orchestrator/0ca80cfc370b21443c4a2b9d22199c047b8c74e7"} {
		if !strings.Contains(pack, want) {
			t.Errorf("context-pack.md lacks %q", want)
		}
	}
	if n := len(strings.Fields(pack)); n > maxPackWords {
		t.Fatalf("pack has %d words", n)
	}
}

func TestIntakeWaitsForSpawnMarkerBeforeReadingModes(t *testing.T) {
	svc, _ := newSvc(t)
	svc.spawnMarkerWait = 5 * time.Second
	sessionID := "delayed"
	markerWritten := make(chan error, 1)
	go func() {
		time.Sleep(2 * time.Second)
		if err := svc.store.markOff("comment", sessionID); err != nil {
			markerWritten <- err
			return
		}
		if err := svc.store.setSessionProject(sessionID, "p1"); err != nil {
			markerWritten <- err
			return
		}
		markerWritten <- svc.store.markSpawned(sessionID, spawned{At: time.Now(), Repo: "aoagents/agent-orchestrator", PR: 6394})
	}()

	run, err := svc.Intake(ctx, sessionID, prURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-markerWritten; err != nil {
		t.Fatal(err)
	}
	if run.Comment != CommentOff || run.ProjectID != "p1" {
		t.Fatalf("intake read modes before the delayed marker: comment %q project %q", run.Comment, run.ProjectID)
	}
}

func TestIntakeMissingSpawnMarkerFailsClosedWithoutWaitingTenSeconds(t *testing.T) {
	svc, _ := newSvc(t)
	svc.spawnMarkerWait = time.Millisecond
	run, err := svc.Intake(ctx, "missing-marker", prURL)
	if err != nil {
		t.Fatal(err)
	}
	if run.Comment != CommentOff {
		t.Fatalf("missing spawn marker allowed comment mode %q, want off", run.Comment)
	}
}

func TestIntakeReturnsSpawnMarkerReadErrors(t *testing.T) {
	svc, _ := newSvc(t)
	svc.spawnMarkerWait = 0
	sessionID := "unreadable-marker"
	if err := os.MkdirAll(svc.store.spawnedDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(svc.store.spawnedDir(), sessionID), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Intake(ctx, sessionID, prURL); err == nil || !strings.Contains(err.Error(), "read spawned marker") {
		t.Fatalf("intake error = %v, want propagated spawned marker read error", err)
	}
}

func TestIntakeCorruptSpawnMarkerKeepsCommentOff(t *testing.T) {
	for _, tc := range []struct{ name, contents string }{
		{"invalid JSON", "{"},
		{"missing fields", `{}`},
		{"different pull request", `{"at":"2026-10-14T00:00:00Z","repo":"other/repo","pr":7}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newSvc(t)
			svc.spawnMarkerWait = 0
			sessionID := "corrupt-marker"
			if err := os.MkdirAll(svc.store.spawnedDir(), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(svc.store.spawnedDir(), sessionID), []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			run, err := svc.Intake(ctx, sessionID, prURL)
			if err != nil {
				t.Fatal(err)
			}
			if run.Comment != CommentOff {
				t.Fatalf("corrupt spawn marker allowed comment mode %q, want off", run.Comment)
			}
		})
	}
}

func TestIntakeIssuePriorRunsRecipeAndCaps(t *testing.T) {
	svc, _ := newSvc(t)
	gh := svc.gh.(*fakeGH)
	user := map[string]any{"login": "u"}
	var pr map[string]any
	if err := json.Unmarshal(gh.out["pr view"], &pr); err != nil {
		t.Fatal(err)
	}
	var comments, issueComments []map[string]any
	for i := 1; i <= 12; i++ {
		comments = append(comments, map[string]any{"author": user, "body": fmt.Sprintf("comment-%02d", i), "createdAt": time.Date(2026, 10, i, 0, 0, 0, 0, time.UTC)})
	}
	for i := 1; i <= 7; i++ {
		issueComments = append(issueComments, map[string]any{"author": user, "body": fmt.Sprintf("issue-comment-%d", i)})
	}
	pr["comments"], pr["body"] = comments, strings.Repeat("word ", 2000)
	pr["reviews"] = []map[string]any{{"author": user, "body": "", "state": "APPROVED", "submittedAt": time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}}
	pr["closingIssuesReferences"] = []map[string]any{{"number": 7, "repository": map[string]any{"name": "agent-orchestrator", "owner": map[string]any{"login": "OrchestratorInc"}}}}
	gh.out["pr view"], _ = json.Marshal(pr)
	gh.out["issue view"], _ = json.Marshal(map[string]any{"title": "Menu closes by itself", "body": "steps to reproduce", "comments": issueComments})

	repo := "orchestratorinc/agent-orchestrator"
	is(t, "")(nil, svc.store.Save(Run{ID: "run_old", Repo: repo, PRNumber: 5}))
	is(t, "")(nil, svc.store.Save(Run{ID: "run_noverdict", Repo: repo, PRNumber: 6}))
	is(t, "")(nil, svc.store.WriteFile("run_old", "verdict.md", []byte("# Verdict: fail\n\nmore")))
	recipes := filepath.Join(svc.store.root, "..", "recipes")
	is(t, "")(nil, os.MkdirAll(recipes, 0o750))
	is(t, "")(nil, os.WriteFile(filepath.Join(recipes, "orchestratorinc__agent-orchestrator.yaml"), []byte("kind: electron\n"), 0o600))

	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil {
		t.Fatal(err)
	}
	if want := "issue view 7 --repo " + repo + " --json title,body,comments"; gh.calls[1] != want {
		t.Fatalf("issue call %q, want %q", gh.calls[1], want)
	}
	if want := []IssueRef{{Number: 7, Title: "Menu closes by itself", URL: "https://github.com/" + repo + "/issues/7"}}; !reflect.DeepEqual(run.Issues, want) {
		t.Fatalf("issues %+v, want %+v", run.Issues, want)
	}
	pack := readRunFile(t, svc, run, "context-pack.md")
	for _, want := range []string{"[truncated at 1500 words]", "Menu closes by itself", "issue-comment-5", "comment-04", "kind: electron"} {
		if !strings.Contains(pack, want) {
			t.Errorf("context-pack.md lacks %q", want)
		}
	}
	for _, unwanted := range []string{"issue-comment-6", "comment-03", "run_noverdict", "run_old", "none yet"} {
		if strings.Contains(pack, unwanted) {
			t.Errorf("context-pack.md has %q", unwanted)
		}
	}
	if strings.Index(pack, "comment-12") > strings.Index(pack, "APPROVED") { // newest last
		t.Error("comments are not oldest first")
	}
}

func TestDiffExcerpt(t *testing.T) {
	file := func(name string, added, context int) string {
		s := fmt.Sprintf("diff --git a/%[1]s b/%[1]s\n--- a/%[1]s\n+++ b/%[1]s\n@@ -1 +1 @@\n", name)
		return s + strings.Repeat(" context\n", context) + strings.Repeat("+added line\n", added)
	}
	diff := file("small.go", 2, 0) + file("big.go", 250, 10) + file("mid.go", 60, 0)
	lines := pickDiff(splitDiff(diff), maxDiffLines, nil)
	if len(lines) != maxDiffLines || lines[0] != "diff --git a/big.go b/big.go" || !strings.Contains(lines[len(lines)-1], "file truncated") || strings.Contains(strings.Join(lines, "\n"), "small.go") {
		t.Fatalf("excerpt: %d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	// a big PR's excerpt puts UI source first, files the PR text names next, deletions and lockfiles last
	ui, named, lock := file("frontend/src/renderer/components/Picker.tsx", 2, 0), file("backend/internal/cli/hooks.go", 5, 0), file("frontend/package-lock.json", 900, 0)
	gone := strings.Replace(file("backend/old.go", 0, 0), "@@", "deleted file mode 100644\n@@", 1)
	names := namedIn("fix: hooks\n\nThe hooks package now retries.")
	ranked := pickDiff(splitDiff(lock+gone+named+ui), 2000, func(f fileDiff) int { return excerptRank(f, names) })
	at := func(name string) int { return strings.Index(strings.Join(ranked, "\n"), name) }
	if ui, named, gone, lock := at("Picker.tsx"), at("hooks.go"), at("old.go"), at("package-lock.json"); ui < 0 || ui > named || named > gone || named > lock {
		t.Fatalf("big PR excerpt order: ui %d, named %d, deleted %d, lock %d", ui, named, gone, lock)
	}
	huge := strings.Repeat("+"+strings.Repeat("word ", 20)+"\n", 400)
	pack := renderPack(intakeData{repo: "o/r", pr: pullRequest{Number: 1}, diff: "diff --git a/h b/h\n" + huge}, "")
	if n := len(strings.Fields(pack)); n > maxPackWords || !strings.Contains(pack, "full diff: diff.patch") || !strings.Contains(pack, "```diff") {
		t.Fatalf("pack has %d words or lost its excerpt", n)
	}
}

// The two fatal intake failures: a PR gh cannot find (asked twice), and commits git cannot bring into the mirror
// after a retry (the pull ref is tried too). Each leaves the run retryable in place, with no partial files.
func TestIntakeFatalFailuresLeaveNoPartialFiles(t *testing.T) {
	for _, c := range []struct{ gh, git, msg, code string }{
		{"pr view", "", "GraphQL: Could not resolve to a PullRequest with the number of 6394.", "TESTING_GH_NOT_FOUND"},
		{"", "fetch --no-tags", "fatal: unable to access 'https://github.com/': Could not resolve host", "TESTING_COMMITS_MISSING"},
		{"", "clone --bare", "fatal: unable to access 'https://github.com/': 504", "TESTING_COMMITS_MISSING"},
	} {
		svc, now := newSvc(t)
		gh, f := svc.gh.(*fakeGH), svc.runner.(*fakeRunner)
		if c.gh != "" {
			gh.err[c.gh] = errors.New(c.msg)
		} else {
			f.gitErr[c.git] = errors.New(c.msg)
		}
		run, err := svc.Intake(ctx, "s1", prURL)
		is(t, c.code)(nil, err)
		if c.gh != "" && len(gh.calls) != 2 { // a new PR can be unknown for a few seconds
			t.Fatalf("%s: gh was asked %d times", c.code, len(gh.calls))
		}
		if c.git == "fetch --no-tags" && (f.count("fetch --no-tags origin "+head6394+" "+base6394) != 2 || f.count("fetch --no-tags origin refs/pull/6394/head") != 2) {
			t.Fatalf("%s: fetches %q", c.code, f.calls)
		}
		entries, _ := os.ReadDir(filepath.Join(svc.store.root, run.ID))
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if !slices.Equal(names, []string{"owner-logins.json", "receipts", "run.json"}) { // no diff.patch, files.txt, context-pack.md or .tmp-* files
			t.Fatalf("%s: files after failure: %v", c.code, names)
		}
		if saved, err := svc.store.Get(run.ID); err != nil || saved.State != RunIntake || saved.Error == "" || saved.HeadSHA != "" {
			t.Fatalf("%s: saved %+v err %v", c.code, saved, err)
		}

		delete(gh.err, c.gh) // retry in place restarts the clock
		delete(f.gitErr, c.git)
		*now = now.Add(5 * time.Minute)
		retry, err := svc.Intake(ctx, "s1", prURL)
		if err != nil || retry.Error != "" || retry.HeadSHA == "" || !retry.Deadline.Equal(now.Add(RunBudget)) {
			t.Fatalf("%s: retry %+v err %v", c.code, retry, err)
		}
		readRunFile(t, svc, retry, "context-pack.md")
		is(t, "TESTING_RUN_EXISTS")(svc.Intake(ctx, "s1", prURL)) // pinned runs keep the 409
	}
}

// waitProbe is a context that tells when its Done channel is first asked for, which is what a call about to wait on
// something does.
type waitProbe struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (p *waitProbe) Done() <-chan struct{} {
	p.once.Do(func() { close(p.waiting) })
	return p.Context.Done()
}

// A retry while the first intake is still at work (a cold mirror clone that outlived the CLI's call) waits for it and
// gets the pinned run; the mirror is cloned once.
func TestIntakeRetryWaitsForTheOneInFlight(t *testing.T) {
	svc, _ := newSvc(t)
	first := svc.runner.(*fakeRunner)
	gate := &gatedRunner{inner: first, gate: make(chan struct{}), entered: make(chan struct{})}
	svc.runner = gate
	type result struct {
		run Run
		err error
	}
	results := make(chan result, 2)
	intake := func(ctx context.Context) {
		run, err := svc.Intake(ctx, "s1", prURL)
		results <- result{run, err}
	}
	wait := func(c <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-c:
		case <-time.After(10 * time.Second):
			t.Fatal(what)
		}
	}
	go intake(ctx)
	wait(gate.entered, "the first intake never reached the clone") // it holds the in-flight marker and waits at the clone
	probe := &waitProbe{Context: ctx, waiting: make(chan struct{})}
	go intake(probe)
	wait(probe.waiting, "the second intake never waited for the first") // it saw the marker and is waiting on it
	close(gate.gate)
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil || r.run.HeadSHA != head6394 {
			t.Fatalf("intake %d: %+v err %v", i, r.run, r.err)
		}
	}
	if first.count("clone --bare") != 1 {
		t.Fatalf("the mirror was cloned %d times", first.count("clone --bare"))
	}
}

func TestIntakeLive(t *testing.T) {
	if os.Getenv("LITMUS_LIVE") != "1" {
		t.Skip("set LITMUS_LIVE=1 to call the real gh CLI")
	}
	svc := New(NewStore(t.TempDir()), nil)
	run, err := svc.Intake(ctx, "live", "https://github.com/OrchestratorInc/agent-orchestrator/pull/6394")
	if err != nil {
		t.Fatal(err)
	}
	pack := readRunFile(t, svc, run, "context-pack.md")
	head := strings.Join(strings.SplitN(pack, "\n", 21)[:20], "\n")
	t.Logf("run %+v\ncontext-pack.md: %d words\n%s", run, len(strings.Fields(pack)), head)
}

func TestAreasOfNamesChangedPartsInPlainWords(t *testing.T) {
	got := areasOf([]string{
		"frontend/src/renderer/shell/sidebar/Sidebar.tsx", "frontend/src/renderer/shell/sidebar/useHistory.ts", "frontend/src/renderer/shell/sidebar/history.ts",
		"backend/internal/litmus/comment_test.go", "frontend/e2e/tests/nav.spec.ts", "frontend/e2e/helpers.ts", "README.md", "package.json", "backend/internal/chat_view/view.go",
		"frontend/src/renderer/components/TitlebarNav.tsx", "frontend/src/renderer/components/chat/Composer.tsx", "backend/internal/litmus/comment.go",
	})
	want := []Area{{Name: "Shell sidebar", Files: 3, Stems: []string{"Sidebar", "useHistory", "history"}}, {Name: "Tests", Files: 3},
		{Name: "Backend chat view", Files: 1, Stems: []string{"view"}}, {Name: "Backend litmus", Files: 1, Stems: []string{"comment"}}, {Name: "Docs", Files: 1, Stems: []string{"README"}},
		{Name: "Project files", Files: 1, Stems: []string{"package"}}, {Name: "Renderer chat", Files: 1, Stems: []string{"Composer"}}, {Name: "Renderer components", Files: 1, Stems: []string{"TitlebarNav"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("areas %+v, want %+v", got, want)
	}
}

// suggestFixture loads testdata/suggest-<n>.json: the title, body and changed files of a real PR.
func suggestFixture(t *testing.T, n string) intakeData {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "suggest-"+n+".json"))
	if err != nil {
		t.Fatal(err)
	}
	d := intakeData{words: true}
	var files struct {
		Files []struct {
			Path                 string
			Additions, Deletions int
		}
	}
	if err := errors.Join(json.Unmarshal(raw, &d.pr), json.Unmarshal(raw, &files)); err != nil {
		t.Fatal(err)
	}
	for _, f := range files.Files {
		d.files = append(d.files, changedFile{Status: "M", Path: f.Path, Added: f.Additions, Deleted: f.Deletions})
	}
	return d
}

func TestSuggestedChecksOnThreeRealPRs(t *testing.T) {
	for n, want := range map[string]string{
		"6403": "## Suggested checks\n\n- Headline claim: keep navigation arrows visible with the sidebar expanded\n- Something visible in the renderer component TitlebarNav still works.\n",
		"6266": "## Suggested checks\n\n- Headline claim: refine Harness settings layout\n- Something visible in the renderer settings (HarnessLogo, HarnessSettingsSection) still works.\n- Something visible in the renderer styles still works.\n",
		"6387": "## Suggested checks\n\n- Headline claim: acknowledge reports inside Claude paste wrappers\n- Something visible in the cli package (hooks) still works.\n",
	} {
		d := suggestFixture(t, n)
		got := suggestedChecks(d)
		t.Logf("#%s:\n%s", n, got)
		if got != want {
			t.Errorf("#%s:\n got %q\nwant %q", n, got, want)
		}
		if pack := renderPack(d, ""); !strings.Contains(pack, "\n"+want+"\n") || strings.Index(pack, want) > strings.Index(pack, "## 1. PR") {
			t.Errorf("#%s: the section is not at the top of the pack", n)
		}
		noSlop(t, strings.ReplaceAll(got, "Headline claim", "Main claim"))
	}
}

func TestSuggestedChecksTakesTheClaimSentenceAndTheIssueAndStopsAtFiveLines(t *testing.T) {
	d := intakeData{issue: &issue{Number: 12, Title: "Arrows vanish"}, words: true}
	d.pr.Title = "fix(shell)!: keep the navigation arrows"
	d.pr.Body = "## What\n\nSome context here.\n\n```\nAdds a code line that is not prose.\n```\n\nThis PR keeps the [arrows](http://x) visible. More text.\n"
	for _, p := range []string{"frontend/src/renderer/components/a/A.tsx", "frontend/src/renderer/hooks/b.ts", "backend/internal/cli/c.go", "backend/internal/store/d.go", "docs/e.md", "backend/internal/cli/c_test.go"} {
		d.files = append(d.files, changedFile{Status: "M", Path: p})
	}
	got := suggestedChecks(d)
	lines := strings.Split(strings.TrimSpace(strings.SplitN(got, "\n\n", 2)[1]), "\n")
	if len(lines) != 5 || lines[0] != "- Headline claim: keep the navigation arrows" || lines[1] != "- In the author's words: This PR keeps the arrows visible." || lines[2] != "- Linked issue #12: Arrows vanish" {
		t.Errorf("lines %q", lines)
	}
	for _, l := range lines[3:] {
		if !strings.HasPrefix(l, "- Something visible in ") || strings.Contains(l, "docs") || strings.Contains(l, "test") {
			t.Errorf("area line %q", l)
		}
	}
	for body, want := range map[string]string{
		"Adds a retry on failure. Other things.":                              "Adds a retry on failure.",
		"Nothing here. Users can now pick a theme.":                           "Users can now pick a theme.",
		"The menu closes so that the list stays visible":                      "The menu closes so that the list stays visible",
		"This PR contains only four frontend files. Adds a retry on failure.": "Adds a retry on failure.",
		"Fixes it.":                            "", // too short to quote
		"The composer now waits. Adding more.": "",
	} {
		if got := claimSentence(body); got != want {
			t.Errorf("claimSentence(%q) = %q, want %q", body, got, want)
		}
	}
}

// A PR is big at more than 30 files or more than 2,000 changed lines (added plus deleted, by git's counts); the patch
// text, which is cut at the cap, does not decide it.
func TestBigIsMoreThan30FilesOrMoreThan2000ChangedLines(t *testing.T) {
	many := func(n, added, deleted int) intakeData {
		var d intakeData
		for i := 0; i < n; i++ {
			d.files = append(d.files, changedFile{Status: "M", Path: fmt.Sprintf("src/f%d.go", i), Added: added, Deleted: deleted})
		}
		return d
	}
	for name, tc := range map[string]struct {
		d   intakeData
		big bool
	}{
		"30 small files":            {many(30, 1, 0), false},
		"31 small files":            {many(31, 1, 0), true},
		"2,000 lines in one file":   {many(1, 1000, 1000), false},
		"2,001 lines in one file":   {many(1, 1001, 1000), true},
		"2,001 deleted lines":       {many(1, 0, 2001), true},
		"2,100 lines over 21 files": {many(21, 50, 50), true},
		"binary files add no lines": {intakeData{files: []changedFile{{Path: "a.png", Binary: true}, {Path: "b.go", Added: 3}}}, false},
		"a long patch of few lines": {intakeData{files: many(2, 5, 5).files, diff: strings.Repeat("+x\n", 5000)}, false},
	} {
		if got := tc.d.big(); got != tc.big {
			t.Errorf("%s: big = %v, want %v", name, got, tc.big)
		}
	}
}

// The linked issue is a suggested check on both branches; a big PR still gets three checks beyond the context lines.
func TestSuggestedChecksKeepTheLinkedIssueOnBigAndSmallPRs(t *testing.T) {
	for _, n := range []int{30, 31} {
		d := intakeData{issue: &issue{Number: 12, Title: "Arrows vanish"}, words: true}
		d.pr.Title = "fix(shell)!: keep the navigation arrows"
		for i := 0; i < n; i++ {
			d.files = append(d.files, changedFile{Status: "M", Path: fmt.Sprintf("backend/internal/cli/f%d.go", i), Added: 1})
		}
		lines := strings.Split(strings.TrimSpace(strings.SplitN(suggestedChecks(d), "\n\n", 2)[1]), "\n")
		if lines[0] != "- Headline claim: keep the navigation arrows" || lines[1] != "- Linked issue #12: Arrows vanish" {
			t.Errorf("%d files: lines %q", n, lines)
		}
		if big := n > bigFiles; big != d.big() || (big && (len(lines) != 4 || !strings.HasPrefix(lines[2], "- One thing next to the headline claim") || lines[3] != "- Smoke: the app opens and a chat runs.")) ||
			(!big && (len(lines) != 3 || !strings.HasPrefix(lines[2], "- Something visible in the cli package"))) {
			t.Errorf("%d files (big %v): lines %q", n, d.big(), lines)
		}
	}
}

// mapTree is a codeTree over files held in memory.
type mapTree map[string]string

func (m mapTree) file(path string) []string {
	if s, ok := m[path]; ok {
		return strings.Split(s, "\n")
	}
	return nil
}

func (m mapTree) grep(word string) (hits []codeHit) {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
	for _, p := range slices.Sorted(maps.Keys(m)) {
		for i, l := range strings.Split(m[p], "\n") {
			if re.MatchString(l) {
				hits = append(hits, codeHit{p, i + 1})
			}
		}
	}
	return hits
}

// gateFixture is a trimmed copy of the three #6266 files that decide whether the Local/Cloud tabs show.
var gateFixture = mapTree{
	"frontend/src/renderer/components/settings/HarnessSettingsSection.tsx": `import { useCloudGate } from "../../hooks/useCloudGate";
function isActive(job: InstallJob | undefined): boolean {
	return job?.status === "running";
}
export function HarnessSettingsSection() {
	const { cloudEnabled } = useCloudGate();
	const isInstalled = installed.has(agentId);
	return cloudEnabled ? <Tabs className="harness-tabs" /> : null;
}`,
	"frontend/src/renderer/hooks/useCloudGate.ts": `import { useSettings } from "./useSettings";

export interface CloudGate {
	/** Whether the cloud offering is available (flag + entitled client + control plane). */
	cloudEnabled: boolean;
	localEnabled: boolean;
}

export function useCloudGate(enabled = true): CloudGate {
	return { cloudEnabled: settings?.cloudEnabled ?? false, localEnabled: true };
}`,
	"frontend/src/renderer/components/settings/GeneralSettingsSection.tsx": `
/**
 * Developer Mode-only toggle for the cloud offering. Persisted daemon-side (a
 * preference, not renderer state) so every surface resolves the same gate; the
 * daemon combines it with its baked control-plane URL into cloudEnabled, which
 * is what reveals sign-in, cloud projects, and sandbox sessions.
 */
function CloudOfferingRow() {
	return null;
}`,
	"frontend/src/renderer/components/settings/HarnessSettingsSection.test.tsx": "const cloudEnabled = true;",
}

func TestGateNotesExplainHowTheTouchedUIIsReached(t *testing.T) {
	got := gateNotes(gateFixture, []string{"frontend/src/renderer/components/settings/HarnessSettingsSection.tsx"})
	for _, want := range []string{"### useCloudGate", "hooks/useCloudGate.ts:9", "export function useCloudGate(enabled = true): CloudGate {",
		"### cloudEnabled", "hooks/useCloudGate.ts:5", "> /** Whether the cloud offering is available", "> cloudEnabled: boolean;",
		"GeneralSettingsSection.tsx:2", "> * Developer Mode-only toggle for the cloud offering.", "> function CloudOfferingRow() {"} {
		if !strings.Contains(got, want) {
			t.Errorf("gate notes lack %q:\n%s", want, got)
		}
	}
	// a local variable nobody explains is not a gate, and tests are not read
	for _, unwanted := range []string{"isActive", "isInstalled", "test.tsx"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("gate notes have %q:\n%s", unwanted, got)
		}
	}
	if gateNotes(gateFixture, nil) != "" || uiFiles(intakeData{files: []changedFile{{Status: "M", Path: "backend/internal/cli/testing.go"}, {Status: "M", Path: "frontend/src/renderer/a.test.tsx"},
		{Status: "M", Path: "frontend/src/renderer/styles.css"}, {Status: "D", Path: "frontend/src/renderer/Gone.tsx"}}}) != nil {
		t.Error("a PR with no UI source file has gate notes")
	}
}

func TestGateNotesCapAtEightAndPutOutrightGatesFirst(t *testing.T) {
	var src, hooks strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&src, "if (isShared%d() && flag%dEnabled) {}\n", i, i)
		fmt.Fprintf(&hooks, "export function isShared%d() {}\nconst flag%dEnabled = true;\n", i, i)
	}
	tree := mapTree{"app/ui/x.tsx": src.String(), "lib/gates.ts": hooks.String()}
	got := gateNotes(tree, []string{"app/ui/x.tsx"})
	if n := strings.Count(got, "### "); n != 8 || strings.Contains(got, "### isShared") {
		t.Fatalf("%d gates, want 8 outright ones:\n%s", n, got)
	}
}

func TestTestedSentencesQuoteHowTheAuthorTested(t *testing.T) {
	d := suggestFixture(t, "6266")
	got := testedSentences(d.pr.Body)
	if len(got) != 5 || got[0] != "Harness settings and logo tests: 51 passed." || !strings.Contains(got[1], "with the same fake daemon/cloud data.") {
		t.Fatalf("tested sentences %q", got)
	}
	if len(testedSentences("Adds a button.\n\n```\ntested locally in a fence.\n```\nNothing else.")) != 0 {
		t.Error("a code fence counts as the author's words")
	}
}

func TestContextSectionsSayWhereTheCodeIsAndStaySilentWithoutFacts(t *testing.T) {
	d := suggestFixture(t, "6266")
	d.headDir, d.baseDir = "/data/repos/o__r/headsha", "/data/repos/o__r/basesha"
	d.gates = "### cloudEnabled\n\n- a.ts:1\n> x\n"
	got := contextSections(d)
	for _, want := range []string{"## Where the code is", "- PR build checkout: /data/repos/o__r/headsha", "- Main checkout: /data/repos/o__r/basesha",
		"You may read any file there (Read, Grep, Glob) to learn how a feature is reached or gated; you cannot change them.", "## How the touched features are gated", "### cloudEnabled",
		"## How the author tested", `- "Harness settings and logo tests: 51 passed."`} {
		if !strings.Contains(got, want) {
			t.Errorf("sections lack %q:\n%s", want, got)
		}
	}
	if got := contextSections(intakeData{}); got != "" {
		t.Errorf("empty PR has sections %q", got)
	}
}

// TestContextSectionsOnTheRealPR6266 composes the three sections from the real PR build checkout of #6266. It needs
// that checkout, so it only runs with LITMUS_6266_CHECKOUT=<repos dir>/<head sha> (and LITMUS_6266_MAIN for the main one).
func TestContextSectionsOnTheRealPR6266(t *testing.T) {
	head := os.Getenv("LITMUS_6266_CHECKOUT")
	if head == "" {
		t.Skip("set LITMUS_6266_CHECKOUT to the #6266 head checkout to run")
	}
	svc, _ := newSvc(t)
	d := suggestFixture(t, "6266")
	d.pr.HeadRefOid = filepath.Base(head)
	d.headDir, d.baseDir = head, cmp.Or(os.Getenv("LITMUS_6266_MAIN"), filepath.Join(filepath.Dir(head), "main"))
	d.gates, _ = svc.gateNotes(ctx, head, d)
	got := contextSections(d)
	t.Logf("\n%s", got)
	for _, want := range []string{"### useCloudGate", "### cloudEnabled", "Developer Mode-only toggle", "fake daemon/cloud data"} {
		if !strings.Contains(got, want) {
			t.Errorf("sections lack %q", want)
		}
	}
}

func TestBodyIssues(t *testing.T) {
	const repo = "o/r"
	cases := []struct {
		body string
		want []issueKey
	}{
		{"Closes #6390", []issueKey{{repo, 6390}}},
		{"This fixes #12.\n\nResolves: #13 and closed #12 again, fixes #14, fix #15", []issueKey{{repo, 12}, {repo, 13}, {repo, 14}}},
		{"Fixes Other/Repo#5", []issueKey{{"other/repo", 5}}},
		{"resolves https://github.com/O/R/issues/9", []issueKey{{repo, 9}}},
		{"See https://github.com/x/y/issues/4 and https://github.com/x/y/issues/5", []issueKey{{"x/y", 4}}}, // no phrase: the first issue URL
		{"Closes #1\n\nSee https://github.com/x/y/issues/4", []issueKey{{repo, 1}}},
		{"<!-- Closes #123 -->\nNo issue.", nil},
		{"Closes # and fixes the menu, relates to #77, prefixes #8", nil},
		{"", nil},
	}
	for _, c := range cases {
		if got := bodyIssues(c.body, repo); !slices.Equal(got, c.want) {
			t.Errorf("bodyIssues(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

// issueGH is fixtureGH with a body that closes issues, and an issue view that answers from a title per number.
type issueGH struct {
	*fakeGH
	titles map[string]string
}

func (g *issueGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	if args[0] == "issue" {
		g.calls = append(g.calls, strings.Join(args, " "))
		title, ok := g.titles[args[2]]
		if !ok {
			return nil, errors.New("GraphQL: Could not resolve to an issue with the number of " + args[2] + ".")
		}
		return json.Marshal(map[string]any{"title": title, "body": "steps", "comments": []any{}})
	}
	return g.fakeGH.Run(ctx, args...)
}

func TestIntakeStoresClosedIssues(t *testing.T) {
	for name, c := range map[string]struct {
		body   string
		titles map[string]string
		want   []IssueRef
	}{
		"one": {"Keeps the menu open.\n\nCloses #6390", map[string]string{"6390": "Menu closes by itself"},
			[]IssueRef{{6390, "Menu closes by itself", "https://github.com/orchestratorinc/agent-orchestrator/issues/6390"}}},
		"two, third unreadable": {"Fixes #6390. Resolves #6391. Closes #1.", map[string]string{"6390": "A", "6391": "B"},
			[]IssueRef{{6390, "A", "https://github.com/orchestratorinc/agent-orchestrator/issues/6390"}, {6391, "B", "https://github.com/orchestratorinc/agent-orchestrator/issues/6391"}}},
		"mention gh cannot read": {"Closes #999", nil, nil}, // a gap, never fatal
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newSvc(t)
			gh := svc.gh.(*fakeGH)
			var pr map[string]any
			if err := json.Unmarshal(gh.out["pr view"], &pr); err != nil {
				t.Fatal(err)
			}
			pr["body"] = c.body
			gh.out["pr view"], _ = json.Marshal(pr)
			svc.gh = &issueGH{fakeGH: gh, titles: c.titles}
			run, err := svc.Intake(ctx, "s1", prURL)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(run.Issues, c.want) {
				t.Fatalf("issues %+v, want %+v", run.Issues, c.want)
			}
			if saved, err := svc.store.Get(run.ID); err != nil || !reflect.DeepEqual(saved.Issues, c.want) {
				t.Fatalf("saved issues %+v err %v", saved.Issues, err)
			}
			if first := gh.calls[1]; len(c.want) > 0 && !strings.HasSuffix(first, "--json title,body,comments") {
				t.Fatalf("first issue call %q", first)
			}
			if len(c.want) > 1 && !strings.HasSuffix(gh.calls[2], "--json title") {
				t.Fatalf("second issue call %q", gh.calls[2])
			}
			pack := readRunFile(t, svc, run, "context-pack.md")
			if len(c.want) > 0 && !strings.Contains(pack, "#6390 "+c.want[0].Title) {
				t.Errorf("pack lacks the first issue")
			}
			if len(c.want) == 0 && (!slices.Equal(run.Gaps, []string{"I could not read the linked issue #999 (not found)."}) || !strings.Contains(pack, "## Gaps in this pack") || !strings.Contains(pack, "None linked (could not read the issue the PR names, see Gaps).")) {
				t.Errorf("an unreadable mention: gaps %q\n%s", run.Gaps, pack)
			}
		})
	}
}

func TestSuggestedChecksLeadsWithTheIssueWhenTheTitleIsVaguer(t *testing.T) {
	const issueTitle = "Navigation arrows disappear when the sidebar is expanded"
	for title, wantHead := range map[string]string{
		"fix: arrows":                            issueTitle, // under 25 characters
		"fix(shell):":                            issueTitle, // a prefix and nothing else
		"fix(shell): keep arrows visible always": "keep arrows visible always",
	} {
		d := intakeData{issue: &issue{Number: 6390, Title: issueTitle}, words: true}
		d.pr.Title = title
		got := suggestedChecks(d)
		want := "## Suggested checks\n\n- Headline claim: " + wantHead + "\n"
		if wantHead == "keep arrows visible always" {
			want += "- Linked issue #6390: " + issueTitle + "\n"
		}
		if got != want {
			t.Errorf("%q:\n got %q\nwant %q", title, got, want)
		}
	}
	d := intakeData{issue: &issue{Number: 6390, Title: "Arrows"}, words: true}
	d.pr.Title = "fix: keep arrows visible"
	if got := suggestedChecks(d); !strings.Contains(got, "Headline claim: keep arrows visible\n") {
		t.Errorf("a shorter issue title replaced the PR's: %q", got)
	}
}

// The base build is the base branch where the PR branched, not its tip: other people's commits since then cannot make
// main differ from the PR.
func TestIntakeBuildsMainAtTheForkPoint(t *testing.T) {
	fork := strings.Repeat("c", 40)
	forked := func(t *testing.T, count string, countErr error) (Run, *fakeRunner) {
		t.Helper()
		svc, _ := newSvc(t)
		f := svc.runner.(*fakeRunner)
		f.gitOut["merge-base "+base6394] = fork + "\n"
		f.gitOut["rev-list --count "+fork+".."+base6394] = count
		f.gitErr["rev-list --count"] = countErr
		run, err := svc.Intake(ctx, "s1", prURL)
		if err != nil {
			t.Fatal(err)
		}
		return run, f
	}
	run, f := forked(t, "9\n", nil)
	wantGap := "The PR branched from main 9 commits ago; 'main' in this report is that branch point (cccccccc), not today's main."
	if run.BaseSHA != fork || run.HeadSHA != head6394 || !slices.Equal(run.Gaps, []string{wantGap}) {
		t.Fatalf("base %s head %s gaps %q", run.BaseSHA, run.HeadSHA, run.Gaps)
	}
	for _, id := range []string{fork, base6394, head6394} { // the fork point is pinned beside the tip
		if f.count("update-ref refs/litmus/keep/"+id+" "+id) != 1 {
			t.Errorf("%s was not pinned once:\n%s", id, strings.Join(f.calls, "\n"))
		}
	}
	if f.count("diff --numstat -z -M "+fork+" "+head6394) != 1 {
		t.Errorf("the changes were not read from the fork point:\n%s", strings.Join(f.calls, "\n"))
	}

	if run, _ := forked(t, "1\n", nil); !strings.Contains(run.Gaps[0], "main 1 commit ago;") {
		t.Errorf("one commit: %q", run.Gaps)
	}
	// Without a count the tip stays the base and nothing is claimed about the fork.
	if run, _ := forked(t, "", errors.New("fatal: bad revision")); run.BaseSHA != base6394 || len(run.Gaps) != 0 {
		t.Errorf("rev-list failed: base %s gaps %q", run.BaseSHA, run.Gaps)
	}

	// No fork point at all: the tip, with the gap intake already gave.
	svc, _ := newSvc(t)
	svc.runner.(*fakeRunner).gitErr["merge-base "+base6394] = errors.New("fatal: no merge base")
	if run, err := svc.Intake(ctx, "s1", prURL); err != nil || run.BaseSHA != base6394 || len(run.Gaps) != 1 || !strings.HasPrefix(run.Gaps[0], "I found no fork point") {
		t.Errorf("no merge base: base %s gaps %q err %v", run.BaseSHA, run.Gaps, err)
	}
}

// The fork and the base being one commit (the base has not moved) leaves the run exactly as it was: no gap.
func TestIntakeAtTheForkPointAddsNoGap(t *testing.T) {
	svc, _ := newSvc(t)
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || run.BaseSHA != base6394 || len(run.Gaps) != 0 || svc.runner.(*fakeRunner).count("rev-list") != 0 {
		t.Fatalf("base %s gaps %q err %v", run.BaseSHA, run.Gaps, err)
	}
}

// A merged PR (the head is already in the base, so the fork point is the head) keeps the base tip and claims no fork:
// the two builds must not share one checkout directory.
func TestIntakeMergedPRKeepsTheBaseTip(t *testing.T) {
	svc, _ := newSvc(t)
	f := svc.runner.(*fakeRunner)
	f.gitOut["merge-base "+base6394] = head6394 + "\n"
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || run.BaseSHA != base6394 || run.HeadSHA != head6394 || len(run.Gaps) != 0 || f.count("rev-list") != 0 {
		t.Fatalf("base %s head %s gaps %q err %v", run.BaseSHA, run.HeadSHA, run.Gaps, err)
	}
	if filepath.Join(svc.store.reposDir(run.Repo), run.HeadSHA) == filepath.Join(svc.store.reposDir(run.Repo), run.BaseSHA) {
		t.Errorf("both legs resolve to one checkout dir for %s", run.HeadSHA)
	}
}

// Section 8 plans pairs as advice with no cap, and tells the tester to finish a check on both builds before the next one.
func TestPackSaysHowToPlanAndRunPairs(t *testing.T) {
	for _, n := range []int{30, 31} {
		d := intakeData{repo: "o/r", pr: pullRequest{Number: 1}}
		for i := 0; i < n; i++ {
			d.files = append(d.files, changedFile{Path: fmt.Sprintf("backend/internal/cli/f%d.go", i), Status: "M", Added: 1})
		}
		pack := renderPack(d, "")
		next := pack[strings.Index(pack, "## 7. What to do next"):]
		plan, other := "Plan at most 6 pairs of checks", "Plan at most 4 pairs"
		if n > bigFiles {
			plan, other = "Plan at most 4 pairs of checks", "Plan at most 6 pairs"
		}
		for _, want := range []string{plan, "(4 for a PR over 30 files, 6 otherwise)", "split a check that mixes two things", "Litmus enforces no limit on checks",
			"`ao testing launch head`, `ao testing launch base`", "seed step N failed", "its sample content is missing: do not fail a check", "may itself be a finding", "then at once on main with `--leg base`, before the next check", "A check is done only when it has a result on both",
			"end the main side as `absent` with a screenshot of where it would be; `fail` means the feature is there and broken", "add another pair", "Never add a check that runs on the PR build alone", browserDataRule} {
			if !strings.Contains(next, want) {
				t.Errorf("%d files: section 7 lacks %q:\n%s", n, want, next)
			}
		}
		if strings.Contains(next, "accepts at most") {
			t.Errorf("%d files: section 7 states a cap on checks", n)
		}
		if strings.Contains(next, other) {
			t.Errorf("%d files: section 7 also says %q", n, other)
		}
	}
}

// The pack always sends the tester to observable app checks, never PR/package tests.
func TestPackDoesNotSuggestRunningPRTests(t *testing.T) {
	for _, path := range []string{"frontend/src/renderer/components/chat/Bar.tsx", "backend/internal/cli/hooks.go"} {
		pack := renderPack(intakeData{repo: "o/r", pr: pullRequest{Number: 1}, files: []changedFile{{Path: path, Status: "M", Added: 3}}}, "")
		const heading = "## 7. What to do next"
		if !strings.Contains(pack, heading) {
			t.Fatalf("pack for %s is missing %q", path, heading)
		}
		for _, banned := range []string{"ao testing test", "package tests", "with-tests-from", "test check"} {
			if strings.Contains(pack, banned) {
				t.Errorf("pack for %s suggests %q:\n%s", path, banned, pack)
			}
		}
	}
}
