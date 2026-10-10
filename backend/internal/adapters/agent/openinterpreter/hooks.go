package openinterpreter

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var managedHooks = []struct{ event, key, action string }{
	{"SessionStart", "session_start", "session-start"},
	{"UserPromptSubmit", "user_prompt_submit", "user-prompt-submit"},
	{"PostToolUse", "post_tool_use", "post-tool-use"},
}

func appendSessionHooks(cmd *[]string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("open-interpreter: resolve hook executable: %w", err)
	}
	// Enable the documented hook feature for this invocation. Existing hooks
	// still require their own persisted exact-definition trust and enablement.
	*cmd = append(*cmd, "-c", "features.hooks=true")
	appendHookFlags(cmd, executable, runtime.GOOS == "windows")
	return nil
}

// IsRootHook prevents child hooks from changing the AO root native identity.
// The native SessionStart dispatcher skips internal/synthetic subagents, while
// tool and submit payloads identify thread-spawned children through agent_id.
func IsRootHook(payload []byte) bool {
	var input struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
		ParentID  string `json:"parent_session_id"`
	}
	if json.Unmarshal(payload, &input) != nil || input.AgentID != "" || input.AgentType != "" || input.ParentID != "" {
		return false
	}
	_, err := nativeID(input.SessionID)
	return err == nil
}

// SessionFlags aggregate with native config, and their per-definition trust
// state is process-local. Never use --dangerously-bypass-hook-trust: it would
// also authorize unrelated, previously untrusted project and user commands.
func appendHookFlags(cmd *[]string, executable string, windows bool) {
	prefix := "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "'"
	source := "/<session-flags>/config.toml"
	if windows {
		prefix = "& '" + strings.ReplaceAll(executable, "'", "''") + "'"
		source = `C:\<session-flags>\config.toml`
	}
	states := make([]string, 0, len(managedHooks))
	for _, hook := range managedHooks {
		command := prefix + " hooks open-interpreter " + hook.action
		*cmd = append(*cmd, "-c", fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command=%s,timeout=5}]}]`, hook.event, tomlString(command)))
		identity := map[string]any{
			"event_name": hook.key,
			"hooks":      []any{map[string]any{"type": "command", "command": command, "timeout": 5, "async": false}},
		}
		key := source + ":" + hook.key + ":0:0"
		states = append(states, tomlString(key)+"={enabled=true,trusted_hash="+tomlString(hashIdentity(identity))+"}")
	}
	*cmd = append(*cmd, "-c", "hooks.state={"+strings.Join(states, ",")+"}")
}

func hashIdentity(identity map[string]any) string {
	// Rust version_for_toml hashes canonical sorted JSON, without HTML escaping.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(identity)
	digest := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}))
	return fmt.Sprintf("sha256:%x", digest)
}

func tomlString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '\\':
			out.WriteString(`\\`)
		case r == '"':
			out.WriteString(`\"`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&out, `\u%04X`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// DeriveActivityState reports only completed root tool work. SessionStart is
// metadata; submit, permission and stop callbacks cannot prove acceptance,
// blocking or settlement because other hooks may still veto/continue them.
func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if json.Unmarshal(payload, &input) != nil || input.AgentID != "" {
		return "", false
	}
	if event == "post-tool-use" {
		return domain.ActivityActive, true
	}
	return "", false
}
