// Package triage owns support investigations independently of the AO daemon.
package triage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ProtocolVersion identifies the private support-host wire contract.
const ProtocolVersion = 1

// Choice is an explicitly configured harness/model selection, not a public
// provider catalog. Credential values must never appear in this projection.
type Choice struct {
	Harness           string                `json:"harness"`
	Label             string                `json:"label,omitempty"`
	Models            []ModelChoice         `json:"models"`
	ModelsMissing     bool                  `json:"modelsMissing,omitempty"`
	Accounts          []Account             `json:"accounts,omitempty"`
	Available         bool                  `json:"available"`
	UnavailableReason string                `json:"unavailableReason,omitempty"`
	Permissions       domain.PermissionMode `json:"permissions,omitempty"`
	Mode              string                `json:"mode,omitempty"`
	SessionMode       domain.SessionMode    `json:"sessionMode,omitempty"`
}

// ModelChoice is a model explicitly present in the user's configuration.
type ModelChoice struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Efforts []string `json:"efforts,omitempty"`
	Default bool     `json:"default,omitempty"`
}

// Account references a configured login without carrying its credentials.
type Account struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default,omitempty"`
}

// Selection identifies the exact agent, model and account to launch.
type Selection struct {
	Harness     string                `json:"harness"`
	Model       string                `json:"model,omitempty"`
	Effort      string                `json:"effort,omitempty"`
	AccountID   string                `json:"accountId,omitempty"`
	Permissions domain.PermissionMode `json:"permissions,omitempty"`
	Mode        string                `json:"mode,omitempty"`
	SessionMode domain.SessionMode    `json:"sessionMode,omitempty"`
}

// Origin contains the context available at the initiating UI action.
type Origin struct {
	AppVersion   string          `json:"appVersion,omitempty"`
	BuildMode    string          `json:"buildMode,omitempty"`
	HostID       string          `json:"hostId,omitempty"`
	Surface      string          `json:"surface,omitempty"`
	SessionID    string          `json:"sessionId,omitempty"`
	DaemonState  string          `json:"daemonState,omitempty"`
	DaemonCode   string          `json:"daemonCode,omitempty"`
	DaemonOutput string          `json:"daemonOutput,omitempty"`
	Machine      json.RawMessage `json:"machine,omitempty"`
	Session      json.RawMessage `json:"session,omitempty"`
}

// StartRequest captures the initiating context and publication authorization.
type StartRequest struct {
	Selection
	Observation       string `json:"observation,omitempty"`
	Origin            Origin `json:"origin"`
	PublishAuthorized bool   `json:"publishAuthorized"`
	PreparationID     string `json:"preparationId,omitempty"`
}

// Command and Response use newline-delimited JSON over private process pipes.
type Command struct {
	ID         string                `json:"id"`
	Type       string                `json:"type"`
	RunID      string                `json:"runId,omitempty"`
	SessionID  string                `json:"sessionId,omitempty"`
	Start      *StartRequest         `json:"start,omitempty"`
	Choices    []Choice              `json:"choices,omitempty"`
	Origin     *Origin               `json:"origin,omitempty"`
	Text       string                `json:"text,omitempty"`
	RequestID  string                `json:"requestId,omitempty"`
	DecisionID string                `json:"decisionId,omitempty"`
	Action     ports.ChatInputAction `json:"action,omitempty"`
	Content    map[string]any        `json:"content,omitempty"`
	Rows       int                   `json:"rows,omitempty"`
	Cols       int                   `json:"cols,omitempty"`
}

// Response is a command result or an unsolicited run or terminal update.
type Response struct {
	Version  int            `json:"version"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type"`
	RunID    string         `json:"runId,omitempty"`
	Data     string         `json:"data,omitempty"`
	Sequence int64          `json:"sequence,omitempty"`
	Run      *Run           `json:"run,omitempty"`
	Runs     []Run          `json:"runs,omitempty"`
	Choices  []Choice       `json:"choices,omitempty"`
	Launch   *SessionLaunch `json:"launch,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// SessionLaunch is immutable desktop-main input for ordinary session retries.
type SessionLaunch struct {
	ClientRequestID string             `json:"clientRequestId"`
	Prompt          string             `json:"prompt"`
	Attachments     []LaunchAttachment `json:"attachments"`
}

// LaunchAttachment carries the frozen file bytes for ordinary session creation.
type LaunchAttachment struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// Message is a persisted, scrubbed conversation entry.
type Message struct {
	ID   string    `json:"id"`
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// PendingInput carries the selected driver's native approval or input request.
// Detail remains opaque so IDs, URLs and schemas reach the normal UI unchanged.
type PendingInput struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Summary string          `json:"summary"`
	Detail  json.RawMessage `json:"detail"`
}

// Evidence records a diagnostic fact, its source, time and availability.
type Evidence struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Source      string         `json:"source"`
	CollectedAt time.Time      `json:"collectedAt"`
	Status      string         `json:"status"`
	Text        string         `json:"text,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
}

// Run is the private saved investigation and its desktop projection.
type Run struct {
	ID                     string         `json:"id"`
	Version                int            `json:"version"`
	CreatedAt              time.Time      `json:"createdAt"`
	UpdatedAt              time.Time      `json:"updatedAt"`
	Request                StartRequest   `json:"request"`
	State                  string         `json:"state"`
	Activity               string         `json:"activity"`
	Messages               []Message      `json:"messages"`
	Evidence               []Evidence     `json:"evidence,omitempty"`
	PendingInput           *PendingInput  `json:"pendingInput,omitempty"`
	Error                  string         `json:"error,omitempty"`
	Preparation            bool           `json:"preparation,omitempty"`
	Execution              string         `json:"execution,omitempty"`
	SessionID              string         `json:"sessionId,omitempty"`
	SessionLaunchStarted   bool           `json:"sessionLaunchStarted,omitempty"`
	Launch                 *SessionLaunch `json:"launch,omitempty"`
	ProviderConversationID string         `json:"providerConversationId,omitempty"`
	ProviderTurnID         string         `json:"providerTurnId,omitempty"`
	Transport              string         `json:"transport,omitempty"`
	TerminalReplay         string         `json:"terminalReplay,omitempty"`
	TerminalSequence       int64          `json:"terminalSequence,omitempty"`
}

// Dependencies keep local collection and native agent I/O testable.
type Dependencies struct {
	Catalog             func(context.Context) ([]Choice, error)
	Configure           func(context.Context, []Choice) error
	Collect             func(context.Context, Origin) ([]Evidence, error)
	StartAgent          func(context.Context, Run) (AgentTransport, error)
	RecoverConversation func(context.Context, Run) error
	Now                 func() time.Time
}

// Options binds the private helper's data directory and dependencies.
type Options struct {
	DataDir string
	Deps    Dependencies
}
