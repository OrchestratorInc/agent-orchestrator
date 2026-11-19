package claudeacp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// claude-agent-acp reads these from its own process environment, so chats that
// differ in any of them need separate adapter processes.
var adapterEnvKeys = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_MODEL",
	"ANTHROPIC_VERTEX_BASE_URL", "CLAUDE_AGENT_LOGS", "CLAUDE_CODE_EXECUTABLE", "CLAUDE_CODE_REMOTE",
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CONFIG_DIR", "CLAUDE_MODEL_CONFIG",
	"IS_SANDBOX", "MAX_THINKING_TOKENS", "NO_BROWSER", "SSH_CLIENT", "SSH_CONNECTION", "SSH_TTY",
}

// The adapter process needs these to run. Every other variable reaches each
// claude process through its session's env, so the process keeps no stale ones.
var adapterBaseEnvKeys = []string{"HOME", "LANG", "LC_ALL", "LOGNAME", "PATH", "SHELL", "TMPDIR", "USER"}

// maxSocketPath stays under the 104-byte sun_path limit on macOS.
const maxSocketPath = 100

// sessionEnv holds each shared-adapter chat's full env until claudeSessionMeta
// sends it with session/new.
var sessionEnv sync.Map // domain.SessionID -> map[string]string

// sharedAdapterEnvs splits a chat's effective env (the daemon's env with the
// launch overlay) into the adapter process's env and the session's env.
func sharedAdapterEnvs(overlay map[string]string, workspace string) (server, session map[string]string) {
	session = map[string]string{}
	for _, entry := range processenv.Merge(overlay) {
		if key, value, ok := strings.Cut(entry, "="); ok {
			session[key] = value
		}
	}
	// A per-chat adapter ran in the workspace; the shared one does not.
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_AGENT_LOGS"} {
		if value := session[key]; value != "" && !filepath.IsAbs(value) {
			session[key] = filepath.Join(workspace, value)
		}
	}
	server = map[string]string{}
	for _, key := range append(append([]string{}, adapterEnvKeys...), adapterBaseEnvKeys...) {
		if value, ok := session[key]; ok {
			server[key] = value
		}
	}
	return server, session
}

// sharedAdapterSocket names the adapter process for exactly this runtime and env.
func sharedAdapterSocket(dataDir string, rt runtimeLaunch, server map[string]string) (string, error) {
	identity, err := json.Marshal([]any{rt.command, rt.server, server})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(identity)
	return filepath.Join(dataDir, "acp", hex.EncodeToString(sum[:6])+".sock"), nil
}

// sharedAdapterLaunch connects the chat to a running adapter process, starting
// one if needed. ok is false when the chat should start its own adapter.
func sharedAdapterLaunch(ctx context.Context, cfg acpdriver.LaunchConfig, rt runtimeLaunch, env map[string]string) (acpdriver.Launch, bool, error) {
	server, session := sharedAdapterEnvs(env, cfg.WorkspacePath)
	socket, err := sharedAdapterSocket(cfg.DataDir, rt, server)
	if err != nil {
		return acpdriver.Launch{}, false, err
	}
	if len(socket) > maxSocketPath {
		return acpdriver.Launch{}, false, fmt.Errorf("socket path %q is longer than %d bytes", socket, maxSocketPath)
	}
	if err := ensureSharedAdapter(ctx, socket, rt, server); err != nil {
		return acpdriver.Launch{}, false, err
	}
	sessionEnv.Store(cfg.SessionID, session)
	return acpdriver.Launch{Command: persistenthost.SocketProvider, Args: []string{socket}}, true, nil
}

func sharedSessionEnv(id domain.SessionID) map[string]string {
	env, _ := sessionEnv.Load(id)
	values, _ := env.(map[string]string)
	return values
}

func adapterListening(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
