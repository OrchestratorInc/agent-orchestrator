package sessionmanager

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

// mcpCapableAgent is a harness that can register an MCP server on its launch
// command line, the way Claude Code and Codex can.
type mcpCapableAgent struct {
	fakeAgent
	servers []ports.MCPServerConfig
}

func (a *mcpCapableAgent) MCPServerArgs(servers []ports.MCPServerConfig) ([]string, error) {
	a.servers = servers
	return []string{"--mcp-config", "server"}, nil
}

// mcpRecordingAgent records the launch config it is handed, so a test can
// assert that the tool server reached the harness.
type mcpRecordingAgent struct {
	fakeAgent
	lastLaunch ports.LaunchConfig
}

func (a *mcpRecordingAgent) MCPServerArgs(servers []ports.MCPServerConfig) ([]string, error) {
	return []string{"--mcp-config", "server"}, nil
}

func (a *mcpRecordingAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.lastLaunch = cfg
	return []string{"launch"}, nil
}

// mcpIncapableAgent is a harness that cannot take a tool server.
type mcpIncapableAgent struct{ fakeAgent }

// aoTestManager is a manager whose data dir holds the given provider keys and
// whose own binary is at an absolute path, as a running daemon's is. It
// returns the manager and that path.
func aoTestManager(t *testing.T, keys map[string]string) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	for name, value := range keys {
		if err := providerkeys.Save(context.Background(), dir, name, value); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}
	executable := filepath.Join(t.TempDir(), "ao")
	m := New(Deps{
		DataDir: dir,
		Executable: func() (string, error) {
			return executable, nil
		},
	})
	return m, executable
}

// aoTestSpawnManager is a manager that can spawn: it has a store, a
// workspace, and a runtime.
func aoTestSpawnManager(t *testing.T, agent ports.Agent) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	if err := providerkeys.Save(context.Background(), dir, providerkeys.ToolKeyGMI, "gmi-key"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	executable := filepath.Join(t.TempDir(), "ao")
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   dir,
		Executable: func() (string, error) {
			return executable, nil
		},
		// The fake agent's argv is not a real binary, so the pre-launch check
		// is stubbed the way the other manager fixtures stub it.
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	return m, executable
}

// A worker session gets AO's tool server only when the server has something
// to offer it. Without a GMI Cloud key the session is launched exactly as
// before, so a user who never asked for GMI never sees a new tool.
func TestWorkerGetsNoToolServerWithoutAGMIKey(t *testing.T) {
	m, _ := aoTestManager(t, nil)
	agent := &mcpCapableAgent{}
	if servers := m.aoWorkerMCPServers(agent, map[string]string{}); servers != nil {
		t.Fatalf("servers = %#v, want none without a GMI key", servers)
	}
	if agent.servers != nil {
		t.Fatalf("the harness was asked about %#v, want no question", agent.servers)
	}
}

func TestWorkerGetsTheToolServerWithAGMIKey(t *testing.T) {
	m, executable := aoTestManager(t, map[string]string{providerkeys.ToolKeyGMI: "gmi-key"})
	dir := m.dataDir
	agent := &mcpCapableAgent{}
	env := map[string]string{
		EnvSessionID: "mer-1",
		EnvRunFile:   "/ao/running.json",
		EnvDataDir:   dir,
	}
	servers := m.aoWorkerMCPServers(agent, env)
	want := []ports.MCPServerConfig{{
		Name:    "ao",
		Command: executable,
		Args:    []string{"mcp"},
		Env: map[string]string{
			EnvSessionID: "mer-1",
			EnvRunFile:   "/ao/running.json",
			EnvDataDir:   dir,
			// The worker has no chat thread, so the server offers only the
			// tools that do not need one.
			config.EnvMCPToolGroups: config.MCPToolGroupGMI,
		},
	}}
	if !reflect.DeepEqual(servers, want) {
		t.Fatalf("servers = %#v, want %#v", servers, want)
	}
}

// The key never travels to the harness: the server reads it from AO's store.
func TestWorkerToolServerEnvCarriesNoCredential(t *testing.T) {
	m, _ := aoTestManager(t, map[string]string{providerkeys.ToolKeyGMI: "gmi-key"})
	servers := m.aoWorkerMCPServers(&mcpCapableAgent{}, map[string]string{EnvDataDir: m.dataDir})
	if len(servers) != 1 {
		t.Fatalf("servers = %#v, want one", servers)
	}
	for key, value := range servers[0].Env {
		if key == providerkeys.ToolKeyGMI || value == "gmi-key" {
			t.Fatalf("the server's env carries the credential: %s=%s", key, value)
		}
	}
}

// A harness that cannot take a tool server is launched exactly as before.
func TestWorkerWithAnIncapableHarnessGetsNoToolServer(t *testing.T) {
	m, _ := aoTestManager(t, map[string]string{providerkeys.ToolKeyGMI: "gmi-key"})
	if servers := m.aoWorkerMCPServers(&mcpIncapableAgent{}, map[string]string{}); servers != nil {
		t.Fatalf("servers = %#v, want none for a harness that cannot take one", servers)
	}
}

// Without an absolute path to the daemon's own binary there is no server to
// run, so the session runs without one.
func TestWorkerWithNoExecutableGetsNoToolServer(t *testing.T) {
	m, _ := aoTestManager(t, map[string]string{providerkeys.ToolKeyGMI: "gmi-key"})
	m.executable = func() (string, error) {
		return "ao", nil
	}
	if servers := m.aoWorkerMCPServers(&mcpCapableAgent{}, map[string]string{}); servers != nil {
		t.Fatalf("servers = %#v, want none without an absolute executable", servers)
	}
}

// A spawned worker hands the harness the tool server, so the harness can
// register it on the launch command line.
func TestSpawnedWorkerHandsTheHarnessTheToolServer(t *testing.T) {
	agent := &mcpRecordingAgent{}
	m, _ := aoTestSpawnManager(t, agent)
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(agent.lastLaunch.MCPServers) != 1 {
		t.Fatalf("launch config = %#v, want one tool server", agent.lastLaunch.MCPServers)
	}
	server := agent.lastLaunch.MCPServers[0]
	if server.Name != "ao" || server.Command == "" || len(server.Args) != 1 || server.Args[0] != "mcp" {
		t.Fatalf("server = %#v, want ao's own mcp server", server)
	}
	if server.Env[config.EnvMCPToolGroups] != config.MCPToolGroupGMI {
		t.Fatalf("server env = %#v, want the GMI group alone", server.Env)
	}
}
