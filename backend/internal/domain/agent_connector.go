package domain

import "time"

// AgentConnectorStatus is how an agent's CLI last found one connector.
type AgentConnectorStatus string

const (
	// AgentConnectorConnected means the connector answered and its tools load.
	AgentConnectorConnected AgentConnectorStatus = "connected"
	// AgentConnectorNeedsAuth means the connector is added but its own sign-in
	// (for example Google's) is missing or expired, on the provider's site.
	AgentConnectorNeedsAuth AgentConnectorStatus = "needs_auth"
	// AgentConnectorFailed means the connector could not be reached.
	AgentConnectorFailed AgentConnectorStatus = "failed"
	// AgentConnectorUnknown means the CLI reported a state AO does not recognize.
	AgentConnectorUnknown AgentConnectorStatus = "unknown"
)

// AgentConnector is one connector an account's own provider account carries,
// such as a claude.ai connector (Gmail, Google Calendar, Google Drive).
type AgentConnector struct {
	Name   string
	Status AgentConnectorStatus
}

// AgentConnectorsUnavailable says why an account cannot load connectors at
// all, so no list is read for it.
type AgentConnectorsUnavailable string

const (
	// AgentConnectorsSetupToken means a `claude setup-token` token signs the
	// account in; it is limited to inference and cannot load claude.ai
	// connectors (#9101).
	AgentConnectorsSetupToken AgentConnectorsUnavailable = "setup_token"
	// AgentConnectorsAPIKey means an API key signs the account in, which has no
	// provider account behind it.
	AgentConnectorsAPIKey AgentConnectorsUnavailable = "api_key"
	// AgentConnectorsUnsupported means the account's provider has no connectors.
	AgentConnectorsUnsupported AgentConnectorsUnavailable = "unsupported"
)

// AgentConnectorsReading is the last connector list read for one account.
// Unavailable set means no list was read; Error set means the read failed.
type AgentConnectorsReading struct {
	AccountID   string
	Unavailable AgentConnectorsUnavailable
	Connectors  []AgentConnector
	Error       string
	CheckedAt   time.Time
}
