package claudeacp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
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

// maxSocketPath stays under the 104-byte sun_path limit on macOS.
const maxSocketPath = 100

// sessionEnv holds each shared-adapter chat's launch env until claudeSessionMeta
// sends it with session/new.
var sessionEnv sync.Map // domain.SessionID -> map[string]string

// sharedAdapterLaunch connects the chat to a running adapter process, starting
// one if needed. ok is false when the chat should start its own adapter.
func sharedAdapterLaunch(ctx context.Context, cfg acpdriver.LaunchConfig, rt runtimeLaunch, env map[string]string) (acpdriver.Launch, bool, error) {
	adapterEnv := map[string]string{}
	for _, key := range adapterEnvKeys {
		if value, ok := env[key]; ok {
			adapterEnv[key] = value
		}
	}
	identity, err := json.Marshal([]any{rt.command, rt.server, adapterEnv})
	if err != nil {
		return acpdriver.Launch{}, false, err
	}
	sum := sha256.Sum256(identity)
	socket := filepath.Join(cfg.DataDir, "acp", hex.EncodeToString(sum[:6])+".sock")
	if len(socket) > maxSocketPath {
		return acpdriver.Launch{}, false, fmt.Errorf("socket path %q is longer than %d bytes", socket, maxSocketPath)
	}
	if err := ensureSharedAdapter(ctx, socket, rt, adapterEnv); err != nil {
		return acpdriver.Launch{}, false, err
	}
	sessionEnv.Store(cfg.SessionID, env)
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
