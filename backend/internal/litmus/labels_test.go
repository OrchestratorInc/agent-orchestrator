package litmus

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunLabelsNameTargetsAndBuilds(t *testing.T) {
	for _, tc := range []struct {
		run               Run
		leg, label, build string
		title             string
	}{
		{Run{Kind: "pr", PRNumber: 7}, "head", "PR #7", "PR", "Litmus: PR build #7"},
		{Run{Kind: "pr", PRNumber: 7}, "base", "PR #7", "main", "Litmus: main for #7"},
		{Run{PRNumber: 7}, "head", "PR #7", "PR", "Litmus: PR build #7"},
		{Run{Kind: "issue", IssueNumber: 37}, "main", "issue #37", "main", "Litmus: main build for issue #37"},
		{Run{Kind: "issue", IssueNumber: 37}, "head", "issue #37", "candidate PR", "Litmus: candidate PR build for issue #37"},
		{Run{}, "head", "PR", "PR", "Litmus: PR build"},
		{Run{}, "base", "PR", "main", "Litmus: main"},
		{Run{Kind: "issue"}, "main", "issue", "main", "Litmus: main build for issue"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if tc.run.Label() != tc.label || tc.run.LegName(tc.leg) != tc.build || tc.run.windowTitle(tc.leg) != tc.title {
				t.Fatalf("label %q build %q title %q", tc.run.Label(), tc.run.LegName(tc.leg), tc.run.windowTitle(tc.leg))
			}
			env, err := legEnv(Recipe{}, tc.run, tc.leg, "/leg", "/root", "", 1234, 5678, "/private/tmp/lt-fixture")
			is(t, "")(nil, err)
			if envValue(env, "LITMUS_WINDOW_TITLE") != tc.title {
				t.Fatal("launch environment lost the window title")
			}
		})
	}
}

func TestRunLegNamesReachFindingsAndCoverage(t *testing.T) {
	for _, tc := range []struct {
		run        Run
		leg, label string
	}{
		{Run{Kind: "pr", PRNumber: 7}, "head", "PR"},
		{Run{Kind: "pr", PRNumber: 7}, "base", "main"},
		{Run{Kind: "issue", IssueNumber: 37}, "main", "main"},
		{Run{Kind: "issue", IssueNumber: 37}, "head", "candidate PR"},
	} {
		t.Run(tc.run.Label()+"/"+tc.leg, func(t *testing.T) {
			v := Verdict{
				Result: "fail", Headline: "Menu closes.", Environment: "fixture",
				Findings: []Finding{{Title: "Menu", Severity: "bug", Leg: tc.leg, Receipts: []int{1}, Text: "Tester's head and base text."}},
				Coverage: []Coverage{{Leg: tc.leg, Receipts: []int{1}, Text: "Opened the menu."}},
			}
			receipts := []Receipt{{Seq: 1, Leg: tc.leg}}
			body := renderVerdict(tc.run, v, receipts, t.TempDir(), "owner logins: unverified", evidence{})
			links := tc.leg + " receipt 1 (after-shot unavailable)"
			want := "# Verdict: fail\n\nMenu closes.\n\n## Findings\n\n### Menu\n\nbug, " + tc.label + "\n\nTester's head and base text.\n\nEvidence: " + links + "\n\n## Coverage\n\n" + tc.label + ", " + links + "\n\nOpened the menu.\n\n## Environment\n\nfixture\n\nReceipts: 1\n\nowner logins: unverified\n\n" + commentMarker(tc.run) + "\n"
			if body != want {
				t.Fatalf("rendered comment:\n%s\nwant:\n%s", body, want)
			}
		})
	}
}

func TestRunLabelsReachSeedAndRecordingGaps(t *testing.T) {
	for _, kind := range []string{"pr", "issue"} {
		t.Run(kind, func(t *testing.T) {
			svc, _ := newSvc(t)
			run := Run{ID: "run_labels", Kind: kind, PRNumber: 7, IssueNumber: 37, CandidateURL: "https://github.com/o/r/pull/8"}
			is(t, "")(nil, svc.store.Save(run))
			var gaps []string
			for _, leg := range run.targets() {
				gap, ok := run.seedGapOf(ctx, nil, leg.Name, &seedError{step: 1, reason: "no fixture"})
				want := "The " + run.LegName(leg.Name) + " build started without its sample content (seed step 1 failed: no fixture)."
				if !ok || gap != want {
					t.Fatalf("seed gap %q ok %t, want %q", gap, ok, want)
				}
				svc.recordingProblem(run.ID, leg.Name, errors.New("page unavailable"))
				gaps = append(gaps, "The "+run.LegName(leg.Name)+" build's recording was not available: page unavailable.")
			}
			run, err := svc.store.Get(run.ID)
			is(t, "")(nil, err)
			if !slices.Equal(run.Gaps, gaps) {
				t.Fatalf("recording gaps %q, want %q", run.Gaps, gaps)
			}
			body := renderVerdict(run, Verdict{Result: "blocked", BlockedOn: "fixture"}, nil, t.TempDir(), "", evidence{})
			for _, gap := range gaps {
				if !strings.Contains(body, gap) {
					t.Fatalf("recording gap missing from verdict: %s", body)
				}
			}
		})
	}
}

func TestEvidenceCommitNamesRunKind(t *testing.T) {
	for _, kind := range []string{"pr", "issue"} {
		t.Run(kind, func(t *testing.T) {
			svc, git, run, stage := evRun(t, EvidencePost)
			run.Kind, run.IssueNumber = kind, 37
			_, err := svc.pushEvidence(ctx, run, stage, []string{"head.gif"}, svc.credentialPattern(ctx))
			is(t, "")(nil, err)
			label := "PR 7"
			if kind == "issue" {
				label = "issue 37"
			}
			want := "-c " + ghCredentials + " commit -q --no-gpg-sign --no-verify -m litmus: evidence for " + label + " run " + run.ID
			if !slices.Contains(git.argv(), want) {
				t.Fatalf("missing commit label %q in %v", want, git.argv())
			}
		})
	}
}

func TestWindowLabelUsesRunTitle(t *testing.T) {
	for _, run := range []Run{{Kind: "pr", PRNumber: 7}, {Kind: "issue", IssueNumber: 37, CandidateURL: "https://github.com/o/r/pull/8"}} {
		for _, leg := range run.targets() {
			t.Run(run.Label()+"/"+leg.Name, func(t *testing.T) {
				dir := t.TempDir()
				is(t, "")(nil, writeWindowLabel(dir))
				env, err := legEnv(Recipe{}, run, leg.Name, dir, "/root", "", 1234, 5678, "/private/tmp/lt-fixture")
				is(t, "")(nil, err)
				script, err := json.Marshal(filepath.Join(dir, windowLabelScriptName))
				is(t, "")(nil, err)
				title, err := json.Marshal(envValue(env, "LITMUS_WINDOW_TITLE"))
				is(t, "")(nil, err)
				out := runNode(t, `
const vm = require("vm"), fs = require("fs");
let appTitle, windowTitle = "Original";
const window = {getTitle: () => windowTitle, setTitle: value => {windowTitle = value;}};
const electron = {
  app: {setName: value => {appTitle = value;}, whenReady: () => Promise.resolve()},
  nativeImage: {createFromPath: () => ({isEmpty: () => true})},
  BaseWindow: {getAllWindows: () => [window]}
};
vm.runInNewContext(fs.readFileSync(`+string(script)+`, "utf8"), {
  process: {versions: {electron: "test"}, type: "browser", platform: "darwin", env: {LITMUS_WINDOW_TITLE: `+string(title)+`}},
  require: name => {if (name !== "electron") throw new Error(name); return electron;},
  console: {error: () => {}},
  setInterval: () => ({unref: () => {}})
});
Promise.resolve().then(() => console.log(JSON.stringify({appTitle, windowTitle})));
`)
				var got struct{ AppTitle, WindowTitle string }
				is(t, "")(nil, json.Unmarshal(out, &got))
				if got.AppTitle != run.windowTitle(leg.Name) || got.WindowTitle != got.AppTitle {
					t.Fatalf("preload labels: %+v", got)
				}
			})
		}
	}
}
