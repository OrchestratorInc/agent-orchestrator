package litmus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// commentDoc is one GitHub comment and its local draft. The first document is
// the only one that carries the run marker; the other documents are addressed
// by the ids recorded alongside the run's drafts.
type commentDoc struct {
	Name      string
	Body      string
	Marker    bool
	ImageFile string
	ClipFile  string
	Number    int
	Fixed     bool
	CommentID int64
}

type commentManifestEntry struct {
	Name   string
	File   string
	Number int
	Fixed  bool
	ID     int64
}

// PostedComment is the durable mapping from a check name to its PR comment.
// Number is the bug number used in the rendered body; Fixed keeps reruns from
// treating a resolved comment as a new bug.
type PostedComment struct {
	Name   string `json:"name"`
	ID     int64  `json:"id,omitempty"`
	URL    string `json:"url,omitempty"`
	Number int    `json:"number,omitempty"`
	Fixed  bool   `json:"fixed,omitempty"`
}

// bugComment is a head-side failure for which main either passed, has no
// feature, or has no result. A base failure is deliberately excluded: it is
// not evidence that this PR introduced the failure.
type bugComment struct {
	Check     Check
	Base      *Check
	Number    int
	Total     int
	ImageFile string
	ImageURL  string
	ClipFile  string
	ClipURL   string
}

// Comment modes, set on the run by intake.
const (
	CommentPost = "post"
	CommentOff  = "off"
)

const (
	commentCap     = 6000 // GitHub allows far more; a PR comment this long is not read
	commentTimeout = 60 * time.Second
)

// commentFit is one squeeze of the body: how long a table note, a Limits bullet and a Notes entry may be, and how many
// bullets show. A fit with no notes length has no Notes section, which carries each note in full.
type commentFit struct{ note, limit, bullets, notes int }

// commentFits run from roomy to tight; the table rows are clipped last.
var commentFits = []commentFit{{80, 300, 12, 600}, {80, 150, 6, 300}, {80, 80, 3, 150}, {60, 60, 2, 0}, {40, 40, 1, 0}}

func commentMarker(repo string, n int) string {
	return fmt.Sprintf("<!-- litmus:comment %s#%d -->", repo, n)
}

// litmusCommentMarker is the marker used by the per-bug comment set. The
// repository-qualified form above is retained for comments made by older
// Litmus runs and is accepted when reading them.
func litmusCommentMarker() string { return "<!-- litmus:comment -->" }

// failingBugComments returns only head checks that can be attributed to this
// PR. A matching base failure or partial result is a pre-existing problem and
// therefore stays in the ordinary run summary.
func failingBugComments(checks []Check) []bugComment {
	var out []bugComment
	for _, c := range checks {
		if c.onLeg() != "head" || (c.State != CheckFail && c.State != CheckAbsent) {
			continue
		}
		base := baseTwinCheck(checks, c)
		if base == nil || (base.State != CheckPass && base.State != CheckAbsent) {
			continue
		}
		out = append(out, bugComment{Check: c, Base: base})
	}
	for i := range out {
		out[i].Number, out[i].Total = i+1, len(out)
	}
	return out
}

func postedCommentFor(comments []PostedComment, name string) (PostedComment, bool) {
	for _, c := range comments {
		if strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(name)) {
			return c, true
		}
	}
	return PostedComment{}, false
}

func (s *Service) previousComments(run Run) []PostedComment {
	current := slices.Clone(run.Comments)
	runs, err := s.store.List()
	if err != nil {
		return current
	}
	for i := len(runs) - 1; i >= 0; i-- {
		other := runs[i]
		if other.ID == run.ID || other.Repo != run.Repo || other.PRNumber != run.PRNumber || len(other.Comments) == 0 {
			continue
		}
		return mergeCommentMappings(current, other.Comments)
	}
	return current
}

func (s *Service) numberBugComments(run Run, bugs []bugComment) {
	previous := s.previousComments(run)
	used := map[int]bool{}
	next := 1
	for _, old := range previous {
		if strings.EqualFold(strings.TrimSpace(old.Name), "summary") {
			continue
		}
		if old.Number > 0 {
			used[old.Number] = true
			next = max(next, old.Number+1)
		}
	}
	for i := range bugs {
		if old, ok := postedCommentFor(previous, bugs[i].Check.Name); ok && old.Number > 0 {
			bugs[i].Number = old.Number
			continue
		}
		bugs[i].Number = next
		used[next] = true
		next++
	}
	total := len(bugs)
	for _, bug := range bugs {
		total = max(total, bug.Number)
	}
	for i := range bugs {
		bugs[i].Total = total
	}
}

// baseTwinCheck is the check-name form of baseTwin. It returns nil when main
// was not repeated, which is still useful evidence for a head-only failure.
func baseTwinCheck(checks []Check, c Check) *Check {
	var fallback *Check
	for i := range checks {
		b := &checks[i]
		if b.onLeg() != "base" || !strings.EqualFold(b.Name, c.Name) || !slices.Contains(outcomes, b.State) {
			continue
		}
		if b.State == CheckPass || b.State == CheckAbsent {
			return b
		}
		fallback = b
	}
	return fallback
}

// noteSentences keeps at most four tester sentences. It is intentionally
// conservative: a short fragment is still useful evidence, but the public
// comment must never expose engine refs or markdown supplied by the tester.
func noteSentences(run Run, v Verdict, c Check) string {
	note := builds(plainTester(c.Note, run, v.Checks), run)
	note = strings.TrimSpace(note)
	if note == "" {
		note = outcomeWords[c.State]
	}
	if note == "" {
		note = plainTester(c.Expect, run, v.Checks)
	}
	var scrubbed strings.Builder
	start := 0
	for _, loc := range append(sentenceStop.FindAllStringIndex(note, -1), []int{len(note), len(note)}) {
		// A semicolon can start the receipt clause, so keep it within the sentence.
		if loc[0] < len(note) && note[loc[0]] == ';' {
			continue
		}
		sentence := note[start:loc[0]]
		if match := receiptMention.FindStringIndex(sentence); match != nil {
			cut := strings.LastIndexAny(sentence[:match[0]], ",;")
			if cut < 0 {
				cut = 0
			}
			sentence = strings.TrimSpace(sentence[:cut])
		}
		if strings.TrimSpace(sentence) != "" {
			scrubbed.WriteString(sentence)
			scrubbed.WriteString(note[loc[0]:loc[1]])
		}
		start = loc[1]
	}
	note = scrubbed.String()
	var parts []string
	start = 0
	for _, loc := range sentenceStop.FindAllStringIndex(note, -1) {
		if p := strings.TrimSpace(note[start:loc[1]]); p != "" {
			parts = append(parts, p)
		}
		start = loc[1]
		if len(parts) == 4 {
			break
		}
	}
	if len(parts) < 4 && strings.TrimSpace(note[start:]) != "" {
		parts = append(parts, strings.TrimSpace(note[start:]))
	}
	for i := range parts {
		parts[i] = plainSentence(parts[i], 280)
	}
	return mdText(strings.Join(parts, " "), 1100)
}

// bugCommentBody is deliberately small enough to leave room for one image
// and one check recording. Image and clip are repository URLs after evidence
// publishing, or run-relative files while a draft waits for approval.
func bugCommentBody(run Run, v Verdict, b bugComment, image, clip string, marker bool) string {
	parts := []string{fmt.Sprintf("Bug %d of %d:", b.Number, b.Total), noteSentences(run, v, b.Check)}
	if v.StoppedEarly != "" {
		parts = append(parts, "Stopped early: "+plainSentence(plainTester(v.StoppedEarly, run, v.Checks), 300))
	}
	if lines := checkConsoleErrors(b.Check); len(lines) > 0 {
		parts = append(parts, "<details><summary>App console errors during this check</summary>\n\n```\n"+strings.Join(lines, "\n")+"\n```\n\n</details>")
	}
	if image != "" {
		parts = append(parts, "![The base build on the left and the PR build on the right]("+image+")")
	} else {
		parts = append(parts, "A comparison image was not available because "+run.baseName()+" had no matching shot.")
	}
	if clip != "" {
		parts = append(parts, "<details><summary>Recording for "+mdText(strings.ReplaceAll(b.Check.Name, "-", " "), 120)+"</summary>\n\n[Watch the check]("+clip+")\n\n</details>")
	} else {
		parts = append(parts, "<details><summary>Recording for "+mdText(strings.ReplaceAll(b.Check.Name, "-", " "), 120)+"</summary>\n\nThe check recording was not available.\n\n</details>")
	}
	if marker {
		parts = append(parts, litmusCommentMarker())
	}
	return strings.Join(parts, "\n\n")
}

// consolePaths matches URLs as well as paths so remote URLs stay intact.
var consolePaths = regexp.MustCompile("(?:[a-zA-Z][a-zA-Z0-9+.-]*://|[a-zA-Z]:[\\\\/]|/)[^\\s\"'`<>()]+")

func checkConsoleErrors(c Check) []string {
	var lines []string
	seen := map[string]bool{}
	for _, r := range c.Receipts {
		if r.onLeg() != "head" {
			continue
		}
		for _, line := range strings.Split(r.ConsoleTail, "\n") {
			line = strings.TrimSpace(line)
			line = consolePaths.ReplaceAllStringFunc(line, func(path string) string {
				if strings.Contains(path, "://") && !strings.HasPrefix(path, "file://") {
					return path
				}
				return filepath.Base(strings.ReplaceAll(path, `\`, "/"))
			})
			chars := []rune(line)
			line = string(chars[:min(len(chars), 200)])
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			lines = append(lines, line)
			if len(lines) == 5 {
				return lines
			}
		}
	}
	return lines
}

// summaryCommentBody is the compact two-sentence form for a passing run. A
// partial or skipped check gets its own sentence so a reader can see what was
// left unfinished without scanning the larger legacy summary.
func summaryCommentBody(run Run, v Verdict, now time.Time, marker bool) string {
	var paragraphs []string
	main := run.baseName()
	both := slices.ContainsFunc(v.Checks, func(c Check) bool {
		return c.onLeg() == "base" || slices.ContainsFunc(c.Receipts, func(r Receipt) bool { return r.onLeg() == "base" })
	})
	started := len(run.Legs) > 0 || v.Overall != CheckBlocked || slices.ContainsFunc(v.Checks, func(c Check) bool { return len(c.Receipts) > 0 })
	if started {
		where := "on the PR build"
		if both {
			where += " and on " + main
		}
		took := lasted(now.Sub(run.CreatedAt))
		if run.prepared() {
			builds, testing := run.buildsAndTesting(now)
			took = "builds " + lasted(builds) + ", testing " + lasted(testing)
		}
		paragraphs = append(paragraphs, fmt.Sprintf("Litmus tried #%d %s (%s).", run.PRNumber, where, took))
	} else {
		paragraphs = append(paragraphs, fmt.Sprintf("Litmus could not start on #%d.", run.PRNumber))
	}
	tally := count(v.Checks)
	result := verdictLine(v.Overall, tally, main)
	if v.Overall == CheckPass && tally.passed > 0 {
		word := "checks"
		if tally.passed == 1 {
			word = "check"
		}
		result = fmt.Sprintf("the PR does what it says in the %d %s I finished", tally.passed, word)
	}
	paragraphs = append(paragraphs, lowerFirst(result)+".")
	for _, gap := range run.Gaps {
		if text := plainSentence(gap, 400); text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	for _, c := range v.Checks {
		if c.onLeg() != "head" || (c.State != CheckPartial && c.State != CheckSkipped) {
			continue
		}
		label := lowerLead(mdText(clause(cmp.Or(c.Expect, strings.ReplaceAll(c.Name, "-", " ")), 180), 220))
		if c.State == CheckSkipped {
			paragraphs = append(paragraphs, "I could not check that "+label+skipWhy(c.Note)+".")
		} else {
			paragraphs = append(paragraphs, "I could only partly check that "+label+skipWhy(c.Note)+".")
		}
	}
	if marker {
		paragraphs = append(paragraphs, litmusCommentMarker())
	}
	return strings.Join(paragraphs, "\n\n")
}

// fixedCommentBody keeps the marker on the first comment so a later rerun can
// still find the run, while the visible text is exactly the requested result.
func fixedCommentBody(sha string, marker bool) string {
	body := "Fixed at " + clip(strings.TrimSpace(sha), 80)
	if marker {
		body += "\n\n" + litmusCommentMarker()
	}
	return body
}

// mdText makes tester-written text safe for a public comment: one line, clipped, no raw HTML or entities, no
// link or image syntax, no engine refs (@e12 would also ping a user) and no @mention.
func mdText(s string, n int) string {
	s = cell(strings.ReplaceAll(clip(engineRef.ReplaceAllString(strings.Join(strings.Fields(s), " "), "that element"), n), `\`, `\\`))
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", "[", `\[`, "@", "@\u200b").Replace(s)
}

var (
	engineRef = regexp.MustCompile(`(?:\bref[ =]+)?\B@e\d+\b`)
	legPrefix = regexp.MustCompile(`(?i)^(base|head)( leg)?:\s*`)
)

// plainSentence is a tester's note as one sentence of a public comment: no "Base:" prefix, clipped to n, ending in a stop.
func plainSentence(note string, n int) string {
	out := plainCell(note, n)
	if out == "" || strings.HasSuffix(out, "...") || strings.ContainsAny(out[len(out)-1:], "!?") {
		return out
	}
	if trailingEntity.MatchString(out) {
		return out + "."
	}
	return strings.TrimRight(out, ".;:,") + "."
}

// shorten cuts text to n characters at a word, with "..." when it cut.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	s = s[:n-3]
	if i := strings.LastIndex(s, " "); i > n/2 {
		s = s[:i]
	}
	return s + "..."
}

var danglingWord = regexp.MustCompile(`(?i) (and|or|but|the|a|an|of|to|with|in|on|at|for|is|are)$`)

// clause is s as the end of a sentence: cut at a word when over n characters (never mid-word, never inside an
// unclosed bracket or after a word that wants another) and with no stop of its own, so the caller's period is the only one.
func clause(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		cut := string(r[:n])
		if r[n] != ' ' {
			if i := strings.LastIndex(cut, " "); i > 0 {
				cut = cut[:i]
			}
		}
		if i := strings.LastIndex(cut, "("); i >= 0 && !strings.Contains(cut[i:], ")") {
			cut = cut[:i]
		}
		s = danglingWord.ReplaceAllString(strings.TrimRight(cut, " "), "")
	}
	return strings.TrimRight(s, " .,;:")
}

// sentences is s cut back to a whole sentence when it is over n characters and has a stop inside them.
func sentences(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		if i := strings.LastIndex(string(r[:n]), ". "); i > 0 {
			return s[:i]
		}
	}
	return s
}

var testerWords = regexp.MustCompile(`(?i)\b(head|base|c\d+)\b`)

// plainTester is a tester's text in the reader's words, for a tester who ignored the rules: head is the PR build,
// base is the base branch, and a check id is that check's name.
func plainTester(s string, run Run, checks []Check) string {
	return testerWords.ReplaceAllStringFunc(legPrefix.ReplaceAllString(strings.TrimSpace(s), ""), func(w string) string {
		out := "that check"
		switch low := strings.ToLower(w); low {
		case "head":
			out = "the PR build"
		case "base":
			out = cmp.Or(run.BaseRef, "main")
		default:
			if i := slices.IndexFunc(checks, func(c Check) bool { return strings.EqualFold(c.ID, low) }); i >= 0 {
				out = strings.ReplaceAll(checks[i].Name, "-", " ")
			}
		}
		if first, _ := utf8.DecodeRuneInString(w); unicode.IsUpper(first) {
			out = recase(out, true)
		}
		return out
	})
}

// buildName is a lead-in or inner mention of a build, which the comment adds itself: "on the PR build", "in the PR build", "on main".
func buildName(run Run) string {
	return `(?:on|in) (?:the PR build|main|` + regexp.QuoteMeta(run.baseName()) + `)`
}

// builds strips the build a tester's note names, since checkPara and the captions lead with it: a leading
// "On the PR build," or "On main" (comma optional), and the same words inside the first clause (up to the first comma
// or period). A note that lost its opening gets a capital back.
func builds(note string, run Run) string {
	s, name := strings.TrimSpace(note), buildName(run)
	lead := regexp.MustCompile(`(?i)^` + name + `(?:,\s*|\s+|$)`)
	inner := regexp.MustCompile(`(?i)\s+` + name + `(\s|[,.;:]|$)`)
	cut := lead.ReplaceAllString(s, "")
	first, rest := cut, ""
	if i := strings.IndexAny(cut, ",."); i >= 0 {
		first, rest = cut[:i], cut[i:]
	}
	cut = inner.ReplaceAllString(first, "$1") + rest
	if cut == s {
		return s
	}
	return recase(strings.TrimSpace(cut), true)
}

// subjectLed reports whether a note (already through builds) opens with a build as its subject, "The PR build shows",
// "The main build is" or "Main has", so the comment keeps it as its own sentence instead of adding "On the PR build,".
func subjectLed(note string, run Run) bool {
	return regexp.MustCompile(`(?i)^(?:the (?:PR|main) build|main|` + regexp.QuoteMeta(run.baseName()) + `)(?:'s)?(?:\s|$)`).MatchString(note)
}

var compared = regexp.MustCompile(`(?i)\bcompared\b`)

// withoutComparison drops the sentences of a tester's note that say what was or was not compared with the base branch.
// plainVerdict applies it only to a check with no result on the base branch, where such a claim would be false. The other sentences stay. Sentences end at . ! ? or ; before a space.
func withoutComparison(note string, run Run) string {
	main := regexp.MustCompile(`(?i)\b(?:main|` + regexp.QuoteMeta(run.baseName()) + `)\b`)
	var kept []string
	start, dropped := 0, 0
	for _, loc := range append(sentenceStop.FindAllStringIndex(note, -1), []int{len(note), len(note)}) {
		if s := note[start:loc[1]]; strings.TrimSpace(s) != "" {
			if compared.MatchString(s) && main.MatchString(s) {
				dropped++
			} else {
				kept = append(kept, strings.TrimSpace(s))
			}
		}
		start = loc[1]
	}
	out := strings.Join(kept, " ")
	if trimmed := strings.TrimRight(out, " ;,"); dropped > 0 && trimmed != out { // a dropped clause leaves its separator behind
		out = trimmed + "."
	}
	return out
}

var sentenceStop = regexp.MustCompile(`[.!?;](?:\s+|$)`)
var receiptMention = regexp.MustCompile(`(?i)receipts?/?\s*\d+(-after\.png)?`)
var trailingEntity = regexp.MustCompile(`&(?:amp;)*[A-Za-z0-9#]+;$`)

// plainVerdict is v with the tester's own words (in each check's expectation and note, the skip reason and a blocked
// reason) said in the reader's. The checks are copied; v is not changed.
func plainVerdict(run Run, v Verdict) Verdict {
	checks := slices.Clone(v.Checks)
	for i := range checks {
		note := plainTester(checks[i].Note, run, v.Checks)
		if c := checks[i]; c.onLeg() == "head" && slices.Contains([]string{"", string(CheckSkipped)}, baseResult(v.Checks, c)) { // nothing was compared, so a claim that it was is false
			note = withoutComparison(note, run)
		}
		checks[i].Expect, checks[i].Note = plainTester(checks[i].Expect, run, v.Checks), builds(note, run)
	}
	if v.Overall == CheckBlocked { // another verdict's reasons begin with a check id and name, which the comment matches on
		v.Reasons = slices.Clone(v.Reasons)
		for i, r := range v.Reasons {
			v.Reasons[i] = plainTester(r, run, v.Checks)
		}
	}
	v.SkipBase = plainTester(v.SkipBase, run, v.Checks)
	v.Checks = checks
	return v
}

// recase raises or lowers the first letter; a lower-case request leaves acronyms and names (second letter capital) and "I" alone.
func recase(s string, up bool) string {
	r := []rune(s)
	if len(r) > 1 && up {
		r[0] = unicode.ToUpper(r[0])
	} else if len(r) > 1 && unicode.IsUpper(r[0]) && !unicode.IsUpper(r[1]) && (r[0] != 'I' || r[1] != ' ') {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
}

func lowerFirst(s string) string { return recase(s, false) }

// leadWords are the words an expectation can open with that are not a name: lowering them is safe.
var leadWords = []string{"a", "an", "the", "after", "when", "with", "in", "on", "at", "if", "once", "every", "each", "both", "while", "before"}

// lowerLead lowers the first letter of an expectation that follows "that", unless the word is a label or a name.
// A function word or an -ing word ("typing", "opening") is always lowered. Any other word stays when it is all
// capitals or holds a digit, dot or slash (CLI, Orchestrator.inc, Local/Cloud), or when the same capitalised word
// turns up again, not at the start of a sentence, later in the expectation or in one of the others (Go in "Go back
// and Go forward"). Everything else is a common noun that only sat first: "Search field" becomes "search field".
func lowerLead(s string, others ...string) string {
	w, rest, _ := strings.Cut(s, " ")
	w = strings.Trim(w, `.,;:!?()"'`)
	if lw := strings.ToLower(w); slices.Contains(leadWords, lw) || (len(lw) > 4 && strings.HasSuffix(lw, "ing")) {
		return lowerFirst(s)
	}
	if w == "" || strings.ContainsAny(w, "0123456789./") || (len(w) > 1 && w == strings.ToUpper(w)) {
		return s
	}
	if capitalised(w, rest, true) || slices.ContainsFunc(others, func(o string) bool { return capitalised(w, o, false) }) {
		return s
	}
	return lowerFirst(s)
}

// capitalised reports whether w turns up in text anywhere but at the start of a sentence. mid says text itself
// begins mid-sentence (the rest of an expectation after its first word); otherwise text begins a sentence.
func capitalised(w, text string, mid bool) bool {
	for _, t := range strings.Fields(text) {
		if mid && strings.Trim(t, `.,;:!?()"'`) == w {
			return true
		}
		mid = !strings.ContainsAny(t[len(t)-1:], ".!?")
	}
	return false
}

// checkLabel names a check by what it expects, in 60 characters (its name, de-slugged, when there is no expectation).
func checkLabel(c Check) string {
	return mdText(shorten(cmp.Or(c.Expect, strings.ReplaceAll(c.Name, "-", " ")), 60), 80)
}

// rowLabel is the label of the table row c belongs to: a base check beside its head twin is named like the twin.
func rowLabel(checks []Check, c Check) string {
	if c.onLeg() == "base" {
		if i := slices.IndexFunc(checks, func(h Check) bool { return h.onLeg() == "head" && strings.EqualFold(h.Name, c.Name) }); i >= 0 {
			return checkLabel(checks[i])
		}
	}
	return checkLabel(c)
}

// cellWords are the table's words for a result.
var cellWords = map[string]string{"pass": "works", "fail": "fails", "partial": "partly", "absent": "not there", "skipped": "not reached", "observed": "looked at", "not compared on base": "not compared", "-": "not compared"}

func cellWord(s string) string { return cmp.Or(cellWords[s], s) }

// tally counts the head checks by what they showed.
type tally struct {
	passed, shown, pre, prePartial, notCompared, failed, partial, unreached int
	compared, notRun                                                        int // head checks with and without a result on main (baseCoverage)
}

func count(checks []Check) (t tally) {
	compared, notRun := baseCoverage(checks)
	t.compared, t.notRun = len(compared), len(notRun)
	for _, c := range checks {
		if c.onLeg() != "head" {
			continue
		}
		base := baseResult(checks, c)
		switch {
		case c.unreached():
			t.unreached++
			continue
		case preExisting(checks, c):
			t.pre++
			if c.State == CheckPartial {
				t.prePartial++
			}
			continue
		case base == "" || base == string(CheckSkipped):
			t.notCompared++
		case c.State == CheckPartial:
			t.partial++
		}
		if c.State == CheckPass {
			t.passed++
			if failedOnBase(checks, c) {
				t.shown++
			}
		}
		if c.State == CheckFail || c.State == CheckAbsent {
			t.failed++
		}
	}
	return t
}

// verdictLine is the sentence after "Result:", from the overall verdict and what the checks showed.
func verdictLine(overall CheckState, t tally, main string) string {
	left := t.notCompared + t.partial + t.unreached
	switch {
	case overall == CheckBlocked:
		return "could not test this one"
	case overall == CheckFail:
		return "something broke; do not merge yet"
	case overall == CheckPartial && t.failed > 0:
		return "the PR build and " + main + " both fail; I could not tell whether it is the same fault; read the finding before merging"
	case t.unreached > 0 && t.passed+t.pre+t.notCompared+t.failed+t.partial == 0:
		return "could not test this from the app"
	case t.passed == 0: // nothing failed, nothing was confirmed
		return "nothing failed, but I could not confirm the change from the app; read the diff before merging"
	case overall == CheckPartial && left == 0:
		return "the PR build works, but nothing differed from " + main + "; read the diff before merging"
	case overall == CheckPass:
		word := "checks"
		if t.passed == 1 {
			word = "check"
		}
		line := fmt.Sprintf("the PR does what it says in the %d %s I finished", t.passed, word)
		if left > 0 {
			line += ". " + leftover(t.notCompared+t.partial, t.unreached)
		}
		return line
	case overall == CheckPartial && t.compared > 0:
		few := func(n int, one, many string) string { return map[bool]string{true: one, false: many}[n == 1] }
		line := "the PR build works as far as I could check; " + fmt.Sprint(t.compared) + few(t.compared, " check was", " checks were") + " compared on " + main + " and showed no difference"
		if t.notRun > 0 {
			line += ", " + fmt.Sprint(t.notRun) + few(t.notRun, " was", " were") + " not"
		}
		return line + "; read the diff before merging"
	case overall == CheckPartial:
		return "the PR build works as far as I could check; nothing was compared against " + main + ", so read the diff before merging"
	}
	return "the PR build works as far as I could check. " + leftover(t.notCompared+t.partial, t.unreached)
}

// leftover says what the checks that did not finish as a comparison came to: some only ran on the PR build, some could
// not be done at all (skipped, or never reached), or both.
func leftover(only, undone int) string {
	few := func(n int, one, many string) string { return map[bool]string{true: one, false: many}[n == 1] }
	switch {
	case only > 0 && undone > 0:
		return few(only, "One check only ran on the PR build", "A few checks only ran on the PR build") + " and " + few(undone, "one could not be done", "a few could not be done")
	case only > 0:
		return few(only, "One check only ran on the PR build", "A few checks only ran on the PR build")
	}
	return few(undone, "One check could not be done", "A few checks could not be done")
}

// plainReason says a derive reason in the comment's words; a tester's own reason passes through.
func plainReason(r, main string) string {
	switch r {
	case reasonPreOnly:
		return "Whatever failed on the PR build failed on " + main + " too."
	case reasonPrePartial:
		return "What I could not confirm on the PR build I could not confirm on " + main + " either."
	case reasonPreBoth:
		return plainReason(reasonPreOnly, main) + " " + plainReason(reasonPrePartial, main)
	case reasonNoneReached:
		return "I could not reach any check on the PR build."
	case reasonNoDifference:
		return "Nothing passed on the PR build that failed on " + main + ", so I never saw the difference."
	}
	return mdText(r, 300)
}

var outcomeWords = map[CheckState]string{CheckPass: "it worked", CheckFail: "it did not work", CheckPartial: "it only partly worked", CheckAbsent: "it is not there"}

// checkPara tells one check the way a teammate would: what was tried, what happened on the PR build, then on main.
func checkPara(run Run, v Verdict, c Check, n, i int) string {
	notes := make([]string, 0, 2*len(v.Checks)) // every note and expectation: a word capitalised mid-sentence there is a label
	for _, x := range v.Checks {
		notes = append(notes, x.Note, x.Expect)
	}
	label, main := lowerLead(mdText(clause(cmp.Or(c.Expect, strings.ReplaceAll(c.Name, "-", " ")), 160), 400), notes...), run.baseName()
	if c.unreached() {
		return strings.TrimSpace(fmt.Sprintf("I could not check that %s. %s", label, recase(plainSentence(c.Note, n), true)))
	}
	if c.onLeg() == "head" && outcomeWords[c.State] == "" && plainSentence(c.Note, n) == "" { // nothing was seen on the PR build
		return recase(strings.ReplaceAll(c.Name, "-", " "), true) + " could not be run on the PR build."
	}
	say := func(who string, x Check) string {
		note := plainSentence(x.Note, n)
		if subjectLed(note, run) {
			return recase(note, true)
		}
		return fmt.Sprintf("On %s, %s", who, cmp.Or(lowerLead(note, notes...), outcomeWords[x.State]+"."))
	}
	out := []string{[]string{"I checked that ", "Next I checked that ", "I also checked that "}[i%3] + label + "."}
	if c.onLeg() == "head" {
		if c.State == CheckAbsent {
			out = append(out, "The PR build does not have this.")
		}
		out = append(out, say("the PR build", c))
	}
	switch twin := baseTwin(v.Checks, c); {
	case c.onLeg() == "base":
		out = append(out, say(main, c))
	case twin >= 0 && v.Checks[twin].State == CheckSkipped:
		out = append(out, "I did not repeat this on "+main+skipWhy(v.Checks[twin].Note)+".")
	case twin >= 0 && v.Checks[twin].State == CheckAbsent: // main has no such feature: it is new, not something that failed there
		line := main + " does not have this" + skipWhy(v.Checks[twin].Note) + "."
		if c.State != CheckAbsent {
			line = "This is new in the PR; " + line
		} else {
			line = recase(line, true)
		}
		out = append(out, line)
	case twin >= 0:
		out = append(out, say(main, v.Checks[twin]))
	case baseResult(v.Checks, c) != "":
		out = append(out, "I also looked at it on "+main+".")
	case v.SkipBase != "": // said once, after the result line
	default:
		out = append(out, "I could not compare it with "+main+".")
	}
	switch {
	case c.onLeg() != "head":
	case preExisting(v.Checks, c) && c.State == CheckPartial && baseResult(v.Checks, c) == string(CheckPartial): // the main sentence already says what was seen
	case preExisting(v.Checks, c):
		out = append(out, "The same thing happens on "+main+", so the PR did not cause it.")
	case c.State == CheckPartial && baseResult(v.Checks, c) == string(CheckPartial):
		out = append(out, "It is also partial on "+main+"; I could not tell whether it is the same fault.")
	case c.State == CheckFail && baseResult(v.Checks, c) == string(CheckFail):
		out = append(out, "It also fails on "+main+"; I could not tell whether it is the same fault.")
	case (c.State == CheckFail || c.State == CheckPartial) && baseResult(v.Checks, c) == string(CheckAbsent):
		out = append(out, "Since "+main+" has no such feature, this is the PR's own problem.")
	}
	return strings.Join(out, " ")
}

// skipWhy is ": <why>" for the note on a check the tester did not repeat on the base, or "" when it gave none.
func skipWhy(note string) string {
	if why := lowerFirst(mdText(clause(note, 200), 400)); why != "" {
		return ": " + why
	}
	return ""
}

// lasted is how long a stretch of the run lasted, in whole minutes, for the first line: "7 minutes".
func lasted(d time.Duration) string {
	switch m := int(d.Round(time.Minute) / time.Minute); {
	case m < 1:
		return "under a minute"
	case m == 1:
		return "1 minute"
	default:
		return fmt.Sprintf("%d minutes", m)
	}
}

// renderComment is the PR comment for a stored verdict, ending in the marker that finds it again. It is built from
// the run, the verdict and the published evidence alone and fits commentCap by squeezing everything before the
// table notes. The image lines do not count against the cap; a body that still does not fit loses them.
func renderComment(run Run, v Verdict, ev evidence, now time.Time) string {
	v = plainVerdict(run, v)
	marker := commentMarker(run.Repo, run.PRNumber)
	images := ev.imageChars()
	for _, f := range commentFits {
		if body := commentBody(run, v, ev, now, f) + "\n\n" + marker; len(body)-images <= commentCap {
			return body
		}
	}
	body := commentBody(run, v, evidence{limits: ev.limits}, now, commentFits[len(commentFits)-1]) + "\n\n" + marker
	if len(body) <= commentCap {
		return body
	}
	return strings.ToValidUTF8(body[:commentCap-len(marker)-6], "") + "...\n\n" + marker
}

// issuesFor is " (for #6390)", " (for #6390 and #6391)" or " (for #1, #2 and #3)", each number linked to its issue; ""
// when the PR closes none.
func issuesFor(issues []IssueRef) string {
	refs := make([]string, 0, len(issues))
	for _, is := range issues {
		ref := "#" + strconv.Itoa(is.Number)
		if is.URL != "" {
			ref = "[" + ref + "](" + is.URL + ")"
		}
		refs = append(refs, ref)
	}
	switch len(refs) {
	case 0:
		return ""
	case 1:
		return " (for " + refs[0] + ")"
	}
	return " (for " + strings.Join(refs[:len(refs)-1], ", ") + " and " + refs[len(refs)-1] + ")"
}

func commentBody(run Run, v Verdict, ev evidence, now time.Time, f commentFit) string {
	var blocks []string // paragraphs, joined by a blank line
	add := func(format string, a ...any) { blocks = append(blocks, fmt.Sprintf(format, a...)) }
	main, n := run.baseName(), max(f.notes, f.note)
	var worth, themes []string
	v.Checks = slices.Clone(v.Checks)
	for i, c := range v.Checks {
		if at := strings.Index(strings.ToLower(c.Note), "worth a look:"); at >= 0 {
			worth = append(worth, checkLabel(c)+": "+plainSentence(c.Note[at+len("worth a look:"):], n))
			v.Checks[i].Note = strings.TrimSpace(c.Note[:at])
		}
		for _, r := range c.Receipts {
			if r.ThemeNote != "" {
				themes = append(themes, checkLabel(c)+", "+run.sideName(r.onLeg())+": "+mdText(r.ThemeNote, n))
			}
		}
	}
	var rows []Check
	for _, c := range v.Checks {
		if _, _, beside := compareCells(v.Checks, c); !beside {
			rows = append(rows, c)
		}
	}
	both := slices.ContainsFunc(v.Checks, func(c Check) bool {
		return c.onLeg() == "base" || slices.ContainsFunc(c.Receipts, func(r Receipt) bool { return r.onLeg() == "base" })
	})
	blocked := v.Overall == CheckBlocked // no check ran: the result line and the reason below say it all
	// started: something was built or seen; a blocked run with no leg and no receipt never got that far
	started := len(run.Legs) > 0 || !blocked || slices.ContainsFunc(v.Checks, func(c Check) bool { return len(c.Receipts) > 0 })
	if started {
		took := "(" + lasted(now.Sub(run.CreatedAt)) + ")" // a run that never finished preparing has no testing time to split out
		if run.prepared() {
			builds, testing := run.buildsAndTesting(now)
			took = "(builds " + lasted(builds) + ", testing " + lasted(testing) + ")"
		}
		add("Litmus tried #%d%s on the PR build%s %s.", run.PRNumber, issuesFor(run.Issues), map[bool]string{true: " and on " + main + run.atBranchPoint()}[both], took)
	} else { // nothing was built: say so, not "tried on the PR build"
		why := ""
		if blocked && len(v.Reasons) > 0 {
			why = " (" + lowerFirst(mdText(clause(v.Reasons[0], 120), 200)) + ")"
		}
		add("Litmus could not start on #%d%s%s. %s", run.PRNumber, issuesFor(run.Issues), why, run.builtLine())
	}
	add("**Result: %s.**", verdictLine(v.Overall, count(v.Checks), main))
	if why := lowerFirst(mdText(clause(v.SkipBase, 200), 400)); why != "" && !blocked {
		switch compared, notRun := baseCoverage(v.Checks); { // each paragraph below says why; this says how much
		case len(compared) == 0:
			add("I did not run %s: %s.", main, why)
		case len(notRun) > 0:
			labels := make([]string, len(notRun))
			for i, c := range notRun {
				labels[i] = strings.ReplaceAll(c.Name, "-", " ")
			}
			add("I compared %d %s on %s; %d %s not run there: %s.", len(compared), map[bool]string{true: "check", false: "checks"}[len(compared) == 1], main, len(notRun), map[bool]string{true: "was", false: "were"}[len(notRun) == 1], strings.Join(labels, ", "))
		}
	}
	if blocked {
		rows = nil
	}
	if len(rows) > 4 { // a handful of checks read better as paragraphs; a long list also gets a table to scan
		table := make([]string, 0, 2+len(rows))
		table = append(table, "| what was checked | PR build | "+main+" | note |", "|---|---|---|---|")
		for _, c := range rows {
			head, base, _ := compareCells(v.Checks, c)
			table = append(table, fmt.Sprintf("| %s | %s | %s | %s |", checkLabel(c), cellWord(head), cellWord(base), plainCell(c.Note, f.note)))
		}
		add("%s", strings.Join(table, "\n"))
	}
	for i, c := range rows {
		add("%s", checkPara(run, v, c, n, i))
	}
	if len(worth) > 0 {
		add("Worth a look: %s", strings.Join(worth, " "))
	}
	if len(themes) > 0 {
		add("%s", strings.Join(themes, "\n"))
	}
	for _, it := range ev.items {
		if it.kind == kindHero {
			add("%s\n%s", it.image(), it.caption)
		}
	}
	if line := areaLine(run.Areas, v.Checks); line != "" {
		add("%s", line)
	}
	more, vids := slices.DeleteFunc(slices.Clone(ev.items), func(it evItem) bool { return it.kind == kindHero }), videoParts(run, ev)
	if len(more)+len(vids) > 0 {
		slices.SortStableFunc(more, func(a, b evItem) int { return a.kind - b.kind }) // the recordings, then the screenshots
		parts := append([]string{"<details><summary>Recordings and every screenshot</summary>"}, vids...)
		for _, it := range more {
			parts = append(parts, it.caption+"\n"+it.image())
		}
		add("%s\n\n</details>", strings.Join(parts, "\n\n"))
	}
	var gaps, limits []string
	for _, g := range run.Gaps { // what intake could not get, as the pack and verdict.md say it: in full at every fit, the other limits give way
		gaps = append(gaps, mdText(g, math.MaxInt))
	}
	limits = append(limits, ev.limits...)
	for i, r := range v.Reasons { // a reason that opens with a check's id and name is that check's note, already told above
		switch {
		case blocked && !started && i == 0 && clause(r, 120) == strings.TrimRight(strings.Join(strings.Fields(r), " "), " .,;:"): // the first line carries all of it
		case blocked:
			limits = append(limits, "I could not run the checks. "+recase(plainSentence(r, 300), true))
		case r == reasonNoneReached && count(v.Checks).unreached > 0: // every check was skipped: the result line says so
		case !slices.ContainsFunc(v.Checks, func(c Check) bool { return strings.HasPrefix(r, c.ID+" "+c.Name+": ") }):
			limits = append(limits, plainReason(r, main))
		}
	}
	room := max(f.bullets-len(gaps), 0) // the gaps take bullets first and are never clipped or folded into "And N more."
	for i, l := range limits {
		if i == room {
			limits = append(limits[:i], fmt.Sprintf("And %d more.", len(limits)-i))
			break
		}
		limits[i] = clip(l, f.limit)
	}
	if limits = append(gaps, limits...); len(limits) > 0 {
		add("%s", strings.Join(limits, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

// videoParts are the details block's entries for the uploaded videos, ahead of the GIFs: a caption, then the link in
// a paragraph of its own so GitHub shows a player. A video that did not go up says so, and points at the GIF when
// there is one.
func videoParts(run Run, ev evidence) (out []string) {
	for _, v := range ev.videos {
		side := sideLabel(run, v.leg)
		if v.url == "" {
			note := side + " video could not be attached"
			if slices.ContainsFunc(ev.items, func(it evItem) bool { return it.kind == kindGIF && it.file == sideSlug(run, v.leg)+".gif" }) {
				note += "; the GIF below shows the steps"
			}
			out = append(out, note+".")
			continue
		}
		how := map[bool]string{true: "only the moments around each step"}[v.cut]
		caption := slices.DeleteFunc([]string{side, spanText(v.span), how}, func(s string) bool { return s == "" })
		out = append(out, strings.Join(caption, ", ")+"\n\n"+v.url)
	}
	return out
}

var (
	words     = regexp.MustCompile(`[a-z0-9]+`)
	nameWords = regexp.MustCompile(`[A-Z]+[a-z0-9]*|[a-z0-9]+`) // TitlebarNav is titlebar and nav
)

// bareStems are file-name words too common to show that a check was about the file.
var bareStems = []string{"index", "main", "app", "use", "get", "set", "util", "utils", "type", "types", "test", "spec", "project", "file", "files"}

// covers reports whether a check's words share a stem with a word of one of the area's file names: nav is the stem
// of navigate. A run from before areas kept file names falls back to the words of the area's own name.
func covers(said []string, a Area) bool {
	stems := a.Stems
	if len(stems) == 0 {
		stems = []string{a.Name}
	}
	for _, stem := range stems {
		for _, w := range nameWords.FindAllString(stem, -1) {
			if w = strings.ToLower(w); len(w) >= 3 && !slices.Contains(bareStems, w) && slices.ContainsFunc(said, func(t string) bool { return strings.HasPrefix(t, w) }) {
				return true
			}
		}
	}
	return false
}

// areaLine says which changed areas (six at most) the run did not check, as one sentence, or "" when it checked them
// all. Tests are never run here. Another area is covered when a check that ran on the PR build (pass, fail or partial;
// a skipped check ran nothing) shares a word stem with one of its files, or when it is the only one: the checks came
// from the PR's own claims, so they are about it.
func areaLine(areas []Area, checks []Check) string {
	var said []string
	ran := false
	for _, c := range checks {
		if c.onLeg() != "head" || (c.State != CheckPass && c.State != CheckFail && c.State != CheckPartial) {
			continue
		}
		ran = true
		said = append(said, words.FindAllString(strings.ToLower(c.Name+" "+c.Expect+" "+c.Note), -1)...)
	}
	others := len(areas)
	if slices.ContainsFunc(areas, func(a Area) bool { return a.Name == "Tests" }) {
		others--
	}
	var left []string
	for _, a := range areas {
		if a.Name != "Tests" && ran && (others == 1 || covers(said, a)) {
			continue
		}
		left = append(left, fmt.Sprintf("%s (%d %s)", lowerFirst(mdText(a.Name, 40)), a.Files, map[bool]string{true: "file", false: "files"}[a.Files == 1]))
	}
	if len(left) == 0 {
		return ""
	}
	out := "Not checked by this run: "
	if len(left) > 6 {
		return out + strings.Join(left[:6], ", ") + fmt.Sprintf(", and %d more %s.", len(left)-6, map[bool]string{true: "area", false: "areas"}[len(left)-6 == 1])
	}
	return out + strings.Join(left, ", ") + "."
}

// singular drops the plural s of a name's last word: components is component, but status stays.
func singular(s string) string {
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") && !strings.HasSuffix(s, "us") && !strings.HasSuffix(s, "is") {
		return s[:len(s)-1]
	}
	return s
}

// plainCell is a tester's note without its "Base:" prefix, clipped and made safe: the table's note column.
func plainCell(note string, n int) string {
	return mdText(legPrefix.ReplaceAllString(strings.TrimSpace(note), ""), n)
}

// postComment writes comment.md (the exact body) and, when the run asked for it and is not blocked, publishes the
// evidence images and posts it. It never fails the submit: the outcome is the comment's URL and a state (posted,
// updated, off or failed:<reason>), plus what became of the evidence. A run that asks before posting stops after the
// first step (see closeRun) and takes the second (publishComment) when its post is approved.
func (s *Service) postComment(ctx context.Context, run Run, v Verdict) (url, state string, ev evidence) {
	body, ev, err := s.composeComment(run, v)
	if err != nil {
		return "", s.commentFailed(run, err), ev
	}
	if run.Comment != CommentPost || v.Overall == CheckBlocked {
		return "", CommentOff, ev
	}
	return s.publishComment(ctx, run, v, body)
}

// writeComment renders the comment for ev and writes it to comment.md.
func (s *Service) writeComment(run Run, v Verdict, ev evidence) (string, error) {
	body := renderComment(run, v, ev, s.clock())
	return body, s.store.WriteFile(run.ID, "comment.md", []byte(body))
}

// draftComments creates the reviewable files used by both ordinary posts and
// ask-before-posting runs. A later post reads these files back, so edits made
// while approval is pending survive evidence publication.
func (s *Service) draftComments(run Run, v Verdict) ([]commentDoc, error) {
	dir := filepath.Join(s.store.root, run.ID, "comments")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	plain := plainVerdict(run, v)
	bugs := failingBugComments(plain.Checks)
	s.numberBugComments(run, bugs)
	history := s.previousComments(run)
	markerName := ""
	for _, old := range history {
		if !strings.EqualFold(strings.TrimSpace(old.Name), "summary") && old.Number == 1 {
			markerName = old.Name
			break
		}
	}
	if markerName == "" {
		for _, old := range history {
			if strings.EqualFold(strings.TrimSpace(old.Name), "summary") {
				markerName = old.Name
				break
			}
		}
	}
	isMarker := func(name string, index int) bool {
		return (markerName != "" && strings.EqualFold(strings.TrimSpace(markerName), strings.TrimSpace(name))) || (markerName == "" && len(history) == 0 && index == 0)
	}
	docs := make([]commentDoc, 0, max(len(bugs), 1))
	if len(bugs) == 0 {
		marker := isMarker("summary", 0)
		body := summaryCommentBody(run, plain, s.clock(), marker)
		if plain.Overall == CheckBlocked || plain.Overall == CheckFail {
			body = renderComment(run, plain, evidence{}, s.clock())
			body = strings.ReplaceAll(body, commentMarker(run.Repo, run.PRNumber), litmusCommentMarker())
			if !marker {
				body = strings.ReplaceAll(body, litmusCommentMarker(), "")
			}
		}
		docs = append(docs, commentDoc{Name: "summary", Marker: marker, Body: body})
	} else {
		for i := range bugs {
			bugs[i].ImageFile = fmt.Sprintf("bug-%d.png", bugs[i].Number)
			imagePath := filepath.Join(dir, bugs[i].ImageFile)
			image := ""
			if err := makeBugImage(bugs[i], plain.Checks, imagePath); err != nil {
				s.commentLogf(run, "comment image %s: %v", bugs[i].Check.Name, err)
			} else {
				image = bugs[i].ImageFile
			}
			marker := isMarker(bugs[i].Check.Name, i)
			clip := ""
			if p := checkClipPath(filepath.Join(s.store.root, run.ID), bugs[i].Check.ID); fileExists(p) {
				clip = filepath.ToSlash(filepath.Join("..", "clips", filepath.Base(p)))
			}
			docs = append(docs, commentDoc{Name: bugs[i].Check.Name, Number: bugs[i].Number, Marker: marker, ImageFile: image, ClipFile: clip, Body: bugCommentBody(run, plain, bugs[i], image, clip, marker)})
		}
	}
	current := make(map[string]bool, len(bugs))
	for _, bug := range bugs {
		current[strings.ToLower(strings.TrimSpace(bug.Check.Name))] = true
	}
	for _, old := range history {
		name := strings.TrimSpace(old.Name)
		key := strings.ToLower(name)
		if name == "" || key == "summary" || old.Fixed || current[key] {
			continue
		}
		state, found := headCheckState(plain.Checks, name)
		if !found || state != CheckPass {
			continue
		}
		marker := isMarker(name, len(docs))
		docs = append(docs, commentDoc{Name: name, Number: old.Number, Fixed: true, CommentID: old.ID, Marker: marker, Body: fixedCommentBody(run.HeadSHA, marker)})
	}
	for i := range docs {
		if docs[i].Number == 0 {
			docs[i].Number = i + 1
		}
		if err := writeAtomic(filepath.Join(dir, strconv.Itoa(i+1)+".md"), []byte(docs[i].Body)); err != nil {
			return nil, err
		}
	}
	if err := s.writeCommentManifest(run, docs); err != nil {
		return nil, err
	}
	if len(docs) > 0 {
		if err := s.store.WriteFile(run.ID, "comment.md", []byte(docs[0].Body)); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

// DraftComment is a numbered comment file kept for review before posting.
type DraftComment struct {
	Number int
	Body   string
}

// DraftComments reads the same numbered drafts used by the post path.
func (s *Store) DraftComments(id string) ([]DraftComment, error) {
	dir, err := s.path(id, "comments")
	if err != nil {
		return nil, err
	}
	// Stat first: on Windows, ReadDir on a regular file reports no error or
	// ErrNotExist, so a broken comments path would read as "no drafts".
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: errors.New("not a directory")}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var docs []DraftComment
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".md"))
		if err != nil || n <= 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		docs = append(docs, DraftComment{Number: n, Body: string(data)})
	}
	slices.SortFunc(docs, func(a, b DraftComment) int { return cmp.Compare(a.Number, b.Number) })
	return docs, nil
}

func (s *Service) readDraftComments(run Run) ([]commentDoc, error) {
	drafts, err := s.store.DraftComments(run.ID)
	if err != nil {
		return nil, err
	}
	docs := make([]commentDoc, 0, len(drafts))
	for _, draft := range drafts {
		docs = append(docs, commentDoc{Number: draft.Number, Body: draft.Body, Marker: strings.Contains(draft.Body, litmusCommentMarker())})
	}
	return docs, nil
}

func (s *Service) readCommentManifest(run Run) ([]commentDoc, error) {
	dir := filepath.Join(s.store.root, run.ID, "comments")
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []commentManifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	docs := make([]commentDoc, 0, len(entries))
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(dir, entry.File))
		if err != nil {
			return nil, err
		}
		docs = append(docs, commentDoc{Name: entry.Name, Number: entry.Number, Fixed: entry.Fixed, CommentID: entry.ID, Body: string(body), Marker: strings.Contains(string(body), litmusCommentMarker())})
	}
	return docs, nil
}

func (s *Service) writeCommentManifest(run Run, docs []commentDoc) error {
	dir := filepath.Join(s.store.root, run.ID, "comments")
	entries := make([]commentManifestEntry, 0, len(docs))
	for i, doc := range docs {
		entries = append(entries, commentManifestEntry{Name: doc.Name, File: strconv.Itoa(i+1) + ".md", Number: doc.Number, Fixed: doc.Fixed, ID: doc.CommentID})
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "manifest.json"), data)
}

func (s *Service) commentDrafts(run Run, v Verdict, ev evidence, fallback string) ([]commentDoc, error) {
	docs, err := s.readCommentManifest(run)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		docs, err = s.readDraftComments(run)
		if err != nil {
			return nil, err
		}
	}
	plain := plainVerdict(run, v)
	bugs := failingBugComments(plain.Checks)
	s.numberBugComments(run, bugs)
	history := s.previousComments(run)
	markerName := ""
	for _, old := range history {
		if !strings.EqualFold(strings.TrimSpace(old.Name), "summary") && old.Number == 1 {
			markerName = old.Name
			break
		}
	}
	if markerName == "" {
		for _, old := range history {
			if strings.EqualFold(strings.TrimSpace(old.Name), "summary") {
				markerName = old.Name
				break
			}
		}
	}
	if len(docs) == 0 {
		if len(bugs) == 0 {
			docs = []commentDoc{{Name: "summary", Number: 1, Body: fallback}}
		} else {
			for i, bug := range bugs {
				marker := len(history) == 0 && i == 0
				image := ""
				imageFile := fmt.Sprintf("bug-%d.png", bug.Number)
				if fileExists(filepath.Join(s.store.root, run.ID, "comments", imageFile)) {
					image = imageFile
				}
				clip := ""
				clipPath := checkClipPath(filepath.Join(s.store.root, run.ID), bug.Check.ID)
				if fileExists(clipPath) {
					clip = filepath.ToSlash(filepath.Join("..", "clips", filepath.Base(clipPath)))
				}
				docs = append(docs, commentDoc{Name: bug.Check.Name, Number: bug.Number, Body: bugCommentBody(run, plain, bug, image, clip, marker)})
			}
		}
	}
	for i := range docs {
		if !docs[i].Fixed && i < len(bugs) {
			docs[i].Name = bugs[i].Check.Name
			imageFile := fmt.Sprintf("bug-%d.png", bugs[i].Number)
			// Keep the generated target even when the local file was removed, so a
			// failed evidence publish cannot leak a broken path into GitHub.
			docs[i].ImageFile = imageFile
			clipPath := checkClipPath(filepath.Join(s.store.root, run.ID), bugs[i].Check.ID)
			docs[i].ClipFile = filepath.ToSlash(filepath.Join("..", "clips", filepath.Base(clipPath)))
			docs[i].Number = bugs[i].Number
		}
		if docs[i].Name == "" {
			docs[i].Name = "summary"
		}
		isMarker := (markerName != "" && strings.EqualFold(strings.TrimSpace(markerName), strings.TrimSpace(docs[i].Name))) || (markerName == "" && len(history) == 0 && i == 0)
		if isMarker {
			docs[i].Body = strings.ReplaceAll(docs[i].Body, commentMarker(run.Repo, run.PRNumber), "")
			if !strings.Contains(docs[i].Body, litmusCommentMarker()) {
				docs[i].Body = strings.TrimSpace(docs[i].Body) + "\n\n" + litmusCommentMarker()
			}
			docs[i].Marker = true
		} else {
			docs[i].Body = strings.ReplaceAll(docs[i].Body, litmusCommentMarker(), "")
			docs[i].Body = strings.ReplaceAll(docs[i].Body, commentMarker(run.Repo, run.PRNumber), "")
			docs[i].Marker = false
		}
		if docs[i].ImageFile != "" {
			if url := ev.bugImages[docs[i].Name]; url != "" {
				docs[i].Body = strings.ReplaceAll(docs[i].Body, "("+docs[i].ImageFile+")", "("+url+")")
			} else if strings.Contains(docs[i].Body, "![The base build on the left and the PR build on the right]("+docs[i].ImageFile+")") {
				docs[i].Body = strings.ReplaceAll(docs[i].Body, "![The base build on the left and the PR build on the right]("+docs[i].ImageFile+")\n\n", "")
				docs[i].Body = strings.TrimSpace(docs[i].Body) + "\n\nThe comparison image was not available in the published evidence."
			}
		}
		if docs[i].ClipFile != "" {
			if url := ev.checkClips[docs[i].Name]; url != "" {
				docs[i].Body = strings.ReplaceAll(docs[i].Body, "("+docs[i].ClipFile+")", "("+url+")")
			} else {
				docs[i].Body = strings.ReplaceAll(docs[i].Body, "[Watch the check]("+docs[i].ClipFile+")", "The check recording was not available.")
			}
		}
	}
	return docs, nil
}

func commentIDFromURL(url string) int64 {
	const marker = "#issuecomment-"
	i := strings.LastIndex(url, marker)
	if i < 0 {
		return 0
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(url[i+len(marker):]), 10, 64)
	return id
}

func (s *Service) sendComment(ctx context.Context, run Run, body string, id int64) (sent map[string]any, state string, err error) {
	method, path, state := "POST", "repos/"+run.Repo+"/issues/"+strconv.Itoa(run.PRNumber)+"/comments", "posted"
	if id != 0 {
		method, path, state = "PATCH", "repos/"+run.Repo+"/issues/comments/"+strconv.FormatInt(id, 10), "updated"
	}
	out, err := s.ghSend(ctx, run, method, path, map[string]string{"body": body})
	if err != nil {
		return nil, "", err
	}
	sent = make(map[string]any)
	if err := json.Unmarshal(out, &sent); err != nil {
		return nil, "", err
	}
	if _, ok := sent["id"]; !ok {
		sent["id"] = id
	}
	return sent, state, nil
}

func sentCommentID(sent map[string]any, fallback int64) int64 {
	if id, ok := sent["id"].(float64); ok {
		return int64(id)
	}
	if url, ok := sent["html_url"].(string); ok {
		return commentIDFromURL(url)
	}
	return fallback
}

func sentCommentURL(sent map[string]any) string {
	url, _ := sent["html_url"].(string)
	return url
}

func headCheckState(checks []Check, name string) (CheckState, bool) {
	for _, check := range checks {
		if check.onLeg() == "head" && strings.EqualFold(strings.TrimSpace(check.Name), strings.TrimSpace(name)) {
			return check.State, true
		}
	}
	return "", false
}

func mergeCommentMappings(current, history []PostedComment) []PostedComment {
	out := slices.Clone(current)
	for _, old := range history {
		found := false
		for _, existing := range out {
			if strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(old.Name)) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, old)
		}
	}
	return out
}

func (s *Service) publishCommentSet(ctx context.Context, run Run, v Verdict, ev evidence, fallback string) (string, string, error) {
	docs, err := s.commentDrafts(run, v, ev, fallback)
	if err != nil {
		return "", "", err
	}
	if len(docs) == 0 {
		return "", "", errors.New("no comment drafts found")
	}
	if err := s.store.WriteFile(run.ID, "comment.md", []byte(docs[0].Body)); err != nil {
		return "", "", err
	}
	login, err := s.ghLogin(ctx)
	if err != nil {
		return "", "", err
	}
	api := "repos/" + run.Repo
	raw, err := s.gh.Run(ctx, "api", api+"/issues/"+strconv.Itoa(run.PRNumber)+"/comments", "--paginate")
	if err != nil {
		return "", "", err
	}
	markerID, err := findComment(raw, litmusCommentMarker(), login)
	if err != nil {
		return "", "", fmt.Errorf("unreadable comment list: %w", err)
	}
	if markerID == 0 {
		markerID, err = findComment(raw, commentMarker(run.Repo, run.PRNumber), login)
		if err != nil {
			return "", "", fmt.Errorf("unreadable comment list: %w", err)
		}
	}
	history := s.previousComments(run)
	if len(history) > 0 {
		if _, err := s.update(run.ID, func(cur *Run) { cur.Comments = mergeCommentMappings(cur.Comments, history) }); err != nil {
			return "", "", err
		}
	}
	previous := make(map[string]PostedComment, len(history))
	for _, c := range history {
		previous[strings.ToLower(strings.TrimSpace(c.Name))] = c
	}
	var firstURL, lastState string
	for i, doc := range docs {
		old := previous[strings.ToLower(strings.TrimSpace(doc.Name))]
		id := old.ID
		if id == 0 {
			id = doc.CommentID
		}
		if id == 0 {
			id = commentIDFromURL(old.URL)
		}
		if i == 0 && id == 0 && markerID != 0 && len(history) == 0 {
			id = markerID
		}
		if markerID != 0 {
			if id != markerID {
				doc.Body = strings.ReplaceAll(doc.Body, litmusCommentMarker(), "")
			} else if !strings.Contains(doc.Body, litmusCommentMarker()) {
				doc.Body = strings.TrimSpace(doc.Body) + "\n\n" + litmusCommentMarker()
			}
		}
		if markerID == 0 && i != 0 {
			doc.Body = strings.ReplaceAll(doc.Body, litmusCommentMarker(), "")
		}
		if doc.Fixed && id == 0 {
			continue
		}
		sent, state, err := s.sendComment(ctx, run, doc.Body, id)
		if err != nil {
			return "", "", err
		}
		url := sentCommentURL(sent)
		if firstURL == "" {
			firstURL = url
		}
		lastState = state
		mapping := PostedComment{Name: doc.Name, ID: sentCommentID(sent, id), URL: url, Number: doc.Number, Fixed: doc.Fixed}
		if _, err := s.update(run.ID, func(cur *Run) { cur.Comments = appendCommentMapping(cur.Comments, mapping) }); err != nil {
			return "", "", err
		}
	}
	if v.Overall == CheckFail && s.reviewOn(run) {
		if err := s.requestChanges(ctx, run, login, firstURL); err != nil {
			s.commentLog(run, fmt.Errorf("review not submitted: %w", err))
		}
	}
	return firstURL, lastState, nil
}

func appendCommentMapping(current []PostedComment, next PostedComment) []PostedComment {
	for i := range current {
		if strings.EqualFold(strings.TrimSpace(current[i].Name), strings.TrimSpace(next.Name)) {
			current[i] = next
			return current
		}
	}
	return append(current, next)
}

// composeComment is the first step of posting: it writes comment.md with no evidence in it, and touches no network.
func (s *Service) composeComment(run Run, v Verdict) (string, evidence, error) {
	ev := evidence{state: EvidenceOff, videoState: "off"}
	if _, err := s.writeComment(run, v, ev); err != nil {
		return "", ev, err
	}
	docs, err := s.draftComments(run, v)
	if err != nil {
		return "", ev, err
	}
	if len(docs) == 0 {
		return "", ev, errors.New("no comment draft was written")
	}
	return docs[0].Body, ev, nil
}

// publishComment is the second step: it publishes the evidence images and videos and posts or updates the comment.
// Evidence is published only once the token's login is known, so a gh that cannot post does not push files either.
func (s *Service) publishComment(ctx context.Context, run Run, v Verdict, body string) (url, state string, ev evidence) {
	ev = evidence{state: EvidenceOff, videoState: "off"}
	lctx, cancel := context.WithTimeout(ctx, commentTimeout)
	_, err := s.ghLogin(lctx)
	cancel()
	if err != nil {
		return "", s.commentFailed(run, err), ev
	}
	ev = s.publishEvidence(ctx, run, v)
	ev.checkClips, ev.videoState = s.publishCheckClips(ctx, run, v)
	pctx, cancel := context.WithTimeout(ctx, commentTimeout)
	defer cancel()
	url, state, err = s.publishCommentSet(pctx, run, v, ev, body)
	if err != nil {
		return "", s.commentFailed(run, err), ev
	}
	return url, state, ev
}

// commentFailed logs err to comment.log in the run dir and returns the state it earns.
func (s *Service) commentFailed(run Run, err error) string {
	slog.Warn("litmus: PR comment failed", "run", run.ID, "error", err)
	s.commentLog(run, err)
	reason, _, _ := strings.Cut(err.Error(), "\n")
	return "failed:" + clip(strings.TrimSpace(reason), 120)
}

func (s *Service) commentLog(run Run, err error) { s.commentLogf(run, "%v", err) }

func (s *Service) commentLogf(run Run, format string, a ...any) {
	f, ferr := os.OpenFile(filepath.Join(s.store.root, run.ID, "comment.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if ferr != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s %s\n", s.clock().UTC().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

// requestChanges submits the REQUEST_CHANGES review, unless the token owns the PR: GitHub refuses that from an author.
func (s *Service) requestChanges(ctx context.Context, run Run, login, commentURL string) error {
	n := strconv.Itoa(run.PRNumber)
	author, err := s.gh.Run(ctx, "pr", "view", n, "--repo", run.Repo, "--json", "author", "--jq", ".author.login")
	if err != nil || strings.EqualFold(strings.TrimSpace(string(author)), login) {
		return err
	}
	link := cmp.Or(commentURL, fmt.Sprintf("https://github.com/%s/pull/%d", run.Repo, run.PRNumber))
	_, err = s.ghSend(ctx, run, "POST", "repos/"+run.Repo+"/pulls/"+n+"/reviews",
		map[string]string{"event": "REQUEST_CHANGES", "body": "Litmus found a failing check on this pull request.\nDetails: " + link})
	return err
}

// ghLogin is the login the gh token acts as, asked once per service.
func (s *Service) ghLogin(ctx context.Context) (string, error) {
	s.mu.Lock()
	login := s.login
	s.mu.Unlock()
	if login != "" {
		return login, nil
	}
	out, err := s.gh.Run(ctx, "api", "user", "--jq", ".login")
	if login = strings.TrimSpace(string(out)); err == nil && login == "" {
		err = errors.New("gh api user returned no login")
	}
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.login = login
	s.mu.Unlock()
	return login, nil
}

// ghSend calls the GitHub API with a JSON body read from a file, never from argv, and returns the object gh prints.
func (s *Service) ghSend(ctx context.Context, run Run, method, path string, body map[string]string) ([]byte, error) {
	data, _ := json.Marshal(body)
	f, err := os.CreateTemp(filepath.Join(s.store.root, run.ID), ".gh-*.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	return s.gh.Run(ctx, "api", "--method", method, path, "--input", f.Name())
}

// findComment returns the id of the comment holding marker that login wrote, or 0. `gh --paginate` prints one JSON
// array per page, back to back.
func findComment(raw []byte, marker, login string) (int64, error) {
	for dec := json.NewDecoder(bytes.NewReader(raw)); dec.More(); {
		var page []struct {
			ID   int64
			Body string
			User struct{ Login string }
		}
		if err := dec.Decode(&page); err != nil {
			return 0, err
		}
		for _, c := range page {
			if strings.Contains(c.Body, marker) && strings.EqualFold(c.User.Login, login) {
				return c.ID, nil
			}
		}
	}
	return 0, nil
}
