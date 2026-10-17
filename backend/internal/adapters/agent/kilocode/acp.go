package kilocode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PrepareACPConfigContent merges AO's standing instructions and permission mode
// into the KILO_CONFIG_CONTENT inline config shared by the interactive TUI and
// the `kilocode acp` server. Kilo Code deep-merges this value as its
// highest-precedence config. ACP explicitly asks for edits and shell commands
// in Default mode because the provider otherwise allows them automatically.
// Unrelated inherited permission rules are preserved.
//
// The generated agent is declared primary: verified against Kilo CLI 7.7.5,
// default_agent is ignored for a non-primary agent, and the session silently
// falls back to Kilo's read-only "ask" agent with none of AO's instructions.
//
// Model selection is deliberately not written here: the ACP session advertises
// its own model options, and AO applies the durable choice through
// session/set_model.
func PrepareACPConfigContent(
	existing, systemPrompt, sessionID string,
	permissions ports.PermissionMode,
) (string, error) {
	config := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil {
			return "", fmt.Errorf("kilocode: decode KILO_CONFIG_CONTENT: %w", err)
		}
	}
	if config == nil {
		return "", fmt.Errorf("kilocode: KILO_CONFIG_CONTENT must be an object")
	}
	// Unlike the interactive CLI, ACP allows tools by default. Explicitly ask
	// for edits and commands unless AO's selected tier grants them.
	permission := kilocodePermissionConfig(permissions)
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
		permission = map[string]string{"edit": "ask", "bash": "ask"}
	case ports.PermissionModeAcceptEdits:
		permission["bash"] = "ask"
	}
	inherited := map[string]any{}
	switch value := config["permission"].(type) {
	case nil:
	case map[string]any:
		inherited = value
	case string:
		// Kilo also accepts a single action as a wildcard permission.
		inherited["*"] = value
	default:
		return "", fmt.Errorf("kilocode: KILO_CONFIG_CONTENT permission must be an object or action string")
	}
	for tool, action := range permission {
		inherited[tool] = action
	}
	config["permission"] = inherited
	if strings.TrimSpace(systemPrompt) != "" {
		agents, ok := config["agent"].(map[string]any)
		if config["agent"] != nil && !ok {
			return "", fmt.Errorf("kilocode: KILO_CONFIG_CONTENT agent must be an object")
		}
		if agents == nil {
			agents = map[string]any{}
		}
		agentName := kilocodeAOAgentName(sessionID)
		agents[agentName] = kilocodeAgentSettings{Mode: "primary", Prompt: systemPrompt}
		config["agent"] = agents
		config["default_agent"] = agentName
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("kilocode: encode ACP config: %w", err)
	}
	return string(data), nil
}
