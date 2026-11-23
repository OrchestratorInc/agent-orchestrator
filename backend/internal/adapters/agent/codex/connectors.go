package codex

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ConnectorArgs returns the config overrides that give Codex the tool servers; the token goes in the environment.
func (p *Plugin) ConnectorArgs(servers []ports.ChatMCPServerConfig, token, _ string) (args []string, env map[string]string, err error) {
	set := func(key string, value any) {
		if raw, encodeErr := json.Marshal(value); encodeErr == nil {
			args = append(args, "-c", key+"="+string(raw))
		} else {
			err = encodeErr
		}
	}
	for _, server := range servers {
		base := "mcp_servers." + server.Name
		if server.Type == "http" {
			set(base+".url", server.URL)
			set(base+".bearer_token_env_var", config.EnvConnectorToken)
			continue
		}
		set(base+".command", server.Command)
		set(base+".args", append([]string{}, server.Args...))
		for _, name := range slices.Sorted(maps.Keys(server.Env)) {
			if server.Env[name] != "Bearer "+token {
				set(base+".env."+name, server.Env[name])
			}
		}
		set(base+".env_vars", []string{config.EnvConnectorToken})
	}
	return args, map[string]string{config.EnvConnectorToken: token}, err
}
