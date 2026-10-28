// Package strandsacp contains the unregistered Strands native ACP candidate.
// The released regular CLI cannot broker ACP permissions. Admission therefore
// fails before session/new or session/load; this is not a shipping Chat driver.
package strandsacp

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Driver keeps lifecycle and persistent-host ownership in the shared transport.
// No alternative executable, authored agent, or permission bypass is selected.
type Driver struct{ ports.ChatDriver }

var _ ports.ChatDriver = (*Driver)(nil)
var _ ports.ChatDriverReconnector = (*Driver)(nil)

// NewCandidate binds the existing Strands binary discovery/auth plugin. It is
// intentionally absent from the production registry. Even direct callers are
// rejected before any native session can execute tools: CLI 0.2.0/0.2.1 and
// inspected upstream 1adcaf9f expose no regular-agent requestPermission bridge.
// A protocol version/name match is insufficient to remove this safety gate.
func NewCandidate(plugin nativeacp.Plugin, log *slog.Logger) *Driver {
	return &Driver{ChatDriver: nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessStrands,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityApprovals: false,
			ports.ChatCapabilityInterrupt: false,
			ports.ChatCapabilityResume:    false,
			ports.ChatCapabilityTools:     false,
		},
		Configure: configure,
		ValidateInitialize: func(acpsdk.InitializeResponse) error {
			return admissionError()
		},
		ValidateTurnSettings: validateSettings,
	}, log)}
}

func admissionError() error {
	return fmt.Errorf("%w: Strands regular CLI ACP has no qualified permission broker, executing-tool cancellation, or fresh-instruction restore contract", ports.ErrChatDriverIncompatible)
}

// Probe preserves discovery/auth errors, but never advertises capabilities the
// known native server cannot enforce. Configured credentials are not promoted
// to verified authorization.
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if _, err := d.ChatDriver.Probe(ctx); err != nil {
		return nil, err
	}
	return nil, admissionError()
}

// Resume never honors a fresh-on-missing fallback for this candidate.
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.FreshIfMissing = false
	return d.ChatDriver.Resume(ctx, cfg)
}

// Reconnect uses only the shared host's reconnect path; it cannot launch a
// replacement native process when its detached owner is absent.
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !filepath.IsAbs(cfg.DataDir) || !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, nil, fmt.Errorf("%w: absolute AO data and workspace paths are required", ports.ErrChatDriverUnavailable)
	}
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		// Saved full-agent snapshots can replace launch-time instructions. Do
		// not disguise prompt concatenation or an ignored option as delivery.
		return nil, nil, fmt.Errorf("%w: Strands ACP additive instructions on exact restore are unqualified", ports.ErrChatCapabilityUnavailable)
	}
	if err := validateSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model, Approval: cfg.Permissions}); err != nil {
		return nil, nil, err
	}
	return []string{"--acp-server", "--session", "on", "--set", "session.dir=" + filepath.Join(cfg.DataDir, "agents", "strands", "sessions")}, nil, nil
}

func validateSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if initial != "" && initial != ports.PermissionModeDefault || settings.Approval != "" && settings.Approval != ports.PermissionModeDefault {
		return fmt.Errorf("%w: Strands ACP permission mode is unqualified", ports.ErrChatPermissionModeUnsupported)
	}
	if settings.Model != "" || settings.Effort != "" {
		return fmt.Errorf("%w: Strands ACP live model and effort controls are unsupported", ports.ErrChatCapabilityUnavailable)
	}
	return nil
}
