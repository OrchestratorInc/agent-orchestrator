package crush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	crushConfigDirName      = ".crush"
	crushConfigFileName     = ".crush.json"
	crushSystemPromptName   = "ao-system-prompt.md"
	crushSystemPromptPath   = crushConfigDirName + "/" + crushSystemPromptName
	crushSystemPromptMarker = "agent-orchestrator: managed crush system prompt"

	crushModelSeparator = "/"

	crushModelType = "large"
)

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("crush.GetAgentHooks: WorkspacePath is required")
	}
	prompt, err := crushSystemPromptText(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return fmt.Errorf("crush.GetAgentHooks: %w", err)
	}
	if err := applyCrushModelOverride(cfg.WorkspacePath, cfg.Config.Model); err != nil {
		return fmt.Errorf("crush.GetAgentHooks: %w", err)
	}
	if strings.TrimSpace(prompt) == "" {
		return nil
	}

	promptPath := crushSystemPromptFile(cfg.WorkspacePath)
	if _, err := os.Stat(promptPath); err == nil {
		managed, err := isAOManagedCrushSystemPrompt(promptPath)
		if err != nil {
			return fmt.Errorf("crush.GetAgentHooks: %w", err)
		}
		if !managed {
			return fmt.Errorf("crush.GetAgentHooks: refusing to overwrite non-Orchestrator file at %s", promptPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("crush.GetAgentHooks: stat system prompt: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(promptPath), 0o750); err != nil {
		return fmt.Errorf("crush.GetAgentHooks: create config dir: %w", err)
	}
	content := "<!-- " + crushSystemPromptMarker + " -->\n\n" + strings.TrimRight(prompt, "\n") + "\n"
	if err := hookutil.AtomicWriteFile(promptPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("crush.GetAgentHooks: write system prompt: %w", err)
	}
	if err := mergeCrushContextPath(crushConfigFile(cfg.WorkspacePath), crushSystemPromptPath); err != nil {
		return fmt.Errorf("crush.GetAgentHooks: merge config: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(promptPath), crushSystemPromptName); err != nil {
		return fmt.Errorf("crush.GetAgentHooks: gitignore: %w", err)
	}
	return nil
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("crush.UninstallHooks: workspacePath is required")
	}
	promptPath := crushSystemPromptFile(workspacePath)
	managed, err := isAOManagedCrushSystemPrompt(promptPath)
	if err != nil {
		return fmt.Errorf("crush.UninstallHooks: %w", err)
	}
	if managed {
		if err := os.Remove(promptPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("crush.UninstallHooks: remove system prompt: %w", err)
		}
	}
	if err := removeCrushContextPath(crushConfigFile(workspacePath), crushSystemPromptPath); err != nil {
		return fmt.Errorf("crush.UninstallHooks: merge config: %w", err)
	}
	return nil
}

func applyCrushModelOverride(workspacePath, modelOverride string) error {
	model := strings.TrimSpace(modelOverride)
	if model == "" {
		return nil
	}
	provider, modelID, ok := strings.Cut(model, crushModelSeparator)
	provider, modelID = strings.TrimSpace(provider), strings.TrimSpace(modelID)
	if ok && provider != "" && modelID != "" {
		return mergeCrushModel(crushConfigFile(workspacePath), provider, modelID)
	}
	if ok {
		slog.Default().Warn("crush: skipping model override because provider or model id is empty",
			"model", model)
		return nil
	}
	existingProvider, err := existingCrushModelProvider(crushConfigFile(workspacePath))
	if err != nil {
		return err
	}
	if existingProvider == "" {
		slog.Default().Warn("crush: skipping bare model override because .crush.json has no existing provider to reuse",
			"model", model)
		return nil
	}
	return mergeCrushModel(crushConfigFile(workspacePath), existingProvider, model)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("crush.AreHooksInstalled: workspacePath is required")
	}
	managed, err := isAOManagedCrushSystemPrompt(crushSystemPromptFile(workspacePath))
	if err != nil {
		return false, fmt.Errorf("crush.AreHooksInstalled: %w", err)
	}
	return managed, nil
}

func crushSystemPromptFile(workspacePath string) string {
	return filepath.Join(workspacePath, crushConfigDirName, crushSystemPromptName)
}

func crushConfigFile(workspacePath string) string {
	return filepath.Join(workspacePath, crushConfigFileName)
}

func crushSystemPromptText(inline, file string) (string, error) {
	if strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return "", fmt.Errorf("read system prompt file: %w", err)
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	return strings.TrimRight(inline, "\n"), nil
}

func isAOManagedCrushSystemPrompt(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return strings.Contains(string(data), crushSystemPromptMarker), nil
}

func mergeCrushContextPath(configPath, contextPath string) error {
	cfg, err := readCrushConfig(configPath)
	if err != nil {
		return err
	}
	options := crushConfigObject(cfg, "options")
	paths := crushStringSlice(options["context_paths"])
	if !slices.Contains(paths, contextPath) {
		paths = append(paths, contextPath)
	}
	options["context_paths"] = paths
	cfg["options"] = options
	return writeCrushConfig(configPath, cfg)
}

func mergeCrushModel(configPath, provider, modelID string) error {
	cfg, err := readCrushConfig(configPath)
	if err != nil {
		return err
	}
	models := crushConfigObject(cfg, "models")
	selection := crushConfigObject(models, crushModelType)
	selection["model"] = modelID
	selection["provider"] = provider
	models[crushModelType] = selection
	cfg["models"] = models
	return writeCrushConfig(configPath, cfg)
}

func existingCrushModelProvider(configPath string) (string, error) {
	cfg, err := readCrushConfig(configPath)
	if err != nil {
		return "", err
	}
	models, ok := cfg["models"].(map[string]any)
	if !ok {
		return "", nil
	}
	selection, ok := models[crushModelType].(map[string]any)
	if !ok {
		return "", nil
	}
	provider, ok := selection["provider"].(string)
	if !ok {
		return "", nil
	}
	return strings.TrimSpace(provider), nil
}

func removeCrushContextPath(configPath, contextPath string) error {
	cfg, err := readCrushConfig(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	options, ok := cfg["options"].(map[string]any)
	if !ok {
		return nil
	}
	paths := crushStringSlice(options["context_paths"])
	next := paths[:0]
	for _, path := range paths {
		if path != contextPath {
			next = append(next, path)
		}
	}
	options["context_paths"] = next
	cfg["options"] = options
	return writeCrushConfig(configPath, cfg)
}

func readCrushConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]any{}, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func writeCrushConfig(path string, cfg map[string]any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, data, 0o600)
}

func crushConfigObject(cfg map[string]any, key string) map[string]any {
	if obj, ok := cfg[key].(map[string]any); ok {
		return obj
	}
	return map[string]any{}
}

func crushStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		if typed, ok := v.([]string); ok {
			return slices.Clone(typed)
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
