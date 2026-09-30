package cli

import "time"

type managedAccountDTO struct {
	ID                 string    `json:"id"`
	Provider           string    `json:"provider"`
	Kind               string    `json:"kind"`
	Label              string    `json:"label,omitempty"`
	Email              string    `json:"email,omitempty"`
	Generation         uint64    `json:"generation"`
	ReconnectSupported bool      `json:"reconnectSupported"`
	Status             string    `json:"status"`
	Disabled           bool      `json:"disabled"`
	Unavailable        bool      `json:"unavailable"`
	LastRefreshedAt    time.Time `json:"lastRefreshedAt,omitempty"`
}

type managedLoginDTO struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"accountId,omitempty"`
	Provider    string    `json:"provider"`
	Mode        string    `json:"mode"`
	Status      string    `json:"status"`
	FailureCode string    `json:"failureCode,omitempty"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type managedLoginInstructions struct {
	managedLoginDTO
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	UserCode         string `json:"userCode,omitempty"`
}

type managedLoginCancellationAcknowledgement struct {
	OperationID                     string `json:"operationId"`
	CancellationRequestAcknowledged bool   `json:"cancellationRequestAcknowledged"`
}

type managedAccountsDTO struct {
	Revision      int64               `json:"revision"`
	Availability  string              `json:"availability"`
	Stale         bool                `json:"stale"`
	Accounts      []managedAccountDTO `json:"accounts"`
	OAuthSessions []managedLoginDTO   `json:"oauthSessions"`
}

type managedRemovalSessionDTO struct {
	SessionID       string `json:"sessionId"`
	Provider        string `json:"provider"`
	BindingRevision int64  `json:"bindingRevision"`
	Stopped         bool   `json:"stopped"`
}

type managedRemovalImpactDTO struct {
	AccountID string                     `json:"accountId"`
	Revision  int64                      `json:"revision"`
	Sessions  []managedRemovalSessionDTO `json:"sessions"`
}

type managedRemovalDTO struct {
	ID               string                  `json:"id"`
	AccountID        string                  `json:"accountId"`
	Impact           managedRemovalImpactDTO `json:"impact"`
	Phase            string                  `json:"phase"`
	ErrorCode        string                  `json:"errorCode,omitempty"`
	CanCancel        bool                    `json:"canCancel"`
	RecoveryRequired bool                    `json:"recoveryRequired"`
	CreatedAt        time.Time               `json:"createdAt"`
	UpdatedAt        time.Time               `json:"updatedAt"`
}
