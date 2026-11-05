package computeruse

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
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
// agent's context on every request, so the list stays short: click covers
// double and right clicks, run_steps runs the stepToolNames, and tools for
// zooming, menus, recording, configuration, the clipboard, whole-screen
// capture, scripts, and browsers are left out.
var agentToolNames = []string{
	"list_windows", "launch_app", "bring_to_front", "get_window_state", "click", "type_text", "press_key", "run_steps",
}

// agentToolRules are AO's rules for a tool, added to the driver's description.
var agentToolRules = map[string]string{
	"bring_to_front": "AO: this takes keyboard focus from the user and does not give it back, so ask the user before using it, unless they said they are away from the computer. Use it when a window reads blank or stuck loading because it is hidden behind others; it sends no keys or clicks.",
}

// offeredTool reports whether Call runs tool: only the tools agents are
// offered. The step-only actions run inside the driver as run_steps steps.
func offeredTool(tool string) bool {
	return slices.Contains(agentToolNames, tool)
}

// stepToolNames are actions an agent reaches only as run_steps steps. Their
// arguments are summarized in run_steps' description, since its schema types
// a step's args as any object.
var stepToolNames = []string{"hotkey", "scroll", "set_value", "drag"}

// hiddenArguments are inputs AO sets itself (session, screenshot files),
// refuses (desktop scope, foreground delivery), or the driver ignores
// (capture_mode).
var hiddenArguments = map[string]bool{
	"session": true, "scope": true, "screenshot_out_file": true, "debug_image_out": true, "capture_mode": true,
	"delivery_mode": true, "foreground_fallback": true,
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
		if !ok {
			continue
		}
		if name == "run_steps" {
			tool.Description += s.stepArguments(ctx, binary)
		}
		if rule := agentToolRules[name]; rule != "" {
			tool.Description += "\n\n" + rule
		}
		tools = append(tools, tool)
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

// stepArguments lists the arguments of the step-only tools, required ones
// first and marked with *, for run_steps' description.
func (s *Service) stepArguments(ctx context.Context, binary string) string {
	lines := []string{}
	for _, name := range stepToolNames {
		tool, ok := s.describeAgentTool(ctx, binary, name)
		if !ok {
			continue
		}
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		required := map[string]bool{}
		requiredNames := []string{}
		for _, raw := range asSlice(tool.InputSchema["required"]) {
			if text, _ := raw.(string); text != "" {
				required[text] = true
				requiredNames = append(requiredNames, text+"*")
			}
		}
		optional := []string{}
		for property := range properties {
			if !required[property] {
				optional = append(optional, property)
			}
		}
		sort.Strings(optional)
		lines = append(lines, "- "+name+": "+strings.Join(append(requiredNames, optional...), ", "))
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nThese actions are run_steps steps only, not separate tools; their args (* required; element_token, or pid and window_id, name the target as in click):\n" + strings.Join(lines, "\n")
}

func asSlice(value any) []any {
	items, _ := value.([]any)
	return items
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
