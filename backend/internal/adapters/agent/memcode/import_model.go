package memcode

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
)

const memcodeWorkspaceConfig = ".memcode/config.json"

// The caller is GetAgentHooks, whose WorkspacePath is the AO launch workspace,
// after imported history has moved there. Never use the source/import root or
// the provider's global config. Sol's native session effort cannot be persisted;
// pin the model here so the single confirmed startup command can set effort.
func pinMemcodeSol(ctx context.Context, workspace string) error {
	if !filepath.IsAbs(workspace) {
		return errors.New("memcode model preparation requires an absolute launch workspace")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(".memcode")
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(".memcode", 0o700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("memcode model directory must not be a symlink")
	}
	config, err := memcodeWorkspaceModelConfig(ctx, root)
	if err != nil {
		return err
	}
	config["pinned_model"] = json.RawMessage(`"sol"`)
	// Keep the native v0.50 Sol catalog window paired with the new pin,
	// rather than retaining a previous model's cached context limit.
	config["pinned_window"] = json.RawMessage(`1050000`)
	// Native Config.Validate requires models.coder even when pinned_model is
	// selected. Preserve all existing tiers; initialize only an absent coder.
	models := make(map[string]json.RawMessage)
	if raw, ok := config["models"]; ok && json.Unmarshal(raw, &models) != nil {
		return errors.New("memcode workspace model tiers are malformed")
	}
	if models == nil {
		models = make(map[string]json.RawMessage)
	}
	var coder string
	if raw, ok := models["coder"]; ok && json.Unmarshal(raw, &coder) != nil {
		return errors.New("memcode workspace coder model is malformed")
	}
	if coder == "" {
		models["coder"] = json.RawMessage(`"sol"`)
	}
	config["models"], err = json.Marshal(models)
	if err != nil {
		return err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("memcode workspace model config exceeds read budget")
	}
	temp := ".memcode/.ao-model-" + rand.Text()
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temp, memcodeWorkspaceConfig)
}

func memcodeWorkspaceModelConfig(ctx context.Context, root *os.Root) (map[string]json.RawMessage, error) {
	data, err := memcodeImportFile(ctx, root, memcodeWorkspaceConfig, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]json.RawMessage), nil
	}
	if err != nil {
		return nil, err
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(data, &config) != nil || config == nil {
		return nil, errors.New("memcode workspace model config is malformed")
	}
	return config, nil
}

func memcodeSolPinned(ctx context.Context, workspace string) bool {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	config, err := memcodeWorkspaceModelConfig(ctx, root)
	if err != nil {
		return false
	}
	return string(config["pinned_model"]) == `"sol"`
}

func memcodeSolEffortApplied(workspace, output string) bool {
	if !memcodeSolPinned(context.Background(), workspace) {
		return false
	}
	effort, model := "", ""
	for _, line := range strings.Split(terminalui.PlainTerminalText(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "thinking effort → ") {
			fields := strings.Fields(strings.TrimPrefix(line, "thinking effort → "))
			effort = ""
			if len(fields) > 0 {
				effort = fields[0]
			}
		}
		if strings.HasPrefix(line, "memcode · ") {
			model = ""
			for _, field := range strings.Split(line, "·") {
				if strings.TrimSpace(field) == "sol" {
					model = "sol"
				}
			}
		}
	}
	return model == "sol" && effort == "medium"
}
