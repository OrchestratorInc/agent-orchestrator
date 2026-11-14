package domain

import (
	"encoding/json"
	"time"
)

type SideConversation struct {
	PolicyVersion      int             `json:"-"`
	BoundaryTurnID     string          `json:"-"`
	BoundaryState      string          `json:"-"`
	HasWork            bool            `json:"hasWork"`
	ID                 string          `json:"id"`
	SessionID          SessionID       `json:"sessionId"`
	SourceProviderID   string          `json:"-"`
	MainConversationID string          `json:"mainConversationId"`
	AppRunID           string          `json:"-"`
	ForceNew           bool            `json:"-"`
	LaunchConfig       json.RawMessage `json:"-"`
	ManualLabel        bool            `json:"manualLabel,omitempty"`
	CreateKey          string          `json:"-"`
	ProviderHostID     string          `json:"-"`
	ProviderForkID     string          `json:"-"`
	AnchorTurnID       string          `json:"anchorTurnId,omitempty"`
	ContextMode        string          `json:"contextMode"`
	NativeAnchorID     string          `json:"-"`
	SourceMessageID    string          `json:"sourceMessageId,omitempty"`
	SourceRevision     int64           `json:"sourceRevision,omitempty"`
	SelectedText       string          `json:"selectedText,omitempty"`
	ReferencePending   bool            `json:"-"`
	ReferenceContext   string          `json:"-"`
	SeedHistory        string          `json:"-"`
	Harness            AgentHarness    `json:"-"`
	Model              string          `json:"model,omitempty"`
	Effort             string          `json:"effort,omitempty"`
	Generation         string          `json:"-"`
	Label              string          `json:"label"`
	State              string          `json:"state"`
	ErrorMessage       string          `json:"errorMessage,omitempty"`
	RecreateRequired   bool            `json:"recreateRequired,omitempty"`
	CleanupPending     bool            `json:"cleanupPending,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
	ClosedAt           *time.Time      `json:"-"`
}

type SideTurn struct {
	ID               string          `json:"id"`
	SideID           string          `json:"sideId"`
	ClientMessageID  string          `json:"clientMessageId"`
	Text             string          `json:"text"`
	Content          []SideContent   `json:"-"`
	References       []SideReference `json:"references,omitempty"`
	SelectionText    string          `json:"selectionText,omitempty"`
	ReferenceContext string          `json:"referenceContext,omitempty"`
	DispatchAttempt  int             `json:"-"`
	ProviderTurnID   string          `json:"-"`
	RetryOfTurnID    string          `json:"retryOfTurnId,omitempty"`
	State            string          `json:"state"`
	ErrorMessage     string          `json:"errorMessage,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	StartedAt        *time.Time      `json:"startedAt,omitempty"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
}

type SideReference struct {
	ID             string               `json:"id,omitempty"`
	Order          int64                `json:"order,omitempty"`
	Snapshot       *ChatExcerptSnapshot `json:"snapshot,omitempty"`
	ConversationID string               `json:"conversationId"`
	MessageID      string               `json:"messageId"`
	Revision       int64                `json:"revision"`
	Selection      string               `json:"selection"`
	Comment        string               `json:"comment,omitempty"`
	SourceRole     string               `json:"sourceRole"`
	Context        string               `json:"-"`
}

type SideContent struct {
	Type     string `json:"type"`
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`
	Text     string `json:"text,omitempty"`
}

type SideMessage struct {
	ID             string    `json:"id"`
	SideID         string    `json:"sideId"`
	TurnID         string    `json:"turnId"`
	ProviderItemID string    `json:"-"`
	Role           string    `json:"role"`
	Text           string    `json:"text"`
	Sequence       int64     `json:"sequence"`
	Revision       int64     `json:"revision"`
	Streaming      bool      `json:"streaming"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type SideSnapshot struct {
	Side       SideConversation `json:"side"`
	Turns      []SideTurn       `json:"turns"`
	Messages   []SideMessage    `json:"messages"`
	Activities []SideActivity   `json:"activities"`
	HasMore    bool             `json:"hasMore"`
}

type SideDecision struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Raw   []byte `json:"-"`
}

type SideActivity struct {
	ID             string          `json:"id"`
	SideID         string          `json:"sideId"`
	TurnID         string          `json:"turnId"`
	ProviderItemID string          `json:"-"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	Summary        string          `json:"summary"`
	Text           string          `json:"text,omitempty"`
	Detail         json.RawMessage `json:"detail,omitempty"`
	RequestID      string          `json:"requestId,omitempty"`
	Decisions      []SideDecision  `json:"decisions,omitempty"`
	Input          *SideInput      `json:"input,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
}

type SideInput struct {
	Mode    string         `json:"mode"`
	Message string         `json:"message"`
	URL     string         `json:"url,omitempty"`
	Schema  map[string]any `json:"schema,omitempty"`
}

type SideProviderCleanup struct {
	ForkID    string
	SessionID SessionID
	Harness   AgentHarness
}
