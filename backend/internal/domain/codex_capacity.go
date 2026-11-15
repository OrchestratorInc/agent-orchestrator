package domain

import "time"

type CodexCapacityState string

const (
	CodexCapacityAvailable   CodexCapacityState = "available"
	CodexCapacityNearLimit   CodexCapacityState = "near_limit"
	CodexCapacityExhausted   CodexCapacityState = "exhausted"
	CodexCapacityUnknown     CodexCapacityState = "unknown"
	CodexCapacityUnsupported CodexCapacityState = "unsupported"
)

type CodexCapacityReachedState string

const (
	CodexCapacityNotReached   CodexCapacityReachedState = "not_reached"
	CodexCapacityReached      CodexCapacityReachedState = "reached"
	CodexCapacityReachUnknown CodexCapacityReachedState = "unknown"
)

type CodexCapacityWindow struct {
	UsedPercent           float64    `json:"usedPercent" minimum:"0" maximum:"100"`
	WindowDurationMinutes *int64     `json:"windowDurationMinutes,omitempty"`
	ResetsAt              *time.Time `json:"resetsAt,omitempty"`
}

type CodexCapacityBucket struct {
	LimitID     string                    `json:"limitId"`
	DisplayName *string                   `json:"displayName,omitempty"`
	Primary     *CodexCapacityWindow      `json:"primary,omitempty"`
	Secondary   *CodexCapacityWindow      `json:"secondary,omitempty"`
	Reached     CodexCapacityReachedState `json:"reached" enum:"not_reached,reached,unknown"`
}

type CodexResetCreditsSummary struct {
	AvailableCount   int64      `json:"availableCount" minimum:"0"`
	NearestExpiresAt *time.Time `json:"nearestExpiresAt,omitempty"`
}

type CodexCapacitySnapshot struct {
	State             CodexCapacityState        `json:"state" enum:"available,near_limit,exhausted,unknown,unsupported"`
	Freshness         AgentReadinessFreshness   `json:"freshness" enum:"fresh,stale,checking"`
	Plan              *string                   `json:"plan,omitempty"`
	UsedPercent       *float64                  `json:"usedPercent,omitempty" minimum:"0" maximum:"100"`
	RemainingPercent  *float64                  `json:"remainingPercent,omitempty" minimum:"0" maximum:"100"`
	ResetsAt          *time.Time                `json:"resetsAt,omitempty"`
	ObservedAt        *time.Time                `json:"observedAt,omitempty"`
	CheckedAt         *time.Time                `json:"checkedAt,omitempty"`
	AttemptedAt       *time.Time                `json:"attemptedAt,omitempty"`
	ReasonCode        string                    `json:"reasonCode"`
	Reason            string                    `json:"reason"`
	Overall           *CodexCapacityBucket      `json:"overall,omitempty"`
	AdditionalBuckets []CodexCapacityBucket     `json:"additionalBuckets"`
	ResetCredits      *CodexResetCreditsSummary `json:"resetCredits,omitempty"`
}

type CodexCapacitySummary struct {
	State            CodexCapacityState      `json:"state" enum:"available,near_limit,exhausted,unknown,unsupported"`
	Freshness        AgentReadinessFreshness `json:"freshness" enum:"fresh,stale,checking"`
	Plan             *string                 `json:"plan,omitempty"`
	UsedPercent      *float64                `json:"usedPercent,omitempty" minimum:"0" maximum:"100"`
	RemainingPercent *float64                `json:"remainingPercent,omitempty" minimum:"0" maximum:"100"`
	ResetsAt         *time.Time              `json:"resetsAt,omitempty"`
	ObservedAt       *time.Time              `json:"observedAt,omitempty"`
	ReasonCode       string                  `json:"reasonCode"`
	Reason           string                  `json:"reason"`
}

const (
	CodexCapacityReasonNotChecked          = "capacity_not_checked"
	CodexCapacityReasonChecking            = "capacity_checking"
	CodexCapacityReasonAvailable           = "capacity_available"
	CodexCapacityReasonNearLimit           = "capacity_near_limit"
	CodexCapacityReasonExhausted           = "capacity_exhausted"
	CodexCapacityReasonUnsupported         = "capacity_unsupported"
	CodexCapacityReasonSkippedSignedOut    = "capacity_skipped_signed_out"
	CodexCapacityReasonSkippedAuthUnknown  = "capacity_skipped_auth_unknown"
	CodexCapacityReasonAccountUnavailable  = "capacity_account_unavailable"
	CodexCapacityReasonInvalidated         = "capacity_invalidated"
	CodexCapacityReasonCheckInconclusive   = "capacity_check_inconclusive"
	CodexCapacityReasonCheckTimeout        = "capacity_check_timeout"
	CodexCapacityReasonCheckFailed         = "capacity_check_failed"
	CodexCapacityReasonClientStartFailed   = "capacity_client_start_failed"
	CodexCapacityReasonProviderRejected    = "capacity_provider_rejected"
	CodexCapacityReasonProviderUnavailable = "capacity_provider_unavailable"
	CodexCapacityReasonCheckStopped        = "capacity_check_stopped"
)

func CompactCodexCapacity(snapshot CodexCapacitySnapshot) CodexCapacitySummary {
	return CodexCapacitySummary{
		State: snapshot.State, Freshness: snapshot.Freshness, Plan: snapshot.Plan,
		UsedPercent: snapshot.UsedPercent, RemainingPercent: snapshot.RemainingPercent, ResetsAt: snapshot.ResetsAt,
		ObservedAt: snapshot.ObservedAt, ReasonCode: snapshot.ReasonCode, Reason: snapshot.Reason,
	}
}
