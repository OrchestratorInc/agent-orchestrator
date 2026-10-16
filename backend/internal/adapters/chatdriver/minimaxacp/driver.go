// Package minimaxacp binds MiniMax Code 0.6.5's native ACP server to the shared
// AO Chat transport and renderer. It remains unregistered until native Chat
// conformance is qualified independently of the TUI integration.
package minimaxacp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin reuses the existing native executable, auth probe and bounded private
// profile snapshot. AO never downloads or substitutes an ACP executable.
type Plugin interface {
	nativeacp.Plugin
	PrepareChatProfile(context.Context, string, string, map[string]string) (string, error)
}

// New constructs a candidate binding. Production registration is a separate
// release gate; constructing a driver alone does not establish native support.
func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	return &driver{ChatDriver: nativeacp.New(plugin, nativeacp.Config{
		Harness:                  domain.HarnessMiniMaxCode,
		Configure:                configure(plugin),
		ValidateInitialize:       validateInitialize,
		DecodeProviderConversationID: decodeNativeID,
		RequireAdvertisedOptions: true,
		LaunchSessionOptions:     launchOptions,
		SessionOptions:           sessionOptions,
		ValidateTurnSettings:     validateTurnSettings,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityImages:          false,
			ports.ChatCapabilityEmbeddedContext: false,
			ports.ChatCapabilitySteer:           false,
			ports.ChatCapabilityCompaction:      false,
			ports.ChatCapabilityRollback:        false,
		},
	}, log)}
}

// Keep exact restore strict even when a generic caller permits a fresh session
// for an unstarted conversation. MiniMax's native identity must never change.
type driver struct { ports.ChatDriver }

func (d *driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.FreshIfMissing = false
	return d.ChatDriver.Resume(ctx, cfg)
}

func (d *driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.FreshIfMissing = false
	return d.ChatDriver.(ports.ChatDriverReconnector).Reconnect(ctx, cfg)
}

func configure(plugin Plugin) nativeacp.Configure {
	return func(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model, Approval: cfg.Permissions}); err != nil {
			return nil, nil, err
		}
		if cfg.SystemPrompt != "" && (len(cfg.SystemPrompt) > 512<<10 || strings.TrimSpace(cfg.SystemPrompt) == "") {
			return nil, nil, errors.New("MiniMax Chat standing instructions must be nonempty and bounded")
		}
		profile, err := plugin.PrepareChatProfile(ctx, cfg.DataDir, string(cfg.SessionID), cfg.Env)
		if err != nil {
			return nil, nil, err
		}
		args := []string{"acp"}
		if cfg.SystemPrompt != "" {
			// MiniMax reads this once at process start and appends after its native
			// identity. It is neither a transcript turn nor a replacement prompt.
			// A replacement process gets a fresh file, including after exact load.
			file, err := os.CreateTemp(profile, ".ao-chat-context-*.md")
			if err != nil {
				return nil, nil, err
			}
			_, writeErr := file.WriteString(cfg.SystemPrompt)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = os.Remove(file.Name())
				return nil, nil, errors.Join(writeErr, closeErr)
			}
			args = append(args, "--append-system-prompt-file", file.Name())
		}
		return args, map[string]string{
			"MINIMAX_DATA_DIR": profile, "MAVIS_DATA_DIR": profile,
		}, nil
	}
}

func validateInitialize(init acpsdk.InitializeResponse) error {
	if init.AgentInfo == nil || init.AgentInfo.Name != "minimax-code" || init.AgentInfo.Version != "0.6.5" ||
		init.ProtocolVersion != acpsdk.ProtocolVersionNumber || !init.AgentCapabilities.LoadSession {
		return errors.New("MiniMax Chat requires MiniMax Code 0.6.5 ACP v1 with exact session/load")
	}
	return nil
}

// ACP's modes are default/plan, NOT permission policies. The permissionMode
// select control is process-scoped; each AO Chat session owns its own process.
// A required launch option fails closed even if the provider omits the setter.
func launchOptions(cfg acpdriver.LaunchConfig) []acpdriver.SessionOption {
	return []acpdriver.SessionOption{{ID: "permissionMode", Value: string(ports.NormalizePermissionMode(cfg.Permissions))}}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var out []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		value, _ := modelValue(model) // validated before launch/turn dispatch
		out = append(out, acpdriver.SessionOption{ID: "model", Value: value})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		out = append(out, acpdriver.SessionOption{ID: "thinkingEffort", Value: effort})
	}
	if settings.Approval != "" {
		out = append(out, acpdriver.SessionOption{ID: "permissionMode", Value: string(ports.NormalizePermissionMode(settings.Approval))})
	}
	return out
}

func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	mode := initial
	if settings.Approval != "" {
		mode = settings.Approval
	}
	switch mode {
	case "", ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
	default:
		return fmt.Errorf("%w: MiniMax Chat supports default, auto, and bypassPermissions; acceptEdits has no native equivalent", ports.ErrChatConfigOptionInvalid)
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		if _, err := modelValue(model); err != nil {
			return fmt.Errorf("%w: %w", ports.ErrChatConfigOptionInvalid, err)
		}
	}
	return nil
}

// Agent Harness/New Task models use provider/model. ACP uses its advertised
// escaped selection IDs; preserve those verbatim for the live config picker.
func modelValue(model string) (string, error) {
	if strings.HasPrefix(model, "m:") {
		parts := strings.Split(model, ":")
		if (len(parts) == 4 && parts[3] == "u") || (len(parts) == 5 && parts[3] == "v") {
			for _, part := range parts[1:] {
				if _, err := url.PathUnescape(part); err != nil {
					return "", errors.New("invalid MiniMax ACP model selection")
				}
			}
			if parts[1] != "" && parts[2] != "" {
				return model, nil
			}
		}
		return "", errors.New("invalid MiniMax ACP model selection")
	}
	provider, id, ok := strings.Cut(model, "/")
	if !ok || provider == "" || id == "" {
		return "", errors.New("MiniMax Chat model must be a configured provider/model ID")
	}
	return "m:" + encodeComponent(provider) + ":" + encodeComponent(id) + ":u", nil
}

// Match JavaScript encodeURIComponent, including its punctuation safe set.
func encodeComponent(value string) string {
	var out strings.Builder
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-_.!~*'()", rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}

var nativeIDPattern = regexp.MustCompile(`^mvs_[a-f0-9]{32}$`)

func decodeNativeID(id string) (string, error) {
	if !nativeIDPattern.MatchString(id) {
		return "", errors.New("MiniMax Chat restore requires an exact native mvs_ session ID")
	}
	return id, nil
}
