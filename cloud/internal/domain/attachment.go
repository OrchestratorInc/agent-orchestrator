package domain

import "github.com/aoagents/agent-orchestrator/cloud/internal/attachments"

type Attachment struct {
	attachments.Metadata
	OrgID     string `json:"-"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId,omitempty"`
	CreatorID string `json:"-"`
}
type PrepareAttachment struct {
	attachments.Metadata
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId,omitempty"`
}
