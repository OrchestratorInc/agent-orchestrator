// Package litmus implements agentic PR testing and issue reproduction. A tester drives private builds
// and submits findings and coverage backed by action receipts. These JSON shapes are the on-disk and wire contract.
package litmus

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RunBudget is the PR testing time after prepare finishes. Intake starts a provisional deadline;
// an active prepare uses PrepareBudget and resets the testing deadline on completion.
const RunBudget = 25 * time.Minute

// IssueBudget is the testing time for an issue after preparation.
const IssueBudget = 15 * time.Minute

// PrepareBudget caps one prepare, both builds together. A prepare past it is stuck: the run closes as blocked.
const PrepareBudget = 20 * time.Minute

// A tester that has not run intake (and has no intake or prepare in flight) is reaped when its session has been exited or idle for IdleBudget, or SpawnBudget after it was spawned.
const (
	IdleBudget  = 2 * time.Minute
	SpawnBudget = 5 * time.Minute
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

// Run is one test attempt, owned by one session.
type Run struct {
	Kind          string     `json:"kind"` // pr | issue
	TargetSHA     string     `json:"targetSha,omitempty"`
	ID            string     `json:"id"`
	SessionID     string     `json:"sessionId"`
	ProjectID     string     `json:"projectId,omitempty"`     // the AO project the tester was started in; empty on runs older than the Litmus panel
	Repo          string     `json:"repo"`                    // as gh resolved the PR (follows renames), lowercase owner/repo
	RequestedRepo string     `json:"requestedRepo,omitempty"` // owner/repo from the URL the user gave
	PRNumber      int        `json:"prNumber"`
	IssueNumber   int        `json:"issueNumber,omitempty"`
	CandidateURL  string     `json:"candidateUrl,omitempty"`
	Title         string     `json:"title,omitempty"` // the PR's title at intake; runs from before it was stored take it from context-pack.md
	BaseSHA       string     `json:"baseSha,omitempty"`
	HeadSHA       string     `json:"headSha,omitempty"`
	Issues        []IssueRef `json:"issues,omitempty"`  // the issues the PR closes (at most three), so the comment can say what the PR is for
	Areas         []Area     `json:"areas,omitempty"`   // the parts of the codebase the PR changes, in words, biggest first
	BaseRef       string     `json:"baseRef,omitempty"` // the PR's base branch name, as the comment calls the base build; empty on older runs (main)
	State         RunState   `json:"state"`
	CreatedAt     time.Time  `json:"createdAt"`
	Deadline      time.Time  `json:"deadline"`
	Legs          []Leg      `json:"legs,omitempty"`
	Error         string     `json:"error,omitempty"`        // why the last intake failed (a run with no pinned SHAs may retry), or "blocked at prepare: ..."
	Comment       string     `json:"comment,omitempty"`      // post | off: whether submit posts the verdict to the PR; set by intake, empty on older runs (no post)
	CommentURL    string     `json:"commentUrl,omitempty"`   // the PR comment submit posted or updated
	CommentState  string     `json:"commentState,omitempty"` // posted | updated | partial:<reason> | off | failed:<reason>
	// Evidence is whether submit publishes screenshots and GIFs for the comment: post | off; empty on older runs (off).
	Evidence       string `json:"evidence,omitempty"`
	EvidenceState  string `json:"evidenceState,omitempty"`  // published | off | failed:<reason>
	EvidenceCommit string `json:"evidenceCommit,omitempty"` // the litmus/evidence commit the comment's image links are pinned to
	RepoVisibility string `json:"repoVisibility,omitempty"` // public | private: how image links are written; asked of gh once
	// RepoID is the repo's numeric id, asked of gh once: the video upload endpoint wants it. --no-evidence also turns videos off.
	RepoID      int64             `json:"repoId,omitempty"`
	VideoState  string            `json:"videoState,omitempty"`  // attached | partial:<reason> | off | failed:<reason>
	VideoURLs   map[string]string `json:"videoUrls,omitempty"`   // by finding id: the uploaded video link
	CommentURLs map[string]string `json:"commentUrls,omitempty"` // full marker -> successful comment URL
	// Gaps are what intake could not get, one plain sentence each (a linked issue it could not read, the PR's text when
	// GitHub was down, a seed step that failed at launch); the pack, verdict.md and the comment's Limits show them. A gap never changes the result.
	Gaps []string `json:"gaps,omitempty"`
	// Ready names the builds (head, base or main) that were finished when prepare ran out of time, so the comment can say which.
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
	Name          string      `json:"name"` // base | head | main
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

// Receipt is the evidence for one state-changing action.
type Receipt struct {
	Seq         int       `json:"seq"`
	Leg         string    `json:"leg,omitempty"` // the leg that was driven; empty means head
	Action      string    `json:"action"`
	Theme       string    `json:"theme,omitempty"`
	Args        []string  `json:"args,omitempty"`
	BeforePath  string    `json:"beforePath,omitempty"`
	AfterPath   string    `json:"afterPath,omitempty"`
	ConsoleTail string    `json:"consoleTail,omitempty"`
	Output      string    `json:"output,omitempty"`
	OutputCut   bool      `json:"outputCut,omitempty"` // Output is the last 4 KiB of a longer output
	At          time.Time `json:"at"`
	// TargetRole and TargetName are what the tester's last snapshot called the element it acted on, for plain captions.
	TargetRole string      `json:"targetRole,omitempty"`
	TargetName string      `json:"targetName,omitempty"`
	Box        *ElementBox `json:"box,omitempty"`    // click target in CSS pixels
	Reason     string      `json:"reason,omitempty"` // why a click was refused
	PageErrors string      `json:"pageErrors,omitempty"`
	MS         int64       `json:"ms,omitempty"`
}

// preparedAt is when the builds were ready: prepare gives the tester RunBudget from then, so the deadline says when. A run
// that never prepared has no build time, and its testing time starts at intake.
func (r Run) preparedAt() time.Time {
	if p := r.Deadline.Add(-r.budget()); p.After(r.CreatedAt) {
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

func (r Run) noWords() bool {
	return slices.ContainsFunc(r.Gaps, func(g string) bool { return strings.HasPrefix(g, gapNoWords) })
}

func (r Receipt) onLeg() string { return cmp.Or(r.Leg, "head") }

// Finding and Coverage preserve the tester's text and cite per-leg receipt numbers.
type Finding struct {
	Title    string       `json:"title"`
	Leg      string       `json:"leg"`
	Severity string       `json:"severity"` // bug | worth-a-look
	Receipts []int        `json:"receipts"`
	Text     string       `json:"text"`
	Clip     *FindingClip `json:"clip,omitempty"`
}

// FindingClip names the receipt moments to watch on the finding's leg.
type FindingClip struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type Coverage struct {
	Text     string `json:"text"`
	Leg      string `json:"leg"`
	Receipts []int  `json:"receipts"`
	Trigger  bool   `json:"trigger,omitempty"`
}

type Verdict struct {
	Result         string     `json:"result"` // pass | fail | partial | reproduced | not_reproduced | blocked
	Headline       string     `json:"headline"`
	Findings       []Finding  `json:"findings"`
	Coverage       []Coverage `json:"coverage"`
	Environment    string     `json:"environment"`
	BlockedOn      string     `json:"blocked_on,omitempty"`
	NotTested      string     `json:"not_tested,omitempty"`
	ReceiptCount   int        `json:"receipt_count,omitempty"`
	CommentURL     string     `json:"commentUrl,omitempty"`
	CommentState   string     `json:"commentState,omitempty"`
	EvidenceState  string     `json:"evidenceState,omitempty"`
	EvidenceCommit string     `json:"evidenceCommit,omitempty"`
}

// RunStatus reports the run and long operations still in flight.
type RunStatus struct {
	OwnerLogins string     `json:"ownerLogins,omitempty"`
	Run         Run        `json:"run"`
	InFlight    []InFlight `json:"inFlight,omitempty"`
}

type InFlight struct {
	Verb    string  `json:"verb"`
	Seconds float64 `json:"seconds"`
}

const browserDataRule = "Never import, export, sign in to or read the owner's real browser profiles, cookies, passwords, keychain or accounts; check such a feature only with data created in the test window, or record it in blocked_on."

// Number and URL identify the tested issue or pull request.
func (r Run) Number() int {
	if r.Kind == "issue" {
		return r.IssueNumber
	}
	return r.PRNumber
}

// Label names the target in display text. Runs used only for recipe validation have no number.
func (r Run) Label() string {
	label := r.kindNoun()
	if r.Number() > 0 {
		label += " #" + strconv.Itoa(r.Number())
	}
	return label
}

func (r Run) kindNoun() string {
	if r.Kind == "issue" {
		return "issue"
	}
	return "PR"
}

// LegName names a build without changing its on-disk leg identifier.
func (r Run) LegName(leg string) string {
	switch leg {
	case "head":
		if r.Kind == "issue" {
			return "candidate PR"
		}
		return "PR"
	case "base", "main":
		return "main"
	default:
		return leg
	}
}

func (r Run) URL() string {
	path := "pull"
	if r.Kind == "issue" {
		path = "issues"
	}
	return "https://github.com/" + r.Repo + "/" + path + "/" + strconv.Itoa(r.Number())
}

func (r Run) budget() time.Duration {
	if r.Kind == "issue" {
		return IssueBudget
	}
	return RunBudget
}

func (r Run) pinned() bool {
	if r.Kind == "issue" {
		return r.TargetSHA != ""
	}
	return r.HeadSHA != "" && r.BaseSHA != ""
}

type targetLeg struct {
	Name       string `json:"name"`
	SHA        string `json:"sha"`
	State      string `json:"state,omitempty"`
	DaemonPort int    `json:"daemonPort,omitempty"`
	DebugPort  int    `json:"debugPort,omitempty"`
}

func (r Run) targets() []targetLeg {
	var legs []targetLeg
	if r.Kind == "issue" {
		legs = []targetLeg{{Name: "main", SHA: r.TargetSHA}}
		if r.CandidateURL != "" {
			legs = append(legs, targetLeg{Name: "head", SHA: r.HeadSHA})
		}
	} else {
		legs = []targetLeg{{Name: "head", SHA: r.HeadSHA}, {Name: "base", SHA: r.BaseSHA}}
	}
	for i := range legs {
		for _, l := range r.Legs {
			if l.Name == legs[i].Name {
				legs[i].State, legs[i].DaemonPort, legs[i].DebugPort = l.State, l.DaemonPort, l.DebugPort
			}
		}
	}
	return legs
}
