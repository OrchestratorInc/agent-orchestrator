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
	call := strings.Join(args, " ")
	if err := f.err[call]; err != nil {
		return f.out[call], err
	}
	if out, ok := f.out[call]; ok {
		return out, nil
	}
	key := args[0] + " " + args[1]
	if err := f.err[key]; err != nil {
		return nil, err
	}
	out, ok := f.out[key]
	if !ok && args[0] == "api" && args[1] == "--paginate" {
		var pr map[string]json.RawMessage
		if err := json.Unmarshal(f.out["pr view"], &pr); err != nil {
			return nil, err
		}
		field := "comments"
		if strings.Contains(args[2], "/pulls/") {
			field = "reviewComments"
			if strings.Contains(args[2], "/reviews?") {
				field = "reviews"
			}
		}
		if out := pr[field]; len(out) > 0 {
			return out, nil
		}
		return []byte("[]"), nil
	}
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

func TestIntakePinsAndWritesChanges(t *testing.T) {
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
	if run.BaseRef != "main" {
		t.Fatalf("base ref %q", run.BaseRef)
	}

	// gh is asked for the PR's words only: no file list (gh cuts it at 100) and no diff (GitHub refuses big ones)
	if want := []string{"pr view 6394 --repo aoagents/agent-orchestrator --json " + prFields,
		"api --paginate repos/" + DefaultRepo + "/issues/6394/comments?per_page=100",
		"api --paginate repos/" + DefaultRepo + "/pulls/6394/reviews?per_page=100",
		"api --paginate repos/" + DefaultRepo + "/pulls/6394/comments?per_page=100"}; !slices.Equal(gh.calls, want) {
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
		readRunFile(t, svc, retry, "files.txt")
		is(t, "TESTING_RUN_EXISTS")(svc.Intake(ctx, "s1", prURL)) // pinned runs keep the 409
	}
}

type waitProbe struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (p *waitProbe) Done() <-chan struct{} {
	p.once.Do(func() { close(p.waiting) })
	return p.Context.Done()
}

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

func TestIntakeBodyIssues(t *testing.T) {
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

type issueGH struct {
	*fakeGH
	titles map[string]string
}

func (g *issueGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	if args[0] == "api" && args[1] != "--paginate" {
		g.calls = append(g.calls, strings.Join(args, " "))
		number := filepath.Base(args[1])
		title, ok := g.titles[number]
		if !ok {
			return nil, errors.New("HTTP 404: missing reference")
		}
		return json.Marshal(map[string]any{"title": title, "state": "open", "body": "steps"})
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
			for _, issue := range c.want {
				call := fmt.Sprintf("api repos/%s/issues/%d", DefaultRepo, issue.Number)
				count := 0
				for _, got := range gh.calls {
					if got == call {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("linked issue fetched %d times: %s", count, call)
				}
			}
			if len(c.want) == 0 && !slices.Equal(run.Gaps, []string{"Could not read reference https://github.com/orchestratorinc/agent-orchestrator/issues/999: HTTP 404: missing reference"}) {
				t.Errorf("unreadable issue gaps %q", run.Gaps)
			}
		})
	}
}

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

func TestIntakeAtTheForkPointAddsNoGap(t *testing.T) {
	svc, _ := newSvc(t)
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || run.BaseSHA != base6394 || len(run.Gaps) != 0 || svc.runner.(*fakeRunner).count("rev-list") != 0 {
		t.Fatalf("base %s gaps %q err %v", run.BaseSHA, run.Gaps, err)
	}
}

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

func assertContextFilesNamed(t *testing.T, svc *Service, run Run, names []string) {
	t.Helper()
	pack := readRunFile(t, svc, run, "context-pack.md")
	if lines := strings.Count(pack, "\n"); lines >= 40 {
		t.Fatalf("context pack has %d lines: %s", lines, pack)
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(svc.store.root, run.ID, name)); err != nil {
			t.Fatalf("context file %s: %v", name, err)
		}
		if !strings.Contains(pack, "- "+name+":") {
			t.Errorf("context pack omits %s: %s", name, pack)
		}
	}
	for _, want := range []string{"Read the skill first.\n", run.URL(), run.Kind, run.budget().String(), run.Deadline.UTC().Format(time.RFC3339), "Available verbs: ao testing"} {
		if !strings.Contains(pack, want) {
			t.Errorf("context pack omits %q: %s", want, pack)
		}
	}
	for _, leg := range run.targets() {
		if !strings.Contains(pack, leg.Name+" "+leg.SHA) {
			t.Errorf("context pack omits pinned leg %+v", leg)
		}
	}
	for _, line := range strings.Split(pack, "\n") {
		if name, _, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":"); strings.HasPrefix(line, "- ") && ok {
			if _, err := os.Stat(filepath.Join(svc.store.root, run.ID, name)); err != nil {
				t.Errorf("pack names missing file %s: %v", name, err)
			}
		}
	}
	for _, forbidden := range []string{"suggested checks", "gate notes", "area classifier"} {
		if strings.Contains(strings.ToLower(pack), forbidden) {
			t.Errorf("context pack contains %q", forbidden)
		}
	}
}

func TestIntakeWritesRawPRContextAndAllDiscussionPages(t *testing.T) {
	svc, _ := newSvc(t)
	gh := svc.gh.(*fakeGH)
	body := "<!-- retain <markup> & text -->\nSee #21.\n" + strings.Repeat("keep this sentence intact. ", 5000)
	longComment := strings.Repeat("comment text ", 5000) + "#22"
	gh.setPR(t, map[string]any{"body": body, "labels": []map[string]any{{"name": "bug", "color": "123456"}}})
	gh.out["pr view"] = []byte(strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(string(gh.out["pr view"])))
	commentPage, err := json.Marshal([]map[string]any{{"id": 2, "body": longComment, "user": map[string]string{"login": "maintainer"}, "created_at": "2026-10-01T00:00:00Z"}})
	is(t, "")(nil, err)
	gh.out["api --paginate repos/"+DefaultRepo+"/issues/6394/comments?per_page=100"] = append([]byte(`[{"id":1,"body":"first page"}]`), commentPage...)
	gh.out["api --paginate repos/"+DefaultRepo+"/pulls/6394/reviews?per_page=100"] = []byte(`[{"id":3,"body":"Review #23","state":"CHANGES_REQUESTED"}][{"id":4,"body":"last review"}]`)
	gh.out["api --paginate repos/"+DefaultRepo+"/pulls/6394/comments?per_page=100"] = []byte(`[{"id":5,"body":"Inline #24","path":"src/main.go","line":14}][{"id":6,"body":"last inline comment"}]`)
	for _, n := range []int{21, 22, 23, 24} {
		gh.out[fmt.Sprintf("api repos/%s/issues/%d", DefaultRepo, n)] = []byte(`{"title":"Direct reference","state":"open","body":"Nested #999 is not followed"}`)
	}
	f := svc.runner.(*fakeRunner)
	fork := strings.Repeat("c", 40)
	f.gitOut["merge-base "+base6394] = fork
	f.gitOut["rev-list --count "+fork+".."+base6394] = "1"
	f.gitOut["diff --patch"] += "\n+#999 must not become a reference\n"
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	assertContextFilesNamed(t, svc, run, []string{"pr.json", "refs.json", "diff.patch", "files.txt", "ci.json", "recipe.yaml", "recipe-notes.md", "target.json", "run.json", "context-pack.md"})
	if raw := readRunFile(t, svc, run, "pr.json"); !strings.Contains(raw, "<!-- retain <markup> & text -->") {
		t.Fatal("raw PR text was HTML-escaped")
	}
	var doc struct {
		Title, Body, State, BaseRefOid string
		Labels                         []struct{ Name, Color string }
		Comments                       []struct {
			ID        int
			Body      string
			User      struct{ Login string }
			CreatedAt string `json:"created_at"`
		}
		Reviews        []struct{ Body, State string }
		ReviewComments []struct {
			Body, Path string
			Line       int
		}
	}
	is(t, "")(nil, svc.store.read(run.ID, "pr.json", &doc))
	if doc.Title != run.Title || doc.Body != body || doc.State != "MERGED" || len(doc.Labels) != 1 || doc.Labels[0].Name != "bug" || doc.Labels[0].Color != "123456" || len(doc.Comments) != 2 || doc.Comments[1].Body != longComment || doc.Comments[1].User.Login != "maintainer" || doc.Comments[1].CreatedAt != "2026-10-01T00:00:00Z" {
		t.Fatal("PR context lost raw title, body, labels, state or paginated comment fields")
	}
	if len(doc.Reviews) != 2 || doc.Reviews[0].State != "CHANGES_REQUESTED" || doc.Reviews[1].Body != "last review" || len(doc.ReviewComments) != 2 || doc.ReviewComments[0].Path != "src/main.go" || doc.ReviewComments[0].Line != 14 || doc.ReviewComments[1].Body != "last inline comment" {
		t.Fatal("PR context lost paginated reviews or inline comments")
	}
	if doc.BaseRefOid != base6394 || run.BaseSHA != fork {
		t.Fatalf("raw GitHub identity and pinned identity were conflated: raw %s, pinned %s", doc.BaseRefOid, run.BaseSHA)
	}
	var refs []targetReference
	is(t, "")(nil, svc.store.read(run.ID, "refs.json", &refs))
	if len(refs) != 4 || refs[0].Number != 21 || refs[1].Number != 22 || refs[2].Number != 23 || refs[3].Number != 24 || refs[0].Body != "Nested #999 is not followed" {
		t.Fatalf("direct PR references = %+v", refs)
	}
	for _, call := range gh.calls {
		if strings.Contains(call, "/issues/999") {
			t.Fatalf("followed a fetched body or diff reference: %s", call)
		}
	}
	var original map[string]json.RawMessage
	is(t, "")(nil, json.Unmarshal(gh.out["pr view"], &original))
	var wantCI, gotCI any
	is(t, "")(nil, json.Unmarshal(original["statusCheckRollup"], &wantCI))
	is(t, "")(nil, svc.store.read(run.ID, "ci.json", &gotCI))
	if !reflect.DeepEqual(wantCI, gotCI) {
		t.Fatalf("CI context changed: got %v, want %v", gotCI, wantCI)
	}
	if got := readRunFile(t, svc, run, "recipe.yaml"); got != string(defaultRecipe) {
		t.Fatal("recipe context differs from the selected source")
	}
	recipe, err := svc.store.LoadRecipe(run.Repo)
	is(t, "")(nil, err)
	if got := readRunFile(t, svc, run, "recipe-notes.md"); got != recipe.Notes {
		t.Fatal("recipe notes differ from the selected recipe")
	}
	if strings.Contains(readRunFile(t, svc, run, "context-pack.md"), "keep this sentence intact") {
		t.Fatal("raw PR body leaked into the context index")
	}
}

func TestPRPaginationFailurePreservesWordsAndCompletedPages(t *testing.T) {
	for _, tc := range []struct{ field, endpoint string }{
		{"comments", "issues/6394/comments"},
		{"reviews", "pulls/6394/reviews"},
		{"reviewComments", "pulls/6394/comments"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			svc, _ := newSvc(t)
			gh := svc.gh.(*fakeGH)
			gh.setPR(t, map[string]any{"labels": []map[string]string{{"name": "bug"}}})
			call := "api --paginate repos/" + DefaultRepo + "/" + tc.endpoint + "?per_page=100"
			gh.out[call] = []byte(`[{"id":1,"body":"first page"}]`)
			gh.err[call] = errors.New("502")
			f := svc.runner.(*fakeRunner)
			fork := strings.Repeat("c", 40)
			f.gitOut["merge-base "+base6394] = fork
			f.gitOut["rev-list --count "+fork+".."+base6394] = "1"
			run, err := svc.Intake(ctx, "s1", prURL)
			is(t, "")(nil, err)
			if run.Title == "" || run.HeadSHA != head6394 || run.BaseSHA != fork || f.count("ls-remote --symref") != 0 {
				t.Fatalf("pagination failure discarded PR details or used Git-only intake: %+v", run)
			}
			var doc, original map[string]json.RawMessage
			is(t, "")(nil, svc.store.read(run.ID, "pr.json", &doc))
			is(t, "")(nil, json.Unmarshal(gh.out["pr view"], &original))
			for _, field := range []string{"title", "body", "labels", "state", "baseRefOid", "headRefOid", "statusCheckRollup"} {
				var got, want any
				is(t, "")(nil, json.Unmarshal(doc[field], &got))
				is(t, "")(nil, json.Unmarshal(original[field], &want))
				if !reflect.DeepEqual(got, want) {
					t.Errorf("pagination failure changed %s", field)
				}
			}
			var comments []struct {
				ID   int
				Body string
			}
			is(t, "")(nil, json.Unmarshal(doc[tc.field], &comments))
			if len(comments) != 1 || comments[0].ID != 1 || comments[0].Body != "first page" {
				t.Fatalf("lost completed page: %+v", comments)
			}
			var incomplete []string
			is(t, "")(nil, json.Unmarshal(doc["incomplete"], &incomplete))
			want := tc.field + " page 2: 502"
			if !slices.Equal(incomplete, []string{want}) || doc["unavailable"] != nil {
				t.Fatalf("incomplete list = %v, unavailable = %s", incomplete, doc["unavailable"])
			}
			pack := readRunFile(t, svc, run, "context-pack.md")
			if !strings.Contains(pack, "- pr.json: title, body, labels, state, comments and reviews; incomplete: "+want+"\n") || !strings.Contains(strings.Join(run.Gaps, "\n"), want) {
				t.Fatalf("missing pagination gap: pack %s, gaps %q", pack, run.Gaps)
			}
			if ask, err := svc.mustAsk(run, Verdict{Result: "pass"}); err != nil || !ask {
				t.Fatalf("unreadable comments lost the approval requirement: ask=%t err=%v", ask, err)
			}
			for _, field := range []string{"comments", "reviews", "reviewComments"} {
				if len(doc[field]) == 0 {
					t.Errorf("missing discussion field %s", field)
				}
			}
			assertContextFilesNamed(t, svc, run, []string{"pr.json", "refs.json", "target.json", "ci.json"})
		})
	}
}

func TestPRFirstDiscussionPageFailureKeepsPreviouslyReadComments(t *testing.T) {
	svc, _ := newSvc(t)
	gh := svc.gh.(*fakeGH)
	gh.setPR(t, map[string]any{"comments": []map[string]any{{"body": "Already read", "author": map[string]string{"login": "maintainer"}}}})
	gh.err["api --paginate repos/"+DefaultRepo+"/issues/6394/comments?per_page=100"] = errors.New("502")
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	var doc struct {
		Comments   []struct{ Body string }
		Incomplete []string
	}
	is(t, "")(nil, svc.store.read(run.ID, "pr.json", &doc))
	if len(doc.Comments) != 1 || doc.Comments[0].Body != "Already read" || !slices.Equal(doc.Incomplete, []string{"comments page 1: 502"}) {
		t.Fatalf("lost comments already read with the PR: %+v", doc)
	}
}

func TestExecGHPreservesStdoutWhenALaterPageFails(t *testing.T) {
	dir := t.TempDir()
	is(t, "")(nil, os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nprintf '%s' '[{\"body\":\"first page\"}]'\nprintf '%s' '502' >&2\nexit 1\n"), 0o750))
	t.Setenv("PATH", dir)
	out, err := (execGH{}).Run(ctx, "api", "--paginate", "repos/o/r/issues/1/comments?per_page=100")
	if string(out) != `[{"body":"first page"}]` || err == nil || err.Error() != "502" {
		t.Fatalf("gh lost partial stdout or the failure: out %q, err %v", out, err)
	}
}

func TestIntakePatchCapIncludesTheTruncationNotice(t *testing.T) {
	for _, size := range []int{maxPatch, maxPatch + 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			svc, _ := newSvc(t)
			svc.runner.(*fakeRunner).gitOut["diff --patch"] = strings.Repeat("x", size)
			run, err := svc.Intake(ctx, "s1", prURL)
			is(t, "")(nil, err)
			patch := readRunFile(t, svc, run, "diff.patch")
			if len(patch) > maxPatch || size > maxPatch && !strings.Contains(patch, "[diff.patch cut at") {
				t.Fatalf("patch of %d bytes produced %d bytes without a bounded truncation notice", size, len(patch))
			}
		})
	}
}

func TestIntakeRecordsContextWriteFailureWithoutPinningRun(t *testing.T) {
	svc, _ := newSvc(t)
	path := filepath.Join(svc.store.root, "run_s1", "context-pack.md")
	is(t, "")(nil, os.MkdirAll(path, 0o750))
	run, err := svc.Intake(ctx, "s1", prURL)
	if err == nil || run.Error == "" || run.pinned() {
		t.Fatalf("context write failure was not recorded: run %+v err %v", run, err)
	}
	saved, err := svc.store.Get(run.ID)
	is(t, "")(nil, err)
	if saved.Error != run.Error || saved.pinned() {
		t.Fatalf("saved failure differs: %+v", saved)
	}
}

func TestPRSidebarReferencesShareTheDepthOneCap(t *testing.T) {
	svc, _ := newSvc(t)
	gh := svc.gh.(*fakeGH)
	var linked []map[string]any
	for i := 1; i <= 25; i++ {
		linked = append(linked, map[string]any{"number": i})
	}
	gh.setPR(t, map[string]any{"body": "Closes #1. See #30.", "closingIssuesReferences": linked})
	reads := 0
	svc.gh = ghFunc(func(c context.Context, args ...string) ([]byte, error) {
		if args[0] == "api" && args[1] != "--paginate" {
			reads++
			if reads > 20 || strings.HasSuffix(args[1], "/999") {
				t.Fatalf("reference cap or depth exceeded: %v", args)
			}
			return []byte(`{"title":"Linked issue","state":"open","body":"Nested #999"}`), nil
		}
		if args[0] == "api" && args[1] == "--paginate" && !strings.Contains(args[2], "/6394/") {
			return []byte(`[{"body":"Nested #999"}]`), nil
		}
		return gh.Run(c, args...)
	})
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	var refs []targetReference
	is(t, "")(nil, svc.store.read(run.ID, "refs.json", &refs))
	if len(refs) != 20 || reads != 20 || refs[0].Number != 1 || refs[19].Number != 20 || len(run.Issues) != 1 {
		t.Fatalf("reference cap or deduplication failed: refs %+v, reads %d, issues %+v", refs, reads, run.Issues)
	}
	for _, ref := range refs {
		if len(ref.Comments) != 1 || ref.Body != "Nested #999" || ref.Error != "" {
			t.Fatalf("linked issue context missing: %+v", ref)
		}
	}
}

func TestLinkedIssueCommentFailurePreservesItsTextAndCompletedPages(t *testing.T) {
	svc, _ := newSvc(t)
	gh := svc.gh.(*fakeGH)
	gh.setPR(t, map[string]any{"body": "Closes #7"})
	gh.out["api repos/"+DefaultRepo+"/issues/7"] = []byte(`{"title":"Linked issue","state":"open","body":"Steps"}`)
	call := "api --paginate repos/" + DefaultRepo + "/issues/7/comments?per_page=100"
	gh.out[call] = []byte(`[{"body":"first page"}]`)
	gh.err[call] = errors.New("502")
	run, err := svc.Intake(ctx, "s1", prURL)
	is(t, "")(nil, err)
	var refs []targetReference
	is(t, "")(nil, svc.store.read(run.ID, "refs.json", &refs))
	if len(refs) != 1 || refs[0].Body != "Steps" || len(refs[0].Comments) != 1 || refs[0].Error != "linked issue comments page 2: 502" || len(run.Gaps) != 1 || !strings.Contains(run.Gaps[0], refs[0].Error) || len(run.Issues) != 1 {
		t.Fatalf("linked issue failure discarded text or hid the gap: refs %+v, run %+v", refs, run)
	}
}
