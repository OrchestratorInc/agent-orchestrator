package ports

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// CouncilMember is one harness/model entry in a council fan-out.
type CouncilMember struct {
	Harness domain.AgentHarness
	Model   string
	Effort  string
	// EffortOverride preserves the distinction between an omitted effort and an
	// explicit empty value meaning the provider default, mirroring SpawnConfig.
	EffortOverride bool
}

// CouncilInput asks the session service to fan one brief out to several
// harnesses/models at once, each in its own session, all tagged with a shared
// council group id so clients can present them as one cohort.
type CouncilInput struct {
	ProjectID     domain.ProjectID
	RequestedMode domain.SessionMode
	ApprovalMode  domain.PermissionMode
	Prompt        string
	// DisplayName is the base sidebar label; each member appends its harness so
	// the cohort reads clearly in the sidebar.
	DisplayName string
	Members     []CouncilMember
	Attachments []SpawnAttachment
}

// CouncilMemberResult is the outcome of spawning one council member. Session is
// zero when Err is set; the fan-out is best-effort so a single member's failure
// does not discard the members that started.
type CouncilMemberResult struct {
	Harness domain.AgentHarness
	Model   string
	Session domain.Session
	Err     error
}

// CouncilResult is the outcome of a council fan-out: the shared group id and one
// result per requested member, in request order.
type CouncilResult struct {
	GroupID string
	Members []CouncilMemberResult
}
