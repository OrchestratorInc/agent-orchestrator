package domain

import (
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

var ErrDuplicateReviewRun = errors.New("domain: review run already exists for session and target sha")

type Review struct {
	ID                     string                `json:"id"`
	SessionID              SessionID             `json:"sessionId"`
	ProjectID              ProjectID             `json:"projectId"`
	Harness                ReviewerHarness       `json:"harness"`
	PRURL                  string                `json:"prUrl"`
	ReviewerHandleID       string                `json:"reviewerHandleId"`
	AgentSessionID         string                `json:"agentSessionId"`
	ReviewerLaunchID       string                `json:"-"`
	ReviewerActivityState  ActivityState         `json:"reviewerActivityState,omitempty"`
	InterfaceMode          ReviewerInterfaceMode `json:"interfaceMode"`
	ProviderConversationID string                `json:"providerConversationId"`
	ControllerGeneration   string                `json:"controllerGeneration"`
	ControllerError        string                `json:"controllerError"`
	CreatedAt              time.Time             `json:"createdAt"`
	UpdatedAt              time.Time             `json:"updatedAt"`

	IsArchived bool `json:"-"`
}

type ReviewerInterfaceMode string

const (
	ReviewerInterfaceTUI  ReviewerInterfaceMode = "tui"
	ReviewerInterfaceChat ReviewerInterfaceMode = "chat"
)

type ReviewerSurface struct {
	Mode            ReviewerInterfaceMode `json:"mode"`
	ReviewID        string                `json:"reviewId"`
	Harness         ReviewerHarness       `json:"harness"`
	HandleID        string                `json:"handleId,omitempty"`
	ControllerError string                `json:"controllerError,omitempty"`
}

type ReviewRun struct {
	ID               string              `json:"id"`
	ReviewID         string              `json:"reviewId"`
	SessionID        SessionID           `json:"sessionId"`
	BatchID          string              `json:"batchId"`
	Harness          ReviewerHarness     `json:"harness"`
	TriggerSource    ReviewTriggerSource `json:"triggerSource" enum:"manual,agent,auto"`
	PRURL            string              `json:"prUrl"`
	TargetSHA        string              `json:"targetSha"`
	Status           ReviewRunStatus     `json:"status"`
	Verdict          ReviewVerdict       `json:"verdict"`
	Body             string              `json:"body"`
	GithubReviewID   string              `json:"githubReviewId"`
	CreatedAt        time.Time           `json:"createdAt"`
	DeliveredAt      *time.Time          `json:"deliveredAt,omitempty"`
	AutoInjectReview bool                `json:"autoInjectReview"`
}

type ReviewTriggerSource string

const (
	ReviewTriggerManual ReviewTriggerSource = "manual"
	ReviewTriggerAgent  ReviewTriggerSource = "agent"
	ReviewTriggerAuto   ReviewTriggerSource = "auto"
)

type ReviewRunStatus = contract.AOReviewRunStatus

const (
	ReviewRunRunning   = contract.AOReviewRunRunning
	ReviewRunComplete  = contract.AOReviewRunComplete
	ReviewRunDelivered = contract.AOReviewRunDelivered
	ReviewRunFailed    = contract.AOReviewRunFailed
	ReviewRunCancelled = contract.AOReviewRunCancelled
)

type ReviewVerdict = contract.AOReviewVerdict

const (
	VerdictNone             = contract.AOReviewVerdictNone
	VerdictApproved         = contract.AOReviewVerdictApproved
	VerdictChangesRequested = contract.AOReviewVerdictChangesRequested
)

type CurrentHeadReviewRun struct {
	SessionID        SessionID
	Harness          ReviewerHarness
	PRURL            string
	Status           ReviewRunStatus
	Verdict          ReviewVerdict
	ID               string
	CreatedAt        time.Time
	FailedAutoRuns   int
	AutoRetryBlocked bool
}

const AutoReviewFailedRetryLimit = 3

type ReviewerHandle struct {
	ReviewID  string
	SessionID SessionID
	Harness   ReviewerHarness
	HandleID  string
}
