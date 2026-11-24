package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// maxMCPImageBytes bounds one screenshot sent inline.
const maxMCPImageBytes = 10 << 20

// cuaCallRequest, cuaImage, and cuaCallResponse mirror the daemon's
// computer-use call DTOs.
type cuaCallRequest struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

type cuaImage struct {
	Path     string `json:"path"`
	MimeType string `json:"mimeType"`
}

type cuaCallResponse struct {
	Tool   string     `json:"tool"`
	Result any        `json:"result"`
	Images []cuaImage `json:"images"`
}

type cuaAgentTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// readOnlyCuaTools look at the screen without acting on it.
var readOnlyCuaTools = map[string]bool{"list_windows": true, "get_window_state": true}

// loadCuaTools adds the computer-use tools this server offers: none unless
// the session has computer use, and none while the daemon cannot list them
// (Cua Driver not installed); the list is asked for again on the next
// tools/list or tools/call.
func (s *server) loadCuaTools(ctx context.Context) {
	if os.Getenv(config.EnvComputerUse) != "1" {
		return
	}
	s.cuaMu.Lock()
	defer s.cuaMu.Unlock()
	if s.cuaLoaded {
		return
	}
	var response struct {
		Tools []cuaAgentTool `json:"tools"`
	}
	if err := s.daemon.GetJSON(ctx, "computer-use/agent-tools", &response); err != nil || len(response.Tools) == 0 {
		return
	}
	s.cuaLoaded = true
	for _, tool := range response.Tools {
		readOnly := readOnlyCuaTools[tool.Name]
		s.mcp.AddTool(&mcp.Tool{
			Name:        tool.Name,
			Description: "Computer use (Cua Driver). " + tool.Description,
			InputSchema: cuaInputSchema(tool.InputSchema),
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: readOnly, DestructiveHint: boolPtr(!readOnly),
				IdempotentHint: readOnly, OpenWorldHint: boolPtr(true),
			},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.callCua(ctx, req.Params.Name, req.Params.Arguments), nil
		})
	}
}

// cuaInputSchema returns the driver's schema with an object root, since MCP
// requires every tool to declare one: a schema without a root type keeps its
// properties under type "object", and a missing or non-object schema becomes
// an open object.
func cuaInputSchema(schema map[string]any) map[string]any {
	rootType, hasType := schema["type"]
	if schema == nil || (hasType && rootType != "object") {
		return map[string]any{"type": "object"}
	}
	if hasType {
		return schema
	}
	withType := maps.Clone(schema)
	withType["type"] = "object"
	return withType
}

// callCua runs one Cua Driver tool through the daemon and returns its result
// as text (the window line, the tree or the diff, and the stale snapshots)
// with each screenshot as an image.
func (s *server) callCua(ctx context.Context, tool string, raw json.RawMessage) *mcp.CallToolResult {
	var arguments map[string]any
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return mcpToolError(fmt.Errorf("invalid arguments: %w", err))
		}
	}
	request := cuaCallRequest{Tool: tool, Arguments: arguments, SessionID: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
	var response cuaCallResponse
	if err := s.daemon.PostJSON(ctx, "computer-use/call", request, &response); err != nil {
		return mcpToolError(err)
	}
	text, ok := renderCuaCall(response)
	if !ok {
		encoded, err := json.MarshalIndent(response.Result, "", "  ")
		if err != nil {
			return mcpToolError(err)
		}
		text = string(encoded)
	}
	content := []mcp.Content{mcpText(strings.TrimRight(text, "\n"))}
	for _, image := range response.Images {
		block, err := mcpImage(image)
		if err != nil {
			content = append(content, mcpText(fmt.Sprintf("screenshot %s could not be attached: %v", image.Path, err)))
			continue
		}
		content = append(content, block)
	}
	return &mcp.CallToolResult{Content: content}
}

func mcpImage(image cuaImage) (*mcp.ImageContent, error) {
	info, err := os.Stat(image.Path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxMCPImageBytes {
		return nil, fmt.Errorf("%d bytes is over the %d-byte limit", info.Size(), maxMCPImageBytes)
	}
	data, err := os.ReadFile(image.Path)
	if err != nil {
		return nil, err
	}
	mimeType := image.MimeType
	if mimeType == "" {
		mimeType = "image/png"
	}
	return &mcp.ImageContent{MIMEType: mimeType, Data: data}, nil
}
