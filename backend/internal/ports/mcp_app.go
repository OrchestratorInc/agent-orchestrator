package ports

import (
	"context"
	"encoding/json"
)

// ChatMCPApps bridges only the server and native thread proven by a recorded
// tool call. Drivers retain their existing credentials and transport.
type ChatMCPApps interface {
	MCPAppTools(context.Context, string, string) ([]json.RawMessage, error)
	MCPAppResource(context.Context, string, string, string) (json.RawMessage, error)
	MCPAppCall(context.Context, string, string, string, json.RawMessage) (json.RawMessage, error)
}
