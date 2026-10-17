package workerexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SecretFileGuardArg is the ao-worker subcommand that answers the coding
// agent's PreToolUse hook. The worker registers "<ao-worker> secret-file-guard"
// as that hook when ProtectSecretFiles is set.
const SecretFileGuardArg = "secret-file-guard"

// secretFileGuardMessage is what the agent reads when a command is blocked.
// It names the supported way to use the values so the agent does not keep
// probing for another route to the file.
const secretFileGuardMessage = "Blocked by AO: shell commands that name a .env file are not allowed in this workspace, " +
	"so the agent never sees its secrets. Use the project's own make targets or scripts that load .env " +
	"themselves (for example `make <target>`), or ask the user for the value you need."

// allowedSecretFileSuffixes are committed templates, readable like any file.
var allowedSecretFileSuffixes = []string{".example", ".sample", ".template"}

// RunSecretFileGuard reads a Claude Code or Codex PreToolUse hook payload and
// returns the process exit code: 2 blocks the tool call and shows stderr to the
// agent, 0 lets it run. Both harnesses treat exit 2 from a PreToolUse hook as a
// block and every other outcome of a failed hook as non-blocking, so anything
// unparseable is allowed rather than wedging the agent.
//
// The Read and Edit deny rules (secretFileDenyRules) already cover Claude's
// file tools and the plain file commands Claude recognizes in Bash (cat, head,
// grep, cp, ...). This guard covers what they miss: interpreters and shell
// built-ins (python3 -c "open('.env')", `. ./.env`, `source .env`) and Codex,
// which has no deny rules and reads files only through its shell tool. A
// command that loads .env without naming it, such as `make db-host`, still
// runs; that is the supported way for a project to use its secrets.
func RunSecretFileGuard(in io.Reader, stderr io.Writer) int {
	payload, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return 0
	}
	var hook struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if json.Unmarshal(payload, &hook) != nil {
		return 0
	}
	command := hookCommandText(hook.ToolInput["command"])
	if command == "" {
		command = hookCommandText(hook.ToolInput["cmd"])
	}
	var name string
	var found bool
	switch hook.ToolName {
	case "Bash":
		name, found = secretFileReference(command)
	case "apply_patch":
		// Codex edits files with apply_patch, whose command is the patch.
		// Only the files it touches count; the patch body may mention .env
		// (a .gitignore entry, a compose env_file line) without opening it.
		name, found = patchSecretFileReference(command)
	}
	if found {
		_, _ = fmt.Fprintf(stderr, "%s (found %q)\n", secretFileGuardMessage, name)
		return 2
	}
	return 0
}

// hookCommandText flattens a shell tool's command, which Claude sends as a
// string and Codex may send as an argv list.
func hookCommandText(value any) string {
	switch command := value.(type) {
	case string:
		return command
	case []any:
		parts := make([]string, 0, len(command))
		for _, part := range command {
			if text, ok := part.(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// secretFileReference reports the first .env file a shell command names:
// ".env" or ".env.<name>" as its own path component, e.g. `.env`, `./.env`,
// `"$PWD/.env.local"` or `open('.env')`. Committed templates (.env.example,
// .env.sample, .env.template) and look-alikes such as `.envrc`, `prod.env` or
// `.environment` are not matches.
func secretFileReference(command string) (string, bool) {
	for offset := 0; ; {
		index := strings.Index(command[offset:], ".env")
		if index < 0 {
			return "", false
		}
		start := offset + index
		offset = start + len(".env")
		if start > 0 && isSecretNameByte(command[start-1]) {
			continue
		}
		end := offset
		for end < len(command) && (isSecretNameByte(command[end]) || command[end] == '.') {
			end++
		}
		name := command[start:end]
		suffix := name[len(".env"):]
		if suffix != "" && (suffix[0] != '.' || len(suffix) == 1) {
			continue
		}
		if allowedSecretFileSuffix(suffix) {
			continue
		}
		return name, true
	}
}

// patchSecretFileReference reports the first .env file an apply_patch patch
// adds, updates, deletes or moves to.
func patchSecretFileReference(patch string) (string, bool) {
	for _, line := range strings.Split(patch, "\n") {
		for _, header := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if path, ok := strings.CutPrefix(line, header); ok {
				if name, found := secretFileReference(strings.TrimSpace(path)); found {
					return name, true
				}
			}
		}
	}
	return "", false
}

func allowedSecretFileSuffix(suffix string) bool {
	for _, allowed := range allowedSecretFileSuffixes {
		if suffix == allowed {
			return true
		}
	}
	return false
}

// isSecretNameByte reports bytes that continue a file name, so ".env" inside
// "prod.env" or ".envrc" is not a separate .env file.
func isSecretNameByte(c byte) bool {
	return c == '_' || c == '-' || c == '.' ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// applySecretFileGuardHook registers guard as a PreToolUse hook on Claude's
// Bash tool in a settings object when it is set, replacing any guard entry a
// previous launch wrote, and removes the entry when guard is empty.
func applySecretFileGuardHook(settings map[string]any, guard string) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks != nil {
		groups, _ := hooks["PreToolUse"].([]any)
		kept := make([]any, 0, len(groups))
		for _, value := range groups {
			if !isSecretFileGuardGroup(value) {
				kept = append(kept, value)
			}
		}
		if len(kept) == 0 {
			delete(hooks, "PreToolUse")
		} else {
			hooks["PreToolUse"] = kept
		}
	}
	if guard == "" {
		if hooks != nil && len(hooks) == 0 {
			delete(settings, "hooks")
		}
		return
	}
	hooks = objectValue(settings, "hooks")
	groups, _ := hooks["PreToolUse"].([]any)
	hooks["PreToolUse"] = append(groups, map[string]any{
		"matcher": "Bash",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": guard,
			"timeout": 5,
		}},
	})
}

func isSecretFileGuardGroup(value any) bool {
	group, _ := value.(map[string]any)
	commands, _ := group["hooks"].([]any)
	for _, candidate := range commands {
		entry, _ := candidate.(map[string]any)
		command, _ := entry["command"].(string)
		if strings.HasSuffix(command, " "+SecretFileGuardArg) {
			return true
		}
	}
	return false
}

// codexSecretFileGuardArgs registers guard as a Codex PreToolUse hook through
// session-flag config, the layer AO's other Codex hooks use. The launch passes
// --dangerously-bypass-hook-trust, which these hooks need to run.
func codexSecretFileGuardArgs(guard string) []string {
	if guard == "" {
		return nil
	}
	command := strings.ReplaceAll(guard, `"`, `\"`)
	return []string{"-c", fmt.Sprintf(`hooks.PreToolUse=[{hooks=[{type="command",command="%s",timeout=5}]}]`, command)}
}

// secretFileGuard is the hook command for this launch, or "" when secret
// files are not protected or the worker did not say how to run the guard.
// The guard ignores the environment, but an older ao-worker (after a
// rollback, with the hook still in Claude's settings) would start a second
// worker from the agent's inherited environment; without
// AO_CLOUD_PUBLIC_URL it exits at once instead.
func (b HarnessBuilder) secretFileGuard() string {
	if !b.ProtectSecretFiles {
		return ""
	}
	guard := strings.TrimSpace(b.SecretFileGuard)
	if guard == "" {
		return ""
	}
	return "env -u AO_CLOUD_PUBLIC_URL " + guard
}

// errCodexHooksUnsupported is a Codex app-server that cannot run session hooks
// (too old for hooks/list or session-flag hooks).
var errCodexHooksUnsupported = errors.New("Codex does not support session hooks")

// trustCodexSecretFileGuard marks the guard hook trusted in a Codex
// app-server. Codex runs a session-flag hook only once its hash is trusted;
// the terminal and exec launches pass --dangerously-bypass-hook-trust, which
// app-server does not accept, so Chat records the trust the way Codex's own
// hook review does: hooks/list reports the hook's current hash and
// hooks.state.<key>.trusted_hash stores it in AO's CODEX_HOME config. Only the
// guard is trusted. A Codex without session hooks returns
// errCodexHooksUnsupported, which the caller logs and runs without the guard;
// any other failure fails the turn.
func trustCodexSecretFileGuard(ctx context.Context, conn *codexRPC, cwd, guard string) error {
	var listed struct {
		Data []struct {
			Hooks []struct {
				Key         string `json:"key"`
				Command     string `json:"command"`
				Source      string `json:"source"`
				CurrentHash string `json:"currentHash"`
				TrustStatus string `json:"trustStatus"`
			} `json:"hooks"`
		} `json:"data"`
	}
	if err := conn.request(ctx, "hooks/list", map[string]any{"cwds": []string{cwd}}, &listed); err != nil {
		var rpcErr *codexRPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == codexMethodNotFound {
			return fmt.Errorf("%w: %v", errCodexHooksUnsupported, err)
		}
		return fmt.Errorf("list Codex hooks: %w", err)
	}
	found := false
	trust := map[string]any{}
	for _, entry := range listed.Data {
		for _, hook := range entry.Hooks {
			if hook.Command != guard || hook.Source != "sessionFlags" {
				continue
			}
			found = true
			if hook.TrustStatus != "trusted" && hook.TrustStatus != "managed" && hook.Key != "" && hook.CurrentHash != "" {
				trust[hook.Key] = map[string]any{"trusted_hash": hook.CurrentHash}
			}
		}
	}
	if !found {
		return fmt.Errorf("%w: Codex did not load the secret file guard hook", errCodexHooksUnsupported)
	}
	if len(trust) == 0 {
		return nil
	}
	if err := conn.request(ctx, "config/batchWrite", map[string]any{
		"edits":            []any{map[string]any{"keyPath": "hooks.state", "mergeStrategy": "upsert", "value": trust}},
		"reloadUserConfig": true,
	}, nil); err != nil {
		return fmt.Errorf("trust the secret file guard hook: %w", err)
	}
	return nil
}
