package litmus

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// composeFixture loads a real run's checks and run record (testdata/<name>.json) and writes a stand-in file for every
// after-shot a receipt points at.
func composeFixture(t *testing.T, name string) (Run, []Check) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	is(t, "")(nil, err)
	var f struct {
		Run    Run
		Checks []Check
	}
	is(t, "")(nil, json.Unmarshal(raw, &f))
	dir := t.TempDir()
	for i := range f.Checks {
		for j := range f.Checks[i].Receipts {
			r := &f.Checks[i].Receipts[j]
			if r.AfterPath == "" {
				continue
			}
			r.AfterPath = filepath.Join(dir, filepath.Base(r.AfterPath))
			is(t, "")(nil, os.WriteFile(r.AfterPath, []byte("png"), 0o600))
		}
	}
	return f.Run, f.Checks
}

// composeComment stages the evidence of a fixture and renders the comment the way submit does.
func composeComment(t *testing.T, run Run, v Verdict) (string, []evItem) {
	t.Helper()
	svc, _ := newSvc(t)
	svc.media = &sizedMedia{gif: map[string]int{}}
	dir := filepath.Join(svc.store.root, "run_s1", "evidence")
	is(t, "")(nil, os.MkdirAll(dir, 0o750))
	items, limits, err := svc.stageEvidence(ctx, run, v, dir)
	if err != nil || len(limits) != 0 {
		t.Fatalf("stage: limits %q err %v", limits, err)
	}
	for i := range items {
		items[i].url = evidenceURL(run.Repo, fakeSHA, path.Join(evidencePath(run), items[i].file), false)
	}
	return renderComment(run, v, evidence{state: "published", commit: fakeSHA, items: items}, run.CreatedAt.Add(2*time.Minute)), items
}

func TestComposeRun12(t *testing.T) {
	run, checks := composeFixture(t, "compose-run12")
	// The tester did not repeat c2 on main and said why.
	checks = append(checks, Check{ID: "c4", Name: checks[1].Name, Expect: checks[1].Expect, State: CheckSkipped, Leg: "base",
		Note: "main has no Go back or Go forward buttons, so there was nothing to re-check"})
	v := commentVerdict(checks)
	if v.Overall != CheckPass {
		t.Fatalf("overall %s", v.Overall)
	}
	got, items := composeComment(t, run, v)
	t.Logf("composed comment:\n%s", got)

	for _, want := range []string{
		"**Result: the PR does what it says in the 2 checks I finished. One check only ran on the PR build.**",
		"On the PR build, with the sidebar expanded, Collapse sidebar, Go back, Go forward (both disabled on Home) and the Orchestrator.inc brand are all present. On main, with the sidebar expanded, only Collapse sidebar and the Orchestrator.inc brand show.",
		"On the PR build, opening a project enabled Go back.",
		"I did not repeat this on main: main has no Go back or Go forward buttons, so there was nothing to re-check.\n",
		"Main on the left: with the sidebar expanded, only Collapse sidebar and the Orchestrator.inc brand show. PR build on the right: with the sidebar expanded, Collapse sidebar, Go back, Go forward (both disabled on Home).",
		"\nPR build: opening a project enabled Go back. Collapsing the sidebar kept both arrows, and re-expanding it showed both arrows plus the Orchestrator.inc brand.\n",
		`Step 2, PR build: click "Sample project 10"`,
		"/runs/6403/20261011-171714/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q", want)
		}
	}
	doubled := regexp.MustCompile(`(?i)\b(on|in) (the PR build|main)\b[^.]*\b(on|in) (the PR build|main)\b`)
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "I checked") && !strings.HasPrefix(line, "Next I checked") && !strings.HasPrefix(line, "I also checked") {
			continue
		}
		// a check paragraph names each build once per sentence
		for _, sentence := range strings.Split(line, ". ") {
			if doubled.MatchString(sentence) {
				t.Errorf("a build is named twice in %q", sentence)
			}
		}
	}
	for _, bad := range []string{"/Users/", "run_ao", "ao-10", "look at before merging", "could not compare"} {
		if strings.Contains(got, bad) {
			t.Errorf("comment contains %q", bad)
		}
	}
	for _, it := range items {
		if strings.Contains(it.caption, "/Users/") {
			t.Errorf("caption %q", it.caption)
		}
	}
	noSlop(t, got)
}

func TestComposeRun13SaysTheSkipReasonOnceAndNotUnderTheChecks(t *testing.T) {
	run, checks := composeFixture(t, "compose-run13")
	v := commentVerdict(checks)
	v.SkipBase = "The PR build's search worked, but the Local/Cloud tabs and visual layout could not be confirmed here, so a comparison against main would not change the verdict"
	got, _ := composeComment(t, run, v)
	t.Logf("composed comment:\n%s", got)
	const reason = "I did not run main: the PR build's search worked, but the Local/Cloud tabs and visual layout could not be confirmed here, so a comparison against main would not change the verdict.\n"
	lines := strings.Split(got, "\n")
	if strings.Count(got, "I did not run main") != 1 || strings.Contains(got, "could not compare") || lines[2] != "**Result: the PR build works as far as I could check; nothing was compared against main, so read the diff before merging.**" || lines[4]+"\n" != reason {
		t.Errorf("the reason is said once, right after the result line:\n%s", got)
	}
	for _, p := range strings.Split(got, "\n\n") {
		if strings.HasPrefix(p, "I checked that") || strings.HasPrefix(p, "Next I checked that") {
			if strings.Contains(p, "main") {
				t.Errorf("a check paragraph mentions main: %q", p)
			}
			if !strings.HasSuffix(p, ".") {
				t.Errorf("paragraph does not end after the PR build sentence: %q", p)
			}
		}
	}
	for _, want := range []string{
		" The PR build shows a full-width search field above one row per harness in Settings > Harness; the Local/Cloud tabs did not appear because cloud is off here. I only checked page structure, not the dividers or logo tiles visually.\n",
		"\nPR build. The PR build shows a full-width search field above one row per harness in Settings > Harness; the Local/Cloud tabs did not appear because cloud is off here.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q", want)
		}
	}
	for _, bad := range []string{"On the PR build, the PR build", "PR build: the PR build"} {
		if strings.Contains(got, bad) {
			t.Errorf("the build is named twice: %q", bad)
		}
	}
	noSlop(t, got)
}

func TestBuildsRemovesTheBuildTheCommentAlreadyNames(t *testing.T) {
	run := Run{BaseRef: "main"}
	for in, want := range map[string]string{
		"With the sidebar expanded on the PR build, Collapse sidebar, Go back, Go forward (both disabled on Home) and the Orchestrator.inc brand are all present.":         "With the sidebar expanded, Collapse sidebar, Go back, Go forward (both disabled on Home) and the Orchestrator.inc brand are all present.",
		"On main with the sidebar expanded, only Collapse sidebar and the Orchestrator.inc brand show. Go back and Go forward are missing, which is the bug the PR fixes.": "With the sidebar expanded, only Collapse sidebar and the Orchestrator.inc brand show. Go back and Go forward are missing, which is the bug the PR fixes.",
		"On the PR build, opening a project enabled Go back. Collapsing the sidebar kept both arrows.":                                                                     "Opening a project enabled Go back. Collapsing the sidebar kept both arrows.",
		"in the PR build, it works":                      "It works",
		"The menu stayed open. It is on main too.":       "The menu stayed open. It is on main too.",
		"Typing grok in the PR build's search worked":    "Typing grok in the PR build's search worked",
		"Opened the menu on main and then closed it, ok": "Opened the menu and then closed it, ok",
	} {
		if got := builds(in, run); got != want {
			t.Errorf("builds(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	if got := builds("On develop it fails, on develop twice", Run{BaseRef: "develop"}); got != "It fails, on develop twice" {
		t.Errorf("another base branch is named too: %q", got)
	}
}

func TestHeroCaptionOfASingleSideCarriesItsNote(t *testing.T) {
	if got := heroCaption(commentRun, hero{pr: &shot{}, prNote: "Opening a project enabled Go back. Collapsing the sidebar kept both arrows"}); got != "PR build: opening a project enabled Go back. Collapsing the sidebar kept both arrows." {
		t.Errorf("head only: %q", got)
	}
	if got := heroCaption(commentRun, hero{base: &shot{}, baseNote: "There are no arrows."}); got != "Main: there are no arrows." {
		t.Errorf("base only: %q", got)
	}
	if got := heroCaption(commentRun, hero{pr: &shot{}}); got != "The PR build." {
		t.Errorf("no note: %q", got)
	}
}

func TestANoteThatOpensWithABuildKeepsItsOwnSentence(t *testing.T) {
	run := Run{BaseRef: "main"}
	for in, want := range map[string]bool{"The PR build shows the list.": true, "the main build is empty.": true, "Main has no arrows.": true, "The PR build's search worked.": true,
		"Typing grok in the PR build worked.": false, "Maintenance mode is shown.": false, "It shows on main.": false} {
		if got := subjectLed(in, run); got != want {
			t.Errorf("subjectLed(%q) = %t", in, got)
		}
	}
	if got := heroCaption(run, hero{pr: &shot{}, base: &shot{}, baseNote: "Main has no arrows.", prNote: "The PR build shows both arrows."}); got != "Main on the left. Main has no arrows. PR build on the right. The PR build shows both arrows." {
		t.Errorf("both sides: %q", got)
	}
	if got := heroCaption(run, hero{pr: &shot{}, base: &shot{}, baseNote: "There are no arrows.", prNote: "The PR build shows both arrows."}); got != "Main on the left: there are no arrows. PR build on the right. The PR build shows both arrows." {
		t.Errorf("one side: %q", got)
	}
}

func TestPlainNameDropsLocalPaths(t *testing.T) {
	for in, want := range map[string]string{
		"Sample project 10 /Users/dev/.ao/data/litmus/repos/x/2db3ffe just now": "Sample project 10",
		"Sample project 10\n/home/dev/x just now":                               "Sample project 10",
		"Open  ~/work/app  now":                                                 "Open",
		"/Users/dev/x Settings":                                                 "Settings",
		"Collapse sidebar":                                                      "Collapse sidebar",
	} {
		if got := plainName(in); got != want {
			t.Errorf("plainName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := stepText("click", []string{"@e21"}, "button", "Sample project 10 /Users/dev/.ao/data/x just now"); got != `click "Sample project 10"` {
		t.Errorf("step text: %q", got)
	}
	if got := caption(actionRec{Seq: 2, Action: "click", Args: []string{"@e21"}, TargetRole: "button", TargetName: "Sample project 10 /Users/dev/.ao/x"}); got != `Step 2: click "Sample project 10"` {
		t.Errorf("srt caption: %q", got)
	}
}

func TestEvidencePathHasNoSessionOrRunID(t *testing.T) {
	run := Run{ID: "run_ao-10", SessionID: "ao-10", PRNumber: 6403, CreatedAt: time.Date(2026, 10, 11, 22, 47, 14, 0, time.FixedZone("IST", 5*3600+1800))}
	if got := evidencePath(run); got != "runs/6403/20261011-171714" {
		t.Errorf("evidence path %q", got)
	}
}

func TestSkippedCheckLowercasesItsExpectationAndAllSkippedEndsOnTheResultLine(t *testing.T) {
	checks := []Check{
		{ID: "c1", Name: "report-wrapper", Expect: "A report delivery inside a Claude paste wrapper is recorded", State: CheckSkipped, Note: "this change has no screen."},
		{ID: "c2", Name: "cli-flag", Expect: "I run the new flag and it prints the count", State: CheckSkipped, Note: "no app to drive."},
	}
	got := renderComment(commentRun, commentVerdict(checks), evidence{}, commentRun.CreatedAt)
	for _, want := range []string{"**Result: could not test this from the app.**", "I could not check that a report delivery inside a Claude paste wrapper is recorded. This change has no screen.", "I could not check that I run the new flag"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "could not reach any check") {
		t.Errorf("the result line already says it:\n%s", got)
	}
	some := commentVerdict([]Check{checks[0], {ID: "c3", Name: "x", Expect: "x works", State: CheckPartial, Note: "half", Receipts: []Receipt{{Seq: 1}}}})
	if got := renderComment(commentRun, some, evidence{}, commentRun.CreatedAt); strings.Contains(got, "could not test this from the app") {
		t.Errorf("only some checks were skipped:\n%s", got)
	}
}

func TestLowerLeadKeepsUILabelsAndNames(t *testing.T) {
	notes := []string{"Search field above the list. Rows list every harness.", "Opened Settings > Harness and the Go menu."}
	for in, want := range map[string]string{
		"Go back and Go forward buttons are visible beside the Orchestrator.inc brand when the sidebar is open": "Go back and Go forward buttons are visible beside the Orchestrator.inc brand when the sidebar is open",
		"Search field is full width; Local/Cloud tabs appear above it when cloud is on":                         "search field is full width; Local/Cloud tabs appear above it when cloud is on",
		"Settings > Harness shows tabs above a search field":                                                    "Settings > Harness shows tabs above a search field", // Settings is named mid-sentence in a note
		"Rows have dividers":                            "rows have dividers", // only a sentence start in the notes
		"Go menu opens":                                 "Go menu opens",      // named mid-sentence in a note
		"CLI tests pass":                                "CLI tests pass",
		"Back/forward navigate and stay visible":        "Back/forward navigate and stay visible",
		"Orchestrator.inc brand is shown":               "Orchestrator.inc brand is shown",
		"Tab2 opens":                                    "Tab2 opens",
		"A report delivery is recorded":                 "a report delivery is recorded",
		"With the sidebar expanded, both arrows show":   "with the sidebar expanded, both arrows show",
		"Typing grok filters the list":                  "typing grok filters the list",
		"Opening a project enables Go back":             "opening a project enables Go back",
		"After a restart the menu is still open":        "after a restart the menu is still open",
		"I run the new flag":                            "I run the new flag",
		"The cli package's tests pass":                  "the cli package's tests pass",
		"Settings page opens":                           "Settings page opens",
		"Each harness row has a divider":                "each harness row has a divider",
		"Once the sidebar collapses, the brand is gone": "once the sidebar collapses, the brand is gone",
	} {
		if got := lowerLead(in, notes...); got != want {
			t.Errorf("lowerLead(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	if got := lowerLead("Settings page opens"); got != "settings page opens" {
		t.Errorf("no notes: %q", got)
	}
}

// TestComposeRun14 is the all-partial rerun of #6266: nothing failed on either build, so the comment must not say
// anything failed, and a partial pair is not described as a failure.
func TestComposeRun14(t *testing.T) {
	run, checks := composeFixture(t, "compose-run14")
	for i := range checks {
		checks[i].SameFault = checks[i].onLeg() == "base"
	}
	v := commentVerdict(checks)
	if v.Overall != CheckPartial || !slices.Equal(v.Reasons, []string{reasonPrePartial}) {
		t.Fatalf("overall %s reasons %q", v.Overall, v.Reasons)
	}
	got, _ := composeComment(t, run, v)
	t.Logf("composed comment:\n%s", got)
	paras := strings.Split(got, "\n\n")
	if want := "**Result: nothing failed, but I could not confirm the change from the app; read the diff before merging.**"; paras[1] != want {
		t.Errorf("result line %q", paras[1])
	}
	if !strings.HasPrefix(paras[2], "I checked that search field is full width; Local/Cloud tabs appear above it when cloud is on. On the PR build, ") ||
		strings.Contains(paras[2], "I could not confirm it on main either.") { // the main sentence already says what was seen
		t.Errorf("first paragraph %q", paras[2])
	}
	if want := "\n\nWhat I could not confirm on the PR build I could not confirm on main either.\n\n"; !strings.Contains(got, want) {
		t.Errorf("no %q", want)
	}
	for _, bad := range []string{"Whatever failed", "so the PR did not cause it", "same thing happens", "do not rely on this run"} {
		if strings.Contains(got, bad) {
			t.Errorf("comment contains %q", bad)
		}
	}
	noSlop(t, got)
}

func TestPreExistingReasonSaysWhatWasLeftOut(t *testing.T) {
	mk := func(id, name, leg string, s CheckState) Check {
		return Check{ID: id, Name: name, State: s, Leg: leg, SameFault: leg == "base", Receipts: []Receipt{{Seq: 1, Leg: leg, Action: "click", AfterPath: evidenceFile(t)}}}
	}
	for _, c := range []struct {
		name   string
		checks []Check
		reason string
		plain  string
	}{
		{"fails", []Check{mk("c1", "a", "", CheckFail), mk("c2", "a", "base", CheckFail)}, reasonPreOnly, "Whatever failed on the PR build failed on main too."},
		{"partials", []Check{mk("c1", "a", "", CheckPartial), mk("c2", "a", "base", CheckPartial)}, reasonPrePartial, "What I could not confirm on the PR build I could not confirm on main either."},
		{"both", []Check{mk("c1", "a", "", CheckFail), mk("c2", "a", "base", CheckFail), mk("c3", "b", "", CheckPartial), mk("c4", "b", "base", CheckPartial)}, reasonPreBoth,
			"Whatever failed on the PR build failed on main too. What I could not confirm on the PR build I could not confirm on main either."},
	} {
		overall, reasons := derive(c.checks)
		if overall != CheckPartial || len(reasons) != 1 || reasons[0] != c.reason {
			t.Errorf("%s: %s %q", c.name, overall, reasons)
			continue
		}
		if got := plainReason(reasons[0], "main"); got != c.plain {
			t.Errorf("%s: plain reason %q", c.name, got)
		}
	}
}

func TestVerdictLineForAllPartialAndUnreached(t *testing.T) {
	const confirm = "nothing failed, but I could not confirm the change from the app; read the diff before merging"
	const unfinished = "nothing failed, but I could not confirm the change from the app; read the diff before merging"
	for _, c := range []struct {
		name string
		t    tally
		want string
	}{
		{"partial pairs only", tally{pre: 2, prePartial: 2}, confirm},
		{"a failing pair among them", tally{pre: 2, prePartial: 1}, unfinished},
		{"a check never reached", tally{pre: 1, prePartial: 1, unreached: 1}, unfinished},
		{"a partial that main did not show", tally{pre: 1, prePartial: 1, partial: 1}, unfinished},
	} {
		if got := verdictLine(CheckPartial, c.t, "main"); got != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

func TestCheckParaKeepsNamesAndLowersCommonNouns(t *testing.T) {
	for expect, want := range map[string]string{
		"Go back and Go forward buttons are visible beside the Orchestrator.inc brand when the sidebar is open": "I checked that Go back and Go forward buttons are visible beside the Orchestrator.inc brand when the sidebar is open.",
		"Search field is full width; Local/Cloud tabs appear above it when cloud is on":                         "I checked that search field is full width; Local/Cloud tabs appear above it when cloud is on.",
	} {
		c := Check{ID: "c1", Name: "x", Expect: expect, State: CheckPass, Note: "It works.", Receipts: []Receipt{{Seq: 1, Leg: "head"}}}
		if got := checkPara(commentRun, commentVerdict([]Check{c}), c, 300, 0); !strings.HasPrefix(got, want) {
			t.Errorf("checkPara\n got %q\nwant prefix %q", got, want)
		}
	}
}

// Run ao-17 (#6266): one check passed and failed on main, the other was skipped on the PR build and never ran anywhere.
func TestComposeRun17CountsTheSkippedCheckAsNotDone(t *testing.T) {
	run, checks := composeFixture(t, "compose-run17")
	v := commentVerdict(checks)
	got, _ := composeComment(t, run, v)
	if !strings.Contains(got, "**Result: the PR does what it says in the 1 check I finished. One check could not be done.**") || strings.Contains(got, "only ran on the PR build") {
		t.Errorf("result line:\n%s", got)
	}
	noSlop(t, got)
}

func TestWithoutComparisonDropsOnlyTheSentencesAboutMain(t *testing.T) {
	run := Run{BaseRef: "main"}
	for in, want := range map[string]string{
		"Not yet compared against main; Local/Cloud tabs were not visible because cloud is off here.": "Local/Cloud tabs were not visible because cloud is off here.",
		"Not compared against main.":                                       "",
		"The list showed 5 rows. Compared with main, it has one more row.": "The list showed 5 rows.",
		"Rows are compared by name. The list showed 5 rows.":               "Rows are compared by name. The list showed 5 rows.",
		"The list showed 5 rows. Main was not compared":                    "The list showed 5 rows.",
		"The list showed 5 rows":                                           "The list showed 5 rows",
	} {
		if got := withoutComparison(in, run); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
	if got := withoutComparison("Checked. Not compared with develop.", Run{BaseRef: "develop"}); got != "Checked." {
		t.Errorf("another base branch: %q", got)
	}
}

// A sentence about what was compared with main is false when the check has no result on main, and true when it has one.
func TestANoteAboutWhatWasComparedIsKeptOnlyWhenMainWasRun(t *testing.T) {
	note := "Not yet compared against main; Local/Cloud tabs were not visible because cloud is off here."
	run, checks := composeFixture(t, "compose-run17")
	checks[0].Note = note
	got, _ := composeComment(t, run, commentVerdict(checks)) // c3 is c1's result on main
	if !strings.Contains(got, "ot yet compared against main") || !strings.Contains(got, "ocal/Cloud tabs were not visible because cloud is off here.") {
		t.Errorf("a check that ran on main lost its sentence:\n%s", got)
	}
	checks = slices.DeleteFunc(checks, func(c Check) bool { return c.onLeg() == "base" })
	checks[0].Note = note
	got, _ = composeComment(t, run, commentVerdict(checks))
	if strings.Contains(got, "ot yet compared against main") || !strings.Contains(got, "ocal/Cloud tabs were not visible because cloud is off here.") {
		t.Errorf("a check with no result on main kept its claim:\n%s", got)
	}
}
