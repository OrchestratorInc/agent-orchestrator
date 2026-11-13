package domain

import (
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

// ErrDuplicateReviewRun is returned by InsertReviewRun when a run already exists
// for the same worker session and target commit (the partial unique index from
// migration 0013). It lets the review engine fall back to the recorded run
// instead of surfacing a raw storage error after a reviewer may have launched.
var ErrDuplicateReviewRun = errors.New("domain: review run already exists for session and target sha")

// Review is the per-worker, per-reviewer-harness code-review record. A repeat
// trigger for the same harness reuses this row; the per-pass facts live on
// ReviewRun.
type Review struct {
	ID        string          `json:"id"`
	SessionID SessionID       `json:"sessionId"`
	ProjectID ProjectID       `json:"projectId"`
	Harness   ReviewerHarness `json:"harness"`
	PRURL     string          `json:"prUrl"`
	// ReviewerHandleID is the runtime handle of the live reviewer pane, reused
	// across passes and exposed so the UI can attach its terminal.
	ReviewerHandleID string `json:"reviewerHandleId"`
	AgentSessionID   string `json:"agentSessionId"`
	// ReviewerLaunchID is the AO runtime generation that owns the live reviewer
	// pane on this row. It fences delayed hooks from an older replaced reviewer.
	ReviewerLaunchID string `json:"-"`
	// ReviewerActivityState is the latest activity hook reported by the review
	// pane itself. It is separate from ReviewRun.Status so the UI can distinguish
	// "review pass exists" from "reviewer is actively working right now".
	ReviewerActivityState ActivityState `json:"reviewerActivityState,omitempty"`
	// InterfaceMode selects the durable reviewer surface. Chat reviewers own a
	// native conversation; TUI reviewers continue to use their terminal handle.
	InterfaceMode          ReviewerInterfaceMode `json:"interfaceMode"`
	ProviderConversationID string                `json:"providerConversationId"`
	ControllerGeneration   string                `json:"controllerGeneration"`
	ControllerError        string                `json:"controllerError"`
	CreatedAt              time.Time             `json:"createdAt"`
	UpdatedAt              time.Time             `json:"updatedAt"`

	// IsArchived retires the reviewer surface without deleting its history.
	IsArchived bool `json:"-"`
}

// ReviewerInterfaceMode selects the durable UI surface for a reviewer.
type ReviewerInterfaceMode string

const (
	// ReviewerInterfaceTUI uses the reviewer's terminal handle.
	ReviewerInterfaceTUI ReviewerInterfaceMode = "tui"
	// ReviewerInterfaceChat uses a reviewer-owned native chat.
	ReviewerInterfaceChat ReviewerInterfaceMode = "chat"
)

// ReviewerSurface gives clients one stable identifier for either reviewer UI.
type ReviewerSurface struct {
	Mode            ReviewerInterfaceMode `json:"mode"`
	ReviewID        string                `json:"reviewId"`
	Harness         ReviewerHarness       `json:"harness"`
	HandleID        string                `json:"handleId,omitempty"`
	ControllerError string                `json:"controllerError,omitempty"`
}

// ReviewRun is one review pass against a worker's PR.
type ReviewRun struct {
	ID        string    `json:"id"`
	ReviewID  string    `json:"reviewId"`
	SessionID SessionID `json:"sessionId"`
	// BatchID groups review runs created by one trigger so worker feedback can
	// be delivered once after the whole trigger batch is terminal. Empty marks
	// legacy/single-run delivery.
	BatchID string          `json:"batchId"`
	Harness ReviewerHarness `json:"harness"`
	// TriggerSource records whether this pass was requested by a user, by an
	// AO agent session through the CLI, or by the daemon auto-review coordinator.
	TriggerSource ReviewTriggerSource `json:"triggerSource" enum:"manual,agent,auto"`
	PRURL         string              `json:"prUrl"`
	// TargetSHA is the PR head commit this pass reviewed.
	TargetSHA string          `json:"targetSha"`
	Status    ReviewRunStatus `json:"status" enum:"queued,launching,running,cancelling,completed,failed,cancelled,superseded"`
	Verdict   ReviewVerdict   `json:"verdict"`
	// Body is the review text the reviewer submitted. It is recorded for AO's
	// own tracking; the reviewer also posts the review to the PR itself.
	Body string `json:"body"`
	// GithubReviewID is the id of the GitHub PR review the reviewer posted for
	// this pass (the `gh api .../pulls/{n}/reviews` object id), recorded at
	// submit time. It is empty when the reviewer could not post to the provider.
	// When the pass requests changes, AO includes it in the message to the
	// worker so the worker knows exactly which review to address and reply to.
	GithubReviewID string     `json:"githubReviewId"`
	CreatedAt      time.Time  `json:"createdAt"`
	DeliveredAt    *time.Time `json:"deliveredAt,omitempty"`
	// AutoInjectReview snapshots the session policy when this result is first
	// recorded. Later toggle changes must not rewrite or deliver this run.
	AutoInjectReview bool `json:"autoInjectReview"`
	// FailureReason is set only on failed runs.
	FailureReason ReviewFailureReason `json:"failureReason,omitempty" enum:"preflight_failed,launch_failed,never_started,exited_during_launch,turn_ended_without_submit,process_exited,timed_out,daemon_restarted,cancel_unconfirmed"`
	// CancelledBy is set only on cancelled runs.
	CancelledBy ReviewCancelledBy `json:"cancelledBy,omitempty" enum:"user,worker_lifecycle"`
	// LaunchID is the reviewer launch serving this run; only activity carrying
	// it counts as this run's first activity.
	LaunchID          string     `json:"-"`
	LaunchingAt       *time.Time `json:"launchingAt,omitempty"`
	StartedAt         *time.Time `json:"startedAt,omitempty"`
	FirstActivityAt   *time.Time `json:"firstActivityAt,omitempty"`
	CancelRequestedAt *time.Time `json:"cancelRequestedAt,omitempty"`
	EndedAt           *time.Time `json:"endedAt,omitempty"`
}

// ReviewTriggerSource identifies who initiated a review pass.
type ReviewTriggerSource string

const (
	// ReviewTriggerManual marks a user-initiated review pass.
	ReviewTriggerManual ReviewTriggerSource = "manual"
	// ReviewTriggerAgent marks a pass an AO agent session (a worker reviewing
	// its own PR, or an orchestrator) requested through `ao review trigger`.
	ReviewTriggerAgent ReviewTriggerSource = "agent"
	// ReviewTriggerAuto marks a daemon-initiated review pass.
	ReviewTriggerAuto ReviewTriggerSource = "auto"
)

// ReviewRunStatus is the lifecycle state of a single review pass. It is the
// daemon's own type: AO Cloud's contract.AOReviewRunStatus keeps its original
// values, and ContractStatus projects a daemon run onto it.
type ReviewRunStatus string

// Review run statuses. queued, launching, running and cancelling are active:
// every active run has an owner and a time limit (see internal/review's run
// supervisor). The rest are final and are never overwritten.
const (
	// ReviewRunQueued means the trigger accepted the pass and wrote the row; no
	// reviewer has been touched yet.
	ReviewRunQueued ReviewRunStatus = "queued"
	// ReviewRunLaunching means the daemon is starting a reviewer or sending the
	// task to a live one.
	ReviewRunLaunching ReviewRunStatus = "launching"
	// ReviewRunRunning means the task prompt was delivered to the reviewer.
	ReviewRunRunning ReviewRunStatus = "running"
	// ReviewRunCancelling means a stop was requested and is not confirmed yet.
	ReviewRunCancelling ReviewRunStatus = "cancelling"
	// ReviewRunCompleted means the reviewer submitted a verdict.
	ReviewRunCompleted ReviewRunStatus = "completed"
	// ReviewRunFailed means the pass ended without a verdict; FailureReason
	// says why.
	ReviewRunFailed ReviewRunStatus = "failed"
	// ReviewRunCancelled means a person or the worker lifecycle stopped the pass
	// and the stop was confirmed; CancelledBy says who.
	ReviewRunCancelled ReviewRunStatus = "cancelled"
	// ReviewRunSuperseded means a newer commit on the same PR made the pass
	// pointless.
	ReviewRunSuperseded ReviewRunStatus = "superseded"
)

// ActiveReviewRunStatuses lists the states that hold a review lock for their
// PR head and reviewer harness.
var ActiveReviewRunStatuses = []ReviewRunStatus{ReviewRunQueued, ReviewRunLaunching, ReviewRunRunning, ReviewRunCancelling}

// IsActive reports whether the run still has an owner working towards a final
// state.
func (s ReviewRunStatus) IsActive() bool {
	switch s {
	case ReviewRunQueued, ReviewRunLaunching, ReviewRunRunning, ReviewRunCancelling:
		return true
	default:
		return false
	}
}

// IsFinal reports whether the run reached a final state.
func (s ReviewRunStatus) IsFinal() bool {
	switch s {
	case ReviewRunCompleted, ReviewRunFailed, ReviewRunCancelled, ReviewRunSuperseded:
		return true
	default:
		return false
	}
}

// ContractStatus projects a daemon run status onto AO Cloud's unchanged
// contract vocabulary.
func (s ReviewRunStatus) ContractStatus() contract.AOReviewRunStatus {
	switch s {
	case ReviewRunQueued, ReviewRunLaunching, ReviewRunRunning, ReviewRunCancelling:
		return contract.AOReviewRunRunning
	case ReviewRunCompleted:
		return contract.AOReviewRunComplete
	case ReviewRunCancelled:
		return contract.AOReviewRunCancelled
	default:
		return contract.AOReviewRunFailed
	}
}

// ReviewFailureReason is the daemon-assigned reason a run failed. The reviewer
// never reports one. Clients show one generic message; the code is kept for
// `ao review ls`, telemetry and auto-review retry decisions.
type ReviewFailureReason string

// Review failure reasons.
const (
	ReviewFailureNone                   ReviewFailureReason = ""
	ReviewFailurePreflightFailed        ReviewFailureReason = "preflight_failed"
	ReviewFailureLaunchFailed           ReviewFailureReason = "launch_failed"
	ReviewFailureNeverStarted           ReviewFailureReason = "never_started"
	ReviewFailureExitedDuringLaunch     ReviewFailureReason = "exited_during_launch"
	ReviewFailureTurnEndedWithoutSubmit ReviewFailureReason = "turn_ended_without_submit"
	ReviewFailureProcessExited          ReviewFailureReason = "process_exited"
	ReviewFailureTimedOut               ReviewFailureReason = "timed_out"
	ReviewFailureDaemonRestarted        ReviewFailureReason = "daemon_restarted"
	ReviewFailureCancelUnconfirmed      ReviewFailureReason = "cancel_unconfirmed"
)

// AutoRetryable reports whether auto review may retry a head after a run
// failed for this reason. A failed preflight needs a person to fix the
// harness, and an unconfirmed cancel may still have a reviewer working.
func (r ReviewFailureReason) AutoRetryable() bool {
	return r != ReviewFailurePreflightFailed && r != ReviewFailureCancelUnconfirmed
}

// ReviewCancelledBy records who stopped a cancelled run.
type ReviewCancelledBy string

// Who cancelled a run.
const (
	ReviewCancelledByNone            ReviewCancelledBy = ""
	ReviewCancelledByUser            ReviewCancelledBy = "user"
	ReviewCancelledByWorkerLifecycle ReviewCancelledBy = "worker_lifecycle"
)

// ReviewRunPatch carries the fields one guarded transition writes alongside
// the new status. Nil and empty fields leave the stored value unchanged.
type ReviewRunPatch struct {
	Verdict           ReviewVerdict
	Body              *string
	GithubReviewID    *string
	AutoInjectReview  *bool
	FailureReason     ReviewFailureReason
	CancelledBy       ReviewCancelledBy
	LaunchID          string
	LaunchingAt       *time.Time
	StartedAt         *time.Time
	FirstActivityAt   *time.Time
	CancelRequestedAt *time.Time
	EndedAt           *time.Time
}

// ReviewVerdict is the outcome a reviewer reports. The empty verdict marks a
// run that has not produced an outcome yet.
type ReviewVerdict = contract.AOReviewVerdict

// Review verdicts.
const (
	VerdictNone             = contract.AOReviewVerdictNone
	VerdictApproved         = contract.AOReviewVerdictApproved
	VerdictChangesRequested = contract.AOReviewVerdictChangesRequested
)

// CurrentHeadReviewRun is one AO review pass recorded against a PR's current
// head commit, reduced to the fields a derived read model needs.
type CurrentHeadReviewRun struct {
	SessionID SessionID
	Harness   ReviewerHarness
	PRURL     string
	Status    ReviewRunStatus
	Verdict   ReviewVerdict
	ID        string
	CreatedAt time.Time
	// FailedAutoRuns counts every failed auto review pass on this PR head.
	FailedAutoRuns int
	// AutoRetryBlocked reports a cancelled pass or a verdict on this PR head for
	// this harness, either of which stops auto review from retrying the head.
	AutoRetryBlocked bool
}

// AutoReviewFailedRetryLimit is how many failed auto review passes one PR head
// gets before auto review stops retrying it.
const AutoReviewFailedRetryLimit = 3

// ReviewerHandle names one live reviewer pane's runtime handle, independent
// of whether the worker session that spawned it still exists. A reviewer has
// no session row of its own — its identity is this review's id and
// SessionID (the worker it reviews) plus Harness — so the memory diagnostic
// needs this to find it by anything other than walking session roots.
type ReviewerHandle struct {
	ReviewID  string
	SessionID SessionID
	Harness   ReviewerHarness
	HandleID  string
}
