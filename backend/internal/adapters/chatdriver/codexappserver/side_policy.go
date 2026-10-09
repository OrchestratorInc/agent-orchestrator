package codexappserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const sidePolicyEnvKey = "AO_SIDE_PROVIDER_POLICY"

// Apps and dynamically installed MCP dependencies may expose delegation tools
// outside the native multi-agent registry. They cannot be inherited blindly.
var sideDisabledFeatures = []string{"multi_agent", "multi_agent_v2", "agent_message_board", "apps", "skill_mcp_dependency_install"}

// ValidateSidePolicy requires a provider version with explicit delegation controls.
func (d *Driver) ValidateSidePolicy(ctx context.Context, cfg ports.ChatStartConfig) error {
	if len(cfg.MCPServers) != 0 {
		return fmt.Errorf("side chats cannot use this MCP configuration: delegation-safe tool filtering is unavailable")
	}
	bin, err := d.plugin.ResolveBinary(ctx)
	if err != nil {
		return err
	}
	output, err := d.versionProbe(ctx, bin)
	if err != nil {
		return err
	}
	version, ok := parseCodexVersion(output)
	if !ok || version.less(codexVersion{0, 162, 0}) {
		return fmt.Errorf("side chats require Codex 0.162.0 or newer to disable delegation; update Codex")
	}
	return nil
}

func sidePolicyEnv(inherited map[string]string) map[string]string {
	env := make(map[string]string, len(inherited)+1)
	for key, value := range inherited {
		env[key] = value
	}
	env[sidePolicyEnvKey] = "1"
	return env
}

func sidePolicyArgv(bin string, env map[string]string) []string {
	args := []string{bin, "app-server"}
	if env[sidePolicyEnvKey] == "1" {
		for _, feature := range sideDisabledFeatures {
			args = append(args, "--disable", feature)
		}
	}
	return args
}

func applySideThreadConfig(params map[string]any) {
	cfg, _ := params["config"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	}
	for _, feature := range sideDisabledFeatures {
		cfg["features."+feature] = false
	}
	params["config"] = cfg
}

// Verify effective settings, including project/user MCP configuration, before
// opening a thread. Missing or null values are not proof of enforcement.
func verifySideProviderConfig(ctx context.Context, conv *conversation, workdir string) error {
	var response struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if err := conv.conn.request(ctx, "config/read", map[string]any{"cwd": workdir, "includeLayers": false}, &response); err != nil {
		return fmt.Errorf("cannot verify side-chat delegation restrictions: %w", err)
	}
	var features map[string]*bool
	if err := json.Unmarshal(response.Config["features"], &features); err != nil {
		return fmt.Errorf("cannot verify side-chat feature settings: %w", err)
	}
	for _, feature := range sideDisabledFeatures {
		if enabled := features[feature]; enabled == nil || *enabled {
			return fmt.Errorf("provider did not confirm that %s is disabled; side chat is unavailable", feature)
		}
	}
	var servers map[string]struct {
		Enabled *bool `json:"enabled"`
	}
	raw, present := response.Config["mcp_servers"]
	if !present || string(raw) == "null" {
		return fmt.Errorf("provider did not expose its MCP configuration; side chat is unavailable")
	}
	if err := json.Unmarshal(raw, &servers); err != nil {
		return fmt.Errorf("cannot verify side-chat MCP configuration: %w", err)
	}
	for _, server := range servers {
		if server.Enabled == nil || *server.Enabled {
			return fmt.Errorf("side chats are unavailable with enabled MCP servers: delegation-safe filtering is unavailable; use a provider configuration without MCP servers")
		}
	}
	return nil
}
