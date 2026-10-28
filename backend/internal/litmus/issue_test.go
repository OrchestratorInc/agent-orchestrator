package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testIssueURL = "https://github.com/acme/widget/issues/7"

func TestParseIssueAndTarget(t *testing.T) {
	for _, tc := range []struct {
		input, kind, repo string
		number            int
	}{
		{"https://github.com/Acme/Widget/issues/7", "issue", "acme/widget", 7},
		{" https://github.com/acme/widget/issues/7/ ", "issue", "acme/widget", 7},
		{"https://github.com/acme/widget/pull/8", "pr", "acme/widget", 8},
		{"acme/widget#8", "pr", "acme/widget", 8},
		{"", "", "", 0}, {"acme/widget#0", "", "", 0},
		{"https://github.com/acme/widget/issues/0", "", "", 0},
		{"https://github.com/acme/widget/issues/7?x=1", "", "", 0},
		{"https://github.com/acme/widget/issues/7#issuecomment-1", "", "", 0},
		{"https://github.com/acme/widget/issues/7/comments", "", "", 0},
		{"https://github.com/acme/widget/issues/99999999999999999999999999", "", "", 0},
		{"http://github.com/acme/widget/issues/7", "", "", 0},
		{"https://gitlab.com/acme/widget/issues/7", "", "", 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			kind, repo, n, err := ParseTarget(tc.input)
			if kind != tc.kind || repo != tc.repo || n != tc.number || (err != nil) != (tc.kind == "") {
				t.Fatalf("target %q %q %d err %v", kind, repo, n, err)
			}
			r, i, err := ParseIssue(tc.input)
			if tc.kind == "issue" {
				if err != nil || r != tc.repo || i != tc.number {
					t.Fatalf("issue %q %d err %v", r, i, err)
				}
			} else if err == nil {
				t.Fatal("ParseIssue accepted a non-issue URL")
			}
		})
	}
}

func issueSvc(t *testing.T) (*Service, *fakeRunner, *[]string) {
	t.Helper()
	svc, f := prepSvc(t, map[string]string{"v24": "v24.21.0"})
	f.gitOut = map[string]string{"ls-remote https://github.com/acme/widget.git refs/heads/trunk": base6394 + "\trefs/heads/trunk\n", "diff --patch": "#999"}
	writeFile(t, svc.store.recipePath("acme/widget"), "kind: electron\ntoolchain:\n  node: '24'\ninstall:\n  dir: frontend\n  cmd: npm ci\n  cacheKey: [frontend/package-lock.json]\nbuild:\n  dir: frontend\n  cmd: npm run predev\nrun:\n  dir: frontend\n  cmd: npm\n")
	calls := []string{}
	svc.gh = ghFunc(func(_ context.Context, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "api repos/acme/widget":
			return []byte(`{"default_branch":"trunk"}`), nil
		case "api repos/acme/widget/issues/7":
			return []byte(`{"number":7,"title":"The menu closes","body":"See #8 and https://github.com/Other/Repo/issues/10","state":"open","labels":[{"name":"bug"}]}`), nil
		case "api --paginate repos/acme/widget/issues/7/comments?per_page=100":
			return []byte(`[{"id":1,"body":"Try #9","user":{"login":"reporter"}}][{"id":2,"body":"Still #8","created_at":"2026-10-01T00:00:00Z"}]`), nil
		case "api repos/acme/widget/issues/8":
			return []byte(`{"title":"Related issue","state":"closed","body":"Nested #999 is outside depth one"}`), nil
		case "api repos/acme/widget/issues/9":
			return []byte(`{"title":"Candidate discussion","state":"open","body":"Fix details","pull_request":{"url":"api"}}`), nil
		case "api repos/other/repo/issues/10":
			return []byte(`{"title":"Another repo","state":"open","body":"Details"}`), nil
		case "pr view 11 --repo acme/widget --json headRefOid":
			return []byte(`{"headRefOid":"` + head6394 + `"}`), nil
		default:
			t.Fatalf("unexpected gh read %q", call)
			return nil, errors.New("unexpected read")
		}
	})
	return svc, f, &calls
}

func TestIssueStartResolvesRemoteTipWithoutCloning(t *testing.T) {
	svc, _, _ := issueSvc(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "git.log")
	t.Setenv("LITMUS_TEST_GIT_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$LITMUS_TEST_GIT_LOG"
if [ "$1" = "-C" ]; then
  cd "$2" || exit 1
  shift 2
fi
if [ "$1" = "-c" ]; then shift 2; fi
case "$1" in
  check-ref-format) exit 0 ;;
  ls-remote) printf '%s\t%s\n' '` + base6394 + `' 'refs/heads/trunk' ;;
  clone) echo 'start must not clone' >&2; exit 1 ;;
  *) echo 'unexpected git command' >&2; exit 1 ;;
esac
`
	is(t, "")(nil, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700))
	svc.runner = execRunner{}
	var spawned bool
	svc.WithSpawner(func(_ context.Context, req SpawnRequest) (string, error) {
		spawned = true
		if !strings.Contains(req.Prompt, base6394) {
			t.Fatal("prompt lost the pinned tip")
		}
		return "s1", nil
	})
	_, err := svc.Start(ctx, StartRequest{PRURL: testIssueURL})
	is(t, "")(nil, err)
	data, err := os.ReadFile(log)
	is(t, "")(nil, err)
	want := []string{
		"-C " + svc.store.dataDir() + " check-ref-format refs/heads/trunk",
		"-C " + svc.store.dataDir() + " -c " + ghCredentials + " ls-remote https://github.com/acme/widget.git refs/heads/trunk",
	}
	if !spawned || !slices.Equal(strings.Split(strings.TrimSpace(string(data)), "\n"), want) {
		t.Fatalf("spawned %t git calls %s", spawned, data)
	}
	if pathExists(svc.store.mirrorDir("acme/widget")) || len(svc.mirrorMu) != 0 {
		t.Fatal("start touched the mirror or took its lock")
	}
}

func TestIssueTargetValidatesRemoteTip(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		valid        bool
	}{
		{"one ref", base6394 + "\trefs/heads/trunk\n", true},
		{"unrelated ref", head6394 + "\trefs/heads/other\n" + base6394 + "\trefs/heads/trunk\n", true},
		{"two matching refs", base6394 + "\trefs/heads/trunk\n" + head6394 + "\trefs/heads/trunk\n", false},
		{"malformed id", "bad-id\trefs/heads/trunk\n", false},
		{"short id", base6394[:7] + "\trefs/heads/trunk\n", false},
		{"missing ref", head6394 + "\trefs/heads/other\n", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, f, _ := issueSvc(t)
			f.gitOut["ls-remote https://github.com/acme/widget.git refs/heads/trunk"] = tc.output
			branch, sha, err := svc.issueTarget(ctx, "acme/widget")
			if tc.valid {
				if err != nil || branch != "trunk" || sha != base6394 {
					t.Fatalf("target %q %q err %v", branch, sha, err)
				}
			} else {
				is(t, "TESTING_COMMITS_MISSING")(nil, err)
			}
		})
	}
}

func TestIssueTargetRejectsInvalidBranchBeforeRemoteLookup(t *testing.T) {
	svc, f, _ := issueSvc(t)
	f.gitErr = map[string]error{"check-ref-format refs/heads/trunk": errors.New("invalid branch")}
	_, _, err := svc.issueTarget(ctx, "acme/widget")
	if err == nil || !strings.Contains(err.Error(), "invalid default branch") || f.count("ls-remote") != 0 {
		t.Fatalf("err %v git calls %v", err, f.calls)
	}
}

func TestIssueIntakeKeepsAllCommentsDirectReferencesAndPinnedTarget(t *testing.T) {
	svc, f, calls := issueSvc(t)
	run, err := svc.Intake(ctx, "s1", testIssueURL)
	is(t, "")(nil, err)
	if run.Kind != "issue" || run.IssueNumber != 7 || run.PRNumber != 0 || run.TargetSHA != base6394 || run.BaseRef != "trunk" || !run.Deadline.Equal(run.CreatedAt.Add(IssueBudget)) {
		t.Fatalf("run %+v", run)
	}
	var doc issueDocument
	is(t, "")(nil, svc.store.read(run.ID, "issue.json", &doc))
	if doc.Title != run.Title || doc.State != "open" || len(doc.Labels) != 1 || len(doc.Comments) != 2 || !strings.Contains(string(doc.Comments[1]), "2026-10-01") {
		t.Fatalf("issue %+v", doc)
	}
	var refs []targetReference
	is(t, "")(nil, svc.store.read(run.ID, "refs.json", &refs))
	if len(refs) != 3 || refs[0].Title != "Related issue" || refs[0].State != "closed" || !strings.Contains(refs[0].Body, "#999") || refs[1].Repo != "other/repo" || refs[2].Kind != "pr" {
		t.Fatalf("refs %+v", refs)
	}
	if refs[0].Match != "#8" || refs[1].Match != "https://github.com/Other/Repo/issues/10" {
		t.Fatalf("reference matches lost their literal text: %+v", refs)
	}
	if strings.Contains(strings.Join(*calls, "\n"), "/issues/999") || f.count("diff --patch") != 0 || f.count("update-ref refs/litmus/keep/"+base6394+" "+base6394) != 1 {
		t.Fatalf("gh %v git %v", *calls, f.calls)
	}
	pack := readRunFile(t, svc, run, "context-pack.md")
	if len(strings.Split(strings.TrimSuffix(pack, "\n"), "\n")) >= 40 || !strings.Contains(pack, "Read the skill first.") || !strings.Contains(pack, base6394) || !strings.Contains(pack, "15m0s") {
		t.Fatal(pack)
	}
	assertContextFilesNamed(t, svc, run, []string{"issue.json", "refs.json", "files.txt", "recipe.yaml", "recipe-notes.md", "target.json", "run.json", "context-pack.md"})
	for _, name := range []string{"pr.json", "diff.patch", "ci.json"} {
		if pathExists(filepath.Join(svc.store.root, run.ID, name)) || strings.Contains(pack, "- "+name+":") {
			t.Fatalf("issue context has PR-only file %s", name)
		}
	}
	target := readRunFile(t, svc, run, "target.json")
	if !strings.Contains(target, `"name": "main"`) || strings.Contains(target, `"name": "base"`) || strings.Contains(target, `"name": "head"`) {
		t.Fatal(target)
	}
	is(t, "TESTING_LEG_INVALID")(svc.Launch(ctx, "s1", "base"))
	is(t, "TESTING_LEG_INVALID")(svc.Launch(ctx, "s1", "head"))
}

func TestTargetReferencesMatchUsesLiteralText(t *testing.T) {
	for _, tc := range []struct {
		name, body, comment, match string
		linkedCount                int
	}{
		{"body", "Fixes #52", "See acme/widget#52", "#52", 1},
		{"comment", "", "Fixes #52", "#52", 1},
		{"linked only", "", "", "", 1},
		{"body at cap", "Fixes #52 and #99", "", "#52", 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := issueSvc(t)
			var calls, wantCalls []string
			svc.gh = ghFunc(func(_ context.Context, args ...string) ([]byte, error) {
				calls = append(calls, strings.Join(args, " "))
				if args[1] == "--paginate" {
					return []byte(`[{"body":"Nested #999"}]`), nil
				}
				return []byte(`{"title":"Linked issue","state":"open"}`), nil
			})
			var linked []issueKey
			for i := 0; i < tc.linkedCount; i++ {
				linked = append(linked, issueKey{"acme/widget", 52 + i})
				endpoint := fmt.Sprintf("repos/acme/widget/issues/%d", 52+i)
				wantCalls = append(wantCalls, "api "+endpoint, "api --paginate "+endpoint+"/comments?per_page=100")
			}
			comment, err := json.Marshal(map[string]string{"body": tc.comment})
			is(t, "")(nil, err)
			refs, gaps, err := svc.targetReferences(ctx, "acme/widget", 7, tc.body, []json.RawMessage{comment}, linked)
			if err != nil || len(gaps) != 0 || len(refs) != tc.linkedCount || !slices.Equal(calls, wantCalls) {
				t.Fatalf("refs %+v gaps %v calls %v err %v", refs, gaps, calls, err)
			}
			for i, ref := range refs {
				wantMatch := ""
				if i == 0 {
					wantMatch = tc.match
				}
				if ref.Number != 52+i || ref.Match != wantMatch || len(ref.Comments) != 1 {
					t.Fatalf("reference %d: %+v, want number %d match %q and linked comments", i, ref, 52+i, wantMatch)
				}
			}
		})
	}
}

func TestIssueReferencesCapAtTwentyWithoutFollowingFetchedBodies(t *testing.T) {
	svc, _, _ := issueSvc(t)
	var body strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&body, "#%d ", i)
	}
	calls := 0
	svc.gh = ghFunc(func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if strings.HasSuffix(args[1], "/999") {
			t.Fatal("followed a fetched reference")
		}
		return []byte(`{"title":"Reference","state":"open","body":"Nested #999"}`), nil
	})
	refs, gaps, err := svc.issueReferences(ctx, "acme/widget", issueDocument{Number: 100, Body: body.String(), Comments: []json.RawMessage{json.RawMessage(`{"body":"#31"}`)}})
	if err != nil || len(refs) != 20 || calls != 20 || len(gaps) != 0 || refs[19].Number != 20 {
		t.Fatalf("refs %v gaps %v calls %d err %v", refs, gaps, calls, err)
	}
}

func TestIssueIntakeReportsBrokenCommentPageAndReferenceFailure(t *testing.T) {
	for _, brokenPage := range []bool{true, false} {
		t.Run(fmt.Sprint(brokenPage), func(t *testing.T) {
			svc, _, _ := issueSvc(t)
			gh := svc.gh
			svc.gh = ghFunc(func(c context.Context, args ...string) ([]byte, error) {
				call := strings.Join(args, " ")
				if brokenPage && strings.Contains(call, "--paginate") {
					return []byte(`[{"body":"#8"}][{`), nil
				}
				if !brokenPage && call == "api repos/acme/widget/issues/8" {
					return nil, errors.New("HTTP 404: missing reference")
				}
				return gh.Run(c, args...)
			})
			run, err := svc.Intake(ctx, "s1", testIssueURL)
			if brokenPage {
				if err == nil || !strings.Contains(err.Error(), "comment page") {
					t.Fatalf("err %v", err)
				}
				saved, err := svc.store.Get("run_s1")
				if err != nil || saved.Error == "" || saved.pinned() {
					t.Fatalf("run %+v err %v", saved, err)
				}
			} else {
				if err != nil || len(run.Gaps) != 1 {
					t.Fatalf("run %+v err %v", run, err)
				}
				if refs := readRunFile(t, svc, run, "refs.json"); !strings.Contains(refs, "HTTP 404: missing reference") {
					t.Fatal(refs)
				}
			}
		})
	}
}

func TestIssueStartCarriesPinnedSHAAndCandidateIntoPreparation(t *testing.T) {
	for _, candidate := range []bool{false, true} {
		t.Run(fmt.Sprint(candidate), func(t *testing.T) {
			svc, f, calls := issueSvc(t)
			var spawn SpawnRequest
			svc.WithSpawner(func(_ context.Context, req SpawnRequest) (string, error) { spawn = req; return "s1", nil })
			req := StartRequest{ProjectID: "p1", PRURL: testIssueURL, NoComment: true}
			if candidate {
				req.CandidateURL = "https://github.com/acme/widget/pull/11"
			}
			started, err := svc.Start(ctx, req)
			if err != nil || started.Name != "litmus: issue #7" {
				t.Fatalf("start %+v err %v", started, err)
			}
			for _, want := range []string{testIssueURL, base6394, "main", "15m0s", "issue-repro.md"} {
				if !strings.Contains(spawn.Prompt, want) {
					t.Fatalf("prompt missing %q: %s", want, spawn.Prompt)
				}
			}
			f.gitOut["ls-remote https://github.com/acme/widget.git refs/heads/trunk"] = head6394 + "\trefs/heads/trunk\n"
			run, err := svc.Intake(ctx, "s1", testIssueURL)
			if err != nil || run.TargetSHA != base6394 || run.Comment != CommentOff || run.CandidateURL != req.CandidateURL {
				t.Fatalf("run %+v err %v", run, err)
			}
			if count := strings.Count(strings.Join(*calls, "\n"), "api repos/acme/widget\n"); count != 1 {
				t.Fatalf("default tip read again: %v", *calls)
			}
			at := run.CreatedAt.Add(9 * time.Minute)
			svc.clock = func() time.Time { return at }
			run, err = svc.Prepare(ctx, "s1")
			is(t, "")(nil, err)
			wantLegs := 1
			if candidate {
				wantLegs = 2
			}
			if len(run.Legs) != wantLegs || run.Legs[0].Name != "main" || readManifest(run.Legs[0].Worktree).SHA != base6394 || !run.Deadline.Equal(at.Add(IssueBudget)) {
				t.Fatalf("prepared %+v", run)
			}
			if candidate && (run.Legs[1].Name != "head" || readManifest(run.Legs[1].Worktree).SHA != head6394) {
				t.Fatalf("candidate %+v", run.Legs)
			}
			deadline := run.Deadline
			at = at.Add(time.Minute)
			run, err = svc.Prepare(ctx, "s1")
			if err != nil || !run.Deadline.Equal(deadline) || run.preparedAt() != deadline.Add(-IssueBudget) {
				t.Fatalf("reprepare %+v err %v", run, err)
			}
		})
	}
}

func TestCandidateRequiresIssueInSameRepository(t *testing.T) {
	for _, req := range []StartRequest{
		{PRURL: "acme/widget#1", CandidateURL: "acme/widget#2"},
		{PRURL: testIssueURL, CandidateURL: "other/repo#2"},
		{PRURL: testIssueURL, CandidateURL: testIssueURL},
	} {
		svc, _, _ := issueSvc(t)
		is(t, "TESTING_CANDIDATE_INVALID")(svc.Start(ctx, req))
	}
}

func issueSubmitSvc(t *testing.T) (*Service, string, string) {
	t.Helper()
	svc, _, dir, artifacts := submitSvc(t)
	_, err := svc.update("run_s1", func(r *Run) {
		r.Kind, r.PRNumber, r.IssueNumber, r.TargetSHA, r.CandidateURL = "issue", 0, 37, base6394, "https://github.com/"+r.Repo+"/pull/11"
		r.Legs[0].Name, r.Legs[1].Name = "main", "head"
	})
	is(t, "")(nil, err)
	ledger, err := os.ReadFile(filepath.Join(dir, "actions.jsonl"))
	is(t, "")(nil, err)
	ledgerText := strings.NewReplacer(`"leg":"head"`, `"leg":"main"`, `"leg":"base"`, `"leg":"head"`).Replace(string(ledger))
	writeFile(t, filepath.Join(dir, "actions.jsonl"), ledgerText)
	return svc, dir, artifacts
}

func TestIssueSubmitRequiresLaunchedMainAndMainEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		verdict    Verdict
		unlaunched bool
	}{
		{"main never ran", "TESTING_VERDICT_LEG", Verdict{Result: "reproduced"}, true},
		{"not reproduced main never ran", "TESTING_VERDICT_LEG", Verdict{Result: "not_reproduced"}, true},
		{"head coverage only", "TESTING_VERDICT_COVERAGE", Verdict{Result: "not_reproduced", Coverage: []Coverage{{Leg: "head", Receipts: []int{1}}}}, false},
		{"no coverage receipt", "TESTING_VERDICT_COVERAGE", Verdict{Result: "not_reproduced", Coverage: []Coverage{{Leg: "main"}}}, false},
		{"head bug only", "TESTING_VERDICT_REPRODUCTION", Verdict{Result: "reproduced", Findings: []Finding{{Leg: "head", Severity: "bug", Receipts: []int{1}}}}, false},
		{"main bug without receipt", "TESTING_VERDICT_BUG_RECEIPT", Verdict{Result: "reproduced", Findings: []Finding{{Leg: "main", Severity: "bug"}}}, false},
		{"main note only", "TESTING_VERDICT_REPRODUCTION", Verdict{Result: "reproduced", Findings: []Finding{{Leg: "main", Severity: "worth-a-look", Receipts: []int{1}}}}, false},
		{"bad severity", "TESTING_VERDICT_SEVERITY", Verdict{Result: "reproduced", Findings: []Finding{{Leg: "main", Severity: "Bug", Receipts: []int{1}}}}, false},
		{"PR result", "TESTING_VERDICT_INVALID", Verdict{Result: "pass"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := issueSubmitSvc(t)
			if tc.unlaunched {
				_, err := svc.update("run_s1", func(r *Run) { r.Legs[0].State = "prepared" })
				is(t, "")(nil, err)
			}
			_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, tc.verdict))
			is(t, tc.code)(nil, err)
			if tc.unlaunched && !strings.Contains(err.Error(), "leg main never ran") {
				t.Fatal(err)
			}
		})
	}
}

func TestIssueNotReproducedNeedsTriggerCoverageReceipt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		coverage []Coverage
		code     string
	}{
		{name: "no coverage", code: "TESTING_VERDICT_COVERAGE"},
		{name: "ordinary main receipt", coverage: []Coverage{{Leg: "main", Receipts: []int{1}}}, code: "TESTING_VERDICT_COVERAGE"},
		{name: "trigger without receipt", coverage: []Coverage{{Leg: "main", Trigger: true}}, code: "TESTING_VERDICT_COVERAGE"},
		{name: "missing trigger receipt", coverage: []Coverage{{Leg: "main", Trigger: true, Receipts: []int{9}}}, code: "TESTING_VERDICT_COVERAGE"},
		{name: "receipt exists only on candidate", coverage: []Coverage{{Leg: "main", Trigger: true, Receipts: []int{3}}}, code: "TESTING_VERDICT_COVERAGE"},
		{name: "candidate trigger", coverage: []Coverage{{Leg: "head", Trigger: true, Receipts: []int{1}}}, code: "TESTING_VERDICT_COVERAGE"},
		{name: "candidate trigger and ordinary main receipt", coverage: []Coverage{{Leg: "head", Trigger: true, Receipts: []int{1}}, {Leg: "main", Receipts: []int{2}}}, code: "TESTING_VERDICT_COVERAGE"},
		{name: "trigger cannot borrow ordinary receipt", coverage: []Coverage{{Leg: "main", Trigger: true}, {Leg: "main", Receipts: []int{1}}}, code: "TESTING_VERDICT_COVERAGE"},
		{name: "main trigger receipt", coverage: []Coverage{{Leg: "main", Trigger: true, Receipts: []int{2}}}},
		{name: "trigger follows ordinary coverage", coverage: []Coverage{{Leg: "main", Receipts: []int{1}}, {Leg: "main", Trigger: true, Receipts: []int{2}}}},
		{name: "all cited receipts must exist", coverage: []Coverage{{Leg: "main", Trigger: true, Receipts: []int{1, 9}}}, code: "TESTING_VERDICT_RECEIPT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, dir, _ := issueSubmitSvc(t)
			svc.attach("run_s1", &actionRec{Seq: 3, Leg: "head", Action: "click", At: svc.clock()})
			v := Verdict{Result: "not_reproduced", Coverage: tc.coverage}
			_, err := svc.Submit(ctx, "s1", verdictInput(t, svc, v))
			is(t, tc.code)(nil, err)
			if tc.code == "TESTING_VERDICT_COVERAGE" && !strings.Contains(err.Error(), "not_reproduced needs the trigger coverage item (trigger: true) with an existing receipt on main") {
				t.Fatal(err)
			}
			if tc.code != "" && fileExists(filepath.Join(dir, verdictFile)) {
				t.Fatal("invalid verdict saved")
			}
		})
	}
}

func TestVerdictWordAgreesWithRequiredLeg(t *testing.T) {
	bug := func(leg string) []Finding {
		return []Finding{{Leg: leg, Severity: "bug", Receipts: []int{1}, Clip: &FindingClip{From: 1, To: 1}}}
	}
	coverage := func(leg string, trigger bool) []Coverage {
		return []Coverage{{Leg: leg, Receipts: []int{1}, Trigger: trigger}}
	}
	for _, tc := range []struct {
		name, kind, code string
		v                Verdict
	}{
		{"fail empty", "pr", "TESTING_VERDICT_REPRODUCTION", Verdict{Result: "fail"}},
		{"fail base bug", "pr", "TESTING_VERDICT_REPRODUCTION", Verdict{Result: "fail", Findings: bug("base")}},
		{"fail head bug", "pr", "", Verdict{Result: "fail", Findings: bug("head")}},
		{"reproduced main bug", "issue", "", Verdict{Result: "reproduced", Findings: bug("main")}},
		{"pass trigger", "pr", "", Verdict{Result: "pass", Coverage: coverage("head", true)}},
		{"pass ordinary coverage", "pr", "TESTING_VERDICT_COVERAGE", Verdict{Result: "pass", Coverage: coverage("head", false)}},
		{"pass head bug", "pr", "TESTING_VERDICT_CONTRADICTION", Verdict{Result: "pass", Coverage: coverage("head", true), Findings: bug("head")}},
		{"pass base bug", "pr", "", Verdict{Result: "pass", Coverage: coverage("head", true), Findings: bug("base")}},
		{"not reproduced trigger", "issue", "", Verdict{Result: "not_reproduced", Coverage: coverage("main", true)}},
		{"not reproduced main bug", "issue", "TESTING_VERDICT_CONTRADICTION", Verdict{Result: "not_reproduced", Coverage: coverage("main", true), Findings: bug("main")}},
		{"partial coverage", "pr", "", Verdict{Result: "partial", Coverage: coverage("head", false)}},
		{"partial no coverage", "pr", "TESTING_VERDICT_COVERAGE", Verdict{Result: "partial"}},
		{"partial base coverage", "pr", "TESTING_VERDICT_COVERAGE", Verdict{Result: "partial", Coverage: coverage("base", true)}},
		{"blocked setup", "issue", "", Verdict{Result: "blocked", BlockedOn: "Cannot run the trigger."}},
		{"blocked no reason", "issue", "TESTING_VERDICT_BLOCKED_ON", Verdict{Result: "blocked"}},
		{"blocked main bug", "issue", "TESTING_VERDICT_CONTRADICTION", Verdict{Result: "blocked", BlockedOn: "Time expired.", Findings: bug("main")}},
		{"blocked head bug", "pr", "TESTING_VERDICT_CONTRADICTION", Verdict{Result: "blocked", BlockedOn: "Time expired.", Findings: bug("head")}},
		{"bug without clip", "pr", "TESTING_VERDICT_CLIP", Verdict{Result: "fail", Findings: []Finding{{Leg: "head", Severity: "bug", Receipts: []int{1}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := Run{Kind: tc.kind, Legs: []Leg{{Name: "main", State: "running"}, {Name: "head", State: "running"}, {Name: "base", State: "running"}}}
			receipts := []Receipt{{Seq: 1, Leg: "main"}, {Seq: 1, Leg: "head"}, {Seq: 1, Leg: "base"}}
			is(t, tc.code)(nil, validateVerdict(run, tc.v, receipts))
		})
	}
}

func TestIssueSubmitRendersNumberAndKeepsMainVideo(t *testing.T) {
	for _, result := range []string{"reproduced", "not_reproduced"} {
		t.Run(result, func(t *testing.T) {
			svc, dir, artifacts := issueSubmitSvc(t)
			svc.runner = &videoProbeRunner{duration: "120"}
			run, err := svc.update("run_s1", func(r *Run) {
				r.Legs[0].RecordStartedAt = r.CreatedAt
				r.Legs[0].RecordStoppedAt = r.CreatedAt.Add(2 * time.Minute)
			})
			is(t, "")(nil, err)
			writeFile(t, recordingPath(dir, "main"), "main recording")
			v := Verdict{Result: result, Headline: "Tester's issue words", Coverage: []Coverage{{Leg: "main", Receipts: []int{1}, Trigger: result == "not_reproduced"}}}
			if result == "reproduced" {
				v.Findings = []Finding{{Leg: "main", Severity: "bug", Receipts: []int{1, 2}, Clip: &FindingClip{From: 1, To: 2}}}
			}
			var report string
			svc.WithReporter(func(_ context.Context, _ string, text string) error { report = text; return nil })
			is(t, "")(svc.Submit(ctx, "s1", verdictInput(t, svc, v)))
			if !strings.Contains(report, "#37") || strings.Contains(report, "#0") {
				t.Fatal(report)
			}
			body, err := os.ReadFile(filepath.Join(artifacts, "index.md"))
			if err != nil || !strings.Contains(string(body), "#37") || strings.Contains(string(body), "#0") {
				t.Fatalf("index %s err %v", body, err)
			}
			exists(t, filepath.Join(artifacts, "main.mp4"))
			if row := svc.summarize(run); row.Verdict != result || row.PR.Number != 37 || row.PR.URL != run.URL() {
				t.Fatalf("row %+v", row)
			}
		})
	}
}

func TestIssueMainCleanupAndRetentionUseItsTarget(t *testing.T) {
	svc, _, _ := issueSvc(t)
	run, err := svc.Intake(ctx, "s1", testIssueURL)
	is(t, "")(nil, err)
	at := svc.clock()
	f := &fakeSys{started: map[int]time.Time{100: at}}
	svc.sys, svc.closeEngine = f.ops(), func(string) {}
	_, err = svc.update(run.ID, func(r *Run) { r.Legs = []Leg{{Name: "main", State: "running", DaemonPort: 1234, DebugPort: 5678}} })
	is(t, "")(nil, err)
	target := readRunFile(t, svc, run, "target.json")
	if !strings.Contains(target, `"daemonPort": 1234`) || !strings.Contains(target, `"debugPort": 5678`) {
		t.Fatal(target)
	}
	if pins := svc.neededCommits(svc.store.mirrorDir(run.Repo)); !pins[base6394] {
		t.Fatalf("main commit unpinned: %v", pins)
	}
	dir := svc.store.legDir(run.ID, "main")
	writeFile(t, filepath.Join(dir, "data", "owned.txt"), "private test data")
	is(t, "")(nil, writeOwned(dir, Owned{RunID: run.ID, Leg: "main", PGID: Proc{100, at}, State: "running", Dirs: []string{filepath.Join(dir, "data")}}))
	is(t, "")(svc.Cleanup(ctx, "s1"))
	if pathExists(filepath.Join(dir, "data")) || !slices.Contains(f.log, "killGroup 100") {
		t.Fatalf("main not cleaned: %v", f.log)
	}
}

func TestIssuePrepareOutlastsIntakeDeadlineAndStartsFullTestingBudget(t *testing.T) {
	svc, f, _ := issueSvc(t)
	run, err := svc.Intake(ctx, "s1", testIssueURL)
	is(t, "")(nil, err)
	var now atomic.Int64
	now.Store(run.CreatedAt.UnixNano())
	svc.clock = func() time.Time { return time.Unix(0, now.Load()).UTC() }
	gate := &gatedRunner{inner: f, gate: make(chan struct{}), entered: make(chan struct{})}
	svc.runner = gate
	defer func() {
		select {
		case <-gate.gate:
		default:
			close(gate.gate)
		}
	}()
	type result struct {
		run Run
		err error
	}
	finished := make(chan result, 1)
	go func() { r, err := svc.Prepare(ctx, "s1"); finished <- result{r, err} }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare did not start")
	}
	at := run.Deadline.Add(time.Minute)
	now.Store(at.UnixNano())
	if status, err := svc.Status(ctx, "s1"); err != nil || status.Run.State != RunIntake {
		t.Fatalf("status expired active prepare: %+v err %v", status.Run, err)
	}
	close(gate.gate)
	select {
	case got := <-finished:
		if got.err != nil || got.run.State != RunPrepared || !got.run.Deadline.Equal(at.Add(IssueBudget)) {
			t.Fatalf("prepared %+v err %v", got.run, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("prepare did not finish")
	}
}

func TestIssueJSONKeepsAllPaginatedCommentsWithoutClipping(t *testing.T) {
	svc, f, _ := issueSvc(t)
	f.gitOut["ls-tree -r"] = "README.md\x00docs/café.md\x00src/menu.go\x00"
	original := svc.gh
	var pages strings.Builder
	want := make([]json.RawMessage, 205)
	longText := strings.Repeat("maintainer scope ", 5000)
	for start := 0; start < len(want); start += 100 {
		for i := start; i < min(start+100, len(want)); i++ {
			body := fmt.Sprintf("Comment %d", i)
			if i == len(want)-1 {
				body = longText
			}
			raw, err := json.Marshal(map[string]any{"id": i + 1, "body": body, "user": map[string]string{"login": "maintainer"}, "created_at": "2026-10-01T00:00:00Z", "reactions": map[string]int{"+1": i}})
			is(t, "")(nil, err)
			want[i] = raw
		}
		page, err := json.Marshal(want[start:min(start+100, len(want))])
		is(t, "")(nil, err)
		pages.Write(page)
	}
	svc.gh = ghFunc(func(c context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "api --paginate repos/acme/widget/issues/7/comments?per_page=100" {
			return []byte(pages.String()), nil
		}
		return original.Run(c, args...)
	})
	run, err := svc.Intake(ctx, "s1", testIssueURL)
	is(t, "")(nil, err)
	var doc issueDocument
	is(t, "")(nil, svc.store.read(run.ID, "issue.json", &doc))
	if len(doc.Comments) != len(want) {
		t.Fatalf("saved %d comments, want %d", len(doc.Comments), len(want))
	}
	for i := range want {
		var gotValue, wantValue any
		is(t, "")(nil, json.Unmarshal(doc.Comments[i], &gotValue))
		is(t, "")(nil, json.Unmarshal(want[i], &wantValue))
		gotJSON, _ := json.Marshal(gotValue)
		wantJSON, _ := json.Marshal(wantValue)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("comment %d changed", i)
		}
	}
	if got := readRunFile(t, svc, run, "files.txt"); got != "README.md\ndocs/café.md\nsrc/menu.go\n" || f.count("ls-tree -r --name-only -z "+base6394) != 1 {
		t.Fatalf("issue files do not describe the pinned tree: %q", got)
	}
}

func TestReadGHPagesReportsInvalidPagesAndKeepsCompletedPages(t *testing.T) {
	for _, output := range []string{"", "null", `[{"body":"first"}]null`, `[{"body":"first"}][{`, `{}`, `[]42`} {
		t.Run(output, func(t *testing.T) {
			svc, _ := newSvc(t)
			svc.gh = ghFunc(func(context.Context, ...string) ([]byte, error) { return []byte(output), nil })
			if got, err := svc.readGHPages(ctx, "repos/o/r/issues/1/comments?per_page=100", "issue comment"); err == nil || len(got) != strings.Count(output, `[{"body":"first"}]`) || !strings.Contains(err.Error(), "page") {
				t.Fatalf("pages %q = %s, error %v", output, got, err)
			}
		})
	}
}
