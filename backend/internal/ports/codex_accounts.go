package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	ErrCodexOAuthTokenRevoked           = errors.New("codex oauth token revoked")
	ErrCodexCapacityRequestRejected     = errors.New("codex capacity request rejected")
	ErrCodexCapacityProviderUnavailable = errors.New("codex capacity provider unavailable")
	ErrCodexAccountLogoutUnsupported    = errors.New("codex account logout unsupported")
)

type CodexAccountContext struct {
	Home    string
	Managed bool
}

type CodexAccountObservation struct {
	Authentication domain.AgentAuthenticationState
	Method         domain.CodexAuthMethod
	Email          *string
}

type CodexCapacityObservation struct {
	Plan               *string
	Overall            *domain.CodexCapacityBucket
	AdditionalBuckets  []domain.CodexCapacityBucket
	ResetCredits       *domain.CodexResetCreditsSummary
	ObservedAt         time.Time
	Partial            bool
	AutoResumeResetsAt *time.Time `json:"-"`
}

type CodexUsageObservation struct {
	LatestDayTokens           *int64
	LatestDayStartDate        *string
	LifetimeTokens            *int64
	PeakDailyTokens           *int64
	LongestRunningTurnSeconds *int64
	CurrentStreakDays         *int64
	LongestStreakDays         *int64
	ObservedAt                time.Time
}

type CodexAccountEventKind string

const (
	CodexAccountEventUpdated         CodexAccountEventKind = "account_updated"
	CodexAccountEventCapacityUpdated CodexAccountEventKind = "capacity_updated"
)

type CodexAccountEvent struct {
	Kind     CodexAccountEventKind
	Success  bool
	Failed   bool
	Capacity *CodexCapacityObservation
}

type CodexAccountClient interface {
	Read(ctx context.Context, refreshToken bool) (CodexAccountObservation, error)
	Logout(ctx context.Context) error
	ReadCapacity(ctx context.Context) (CodexCapacityObservation, error)
	ReadUsage(ctx context.Context) (CodexUsageObservation, error)
	ConsumeResetCredit(ctx context.Context, idempotencyKey string) (domain.CodexResetCreditOutcome, error)
	Events() <-chan CodexAccountEvent
	Close() error
}

type CodexAccountClientFactory interface {
	Open(ctx context.Context, account CodexAccountContext) (CodexAccountClient, error)
	Capabilities(ctx context.Context) domain.CodexAccountCapabilities
}
