// Package minimaxacp binds MiniMax Code 0.6.5's native ACP server to the shared
// AO Chat transport and renderer. It remains unregistered until native Chat
// conformance is qualified independently of the TUI integration.
// Cancellation is settled through its native delegation-stop receipt before
// AO admits a subsequent prompt; production registration is a separate gate.
package minimaxacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
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
		Harness:                      domain.HarnessMiniMaxCode,
		Configure:                    configure(plugin),
		ValidateInitialize:           validateInitialize,
		AfterInterrupt:               settleInterrupt,
		DecodeProviderConversationID: decodeNativeID,
		RequireAdvertisedOptions:     true,
		LaunchSessionOptions:         launchOptions,
		SessionOptions:               sessionOptions,
		ResolveSessionOption:         resolveSessionOption,
		ValidateTurnSettings:         validateTurnSettings,
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
type driver struct{ ports.ChatDriver }

func (d *driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.FreshIfMissing = false
	return d.ChatDriver.Resume(ctx, cfg)
}

func (d *driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.FreshIfMissing = false
	reconnector, ok := d.ChatDriver.(ports.ChatDriverReconnector)
	if !ok {
		return nil, fmt.Errorf("%w: MiniMax ACP reconnect is unavailable", ports.ErrChatResumeFailed)
	}
	return reconnector.Reconnect(ctx, cfg)
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
		contextFile := filepath.Join(profile, ".ao-chat-context.md")
		if cfg.SystemPrompt != "" {
			// MiniMax reads this once at process start and appends after its native
			// identity. It is neither a transcript turn nor a replacement prompt.
			// Atomically replace one private file on each process launch.
			if err := hookutil.AtomicWriteFile(contextFile, []byte(cfg.SystemPrompt), 0o600); err != nil {
				return nil, nil, err
			}
			args = append(args, "--append-system-prompt-file", contextFile)
		} else if err := os.Remove(contextFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		// Remove per-launch copies left by earlier AO builds, including when
		// the new launch has no standing context. Never recurse into directories.
		entries, err := os.ReadDir(profile)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), ".ao-chat-context-") || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if err := os.Remove(filepath.Join(profile, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
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

const delegationStopMethod = "mcode/session/delegation/stop"

// MiniMax's cancelled prompt can precede release of the runtime's root turn.
// This supported RPC awaits abortSession's released/not-running boundary and
// reports failures and remaining delegates. A cancel notification has no reply.
// Keep the exact native session; never sleep, retry a prompt, or discard errors.
func settleInterrupt(ctx context.Context, request acpdriver.AgentRequest, sessionID string) error {
	raw, err := request(ctx, delegationStopMethod, map[string]string{"sessionId": sessionID})
	if err != nil {
		return fmt.Errorf("MiniMax native stop receipt: %w", err)
	}
	var result struct {
		Receipt *struct {
			SchemaVersion    int       `json:"schemaVersion"`
			RootSessionID    string    `json:"rootSessionId"`
			RootStopped      bool      `json:"rootStopped"`
			ActiveSessionIDs *[]string `json:"activeSessionIds"`
			FailedSessionIDs *[]string `json:"failedSessionIds"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode MiniMax native stop receipt: %w", err)
	}
	r := result.Receipt
	if r == nil || r.SchemaVersion != 1 || r.RootSessionID != sessionID || !r.RootStopped ||
		r.ActiveSessionIDs == nil || r.FailedSessionIDs == nil ||
		len(*r.ActiveSessionIDs) != 0 || len(*r.FailedSessionIDs) != 0 {
		return errors.New("MiniMax native stop did not confirm the exact root and all delegates stopped")
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
		out = append(out, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		out = append(out, acpdriver.SessionOption{ID: "thinkingEffort", Value: effort})
	}
	if settings.Approval != "" {
		out = append(out, acpdriver.SessionOption{ID: "permissionMode", Value: string(ports.NormalizePermissionMode(settings.Approval))})
	}
	return out
}

// A catalog provider/model ID intentionally omits thinking variants. MiniMax
// distinguishes an undefined variant (:u) from its explicit empty variant
// (:v:), which is the normal custom-provider default. Resolve from the provider's
// actual choices rather than inventing one. Preserve the matching current
// selection; a different model must offer exactly one choice or the caller must
// select an explicit native ID from the live picker.
func resolveSessionOption(option acpdriver.SessionOption, catalog []ports.ChatConfigOption) (acpdriver.SessionOption, error) {
	if option.ID != "model" || strings.HasPrefix(option.Value, "m:") {
		return option, nil
	}
	key, err := modelValue(option.Value)
	if err != nil {
		return option, fmt.Errorf("%w: %w", ports.ErrChatConfigOptionInvalid, err)
	}
	prefix := strings.TrimSuffix(key, "u")
	for _, control := range catalog {
		if control.ID != "model" {
			continue
		}
		var matches []string
		for _, choice := range control.Choices {
			if !strings.HasPrefix(choice.Value, prefix) {
				continue
			}
			if _, err := modelValue(choice.Value); err != nil {
				continue
			}
			if choice.Value == control.Current.Select {
				option.Value = choice.Value
				return option, nil
			}
			matches = append(matches, choice.Value)
		}
		if len(matches) == 1 {
			option.Value = matches[0]
			return option, nil
		}
		return option, fmt.Errorf("%w: MiniMax model %q has %d matching native choices %q (current %q); select an explicit advertised model ID", ports.ErrChatConfigOptionInvalid, option.Value, len(matches), matches, control.Current.Select)
	}
	return option, fmt.Errorf("%w: MiniMax session does not advertise model choices", ports.ErrChatConfigOptionInvalid)
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
