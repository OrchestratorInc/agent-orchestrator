package workerexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/pkg/tasknaming"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// BuildTaskNaming prepares only the selected provider's credential for the
// shared ephemeral Chat turn. No project rules, hooks, or worker authority are
// copied into its disposable workspace.
func (b HarnessBuilder) BuildTaskNaming(ctx context.Context, launch worker.LaunchContext, credential worker.CredentialResponse) (tasknaming.BackgroundConfig, func(), error) {
	var cfg tasknaming.BackgroundConfig
	if credential.Provider != launch.Harness || strings.TrimSpace(credential.Secret) == "" {
		return cfg, nil, errors.New("credential does not match the selected harness")
	}
	harness := launch.Harness
	if harness == "opencode" {
		harness = "opencode-v2"
	}
	if !tasknaming.Supported(harness) {
		return cfg, nil, errors.New("task naming harness is unsupported")
	}
	if launch.Harness == "codex" && credential.CredentialType == "auth_json" {
		var auth map[string]json.RawMessage
		if err := json.Unmarshal([]byte(credential.Secret), &auth); err != nil || auth == nil {
			return cfg, nil, errors.New("invalid Codex authentication for task naming")
		}
		if raw, ok := auth["tokens"]; ok && string(raw) != "null" {
			var tokens map[string]json.RawMessage
			if err := json.Unmarshal(raw, &tokens); err != nil || tokens == nil {
				return cfg, nil, errors.New("invalid Codex authentication for task naming")
			}
			// Cosmetic naming must not rotate the real agent's single-use OAuth
			// refresh token. Stale access instead uses the existing naming fallback.
			tokens["refresh_token"] = json.RawMessage(`""`)
			isolatedTokens, err := json.Marshal(tokens)
			if err != nil {
				return cfg, nil, errors.New("invalid Codex authentication for task naming")
			}
			auth["tokens"] = isolatedTokens
			isolated, err := json.Marshal(auth)
			if err != nil {
				return cfg, nil, errors.New("invalid Codex authentication for task naming")
			}
			credential.Secret = string(isolated)
		}
	}
	parent := filepath.Join(b.DataDir, "background-tasks")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return cfg, nil, err
	}
	root, err := os.MkdirTemp(parent, "task-naming-")
	if err != nil {
		return cfg, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	fail := func(err error) (tasknaming.BackgroundConfig, func(), error) { cleanup(); return cfg, nil, err }
	workspace := filepath.Join(root, "workspace")
	home := filepath.Join(root, "home")
	for _, dir := range []string{workspace, home} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail(err)
		}
	}
	// Chat drivers overlay their environment onto the supervisor's. Explicitly
	// blank every inherited entry, restoring only runtime essentials below.
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		env[key] = ""
	}
	for _, key := range []string{"PATH", "TMPDIR", "TMP", "TEMP", "SystemRoot", "COMSPEC", "LANG"} {
		env[key] = os.Getenv(key)
	}
	env["HOME"], env["USERPROFILE"] = home, home
	env["AO_DATA_DIR"] = root
	env["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
	env["XDG_DATA_HOME"] = filepath.Join(home, ".local", "share")
	env["XDG_CACHE_HOME"] = filepath.Join(home, ".cache")
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(home, ".claude")
	env["CODEX_HOME"] = filepath.Join(root, "codex")
	command := Command{Path: b.binary(launch.Harness), Dir: workspace, Env: env}
	credentialBuilder := b
	credentialBuilder.DataDir, credentialBuilder.ConfigRoot = root, root
	if err := credentialBuilder.configureCredential(&command, launch.Harness, credential); err != nil {
		return fail(err)
	}
	if launch.Harness == "claude-code" {
		adapter, err := EnsureClaudeACPAdapter(ctx)
		if err != nil {
			return fail(err)
		}
		command.Env["AO_CLAUDE_ACP_COMMAND"] = adapter
	}
	cfg = tasknaming.BackgroundConfig{Harness: harness, DataDir: root, WorkspacePath: workspace, Model: launch.Model, Env: command.Env}
	return cfg, cleanup, nil
}
