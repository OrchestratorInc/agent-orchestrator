package claudeacp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
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

// sharedAdapterSupported is false where the adapter cannot run as a shared
// process; those chats start their own adapter.
var sharedAdapterSupported = sharedAdapterAvailable

// sessionEnv holds each shared-adapter chat's full env for claudeSessionMeta.
// One launch reads it once or twice within its startup (a failed session/load
// falls back to session/new), so entries older than sessionEnvTTL are dropped.
var sessionEnv sync.Map // domain.SessionID -> *launchEnv

const sessionEnvTTL = 10 * time.Minute

type launchEnv struct {
	env    map[string]string
	stored time.Time
}

func storeSessionEnv(id domain.SessionID, env map[string]string, now time.Time) {
	sessionEnv.Range(func(key, value any) bool {
		// Delete only the entry seen here, never a newer one stored meanwhile.
		if entry, ok := value.(*launchEnv); !ok || now.Sub(entry.stored) > sessionEnvTTL {
			sessionEnv.CompareAndDelete(key, value)
		}
		return true
	})
	sessionEnv.Store(id, &launchEnv{env: env, stored: now})
}

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
// The file stamps change when an update replaces the runtime at the same path.
func sharedAdapterSocket(dataDir string, rt runtimeLaunch, server map[string]string) (string, error) {
	files := []string{rt.command, rt.server}
	if len(rt.args) > 0 {
		files = append(files, filepath.Join(filepath.Dir(rt.args[0]), "acp-agent.js"))
	}
	stamps := make([]string, 0, len(files))
	for _, path := range files {
		if info, err := os.Stat(path); err == nil {
			stamps = append(stamps, fmt.Sprintf("%s %d %d", path, info.Size(), info.ModTime().UnixNano()))
		}
	}
	identity, err := json.Marshal([]any{stamps, server})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(identity)
	return filepath.Join(dataDir, "acp", hex.EncodeToString(sum[:6])+".sock"), nil
}

// sharedAdapterLaunch connects the chat to a running adapter process, starting
// one if needed. ok is false when the chat should start its own adapter.
func sharedAdapterLaunch(ctx context.Context, cfg acpdriver.LaunchConfig, rt runtimeLaunch, env map[string]string) (acpdriver.Launch, bool, error) {
	if !sharedAdapterSupported {
		return acpdriver.Launch{}, false, nil
	}
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
	storeSessionEnv(cfg.SessionID, session, time.Now())
	return acpdriver.Launch{Command: persistenthost.SocketProvider, Args: []string{socket}}, true, nil
}

func sharedSessionEnv(id domain.SessionID) map[string]string {
	value, _ := sessionEnv.Load(id)
	stored, _ := value.(*launchEnv)
	if stored == nil {
		return nil
	}
	return stored.env
}

func adapterListening(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
