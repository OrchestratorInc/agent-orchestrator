package domain

import "time"

type CodexAccountSwitchSourceKind string

const (
	CodexAccountSwitchSourceManaged CodexAccountSwitchSourceKind = "managed"
	CodexAccountSwitchSourceDevice  CodexAccountSwitchSourceKind = "device"
	CodexAccountSwitchSourceNone    CodexAccountSwitchSourceKind = "none"
)

type CodexAccountSwitchSource struct {
	Kind      CodexAccountSwitchSourceKind
	AccountID string
}

type CodexAccountSwitchInstallationState string

const (
	CodexAccountSwitchTargetInstalled    CodexAccountSwitchInstallationState = "target"
	CodexAccountSwitchSourceInstalled    CodexAccountSwitchInstallationState = "source"
	CodexAccountSwitchCredentialMissing  CodexAccountSwitchInstallationState = "missing"
	CodexAccountSwitchExternalCredential CodexAccountSwitchInstallationState = "external"
)

type CodexAccountSwitchPhase string

const (
	CodexAccountSwitchRequested            CodexAccountSwitchPhase = "requested"
	CodexAccountSwitchCheckpointCredential CodexAccountSwitchPhase = "checkpointing_source"
	CodexAccountSwitchActivatingAccount    CodexAccountSwitchPhase = "activating_target"
	CodexAccountSwitchRecoveryRequired     CodexAccountSwitchPhase = "recovery_required"
	CodexAccountSwitchCompleted            CodexAccountSwitchPhase = "completed"
	CodexAccountSwitchFailed               CodexAccountSwitchPhase = "failed"
)

func (p CodexAccountSwitchPhase) Terminal() bool {
	return p == CodexAccountSwitchCompleted || p == CodexAccountSwitchFailed
}

type CodexAccountSwitch struct {
	ID                     string                       `json:"id"`
	SourceKind             CodexAccountSwitchSourceKind `json:"sourceKind" enum:"managed,device,none"`
	SourceAccountID        string                       `json:"sourceAccountId,omitempty"`
	TargetAccountID        string                       `json:"targetAccountId"`
	Phase                  CodexAccountSwitchPhase      `json:"phase"`
	FailureCode            string                       `json:"failureCode,omitempty"`
	CredentialsCommittedAt *time.Time                   `json:"credentialsCommittedAt,omitempty"`
	CreatedAt              time.Time                    `json:"createdAt"`
	UpdatedAt              time.Time                    `json:"updatedAt"`
	CompletedAt            *time.Time                   `json:"completedAt,omitempty"`
	IdempotencyKey         string                       `json:"-"`
}
