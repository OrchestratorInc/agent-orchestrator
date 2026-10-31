package domain

import "encoding/json"

// MCPAppData is the originating tool call, kept separately from the bounded
// timeline summary. Thread identities are provider facts, never client input.
type MCPAppData struct {
	Server       string          `json:"server"`
	Tool         string          `json:"tool"`
	ResourceURI  string          `json:"resourceUri"`
	ThreadID     string          `json:"threadId"`
	RootThreadID string          `json:"rootThreadId"`
	Arguments    json.RawMessage `json:"arguments"`
	Result       json.RawMessage `json:"result,omitempty"`
}

// MCPAppRecord retains an app after its provider stops. Context is the latest
// app-supplied note for the next turn; it is not a second model transcript.
type MCPAppRecord struct {
	ConversationID string
	ActivityID     string
	Data           MCPAppData
	Document       json.RawMessage
	Context        string
}
