package computeruse

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

type AgentTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema" description:"The tool's JSON Schema input, without the arguments AO sets itself."`
}

var agentToolNames = []string{
	"list_windows", "launch_app", "bring_to_front", "get_window_state", "click", "type_text", "press_key", "run_steps",
}

var agentToolRules = map[string]string{
	"bring_to_front": "Orchestrator: this takes keyboard focus from the user and does not give it back, so ask the user before using it, unless they said they are away from the computer. Use it when a window reads blank or stuck loading because it is hidden behind others; it sends no keys or clicks.",
}

func offeredTool(tool string) bool {
	return slices.Contains(agentToolNames, tool)
}

var stepToolNames = []string{"hotkey", "scroll", "set_value", "drag"}

var hiddenArguments = map[string]bool{
	"session": true, "scope": true, "screenshot_out_file": true, "debug_image_out": true, "capture_mode": true,
	"delivery_mode": true, "foreground_fallback": true,
}

type agentToolCache struct {
	key   string
	tools []AgentTool
}

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
