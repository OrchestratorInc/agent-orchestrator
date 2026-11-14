package domain

import "time"

type AutomationID string

type AutomationRunID string

type AutomationRunStatus string

const (
	AutomationRunPending   AutomationRunStatus = "pending"
	AutomationRunSpawning  AutomationRunStatus = "spawning"
	AutomationRunRunning   AutomationRunStatus = "running"
	AutomationRunCompleted AutomationRunStatus = "completed"
	AutomationRunFailed    AutomationRunStatus = "failed"
)

type Automation struct {
	ID          AutomationID   `json:"id"`
	ProjectID   ProjectID      `json:"projectId"`
	DisplayName string         `json:"displayName"`
	Prompt      string         `json:"prompt"`
	TemplateID  string         `json:"templateId"`
	UseCase     string         `json:"useCase"`
	Kind        SessionKind    `json:"kind"`
	Harness     AgentHarness   `json:"harness,omitempty"`
	Model       string         `json:"model,omitempty"`
	Mode        string         `json:"mode,omitempty"`
	Effort      string         `json:"effort,omitempty"`
	RRuleText   string         `json:"rrule"`
	Timezone    string         `json:"timezone"`
	Enabled     bool           `json:"enabled"`
	NextRunAt   time.Time      `json:"nextRunAt"`
	LastRunAt   *time.Time     `json:"lastRunAt,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	LatestRun   *AutomationRun `json:"-"`
}

const (
	AutomationTemplateCustom = "custom"
	AutomationUseCaseUnknown = "unknown"
)

func ValidAutomationTemplateID(value string) bool {
	switch value {
	case AutomationTemplateCustom, "daily_pr_review_v1", "daily_issue_triage_v1", "weekly_release_notes_v1":
		return true
	default:
		return false
	}
}

func ValidAutomationUseCase(value string) bool {
	switch value {
	case "pr_review", "issue_triage", "repo_digest", "ci_triage", "release_notes", "security_audit", "dependency_maintenance", "docs_maintenance", "other", AutomationUseCaseUnknown:
		return true
	default:
		return false
	}
}

type AutomationFilter struct {
	ProjectID *ProjectID
	Enabled   *bool
	Limit     int64
	Offset    int64
}

type AutomationPage struct {
	Items []Automation `json:"items"`
	Total int64        `json:"total"`
}

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

type AutomationRunFilter struct {
	AutomationID AutomationID
	Status       *AutomationRunStatus
	Limit        int64
	Offset       int64
}

type AutomationRunPage struct {
	Items []AutomationRun `json:"items"`
	Total int64           `json:"total"`
}
