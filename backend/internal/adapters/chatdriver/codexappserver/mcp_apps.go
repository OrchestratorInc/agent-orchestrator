package codexappserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatMCPApps = (*conversation)(nil)

func (c *conversation) MCPAppTools(ctx context.Context, thread, server string) ([]json.RawMessage, error) {
	var cursor *string
	seen := map[string]bool{}
	for {
		var out codexproto.ListMcpServerStatusResponse
		detail := codexproto.McpServerStatusDetailToolsAndAuthOnly
		if err := c.conn.request(ctx, codexproto.MethodMcpServerStatusList, codexproto.ListMcpServerStatusParams{ThreadID: &thread, Cursor: cursor, Detail: &detail}, &out); err != nil {
			return nil, err
		}
		for _, entry := range out.Data {
			if entry.Name != server {
				continue
			}
			tools := make([]json.RawMessage, 0, len(entry.Tools))
			for _, tool := range entry.Tools {
				data, err := json.Marshal(tool)
				if err != nil {
					return nil, err
				}
				tools = append(tools, data)
			}
			return tools, nil
		}
		if out.NextCursor == nil || *out.NextCursor == "" {
			return nil, nil
		}
		if seen[*out.NextCursor] || len(seen) >= 100 {
			return nil, errors.New("MCP server catalog did not finish")
		}
		seen[*out.NextCursor] = true
		cursor = out.NextCursor
	}
}

func (c *conversation) MCPAppResource(ctx context.Context, thread, server, uri string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.conn.request(ctx, codexproto.MethodMcpServerResourceRead, codexproto.McpResourceReadParams{ThreadID: &thread, Server: server, URI: uri}, &out)
	return out, err
}

func (c *conversation) MCPAppCall(ctx context.Context, thread, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.conn.request(ctx, codexproto.MethodMcpServerToolCall, codexproto.McpServerToolCallParams{ThreadID: thread, Server: server, Tool: tool, Arguments: args}, &out)
	return out, err
}
