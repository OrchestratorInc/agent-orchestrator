// Package connectors keeps the MCP servers a user adds once for every agent.
package connectors

import "time"

// Transport is how a connector's server is reached.
type Transport string

// Auth is how a remote connector authenticates.
type Auth string

// Status is the last known state of a connector.
type Status string

// Connector transports, sign-in kinds and states.
const (
	TransportHTTP  Transport = "http"
	TransportStdio Transport = "stdio"

	AuthNone   Auth = "none"
	AuthOAuth  Auth = "oauth"
	AuthHeader Auth = "header"

	StatusUnchecked   Status = "unchecked"
	StatusConnected   Status = "connected"
	StatusNeedsSignIn Status = "needs_signin"
	StatusUnavailable Status = "unavailable"
)

// Connector is one MCP server, without its secret values.
type Connector struct {
	ID          string
	Key         string
	Name        string
	PresetID    string
	Transport   Transport
	URL         string
	Command     string
	Args        []string
	EnvKeys     []string
	HeaderKeys  []string
	Auth        Auth
	OwnClientID string
	SignedIn    bool
	ProjectID   string
	Enabled     bool
	Status      Status
	StatusStep  string
	StatusError string
	Tools       []string
	CheckedAt   time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Input is the editable definition of a connector.
type Input struct {
	Key       string
	Name      string
	PresetID  string
	Transport Transport
	URL       string
	Command   string
	Args      []string
	Env       map[string]string
	Headers   map[string]string
	Auth      Auth
	ProjectID string
	Enabled   *bool

	OAuthClientID     string
	OAuthClientSecret string
}
