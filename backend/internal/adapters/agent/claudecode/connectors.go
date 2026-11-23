package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type connectorServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ConnectorArgs writes the tool servers to a private file and returns the flag
// that makes Claude Code load it, so the session token stays out of the
// command line.
func (p *Plugin) ConnectorArgs(servers []ports.ChatMCPServerConfig, _, file string) (args []string, env map[string]string, err error) {
	entries := make(map[string]connectorServer, len(servers))
	for _, server := range servers {
		entries[server.Name] = connectorServer{
			Type: server.Type, URL: server.URL, Headers: server.Headers,
			Command: server.Command, Args: server.Args, Env: server.Env,
		}
	}
	data, err := json.Marshal(map[string]any{"mcpServers": entries})
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.Chmod(file, 0o600); err != nil {
		return nil, nil, err
	}
	return []string{"--mcp-config", file}, nil, nil
}
