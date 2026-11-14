package cdc

import (
	"encoding/json"
	"time"
)

type EventType string

const (
	EventSessionCreated         EventType = "session_created"
	EventSessionUpdated         EventType = "session_updated"
	EventPRCreated              EventType = "pr_created"
	EventPRUpdated              EventType = "pr_updated"
	EventPRCheckRecorded        EventType = "pr_check_recorded"
	EventPRSessionChanged       EventType = "pr_session_changed"
	EventPRReviewThreadAdded    EventType = "pr_review_thread_added"
	EventPRReviewThreadResolved EventType = "pr_review_thread_resolved"
	EventReviewRunCreated       EventType = "review_run_created"
	EventReviewRunUpdated       EventType = "review_run_updated"
)

type Event struct {
	Seq       int64           `json:"seq"`
	ProjectID string          `json:"projectId"`
	SessionID string          `json:"sessionId,omitempty"`
	Type      EventType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}
