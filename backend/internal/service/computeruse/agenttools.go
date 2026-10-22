package computeruse

import (
	"context"
	"encoding/json"
	"strings"
)

// AgentTool is one driver tool AO offers agents as an MCP tool.
type AgentTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema" description:"The tool's JSON Schema input, without the arguments AO sets itself."`
}

// agentToolNames are the driver tools an agent gets as MCP tools: enough to
// find a window, read it, and act on it. Every tool's definition sits in the
// agent's context, so the list stays short: click covers double and right
// clicks, list_windows and launch_app cover list_apps, and tools for
// recording, configuration, the clipboard, whole-screen capture, scripts, and
// browsers are left to `ao cua`, or turned off.
var agentToolNames = []string{
	"list_windows", "launch_app", "get_window_state", "zoom", "click", "type_text",
	"set_value", "press_key", "hotkey", "scroll", "drag", "invoke_menu", "run_steps",
}

// hiddenArguments are inputs AO sets itself (session, screenshot files),
// refuses (desktop scope), or the driver ignores (capture_mode).
var hiddenArguments = map[string]bool{
	"session": true, "scope": true, "screenshot_out_file": true, "debug_image_out": true, "capture_mode": true,
}

// agentToolCache holds the agent tools for one driver binary and version.
type agentToolCache struct {
	key   string
	tools []AgentTool
}

// AgentTools returns the MCP tools AO offers agents, with each tool's
// description and input schema read from the installed driver, so they match
// its release. They are read once per driver version.
func (s *Service) AgentTools(ctx context.Context) ([]AgentTool, error) {
	binary, err := s.locate(ctx)
	if err != nil {
		return nil, err
	}
	key := binary + "@" + s.version(ctx, binary)
	s.mu.Lock()
	cached := s.agentTools
	s.mu.Unlock()
	if cached.key == key && cached.tools != nil {
		return cached.tools, nil
	}
	tools := make([]AgentTool, 0, len(agentToolNames))
	for _, name := range agentToolNames {
		tool, ok := s.describeAgentTool(ctx, binary, name)
		if ok {
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 {
		return tools, nil
	}
	s.mu.Lock()
	s.agentTools = agentToolCache{key: key, tools: tools}
	s.mu.Unlock()
	return tools, nil
}

// describeAgentTool reads one tool from `cua-driver describe`:
// "name: <tool>\n\ndescription:\n<text>\n\ninput_schema:\n<JSON>". A tool the
// driver lacks, or describes in another shape, is left out.
func (s *Service) describeAgentTool(ctx context.Context, binary, name string) (AgentTool, bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stdout, _, err := s.driver.Run(ctx, binary, "describe", name)
	if err != nil {
		return AgentTool{}, false
	}
	text := strings.ReplaceAll(string(stdout), "\r\n", "\n")
	head, schemaText, found := strings.Cut(text, "\ninput_schema:\n")
	if !found || !strings.HasPrefix(strings.TrimSpace(head), "name: "+name) {
		return AgentTool{}, false
	}
	var schema map[string]any
	if json.Unmarshal([]byte(schemaText), &schema) != nil {
		return AgentTool{}, false
	}
	_, description, _ := strings.Cut(head, "\ndescription:\n")
	return AgentTool{Name: name, Description: cleanText(description), InputSchema: withoutHiddenArguments(schema)}, true
}

func withoutHiddenArguments(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	for name := range properties {
		if hiddenArguments[name] {
			delete(properties, name)
		}
	}
	if required, ok := schema["required"].([]any); ok {
		kept := make([]any, 0, len(required))
		for _, name := range required {
			if text, _ := name.(string); !hiddenArguments[text] {
				kept = append(kept, name)
			}
		}
		schema["required"] = kept
	}
	return schema
}
