package litmus

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

var (
	prTitle = regexp.MustCompile(`(?m)^- #\d+ (.+)$`) // the PR line of context-pack.md
	// noChange matches the conventional-commit types of PRs that claim no behavior change.
	noChange = regexp.MustCompile(`(?i)^(docs|chore|tests?|ci|build|style)(\([^)]*\))?!?:`)
)

// claimsChange reports whether the PR says it changes behavior: its title is not a docs, chore, test, ci,
// build or style commit. A run without a context pack claims nothing.
func claimsChange(runDir string) bool {
	pack, _ := os.ReadFile(filepath.Join(runDir, "context-pack.md"))
	m := prTitle.FindSubmatch(pack)
	return len(m) > 1 && !noChange.Match(m[1])
}

// baseResult is what the base leg showed for a head check: the outcome of a base-leg check of the same
// name, "observed" when only base receipts exist, or "" when the base was never looked at.
func baseResult(checks []Check, c Check) string {
	for _, b := range checks {
		if b.onLeg() == "base" && strings.EqualFold(b.Name, c.Name) && slices.Contains(outcomes, b.State) {
			return string(b.State)
		}
	}
	if slices.ContainsFunc(c.Receipts, func(r Receipt) bool { return r.onLeg() == "base" }) {
		return "observed"
	}
	return ""
}

// ranAndFailed reports whether a base check failed with a saved screenshot.
func ranAndFailed(b Check) bool {
	return b.State == CheckFail && slices.ContainsFunc(b.legReceipts(), func(r Receipt) bool { return fileExists(r.AfterPath) })
}

// failedOnBase reports whether the head check's base twin shows main without the behavior: it ran and failed, or the
// feature is not there at all (absent).
func failedOnBase(checks []Check, c Check) bool {
	i := baseTwin(checks, c)
	return i >= 0 && (ranAndFailed(checks[i]) || checks[i].State == CheckAbsent)
}

// preExisting reports whether a head check that failed or was partial did the same on the base: the PR did not cause it.
// The tester must mark the base twin --same-fault, and only the same state counts: a head fail beside a base partial,
// skip or absent may be the PR's. Absent never counts as the equal state: a feature main does not have cannot have failed there first.
func preExisting(checks []Check, c Check) bool {
	i := baseTwin(checks, c)
	if c.onLeg() != "head" || i < 0 || !checks[i].SameFault || checks[i].State != c.State {
		return false
	}
	return c.State == CheckPartial || (c.State == CheckFail && ranAndFailed(checks[i])) // absent matches neither
}

// headlineRule is the rule derive applies, as verdict.md states it (the result is the overall verdict).
const headlineRule = "The result is fail if any head check was absent or failed, except a head fail beside an unmarked base fail that ran and has evidence; that finding stays in the comment but leaves the result partial. A --same-fault base fail with evidence is pre-existing and left out. Equal partial labels on both builds still count as a failure unless the tester marked the base twin --same-fault. It is pass if every remaining head check passed and at least one passed on the head while failing on the base or while the base did not have the feature at all (absent), which shows the difference. A base failure counts only with an existing after-shot. A head failure or partial beside an absent base is the PR's own, never pre-existing. Otherwise it is partial. Checks the tester could not reach are left out and base-only checks are context."

// The reasons derive gives for a partial with nothing worse; the comment says them in its own words.
const (
	reasonPreOnly      = "every head check that did not pass also did not pass on the base"
	reasonPrePartial   = "every head check that was partial was partial on the base too"
	reasonPreBoth      = "every head check that did not pass also did not pass on the base, failing or partial"
	reasonNoneReached  = "no head check could be reached"
	reasonNoDifference = "no check passed on the head while failing on the base, so the base-vs-head difference was not shown"
)

// derive folds the head checks into the overall outcome by headlineRule. A check that fails on the base is
// usually the bug the PR fixes, so a head pass beside a base fail is the evidence a pass needs.
func derive(checks []Check) (overall CheckState, reasons []string) {
	failed, allPass, differs, reached, preFail, prePartial := false, true, false, false, false, false
	for _, c := range checks {
		if c.onLeg() != "head" || c.unreached() {
			continue
		}
		if preExisting(checks, c) {
			preFail, prePartial = preFail || c.State == CheckFail, prePartial || c.State == CheckPartial
			continue
		}
		if c.State != CheckPass && c.Note != "" {
			reasons = append(reasons, c.ID+" "+c.Name+": "+c.Note)
		}
		unproven := false
		if i := baseTwin(checks, c); i >= 0 && !checks[i].SameFault {
			base := checks[i]
			unproven = c.State == CheckPartial && base.State == CheckPartial ||
				c.State == CheckFail && base.State == CheckFail && ranAndFailed(base)
		}
		if unproven {
			what := "fails"
			if c.State == CheckPartial {
				what = "is partial"
			}
			reasons = append(reasons, c.ID+" "+c.Name+": "+what+" on main too; not shown to be the same fault")
		}
		if c.State == CheckAbsent {
			reasons = append(reasons, c.ID+" "+c.Name+": the PR build does not have this")
		}
		reached = true
		// An evidenced but unmarked head/base fail remains in the findings but does not block the merge.
		matchingFailure := unproven && c.State == CheckFail
		failed, allPass = failed || (c.State == CheckFail && !matchingFailure) || c.State == CheckAbsent || (unproven && c.State == CheckPartial), allPass && c.State == CheckPass
		differs = differs || (c.State == CheckPass && failedOnBase(checks, c))
	}
	switch {
	case failed:
		return CheckFail, reasons
	case allPass && differs:
		return CheckPass, reasons
	case !reached && preFail && prePartial:
		reasons = append(reasons, reasonPreBoth)
	case !reached && prePartial:
		reasons = append(reasons, reasonPrePartial)
	case !reached && preFail:
		reasons = append(reasons, reasonPreOnly)
	case !reached:
		reasons = append(reasons, reasonNoneReached)
	case allPass:
		reasons = append(reasons, reasonNoDifference)
	}
	return CheckPartial, reasons
}

// Submit validates the evidence, closes the video, derives the overall outcome, writes verdict.md and
// copies the evidence into the session's artifact dir. Every check needs an outcome and, unless skipped, a receipt;
// otherwise nothing is written and the offenders are listed.
//
// A prepared base that was never launched refuses the submit (TESTING_BASE_NOT_RUN) while at least baseGuardLeft of the
// deadline remains, unless skipBase carries the reason the tester gave for not running it ("" means no skip). A skip is
// honoured at any time; the head checks that have no result on the base then get a skipped base twin with that reason.
func (s *Service) Submit(ctx context.Context, sessionID, summary, skipBase string) (Verdict, error) {
	skip := strings.Join(strings.Fields(skipBase), " ")
	if skipBase != "" && skip == "" {
		return Verdict{}, invalid("TESTING_REASON_REQUIRED", "--skip-base needs a non-empty --reason")
	}
	run, done, err := s.begin(sessionID, "submit") // a retry after a CLI timeout gets TESTING_BUSY
	if err != nil {
		return Verdict{}, err
	}
	defer done()
	checks, err := s.store.Checks(run.ID)
	if err != nil {
		return Verdict{}, err
	}
	if len(checks) == 0 {
		return Verdict{}, invalid("TESTING_NO_CHECKS", "submit needs at least one check")
	}
	var incomplete []string // no receipt, or never ended
	for _, c := range checks {
		if (len(c.Receipts) == 0 && c.State != CheckSkipped) || !slices.Contains(outcomes, c.State) {
			incomplete = append(incomplete, c.ID+" "+c.Name)
		}
	}
	if len(incomplete) > 0 {
		return Verdict{}, apierr.Invalid("TESTING_CHECKS_INCOMPLETE", "every check needs an outcome and a receipt (a skipped check needs none); incomplete: "+strings.Join(incomplete, "; "), map[string]any{"checks": incomplete})
	}
	if skip == "" && s.baseFailed(run) {
		return Verdict{}, conflict("TESTING_BASE_NOT_RUN", run.baseName()+" was prepared but failed to launch, so there are no base results; submit with `--skip-base --reason \"<say that "+run.baseName()+" failed to launch and why>\"`")
	}
	if skip == "" && s.baseNeverLaunched(run) {
		how := "run " + run.baseName() + " (`ao testing launch base`), repeat each check there in the app and record its visible result"
		return Verdict{}, conflict("TESTING_BASE_NOT_RUN", how+"; record pass, fail, partial, or skipped with a note saying why, or submit with `--skip-base --reason \"<why main was not run>\"`")
	}
	if skip == "" {
		if ids := s.notComparedOnBase(run, checks); len(ids) > 0 {
			return Verdict{}, conflict("TESTING_BASE_INCOMPLETE", baseMissing(run, ids))
		}
	}
	return s.closeRun(ctx, run, skipOnBase(checks, skip), summary, "", skip)
}

// checkIDs lists the ids of the checks, "c11, c12".
func checkIDs(checks []Check) string {
	ids := make([]string, len(checks))
	for i, c := range checks {
		ids[i] = c.ID
	}
	return strings.Join(ids, ", ")
}

// baseGuardLeft is how much of the deadline must remain for an unlaunched base to refuse a submit.
const baseGuardLeft = 4 * time.Minute

// baseCoverage splits the head checks the tester reached by whether they have a result on the base: a base twin that
// was skipped, or no base look at all, is none.
func baseCoverage(checks []Check) (compared, notRun []Check) {
	for _, c := range checks {
		if c.onLeg() != "head" || c.unreached() {
			continue
		}
		if b := baseResult(checks, c); b == "" || b == string(CheckSkipped) {
			notRun = append(notRun, c)
		} else {
			compared = append(compared, c)
		}
	}
	return compared, notRun
}

// skipOnBase records the main side of every head check that has no base result as skipped, with the reason the tester
// gave for --skip-base, so each row says it was not run there. A run with no result on the base at all keeps its
// run-wide reason instead: there is no comparison to qualify.
func skipOnBase(checks []Check, why string) []Check {
	if why == "" {
		return checks
	}
	compared, notRun := baseCoverage(checks)
	if len(compared) == 0 {
		return checks
	}
	out := slices.Clone(checks)
	for _, c := range notRun {
		if baseResult(checks, c) == "" {
			out = append(out, Check{ID: "c" + strconv.Itoa(len(out)+1), Name: c.Name, Expect: c.Expect, State: CheckSkipped, Leg: "base", Note: why})
		}
	}
	return out
}

// baseNeverLaunched is true for a run whose base leg was prepared but not launched and that still has time to launch it.
func (s *Service) baseNeverLaunched(run Run) bool {
	return slices.ContainsFunc(run.Legs, func(l Leg) bool { return l.Name == "base" && l.State == "prepared" }) && run.Deadline.Sub(s.clock()) >= baseGuardLeft
}

// baseFailed is true for a run whose base leg launched and then failed.
func (s *Service) baseFailed(run Run) bool {
	return slices.ContainsFunc(run.Legs, func(l Leg) bool { return l.Name == "base" && l.State == "failed" })
}

// baseMissing tells the tester how to give the head checks ids a result on the base.
func baseMissing(run Run, ids []string) string {
	main, who := run.baseName(), "check "+ids[0]+" has"
	if len(ids) > 1 {
		who = "checks " + strings.Join(ids, ", ") + " have"
	}
	return who + " no result on " + main + "; repeat it there or record it with --result skipped and a note saying why, or submit with `--skip-base --reason \"<why>\"`"
}

// notComparedOnBase lists the head checks the tester could reach that have no base result of any kind (a base check
// ended skipped, with a note saying why, counts), once the base launched
// and while at least baseGuardLeft of the deadline remains: the comment says what each check showed on the base or why it did not.
func (s *Service) notComparedOnBase(run Run, checks []Check) (ids []string) {
	ran := func(l Leg) bool {
		return l.Name == "base" && l.State == "running"
	}
	if run.Deadline.Sub(s.clock()) < baseGuardLeft || !slices.ContainsFunc(run.Legs, ran) {
		return nil
	}
	for _, c := range checks {
		if c.onLeg() == "head" && !c.unreached() && (baseResult(checks, c) == "" || baseSkippedWithoutNote(checks, c)) {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// baseSkippedWithoutNote is true when a head check's only base result is a skip that says nothing about why.
func baseSkippedWithoutNote(checks []Check, c Check) bool {
	i := baseTwin(checks, c)
	return i >= 0 && checks[i].State == CheckSkipped && strings.TrimSpace(checks[i].Note) == ""
}

// SubmitBlocked stops a run early: a recorded failure stays fail, otherwise the result is blocked. No receipts are
// required; verdict.md carries the reason and whatever evidence the run has.
func (s *Service) SubmitBlocked(ctx context.Context, sessionID, reason string) (Verdict, error) {
	if strings.TrimSpace(reason) == "" {
		return Verdict{}, invalid("TESTING_REASON_REQUIRED", "a blocked submit needs a non-empty reason")
	}
	run, done, err := s.begin(sessionID, "submit")
	if err != nil {
		return Verdict{}, err
	}
	defer done()
	checks, err := s.store.Checks(run.ID)
	if err != nil {
		return Verdict{}, err
	}
	return s.closeRun(ctx, run, checks, "", strings.TrimSpace(reason), "")
}

// closeRun turns the checks into a verdict (a recorded fail survives a blocked reason), writes verdict.md,
// publishes the evidence, marks the run submitted and reports the session done. A run of a project that asks before
// posting stops at awaiting-approval instead, with comment.md written and nothing published or posted. skipBase is
// the reason the tester gave for not running the base, and lands in Limits.
func (s *Service) closeRun(ctx context.Context, run Run, checks []Check, summary, blocked, skipBase string) (Verdict, error) {
	runDir := filepath.Join(s.store.root, run.ID)
	run = s.stopRecording(ctx, run) // while the engines are still alive; returns the run with its recording state
	videos := s.finishVideo(ctx, runDir, run)
	var limits []string
	cleanErr := s.teardown(ctx, run) // every close cleans up after itself; a failure is reported, never fatal
	if cleanErr != nil {
		slog.Warn("litmus: cleanup after submit failed", "run", run.ID, "error", cleanErr)
		limits = append(limits, "Cleanup after submit failed, stop leftovers with `ao testing cleanup`: "+clip(cell(cleanErr.Error()), 400))
	}

	if skipBase != "" {
		switch compared, notRun := baseCoverage(checks); {
		case len(compared) == 0:
			limits = append(limits, "base not run: "+clip(cell(skipBase), 300))
		case len(notRun) > 0:
			limits = append(limits, fmt.Sprintf("base compared on %d of %d checks; not run there: %s: %s", len(compared), len(compared)+len(notRun), checkIDs(notRun), clip(cell(skipBase), 300)))
		}
	}
	claims := claimsChange(runDir)
	v := Verdict{Checks: checks, Summary: strings.TrimSpace(summary), SkipBase: skipBase}
	if blocked != "" {
		v.Overall, v.Reasons = derive(checks)
		if v.Overall == CheckFail {
			v.Reasons = append([]string{blocked}, v.Reasons...)
			v.StoppedEarly = blocked
		} else {
			v.Overall, v.Reasons = CheckBlocked, []string{blocked}
		}
	} else {
		v.Overall, v.Reasons = derive(checks)
	}
	md := renderVerdict(run, v, claims, videos, runDir, s.clock(), limits)
	if err := s.store.WriteFile(run.ID, "verdict.md", []byte(md)); err != nil {
		return Verdict{}, err
	}
	if err := publishArtifacts(sessionartifacts.Dir(s.store.dataDir(), domain.SessionID(run.SessionID)), runDir, md, renderIndex(run, v, videos), videos); err != nil {
		return Verdict{}, fmt.Errorf("copy evidence to the session artifact dir: %w", err)
	}
	if s.mustAsk(run, v) { // compose the comment and stop: nothing is published or posted until the user approves it
		if err := s.store.put(run.ID, verdictFile, v); err != nil {
			return Verdict{}, fmt.Errorf("save the verdict for approval: %w", err)
		}
		if _, _, err := s.composeComment(run, v); err != nil {
			return Verdict{}, fmt.Errorf("write comment.md: %w", err)
		}
		v.CommentState, v.EvidenceState = CommentAwaiting, EvidenceOff
		if _, err := s.update(run.ID, func(cur *Run) {
			cur.State, cur.CommentState, cur.EvidenceState = RunAwaitingApproval, v.CommentState, v.EvidenceState
			for i := range cur.Legs {
				if cleanErr == nil {
					cur.Legs[i].State = "cleaned"
				}
			}
		}); err != nil {
			return v, err
		}
	} else {
		if _, err := s.update(run.ID, func(cur *Run) {
			cur.State = RunSubmitted
			for i := range cur.Legs {
				if cleanErr == nil {
					cur.Legs[i].State = "cleaned"
				}
			}
		}); err != nil {
			return v, err
		}
		var ev evidence
		v.CommentURL, v.CommentState, ev = s.postComment(ctx, run, v) // never fails the submit
		v.EvidenceState, v.EvidenceCommit = ev.state, ev.commit
		if _, err := s.update(run.ID, func(cur *Run) {
			cur.CommentURL, cur.CommentState, cur.EvidenceState, cur.EvidenceCommit = v.CommentURL, v.CommentState, v.EvidenceState, v.EvidenceCommit
			cur.VideoState, cur.VideoURLs = ev.videoState, ev.videoURLs()
		}); err != nil {
			slog.Warn("litmus: could not save the comment state", "run", run.ID, "error", err)
		}
	}
	if s.report != nil { // the tester's last act: the session closes itself without `ao report --done`
		note := fmt.Sprintf("Litmus verdict %s for %s#%d: see verdict.md in the session artifacts.", v.Overall, run.Repo, run.PRNumber)
		if err := s.report(ctx, run.SessionID, note); err != nil {
			slog.Warn("litmus: done report failed", "run", run.ID, "session", run.SessionID, "error", err)
		}
	}
	if s.kill != nil { // a finished tester would otherwise sit idle until someone noticed
		time.AfterFunc(endGrace, func() {
			kctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.kill(kctx, run.SessionID); err != nil {
				slog.Warn("litmus: could not stop the tester after its run closed", "run", run.ID, "session", run.SessionID, "error", err)
			}
		})
	}
	s.autoPrune(ctx, run) // the retention pass every finished run ends with; never fails the submit
	return v, nil
}

func cell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", `\|`)
}

// relPath is p relative to the run dir, which is also where it sits in the session artifact dir.
func relPath(runDir, p string) string {
	if rel, err := filepath.Rel(runDir, p); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(p)
}

// fileExists reports whether p is a regular file: the verdict links only to evidence that is there.
func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// untestedLimit is the standing last line of every Limits list.
const untestedLimit = "Only the checks above were tried; the rest of the PR's changes are untested."

// compareCells is a check's head and base cells in the checks table. beside is true for a base-leg check
// that is shown beside its head twin and so gets no row of its own.
func compareCells(checks []Check, c Check) (head, base string, beside bool) {
	if c.onLeg() == "base" {
		if slices.ContainsFunc(checks, func(h Check) bool { return h.onLeg() == "head" && strings.EqualFold(h.Name, c.Name) }) {
			return "", "", true
		}
		return "-", string(c.State), false
	}
	switch base = baseResult(checks, c); {
	case base == string(CheckSkipped):
		base = "not compared on base"
	case base != "":
	case c.unreached():
		base = "-"
	default:
		base = "not compared on base"
	}
	return string(c.State), base, false
}

// checkLimit is the Limits line a check earns, or "": the tester could not reach it, or the base never showed it.
func checkLimit(checks []Check, c Check) string {
	switch {
	case c.unreached():
		return fmt.Sprintf("%s %s (%s leg): could not reach. %s", c.ID, c.Name, c.onLeg(), cell(c.Note))
	case preExisting(checks, c):
		return fmt.Sprintf("%s %s: pre-existing, same on base: %s", c.ID, c.Name, clip(cell(c.Note), 120))
	case c.onLeg() == "head" && baseResult(checks, c) == "":
		return fmt.Sprintf("%s %s: not compared on base, so whether the behavior changed cannot be told.", c.ID, c.Name)
	}
	return ""
}

func renderVerdict(run Run, v Verdict, claims bool, videos []legVideo, runDir string, now time.Time, limits []string) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	p("# Verdict: %s\n\n%s#%d, run %s, base %s, head %s\n", v.Overall, run.Repo, run.PRNumber, run.ID, cmp.Or(run.BaseSHA, "none"), cmp.Or(run.HeadSHA, "none"))
	duration := now.Sub(run.CreatedAt).Round(time.Second).String() + ", never prepared"
	if run.prepared() {
		builds, testing := run.buildsAndTesting(now)
		duration = fmt.Sprintf("builds %s, testing %s of a %s budget", builds.Round(time.Second), testing.Round(time.Second), RunBudget)
	}
	p("Duration: %s. Tokens: not measured by Litmus, see the session's usage. Video: %s.\n", duration, videoLine(videos))
	if v.Overall == CheckBlocked {
		p("## Blocked\n\n%s\n", strings.Join(v.Reasons, "\n"))
		if run.Error != "" {
			p("Daemon: %s\n", run.Error)
		}
	}
	if v.StoppedEarly != "" {
		p("Stopped early: %s\n", v.StoppedEarly)
	}
	if v.Summary != "" {
		p("## Summary\n\n%s\n", v.Summary)
	}
	var gaps []string
	for _, g := range run.Gaps {
		if strings.HasPrefix(g, "The PR build started without its sample content (seed step ") || strings.HasPrefix(g, "The main build started without its sample content (seed step ") ||
			strings.HasPrefix(g, "The PR build's recording was not available:") || strings.HasPrefix(g, "The main build's recording was not available:") {
			limits = append(limits, g)
		} else {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) > 0 {
		p("## Gaps\n\nWhat intake could not get; none of it changes the result.\n")
		for _, g := range gaps {
			p("- %s", g)
		}
		p("")
	}
	p("## Checks\n\n| check | expect | head result | base result | receipts |\n|---|---|---|---|---|")
	for _, c := range v.Checks {
		head, base, beside := compareCells(v.Checks, c)
		if beside {
			continue // shown beside its head check
		}
		var links []string
		for _, r := range c.Receipts {
			switch {
			case r.AfterPath == "":
			case fileExists(r.AfterPath):
				links = append(links, fmt.Sprintf("[%d](%s)", r.Seq, relPath(runDir, r.AfterPath)))
			default:
				limits = append(limits, fmt.Sprintf("%s receipt %d: its after-shot file is missing, so it is not linked.", c.ID, r.Seq))
			}
		}
		p("| %s %s | %s | %s | %s | %s |", c.ID, cell(c.Name), cell(c.Expect), head, base, strings.Join(links, " "))
	}
	p("\n## Notes\n")
	for _, c := range v.Checks {
		p("### %s %s (%s leg): %s\n\nExpected: %s\n\nNote: %s\n", c.ID, c.Name, c.onLeg(), c.State, c.Expect, c.Note)
		switch {
		case c.Diff == "":
		case fileExists(c.Diff):
			p("Base vs head pixel diff: [%s](%s)\n", filepath.Base(c.Diff), relPath(runDir, c.Diff))
		default:
			limits = append(limits, fmt.Sprintf("%s pixel diff: the image file is missing, so it is not linked.", c.ID))
		}
	}
	acts := readActions(runDir)
	p("## Logs\n")
	for _, l := range run.Legs {
		tail := tailFile(filepath.Join(runDir, l.Name, "logs", "dev.log"), 30)
		p("### %s\n\nDaemon log tail:\n\n~~~~\n%s\n~~~~\n\nConsole errors:\n\n~~~~\n%s\n~~~~\n", l.Name, strings.ReplaceAll(tail, "~~~", "---"), strings.ReplaceAll(consoleErrors(acts, l.Name), "~~~", "---"))
	}
	p("## Limits\n")
	if !slices.ContainsFunc(run.Legs, func(l Leg) bool { return l.Name == "base" && l.State == "running" }) {
		p("- The base leg was not launched, so nothing was observed on it.")
	}
	for _, c := range v.Checks {
		if l := checkLimit(v.Checks, c); l != "" {
			p("- %s", l)
		}
	}
	p("- %s", headlineRule)
	p("- The PR title %s a behavior change. That is informational and does not change the result.", map[bool]string{true: "claims", false: "does not claim"}[claims])
	if slices.ContainsFunc(acts, func(a actionRec) bool { return a.Unassigned }) {
		p("- Some state-changing actions ran while no check was open; they are not evidence for any check.")
	}
	for _, l := range limits {
		p("- %s", l)
	}
	p("- %s", untestedLimit)
	return b.String()
}

func renderIndex(run Run, v Verdict, videos []legVideo) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	p("# Litmus: %s#%d, verdict %s\n\nFull report: [verdict.md](verdict.md).\n", run.Repo, run.PRNumber, v.Overall)
	var links []string
	for _, vid := range videos {
		for _, f := range vid.files() {
			links = append(links, fmt.Sprintf("[%s](%s)", f, f))
		}
	}
	if len(links) > 0 {
		p("Video: %s.\n", strings.Join(links, ", "))
	}
	for _, c := range v.Checks {
		p("## %s %s: %s\n", c.ID, c.Name, c.State)
		for _, r := range c.Receipts {
			if r.AfterPath != "" && fileExists(r.AfterPath) {
				p(`<img src="receipts/%s" alt="%s %s" width="480">`, filepath.Base(r.AfterPath), r.onLeg(), r.Action)
			}
		}
		if c.Diff != "" && fileExists(c.Diff) {
			p(`<img src="receipts/%s" alt="pixel diff" width="480">`, filepath.Base(c.Diff))
		}
		p("")
	}
	return b.String()
}

// publishArtifacts copies the receipts and each leg's videos into the session's artifact dir and writes verdict.md and
// index.md beside them; index.md with relative <img> links is how AO shows content inline in the session.
func publishArtifacts(dir, runDir, verdictMD, indexMD string, videos []legVideo) error {
	if dir == "" {
		return errors.New("no artifact dir for this session")
	}
	if err := os.MkdirAll(filepath.Join(dir, "receipts"), 0o750); err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(runDir, "receipts", "*"))
	copies := map[string]string{} // source -> destination
	for _, f := range files {
		copies[f] = filepath.Join(dir, relPath(runDir, f))
	}
	for _, video := range videos {
		if video.raw {
			copies[cmp.Or(video.source, recordingPath(runDir, video.leg))] = filepath.Join(dir, video.leg+".mp4")
		}
		if video.cut {
			copies[cutPath(runDir, video.leg)] = filepath.Join(dir, video.leg+"-cut.mp4")
		}
		for _, clip := range video.clips {
			copies[clip] = filepath.Join(dir, "clips", filepath.Base(clip))
		}
	}
	for src, dst := range copies {
		if fileExists(src) {
			if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
				return err
			}
			if err := copyFile(src, dst); err != nil {
				return err
			}
		}
	}
	if err := writeAtomic(filepath.Join(dir, "verdict.md"), []byte(verdictMD)); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "index.md"), []byte(indexMD))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// readActions loads actions.jsonl up to its first unreadable line.
func readActions(runDir string) (recs []actionRec) {
	f, err := os.Open(filepath.Join(runDir, "actions.jsonl"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	for dec := json.NewDecoder(f); dec.More(); {
		var r actionRec
		if dec.Decode(&r) != nil {
			break
		}
		recs = append(recs, r)
	}
	return recs
}

// consoleErrors collects the distinct console lines mentioning an error that a leg's receipts captured.
func consoleErrors(acts []actionRec, leg string) string {
	var out []string
	for _, a := range acts {
		for _, l := range strings.Split(a.ConsoleTail, "\n") {
			if l = strings.TrimSpace(l); a.Leg == leg && strings.Contains(strings.ToLower(l), "error") && !slices.Contains(out, l) && len(out) < 10 {
				out = append(out, clip(l, 300))
			}
		}
	}
	return strings.Join(out, "\n")
}

// tailFile returns the last n lines of a file, reading only its final 64 KiB.
func tailFile(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return "(no log)"
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() > 64<<10 {
		_, _ = f.Seek(-(64 << 10), io.SeekEnd)
	}
	data, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	lines := strings.Split(lastLines(data, n), "\n")
	for i, l := range lines {
		lines[i] = clip(l, 240)
	}
	return strings.Join(lines, "\n")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
