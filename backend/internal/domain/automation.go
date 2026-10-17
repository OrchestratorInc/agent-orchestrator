package domain

import "time"

// AutomationID identifies one recurring automation definition.
type AutomationID string

// AutomationRunID identifies one logical scheduled occurrence.
type AutomationRunID string

// AutomationRunStatus is the durable lifecycle of a scheduled occurrence.
type AutomationRunStatus string

const (
	// AutomationRunPending is waiting to be claimed by the scheduler.
	AutomationRunPending AutomationRunStatus = "pending"
	// AutomationRunSpawning has been claimed and is creating its session.
	AutomationRunSpawning AutomationRunStatus = "spawning"
	// AutomationRunRunning has an active session.
	AutomationRunRunning AutomationRunStatus = "running"
	// AutomationRunCompleted finished successfully.
	AutomationRunCompleted AutomationRunStatus = "completed"
	// AutomationRunFailed could not complete successfully.
	AutomationRunFailed AutomationRunStatus = "failed"
)

// Automation is the durable user-authored recurring definition. RRuleText is
// canonical; Timezone preserves the wall-clock intent used to advance it.
type Automation struct {
	ID          AutomationID `json:"id"`
	ProjectID   ProjectID    `json:"projectId"`
	DisplayName string       `json:"displayName"`
	Prompt      string       `json:"prompt"`
	TemplateID  string       `json:"templateId"`
	UseCase     string       `json:"useCase"`
	Kind        SessionKind  `json:"kind"`
	Harness     AgentHarness `json:"harness,omitempty"`
	RRuleText   string       `json:"rrule"`
	Timezone    string       `json:"timezone"`
	Enabled     bool         `json:"enabled"`
	NextRunAt   time.Time    `json:"nextRunAt"`
	LastRunAt   *time.Time   `json:"lastRunAt,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	// LatestRun is populated by list/get service reads for API projection. It is
	// derived from automation_runs and never persisted on the definition row.
	LatestRun *AutomationRun `json:"-"`
}

// AutomationTemplateCustom and AutomationUseCaseUnknown preserve compatibility
// for definitions created before optional origin and purpose metadata existed.
const (
	AutomationTemplateCustom = "custom"
	AutomationUseCaseUnknown = "unknown"
)

// ValidAutomationTemplateID accepts only bundled origins and custom definitions.
func ValidAutomationTemplateID(value string) bool {
	switch value {
	case AutomationTemplateCustom, "daily_pr_review_v1", "daily_issue_triage_v1", "weekly_release_notes_v1":
		return true
	default:
		return false
	}
}

// ValidAutomationUseCase accepts the closed vocabulary of declared purposes.
func ValidAutomationUseCase(value string) bool {
	switch value {
	case "pr_review", "issue_triage", "repo_digest", "ci_triage", "release_notes", "security_audit", "dependency_maintenance", "docs_maintenance", "other", AutomationUseCaseUnknown:
		return true
	default:
		return false
	}
}

// AutomationFilter is the storage/service list contract. Nil filters mean all;
// Limit and Offset are normalized by the service before reaching persistence.
type AutomationFilter struct {
	ProjectID *ProjectID
	Enabled   *bool
	Limit     int64
	Offset    int64
}

// AutomationPage is one stable page plus the filtered total.
type AutomationPage struct {
	Items []Automation `json:"items"`
	Total int64        `json:"total"`
}

// AutomationRun is the durable execution record for one scheduled occurrence.
type AutomationRun struct {
	ID             AutomationRunID     `json:"id"`
	AutomationID   AutomationID        `json:"automationId"`
	ScheduledFor   time.Time           `json:"scheduledFor"`
	SessionID      *SessionID          `json:"sessionId,omitempty"`
	Status         AutomationRunStatus `json:"status"`
	AttemptCount   int64               `json:"attemptCount"`
	ClaimedAt      *time.Time          `json:"claimedAt,omitempty"`
	LeaseExpiresAt *time.Time          `json:"leaseExpiresAt,omitempty"`
	StartedAt      *time.Time          `json:"startedAt,omitempty"`
	FinishedAt     *time.Time          `json:"finishedAt,omitempty"`
	ErrorMessage   string              `json:"errorMessage,omitempty"`
	CreatedAt      time.Time           `json:"createdAt"`
	UpdatedAt      time.Time           `json:"updatedAt"`
}

// AutomationRunFilter selects a stable page of one automation's history.
type AutomationRunFilter struct {
	AutomationID AutomationID
	Status       *AutomationRunStatus
	Limit        int64
	Offset       int64
}

// AutomationRunPage is one run-history page plus the filtered total.
type AutomationRunPage struct {
	Items []AutomationRun `json:"items"`
	Total int64           `json:"total"`
}
