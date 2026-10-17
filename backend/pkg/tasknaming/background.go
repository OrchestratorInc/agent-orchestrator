package tasknaming

import (
	"context"
	"errors"
	"maps"
	"path/filepath"

	"github.com/google/uuid"

	chatregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// BackgroundConfig supplies a provider's credentials and disposable workspace.
// The caller owns the workspace and applies its own deadline and concurrency limit.
type BackgroundConfig struct {
	Harness       string
	DataDir       string
	WorkspacePath string
	Model         string
	Env           map[string]string
}

// Supported reports whether the shipped Chat drivers support this harness.
// It does not probe installation or credentials.
func Supported(harness string) bool {
	return chatregistry.Build(nil, nil).SupportsChat(domain.AgentHarness(harness))
}

// RunBackground uses the same ephemeral Chat provider turn as local task naming.
// It creates no durable AO conversation or session controller.
func RunBackground(ctx context.Context, cfg BackgroundConfig, systemPrompt, prompt string) (string, error) {
	if !filepath.IsAbs(cfg.DataDir) {
		return "", errors.New("background task data dir must be absolute")
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return "", errors.New("background task workspace path must be absolute")
	}
	harness := domain.AgentHarness(cfg.Harness)
	permissions := ports.PermissionModeAcceptEdits
	if harness == domain.HarnessKimi {
		permissions = ports.PermissionModeDefault
	}
	env := maps.Clone(cfg.Env)
	if env == nil {
		env = make(map[string]string)
	}
	// Prevent inherited worker hooks and approval bypasses in this cosmetic call.
	env["AO_SESSION_ID"] = ""
	env["AO_PERMISSION_MODE"] = string(permissions)
	service := chat.New(chat.Options{Drivers: chatregistry.Build(nil, nil), NewID: uuid.NewString})
	return service.RunBackgroundTask(ctx, harness, ports.ChatStartConfig{
		DataDir:       cfg.DataDir,
		WorkspacePath: cfg.WorkspacePath,
		Model:         cfg.Model,
		Env:           env,
		Permissions:   permissions,
		SystemPrompt:  systemPrompt,
	}, prompt)
}
