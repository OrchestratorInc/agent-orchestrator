package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// maxMCPImageBytes bounds one screenshot sent inline.
const maxMCPImageBytes = 10 << 20

// mcpState is one `ao mcp` server's memory between messages.
type mcpState struct {
	cuaLoaded bool
	cuaTools  []map[string]any
	cuaNames  map[string]bool
}

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

// cuaTools are the computer-use tools this server offers: none unless the
// session has computer use, and none while the daemon cannot list them (Cua
// Driver not installed); the list is asked for again on the next tools/list.
func (c *commandContext) cuaTools(ctx context.Context, state *mcpState) []map[string]any {
	if os.Getenv(config.EnvComputerUse) != "1" {
		return nil
	}
	if state.cuaLoaded {
		return state.cuaTools
	}
	var response struct {
		Tools []cuaAgentTool `json:"tools"`
	}
	if err := c.getJSON(ctx, "computer-use/agent-tools", &response); err != nil || len(response.Tools) == 0 {
		return nil
	}
	state.cuaLoaded = true
	state.cuaNames = map[string]bool{}
	for _, tool := range response.Tools {
		state.cuaNames[tool.Name] = true
		state.cuaTools = append(state.cuaTools, map[string]any{
			"name":        tool.Name,
			"description": "Computer use (Cua Driver). " + tool.Description,
			"inputSchema": tool.InputSchema,
			"annotations": map[string]any{
				"readOnlyHint": readOnlyCuaTools[tool.Name], "destructiveHint": !readOnlyCuaTools[tool.Name],
				"idempotentHint": readOnlyCuaTools[tool.Name], "openWorldHint": true,
			},
		})
	}
	return state.cuaTools
}

// callCua runs one Cua Driver tool through the daemon and returns its result
// as text (the window line, the tree or the diff, and the stale snapshots)
// with each screenshot as an image.
func (c *commandContext) callCua(ctx context.Context, tool string, raw json.RawMessage) map[string]any {
	var arguments map[string]any
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return mcpToolError(fmt.Errorf("invalid arguments: %w", err))
		}
	}
	request := cuaCallRequest{Tool: tool, Arguments: arguments, SessionID: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
	var response cuaCallResponse
	if err := c.postJSON(ctx, "computer-use/call", request, &response); err != nil {
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
	content := []any{mcpText(strings.TrimRight(text, "\n"))}
	for _, image := range response.Images {
		block, err := mcpImage(image)
		if err != nil {
			content = append(content, mcpText(fmt.Sprintf("screenshot %s could not be attached: %v", image.Path, err)))
			continue
		}
		content = append(content, block)
	}
	return map[string]any{"content": content}
}

func mcpImage(image cuaImage) (map[string]any, error) {
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
	return map[string]any{"type": "image", "mimeType": mimeType, "data": base64.StdEncoding.EncodeToString(data)}, nil
}
