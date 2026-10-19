// Package mastrasdk is an unregistered candidate Chat driver over the public
// Mastra Code SDK. It is not stock mastracode ACP and is not qualified for
// production. The shared ACP driver owns transport, persistent host, lifecycle,
// permissions, interruption and durable AO projection.
package mastrasdk

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

//go:embed bridge.mjs entry.mjs loader.mjs
var source embed.FS

// NewCandidate constructs the actual ports-compatible driver without adding it
// to the shipped registry. Native/AO/UI qualification is still mandatory.
func NewCandidate(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return &candidateDriver{Driver: acpdriver.New(binding(plugin), log)}
}

type candidateDriver struct{ *acpdriver.Driver }

var _ ports.ChatDriver = (*candidateDriver)(nil)
var _ ports.ChatDriverReconnector = (*candidateDriver)(nil)

func (d *candidateDriver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if cfg.ReadOnly || cfg.DisableNetwork || cfg.Ephemeral || len(cfg.AdditionalDirectories) != 0 || len(cfg.MCPServers) != 0 {
		return nil, fmt.Errorf("%w: Mastra SDK candidate does not enforce sandbox, ephemeral, extra-directory or client MCP settings", ports.ErrChatCapabilityUnavailable)
	}
	return d.Driver.Start(ctx, cfg)
}

func validateResume(cfg ports.ChatResumeConfig) error {
	if cfg.FreshIfMissing || strings.TrimSpace(cfg.ProviderConversationID) == "" {
		return fmt.Errorf("%w: Mastra SDK restore requires an exact native identity without fresh fallback", ports.ErrChatResumeFailed)
	}
	if cfg.ReadOnly || len(cfg.AdditionalDirectories) != 0 || len(cfg.MCPServers) != 0 {
		return fmt.Errorf("%w: Mastra SDK candidate cannot enforce requested restore settings", ports.ErrChatCapabilityUnavailable)
	}
	return nil
}

func (d *candidateDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if err := validateResume(cfg); err != nil {
		return nil, err
	}
	return d.Driver.Resume(ctx, cfg)
}

func (d *candidateDriver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if err := validateResume(cfg); err != nil {
		return nil, err
	}
	return d.Driver.Reconnect(ctx, cfg)
}

func binding(plugin nativeacp.Plugin) acpdriver.Config {
	return acpdriver.Config{
		Harness: domain.HarnessMastraCode,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityTools:     true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilityHistory:   true,
			ports.ChatCapabilityModels:    true,
		},
		Probe: func(ctx context.Context) error {
			if _, err := plugin.ResolveBinary(ctx); err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			if _, err := exec.LookPath("node"); err != nil {
				return fmt.Errorf("%w: Node runtime required: %w", ports.ErrChatDriverUnavailable, err)
			}
			status, err := plugin.AuthStatus(ctx)
			if err == nil && status == ports.AgentAuthStatusUnauthorized {
				return ports.ErrChatAuthRequired
			}
			return nil // Configured/unknown auth is not promoted to authorized.
		},
		ValidateTurnSettings: validateSettings,
		ValidateInitialize:   validateInitialize,
		Launch: func(ctx context.Context, cfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
			if err := ctx.Err(); err != nil {
				return acpdriver.Launch{}, err
			}
			if err := validateSettings(cfg.Permissions, ports.ChatTurnSettings{}); err != nil {
				return acpdriver.Launch{}, err
			}
			if !filepath.IsAbs(cfg.DataDir) || !filepath.IsAbs(cfg.WorkspacePath) || cfg.SessionID == "" {
				return acpdriver.Launch{}, fmt.Errorf("Mastra SDK requires an absolute data/workspace path and session owner")
			}
			if len(cfg.SystemPrompt) > 512*1024 {
				return acpdriver.Launch{}, fmt.Errorf("Mastra host instructions exceed 512 KiB")
			}
			for _, key := range []string{"MASTRA_APP_DATA_DIR", "MASTRA_DB_PATH", "MASTRA_DB_URL", "MASTRA_STORAGE_BACKEND", "MASTRACODE_YOLO"} {
				if cfg.Env[key] != "" || os.Getenv(key) != "" {
					return acpdriver.Launch{}, fmt.Errorf("Mastra SDK candidate refuses native storage/approval override %s", key)
				}
			}
			native, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			native, err = filepath.EvalSymlinks(native)
			if err != nil {
				return acpdriver.Launch{}, err
			}
			// Require the real Node entrypoint. Shell/cmd wrappers need explicit
			// qualification; never parse or execute their text to guess an SDK.
			if !filepath.IsAbs(native) || !strings.HasSuffix(native, ".js") {
				return acpdriver.Launch{}, fmt.Errorf("%w: Mastra SDK candidate requires a resolved native JavaScript entrypoint", ports.ErrChatDriverIncompatible)
			}
			node, err := exec.LookPath("node")
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			root := filepath.Join(cfg.DataDir, "mastra-code", "chat", fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.SessionID))))
			module, err := installBridge(root)
			if err != nil {
				return acpdriver.Launch{}, err
			}
			content, err := json.Marshal(struct {
				Workspace    string `json:"workspace"`
				Owner        string `json:"owner"`
				Instructions string `json:"hostInstructions"`
			}{cfg.WorkspacePath, string(cfg.SessionID), cfg.SystemPrompt})
			if err != nil {
				return acpdriver.Launch{}, err
			}
			config := filepath.Join(root, fmt.Sprintf("host-%x.json", sha256.Sum256(content)))
			if err := writePrivate(config, content); err != nil {
				return acpdriver.Launch{}, err
			}
			env := make(map[string]string, len(cfg.Env)+2)
			for key, value := range cfg.Env {
				env[key] = value
			}
			env["MASTRA_APP_DATA_DIR"] = filepath.Join(root, "profile")
			env["MASTRA_DB_PATH"] = filepath.Join(root, "history.db")
			env["MASTRA_OBSERVABILITY_DB_PATH"] = filepath.Join(root, "observability.db")
			env["MASTRA_PLANS_DIR"] = filepath.Join(root, "plans")
			return acpdriver.Launch{Command: node, Args: []string{module, native, config}, Env: env}, nil
		},
	}
}

func validateInitialize(response acpsdk.InitializeResponse) error {
	if response.AgentInfo == nil || response.AgentInfo.Name != "ao-mastra-sdk-candidate" || response.AgentInfo.Version != "1.11.0" || !response.AgentCapabilities.LoadSession {
		return fmt.Errorf("Mastra SDK candidate identity or exact-load contract changed")
	}
	return nil
}

func validateSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	for _, mode := range []ports.PermissionMode{initial, settings.Approval} {
		if mode != "" && mode != ports.PermissionModeDefault {
			return fmt.Errorf("%w: Mastra SDK candidate supports manual native approvals only", ports.ErrChatPermissionModeUnsupported)
		}
	}
	if settings.Effort != "" {
		return fmt.Errorf("%w: Mastra SDK candidate does not implement effort settings", ports.ErrChatCapabilityUnavailable)
	}
	return nil
}

func installBridge(root string) (string, error) {
	bridge, _ := source.ReadFile("bridge.mjs")
	entry, _ := source.ReadFile("entry.mjs")
	loader, _ := source.ReadFile("loader.mjs")
	version := fmt.Sprintf("%x", sha256.Sum256(append(append(append([]byte(nil), bridge...), entry...), loader...)))
	dir := filepath.Join(root, "bridge-"+version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for name, content := range map[string][]byte{"bridge.mjs": bridge, "entry.mjs": entry, "loader.mjs": loader} {
		if err := writePrivate(filepath.Join(dir, name), content); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "entry.mjs"), nil
}

func writePrivate(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("Mastra owned runtime file has unsafe type or permissions: %s", filepath.Base(path))
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(existing) != string(content) {
			return fmt.Errorf("Mastra owned runtime file changed: %s", filepath.Base(path))
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
