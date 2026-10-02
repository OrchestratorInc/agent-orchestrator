package controllers

import "time"

// AccountsManagerSwitchRequest requires an explicit target, timing and observed revision.
type AccountsManagerSwitchRequest struct {
	OperationID      string `json:"operationId" minLength:"1" maxLength:"128"`
	ExpectedRevision int64  `json:"expectedRevision" minimum:"1" maximum:"9007199254740991"`
	Mode             string `json:"mode" enum:"native,managed"`
	AccountID        string `json:"accountId,omitempty" maxLength:"128"`
	Policy           string `json:"policy" enum:"drain,interrupt"`
	NewConversation  bool   `json:"newConversation,omitempty"`
}

// AccountsManagerRemovalRequest binds confirmation to a preview, including revision zero.
type AccountsManagerRemovalRequest struct {
	OperationID      string `json:"operationId" minLength:"1" maxLength:"128"`
	ExpectedRevision int64  `json:"expectedRevision" minimum:"0" maximum:"9007199254740991"`
	Confirmed        bool   `json:"confirmed"`
}

// AccountsManagerControlOperationIDParam scopes retries to an immutable operation.
type AccountsManagerControlOperationIDParam struct {
	OperationID string `path:"operationId" minLength:"1" maxLength:"128"`
}

// AccountsManagerSessionResponse separates the committed binding from a pending switch.
type AccountsManagerSessionResponse struct {
	SessionID string                         `json:"sessionId"`
	Provider  string                         `json:"provider"`
	Mode      string                         `json:"mode" enum:"native,managed"`
	AccountID string                         `json:"accountId,omitempty"`
	Revision  int64                          `json:"revision"`
	Blocked   bool                           `json:"blocked"`
	Switch    *AccountsManagerSwitchResponse `json:"switch,omitempty"`
}

// AccountsManagerSwitchResponse excludes runtime identity and native history.
type AccountsManagerSwitchResponse struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"sessionId"`
	Provider         string    `json:"provider"`
	SourceMode       string    `json:"sourceMode"`
	SourceAccountID  string    `json:"sourceAccountId,omitempty"`
	SourceRevision   int64     `json:"sourceRevision"`
	TargetMode       string    `json:"targetMode"`
	TargetAccountID  string    `json:"targetAccountId,omitempty"`
	TargetRevision   int64     `json:"targetRevision"`
	Policy           string    `json:"policy"`
	NewConversation  bool      `json:"newConversation"`
	Phase            string    `json:"phase"`
	ErrorCode        string    `json:"errorCode,omitempty" enum:"ADMISSION_CHANGED,SOURCE_CHANGED,NATIVE_HISTORY_UNAVAILABLE,SOURCE_INTAKE_UNAVAILABLE,SOURCE_OWNERSHIP_UNCONFIRMED,SOURCE_NOT_QUIESCENT,TARGET_UNAVAILABLE,TARGET_REVALIDATION_UNAVAILABLE,REVOCATION_UNCONFIRMED,SOURCE_STOP_UNCONFIRMED,STOP_RECORD_UNCONFIRMED,BINDING_COMMIT_UNCONFIRMED,BINDING_SYNC_UNCONFIRMED,START_RECORD_UNCONFIRMED,SESSION_UNAVAILABLE,PROJECT_UNAVAILABLE,TARGET_START_UNCONFIRMED,TARGET_NOT_READY,OUTCOME_UNCONFIRMED,DAEMON_RESTARTED,CONTROLLER_CHANGED,TARGET_STOP_UNCONFIRMED,RETRY_COMMIT_UNCONFIRMED,SESSION_INTAKE_UNAVAILABLE,STOP_ADMISSION_CHANGED,REVOCATION_RECORD_UNCONFIRMED,FINAL_ADMISSION_CHANGED,CREDENTIAL_REMOVAL_UNCONFIRMED" doc:"Safe durable diagnostic; absent on success, cancellation, unknown codes and older servers."`
	RecoveryRequired bool      `json:"recoveryRequired"`
	CanRetry         bool      `json:"canRetry"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// AccountsManagerRemovalSessionResponse preserves dormant bindings without runtime handles.
type AccountsManagerRemovalSessionResponse struct {
	SessionID       string `json:"sessionId"`
	Provider        string `json:"provider"`
	BindingRevision int64  `json:"bindingRevision"`
	Stopped         bool   `json:"stopped"`
}

// AccountsManagerRemovalImpactResponse ties confirmation to the complete observed impact.
type AccountsManagerRemovalImpactResponse struct {
	AccountID string                                  `json:"accountId"`
	Revision  int64                                   `json:"revision"`
	Sessions  []AccountsManagerRemovalSessionResponse `json:"sessions"`
}

// AccountsManagerRemovalResponse distinguishes accepted intent from completed deletion.
type AccountsManagerRemovalResponse struct {
	ID               string                               `json:"id"`
	AccountID        string                               `json:"accountId"`
	Impact           AccountsManagerRemovalImpactResponse `json:"impact"`
	Phase            string                               `json:"phase"`
	ErrorCode        string                               `json:"errorCode,omitempty" enum:"ADMISSION_CHANGED,SOURCE_CHANGED,NATIVE_HISTORY_UNAVAILABLE,SOURCE_INTAKE_UNAVAILABLE,SOURCE_OWNERSHIP_UNCONFIRMED,SOURCE_NOT_QUIESCENT,TARGET_UNAVAILABLE,TARGET_REVALIDATION_UNAVAILABLE,REVOCATION_UNCONFIRMED,SOURCE_STOP_UNCONFIRMED,STOP_RECORD_UNCONFIRMED,BINDING_COMMIT_UNCONFIRMED,BINDING_SYNC_UNCONFIRMED,START_RECORD_UNCONFIRMED,SESSION_UNAVAILABLE,PROJECT_UNAVAILABLE,TARGET_START_UNCONFIRMED,TARGET_NOT_READY,OUTCOME_UNCONFIRMED,DAEMON_RESTARTED,CONTROLLER_CHANGED,TARGET_STOP_UNCONFIRMED,RETRY_COMMIT_UNCONFIRMED,SESSION_INTAKE_UNAVAILABLE,STOP_ADMISSION_CHANGED,REVOCATION_RECORD_UNCONFIRMED,FINAL_ADMISSION_CHANGED,CREDENTIAL_REMOVAL_UNCONFIRMED" doc:"Safe durable diagnostic; absent on success, cancellation, unknown codes and older servers."`
	CanCancel        bool                                 `json:"canCancel"`
	RecoveryRequired bool                                 `json:"recoveryRequired"`
	CreatedAt        time.Time                            `json:"createdAt"`
	UpdatedAt        time.Time                            `json:"updatedAt"`
}
