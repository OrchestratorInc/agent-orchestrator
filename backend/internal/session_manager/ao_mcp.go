package sessionmanager

import (
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

// aoMCPServerName is the name AO's own tool server is registered under. The
// harness prefixes the server's tools with it, so a session refers to them as
// "mcp__ao__<tool>".
const aoMCPServerName = "ao"

// aoWorkerMCPServers returns the tool servers AO offers a worker session, and
// whether the session should get one at all.
//
// A worker gets AO's tool server only when the server has something to offer
// it: the GMI Cloud tools, which need a GMI Cloud key in AO's store. Without
// one the session is launched exactly as before, so a user who never asked for
// GMI never sees a new tool.
//
// Only the groups that do not need a chat thread are offered. The HTML tools
// publish into a session's thread, which a worker has no, so the server is
// asked for the GMI group alone.
//
// The server's environment carries only what it needs to find AO's own state:
// the session id, the run file of the daemon that owns it, and the data
// directory. The key stays in AO's store — the server reads it from there, so
// no credential is ever handed to a process AO started.
func (m *Manager) aoWorkerMCPServers(agent ports.Agent, env map[string]string) []ports.MCPServerConfig {
	capable, ok := agent.(ports.AgentMCPServerCapable)
	if !ok {
		// The harness cannot take a tool server, so the session runs without one.
		return nil
	}
	if !m.toolKeyStored(providerkeys.ToolKeyGMI) {
		return nil
	}
	executable, err := m.executable()
	if err != nil || !filepath.IsAbs(executable) {
		m.logger.Warn("worker session gets no ao tool server", "executable", executable, "error", err)
		return nil
	}
	serverEnv := make(map[string]string, 4)
	for _, key := range []string{EnvSessionID, EnvRunFile, EnvDataDir} {
		if value := env[key]; value != "" {
			serverEnv[key] = value
		}
	}
	serverEnv[config.EnvMCPToolGroups] = config.MCPToolGroupGMI
	servers := []ports.MCPServerConfig{{
		Name:    aoMCPServerName,
		Command: executable,
		Args:    []string{"mcp"},
		Env:     serverEnv,
	}}
	// A harness that renders no flags for the server cannot use it, so the
	// session does not carry one.
	args, err := capable.MCPServerArgs(servers)
	if err != nil {
		m.logger.Warn("worker session gets no ao tool server", "error", err)
		return nil
	}
	if len(args) == 0 {
		return nil
	}
	return servers
}

// toolKeyStored reports whether AO holds a key in its provider key store. The
// value itself is never read: the tool that needs it reads it from the store
// itself, so it never travels through the session manager.
func (m *Manager) toolKeyStored(name string) bool {
	keys, err := providerkeys.Load(m.dataDir)
	if err != nil {
		m.logger.Warn("provider keys unavailable", "error", err)
		return false
	}
	return strings.TrimSpace(keys[name]) != ""
}
