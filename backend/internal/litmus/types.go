// Package litmus implements agentic PR testing: a run pins a PR, a worker
// records checks against privately launched builds, and submit turns the
// receipts into a verdict. These JSON shapes are the on-disk and wire contract.
package litmus

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// RunBudget is how long the tester has once the builds are ready: the run's deadline moves to that long after prepare
// finishes. Until then the deadline is that long after intake, which prepare (PrepareBudget at most) must fit inside.
const RunBudget = 25 * time.Minute

// PrepareBudget caps one prepare, both builds together. A prepare past it is stuck: the run closes as blocked.
const PrepareBudget = 20 * time.Minute

// A tester that has not run intake (and has no intake or prepare in flight) is reaped when its session has been exited or idle for IdleBudget, or SpawnBudget after it was spawned.
const (
	IdleBudget  = 2 * time.Minute
	SpawnBudget = 5 * time.Minute
	// ReasonOutOfTime is the note on every check the deadline found without a result.
	ReasonOutOfTime = "ran out of time"
	// ReasonStopped is the reason on a run whose tester stopped before finishing.
	ReasonStopped = "stopped before it finished"
	// ReasonNoIntake is the reason on a run expired before its tester ever ran intake.
	ReasonNoIntake = "the tester never ran intake"
)

// RunState is the lifecycle position of a run.
type RunState string

// Run states. Submitted, expired and cleaned are closed.
const (
	RunIntake    RunState = "intake"
	RunPrepared  RunState = "prepared"
	RunRunning   RunState = "running"
	RunSubmitted RunState = "submitted"
	// RunAwaitingApproval is a submitted run of a project that asks before posting: the verdict and comment.md are
	// written, nothing is published, and the run waits for a post or a discard. It has no deadline.
	RunAwaitingApproval RunState = "awaiting-approval"
	RunExpired          RunState = "expired"
	RunCleaned          RunState = "cleaned"
)

// Closed reports whether the run accepts no further worker verbs.
func (s RunState) Closed() bool {
	return s == RunSubmitted || s == RunAwaitingApproval || s == RunExpired || s == RunCleaned
}

// Run is one PR test attempt, owned by one AO session.
type Run struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"sessionId"`
	ProjectID     string          `json:"projectId,omitempty"`     // the AO project the tester was started in; empty on runs older than the Litmus panel
	Repo          string          `json:"repo"`                    // as gh resolved the PR (follows renames), lowercase owner/repo
	RequestedRepo string          `json:"requestedRepo,omitempty"` // owner/repo from the URL the user gave
	PRNumber      int             `json:"prNumber"`
	Title         string          `json:"title,omitempty"` // the PR's title at intake; runs from before it was stored take it from context-pack.md
	BaseSHA       string          `json:"baseSha,omitempty"`
	HeadSHA       string          `json:"headSha,omitempty"`
	Issues        []IssueRef      `json:"issues,omitempty"`  // the issues the PR closes (at most three), so the comment can say what the PR is for
	Areas         []Area          `json:"areas,omitempty"`   // the parts of the codebase the PR changes, in words, biggest first
	BaseRef       string          `json:"baseRef,omitempty"` // the PR's base branch name, as the comment calls the base build; empty on older runs (main)
	State         RunState        `json:"state"`
	CreatedAt     time.Time       `json:"createdAt"`
	Deadline      time.Time       `json:"deadline"`
	Legs          []Leg           `json:"legs,omitempty"`
	Error         string          `json:"error,omitempty"`        // why the last intake failed (a run with no pinned SHAs may retry), or "blocked at prepare: ..."
	Comment       string          `json:"comment,omitempty"`      // post | off: whether submit posts the verdict to the PR; set by intake, empty on older runs (no post)
	Comments      []PostedComment `json:"comments,omitempty"`     // PR comment ids matched by check name on reruns
	CommentURL    string          `json:"commentUrl,omitempty"`   // the PR comment submit posted or updated
	CommentState  string          `json:"commentState,omitempty"` // posted | updated | off | failed:<reason>
	// Evidence is whether submit publishes screenshots and GIFs for the comment: post | off; empty on older runs (off).
	Evidence       string `json:"evidence,omitempty"`
	EvidenceState  string `json:"evidenceState,omitempty"`  // published | off | failed:<reason>
	EvidenceCommit string `json:"evidenceCommit,omitempty"` // the litmus/evidence commit the comment's image links are pinned to
	RepoVisibility string `json:"repoVisibility,omitempty"` // public | private: how image links are written; asked of gh once
	// RepoID is the repo's numeric id, asked of gh once: the video upload endpoint wants it. --no-evidence also turns videos off.
	RepoID     int64             `json:"repoId,omitempty"`
	VideoState string            `json:"videoState,omitempty"` // attached | partial | off | failed:<reason>
	VideoURLs  map[string]string `json:"videoUrls,omitempty"`  // by check name, or leg on older runs: the uploaded video link
	// Gaps are what intake could not get, one plain sentence each (a linked issue it could not read, the PR's text when
	// GitHub was down, a seed step that failed at launch); the pack, verdict.md and the comment's Limits show them. A gap never changes the result.
	Gaps []string `json:"gaps,omitempty"`
	// Ready names the builds (head, base) that were finished when prepare ran out of time, so the comment can say which.
	Ready []string `json:"ready,omitempty"`
}

// IssueRef is an issue the PR says it closes.
type IssueRef struct {
	Number int    `json:"number"`
	Title  string `json:"title,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Area is a part of the codebase a PR changes ("Renderer components", "Tests") and how many of its files changed.
// Stems are the names of its first eight files without the extension, so the comment can name a lone file.
type Area struct {
	Name  string   `json:"name"`
	Files int      `json:"files"`
	Stems []string `json:"stems,omitempty"`
}

// Leg is one launched target: the PR's base or head, built and run privately.
type Leg struct {
	Name          string      `json:"name"` // base | head
	Worktree      string      `json:"worktree,omitempty"`
	BuildManifest string      `json:"buildManifest,omitempty"`
	PID           int         `json:"pid,omitempty"`
	PIDStart      time.Time   `json:"pidStart,omitzero"`
	DataDir       string      `json:"dataDir,omitempty"`
	DaemonPort    int         `json:"daemonPort,omitempty"`
	DebugPort     int         `json:"debugPort,omitempty"`
	RunFile       string      `json:"runFile,omitempty"`
	Engine        *EngineInfo `json:"engine,omitempty"`
	State         string      `json:"state"`
	// RecordStartedAt is the screencast wall-clock origin, so action times map onto <leg>/<leg>.mp4. Zero: no recording.
	RecordStartedAt time.Time `json:"recordStartedAt,omitzero"`
	// RecordStoppedAt is when frame capture stopped; started without stopped means the recording never finished.
	RecordStoppedAt time.Time `json:"recordStoppedAt,omitzero"`
	// Retained for the generated API contract. The screencast recorder leaves these unset.
	RecordCursor bool   `json:"recordCursor,omitempty"`
	RecordFPS    string `json:"recordFps,omitempty"`
}

// CheckState is a check's progress; pass, fail, partial, absent and skipped are its outcomes.
type CheckState string

// Check states.
const (
	CheckPending CheckState = "pending"
	CheckRunning CheckState = "running"
	CheckPass    CheckState = "pass"
	CheckFail    CheckState = "fail"
	CheckPartial CheckState = "partial"
	// CheckAbsent is "this build does not have the feature at all": it needs a screenshot of where the feature would be,
	// and a fail is the feature being there and broken. On the head it counts as a failure.
	CheckAbsent CheckState = "absent"
	// CheckSkipped is the tester's "could not reach": the check needs no receipt and stays out of the headline.
	CheckSkipped CheckState = "skipped"
	// CheckBlocked is only ever a verdict's overall: the tester could not run the checks.
	CheckBlocked CheckState = "blocked"
)

// Check is one planned observation and the evidence recorded for it.
type Check struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Expect    string     `json:"expect"`
	State     CheckState `json:"state"`
	Note      string     `json:"note,omitempty"`
	Shot      int        `json:"shot,omitempty"`
	Leg       string     `json:"leg,omitempty"`  // base | head; empty means head
	Diff      string     `json:"diff,omitempty"` // pixel-diff image of this check's base and head after-shots (`ao testing compare`)
	Receipts  []Receipt  `json:"receipts,omitempty"`
	SameFault bool       `json:"sameFault,omitempty"` // the base failed or was partial for the same fault as the head
}

// unreached is a check the tester could not reach.
func (c Check) unreached() bool {
	return c.State == CheckSkipped
}

// onLeg is the leg the check observes; checks declared without one observe the head.
func (c Check) onLeg() string { return cmp.Or(c.Leg, "head") }

// legReceipts are the check's receipts from the build it observes.
func (c Check) legReceipts() []Receipt {
	return slices.DeleteFunc(slices.Clone(c.Receipts), func(r Receipt) bool { return r.onLeg() != c.onLeg() })
}

// Receipt is the evidence for one state-changing action.
type Receipt struct {
	Seq         int         `json:"seq"`
	Leg         string      `json:"leg,omitempty"` // the leg that was driven; empty means head
	Action      string      `json:"action"`
	Args        []string    `json:"args,omitempty"`
	BeforePath  string      `json:"beforePath,omitempty"`
	AfterPath   string      `json:"afterPath,omitempty"`
	ThemeShots  []ThemeShot `json:"themeShots,omitempty"`
	ThemeNote   string      `json:"themeNote,omitempty"`
	ConsoleTail string      `json:"consoleTail,omitempty"`
	At          time.Time   `json:"at"`
	// TargetRole and TargetName are what the tester's last snapshot called the element it acted on, for plain captions.
	TargetRole string      `json:"targetRole,omitempty"`
	TargetName string      `json:"targetName,omitempty"`
	Box        *ElementBox `json:"box,omitempty"`    // click target in CSS pixels
	Reason     string      `json:"reason,omitempty"` // why a click was refused
	PageErrors string      `json:"pageErrors,omitempty"`
	MS         int64       `json:"ms,omitempty"`
}

// ThemeShot is an opaque after-shot saved at check end for a system preference.
type ThemeShot struct {
	Theme string `json:"theme"`
	Path  string `json:"path"`
}

// preparedAt is when the builds were ready: prepare gives the tester RunBudget from then, so the deadline says when. A run
// that never prepared has no build time, and its testing time starts at intake.
func (r Run) preparedAt() time.Time {
	if p := r.Deadline.Add(-RunBudget); p.After(r.CreatedAt) {
		return p
	}
	return r.CreatedAt
}

// prepared reports whether prepare finished: only then does the run have a deadline, so a testing time to split out.
func (r Run) prepared() bool { return !r.Deadline.IsZero() }

// buildsAndTesting is how long the builds took and how long the tester had them, from intake to the end at now.
func (r Run) buildsAndTesting(now time.Time) (builds, testing time.Duration) {
	p := r.preparedAt()
	return max(p.Sub(r.CreatedAt), 0), max(now.Sub(p), 0)
}

// baseName is what a reader calls the base build: the PR's base branch, main when the run did not record it.
func (r Run) baseName() string { return mdText(cmp.Or(r.BaseRef, "main"), 60) }

// atBranchPoint is the words that say the base build is the base branch where the PR branched, not its tip: the run
// carries the fork-point gap. "" otherwise.
func (r Run) atBranchPoint() string {
	if slices.ContainsFunc(r.Gaps, func(g string) bool { return strings.HasPrefix(g, forkGapPrefix) }) {
		return " at the PR's branch point"
	}
	return ""
}

// noWords reports whether the run never read the PR's text: its ids came from git alone (see gapNoWords). Such a
// verdict waits for approval instead of posting by itself.
func (r Run) noWords() bool {
	return slices.ContainsFunc(r.Gaps, func(g string) bool { return strings.HasPrefix(g, gapNoWords) })
}

// builtLine says what had been built when a run closed before any leg started: after a prepare timeout, which build was
// ready.
func (r Run) builtLine() string {
	head, base := slices.Contains(r.Ready, "head"), slices.Contains(r.Ready, "base")
	switch {
	case head && !base:
		return fmt.Sprintf("The PR build was ready; %s was not. Nothing was checked.", r.baseName())
	case base && !head:
		return fmt.Sprintf("%s was ready; the PR build was not. Nothing was checked.", recase(r.baseName(), true))
	}
	return "Nothing was built or checked."
}

// sideName is how prose names a leg: "the PR build" for the head, the base branch for the base.
func (r Run) sideName(leg string) string {
	if leg == "base" {
		return r.baseName()
	}
	return "the PR build"
}

func (r Receipt) onLeg() string { return cmp.Or(r.Leg, "head") }

// Verdict is the submitted result: per-check outcomes with their receipts.
type Verdict struct {
	Overall      CheckState `json:"overall"` // pass | fail | partial | blocked
	Checks       []Check    `json:"checks"`
	Reasons      []string   `json:"reasons,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	StoppedEarly string     `json:"stoppedEarly,omitempty"` // a blocked submit that kept a recorded failure
	SkipBase     string     `json:"skipBase,omitempty"`     // why the tester did not run the base (submit --skip-base --reason)
	// CommentURL and CommentState echo the run's: where submit put the verdict on the PR, and how that went.
	CommentURL   string `json:"commentUrl,omitempty"`
	CommentState string `json:"commentState,omitempty"`
	// EvidenceState and EvidenceCommit echo the run's: whether the comment's images were published, and where.
	EvidenceState  string `json:"evidenceState,omitempty"`
	EvidenceCommit string `json:"evidenceCommit,omitempty"`
}

// RunStatus is a run with its checks.
type RunStatus struct {
	OwnerLogins string     `json:"ownerLogins,omitempty"`
	Run         Run        `json:"run"`
	Checks      []Check    `json:"checks"`
	InFlight    []InFlight `json:"inFlight,omitempty"` // long verbs still running for the run, oldest first by verb
}

// InFlight is a long verb (prepare, launch, submit, cleanup) still running for a
// run, and for how long: a prepare that has been building for minutes is not stuck.
type InFlight struct {
	Verb    string  `json:"verb"`
	Seconds float64 `json:"seconds"`
}

// browserDataRule is the one line the tester skill and the pack's last section both carry.
const browserDataRule = "Never import, export, sign in to or read the owner's real browser profiles, cookies, passwords, keychain or accounts; check such a feature only with data created in the test window, or end it as `skipped` with that reason."
