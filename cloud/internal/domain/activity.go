package domain

import "time"

// Activity event kinds: the support trail recorded in ao_activity_events. The
// names are queried by operators (cloud/docs/support-runbook.md) and by
// CloudWatch metric filters (cloud/scripts/lib/offering_monitoring.py), so
// renaming one is a breaking change for both.
const (
	ActivityOrgCreated   = "org.created"
	ActivityMemberJoined = "member.joined"

	ActivityProjectCreated = "project.created"

	ActivitySessionCreateRequested = "session.create_requested"
	ActivitySessionCreateRefused   = "session.create_refused"
	ActivitySessionStartFailed     = "session.start_failed"
	ActivitySessionResumeRequested = "session.resume_requested"

	ActivitySandboxProvisioned = "sandbox.provisioned"
	ActivitySandboxReady       = "sandbox.ready"
	ActivitySandboxPaused      = "sandbox.paused"
	ActivitySandboxResuming    = "sandbox.resuming"
	ActivitySandboxTerminated  = "sandbox.terminated"

	ActivityWorkerConnected    = "worker.connected"
	ActivityWorkerDisconnected = "worker.disconnected"
	ActivityWorkerRelaunched   = "worker.relaunched"
	ActivityGitGrantFailed     = "worker.git_grant_failed"

	ActivityBootstrapStage  = "bootstrap.stage"
	ActivityBootstrapFailed = "bootstrap.failed"

	ActivityCoderConnectionValidated = "coder.connection_validated"

	ActivityCheckoutStarted   = "billing.checkout_started"
	ActivityCheckoutCompleted = "billing.checkout_completed"
	ActivityPlanChanged       = "billing.plan_changed"
	ActivityPlanRefused       = "billing.plan_refused"
	ActivityLimitHit          = "billing.limit_hit"
	ActivityWebhookFailed     = "billing.webhook_failed"
)

// ActivityEvent is one row of an organization's support trail. UserID and
// SessionID are optional (background work has no user). Attrs is a small,
// redacted, flat map; see activity.Sanitize for the bounds applied before a
// write.
type ActivityEvent struct {
	ID        string
	OrgID     string
	UserID    string
	SessionID string
	Kind      string
	Provider  string
	Attrs     map[string]any
	CreatedAt time.Time
}
