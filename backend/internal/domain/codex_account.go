package domain

import "time"

type CodexAccountSource string

const CodexAccountSourceManaged CodexAccountSource = "managed"

type CodexAccountStatus string

const (
	CodexAccountStatusValid     CodexAccountStatus = "valid"
	CodexAccountStatusSignedOut CodexAccountStatus = "signed_out"
	CodexAccountStatusBroken    CodexAccountStatus = "broken"
)

type CodexDeviceReconciliationStatus string

const (
	CodexDeviceReconciliationNotChecked             CodexDeviceReconciliationStatus = "not_checked"
	CodexDeviceReconciliationChecking               CodexDeviceReconciliationStatus = "checking"
	CodexDeviceReconciliationVerified               CodexDeviceReconciliationStatus = "verified"
	CodexDeviceReconciliationTemporarilyUnavailable CodexDeviceReconciliationStatus = "temporarily_unavailable"
	CodexDeviceReconciliationBlocked                CodexDeviceReconciliationStatus = "blocked"
)

type CodexDeviceReconciliation struct {
	Status                CodexDeviceReconciliationStatus `json:"status" enum:"not_checked,checking,verified,temporarily_unavailable,blocked"`
	ActiveAccountVerified bool                            `json:"activeAccountVerified"`
	ReasonCode            string                          `json:"reasonCode"`
	Retryable             bool                            `json:"retryable"`
	AttemptedAt           *time.Time                      `json:"attemptedAt,omitempty"`
	VerifiedAt            *time.Time                      `json:"verifiedAt,omitempty"`
	NextRetryAt           *time.Time                      `json:"nextRetryAt,omitempty"`
}

type CodexAuthMethod string

const (
	CodexAuthMethodChatGPT CodexAuthMethod = "chatgpt"
	CodexAuthMethodAPIKey  CodexAuthMethod = "api_key"
	CodexAuthMethodOther   CodexAuthMethod = "other"
	CodexAuthMethodUnknown CodexAuthMethod = "unknown"
)

type CodexCapabilityState string

const (
	CodexCapabilitySupported   CodexCapabilityState = "supported"
	CodexCapabilityUnsupported CodexCapabilityState = "unsupported"
	CodexCapabilityUnknown     CodexCapabilityState = "unknown"
)

type CodexCapabilityObservation struct {
	State      CodexCapabilityState `json:"state" enum:"supported,unsupported,unknown"`
	ReasonCode string               `json:"reasonCode"`
	Reason     string               `json:"reason"`
}

type CodexAccountCapabilities struct {
	AccountRead        CodexCapabilityObservation `json:"accountRead"`
	NativeLogin        CodexCapabilityObservation `json:"nativeLogin"`
	CapacityRead       CodexCapabilityObservation `json:"capacityRead"`
	UsageRead          CodexCapabilityObservation `json:"usageRead"`
	ResetCreditConsume CodexCapabilityObservation `json:"resetCreditConsume"`
	GlobalSwitch       CodexCapabilityObservation `json:"globalSwitch"`
}

type CodexAccountSnapshot struct {
	ID             string                         `json:"id"`
	Label          string                         `json:"label"`
	Source         CodexAccountSource             `json:"source" enum:"managed"`
	Status         CodexAccountStatus             `json:"status" enum:"valid,signed_out,broken"`
	ReasonCode     string                         `json:"reasonCode"`
	Reason         string                         `json:"reason"`
	Active         bool                           `json:"active"`
	Authentication AgentAuthenticationObservation `json:"authentication"`
	AuthMethod     CodexAuthMethod                `json:"authMethod" enum:"chatgpt,api_key,other,unknown"`
	AccountEmail   *string                        `json:"accountEmail,omitempty"`
	Capacity       CodexCapacitySnapshot          `json:"capacity"`
	UsageSummary   *CodexAccountUsageSummary      `json:"usageSummary,omitempty"`
	CreatedAt      time.Time                      `json:"createdAt"`
}

type CodexAccountUsageSummary struct {
	LatestDayTokens           *int64    `json:"latestDayTokens,omitempty"`
	LatestDayStartDate        *string   `json:"latestDayStartDate,omitempty"`
	LifetimeTokens            *int64    `json:"lifetimeTokens,omitempty"`
	PeakDailyTokens           *int64    `json:"peakDailyTokens,omitempty"`
	LongestRunningTurnSeconds *int64    `json:"longestRunningTurnSeconds,omitempty"`
	CurrentStreakDays         *int64    `json:"currentStreakDays,omitempty"`
	LongestStreakDays         *int64    `json:"longestStreakDays,omitempty"`
	ObservedAt                time.Time `json:"observedAt"`
}

type CodexResetCreditOutcome string

const (
	CodexResetCreditReset           CodexResetCreditOutcome = "reset"
	CodexResetCreditAlreadyRedeemed CodexResetCreditOutcome = "already_redeemed"
	CodexResetCreditNothingToReset  CodexResetCreditOutcome = "nothing_to_reset"
	CodexResetCreditNoCredit        CodexResetCreditOutcome = "no_credit"
)

const (
	CodexAccountReasonValid             = "account_valid"
	CodexAccountReasonSignedOut         = "account_signed_out"
	CodexAccountReasonDescriptorInvalid = "account_descriptor_invalid"
	CodexAccountReasonHomeMissing       = "account_credential_home_missing"
	CodexAccountReasonUnsafePath        = "account_unsafe_path"
	CodexCapabilityReasonSupported      = "supported"
	CodexCapabilityReasonUnsupported    = "unsupported"
	CodexCapabilityReasonUnknown        = "unknown"
)

type CodexAccountLoginStatus string

const (
	CodexAccountLoginPending      CodexAccountLoginStatus = "pending"
	CodexAccountLoginVerifying    CodexAccountLoginStatus = "verifying"
	CodexAccountLoginUnauthorized CodexAccountLoginStatus = "unauthorized"
	CodexAccountLoginRetryable    CodexAccountLoginStatus = "retryable"
	CodexAccountLoginCompleted    CodexAccountLoginStatus = "completed"
	CodexAccountLoginCancelled    CodexAccountLoginStatus = "cancelled"
	CodexAccountLoginFailed       CodexAccountLoginStatus = "failed"
	CodexAccountLoginExpired      CodexAccountLoginStatus = "expired"
)

const (
	CodexAccountLoginReasonPending      = "login_pending"
	CodexAccountLoginReasonCompleted    = "login_completed"
	CodexAccountLoginReasonCancelled    = "login_cancelled"
	CodexAccountLoginReasonFailed       = "login_failed"
	CodexAccountLoginReasonUnauthorized = "login_unauthorized"
	CodexAccountLoginReasonExpired      = "login_expired"
)

type CodexAccountLoginOperation struct {
	OperationID string                  `json:"operationId"`
	AccountID   string                  `json:"accountId,omitempty"`
	Status      CodexAccountLoginStatus `json:"status" enum:"pending,verifying,unauthorized,retryable,completed,cancelled,failed,expired"`
	ReasonCode  string                  `json:"reasonCode"`
	Reason      string                  `json:"reason"`
	Account     *CodexAccountSnapshot   `json:"account,omitempty"`
	ExpiresAt   time.Time               `json:"expiresAt"`
}
