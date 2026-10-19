// Package hermesacp provides an explicitly unregistered Hermes ACP candidate.
// Transport, turns, approvals, interruption, Stop, and detached host ownership
// are implemented by nativeacp/persistenthost, not a second provider transport.
package hermesacp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Driver keeps Hermes-specific admission checks around the shared ACP driver.
// It intentionally implements no handoff or production registry entry. An
// operator must provision native config/credentials/plugins in its isolated
// profile; it never modifies or silently substitutes the user's source profile.
type Driver struct{ ports.ChatDriver }

var _ ports.ChatDriver = (*Driver)(nil)
var _ ports.ChatDriverReconnector = (*Driver)(nil)

// New uses the existing Hermes plugin's exact binary and auth discovery. Native
// defaults remain native-owned. Configured credentials are not authorization.
// Constructor structure follows the independently authored PR6635 binding;
// instruction delivery and permission policy deliberately use qualified seams.
func New(plugin nativeacp.Plugin, log *slog.Logger) *Driver {
	return &Driver{ChatDriver: nativeacp.New(plugin, nativeacp.Config{
		Harness:              domain.HarnessHermes,
		Configure:            configure,
		ValidateInitialize:   validateInitialize,
		SessionMode:          func(ports.PermissionMode) string { return "default" },
		ValidateTurnSettings: validateTurnSettings,
		ValidateConfigOption: func(string, ports.ChatConfigOptionValue) error {
			return fmt.Errorf("%w: Hermes candidate native settings are not qualified", ports.ErrChatConfigOptionInvalid)
		},
	}, log)}
}

// Start rejects safety guarantees Hermes ACP cannot enforce. A provider's
// approval vocabulary is not a filesystem/network sandbox or ephemeral storage.
func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if cfg.ReadOnly || cfg.DisableNetwork || cfg.Ephemeral {
		return nil, fmt.Errorf("%w: Hermes candidate cannot enforce read-only, network-isolated or ephemeral sessions", ports.ErrChatDriverIncompatible)
	}
	return d.ChatDriver.Start(ctx, cfg)
}

func profilePath(data string, session domain.SessionID) (string, error) {
	if !filepath.IsAbs(data) || session == "" {
		return "", fmt.Errorf("hermes ACP: absolute AO data directory and session required")
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(session)))
	return filepath.Join(data, "agents", "hermes-acp", key), nil
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	home, err := profilePath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return nil, nil, err
	}
	// Only the exact AO-owned session profile may be mutated. In particular,
	// never resolve HERMES_HOME through the user's shell/default profile.
	if filepath.Clean(cfg.Env["HERMES_HOME"]) != home {
		return nil, nil, fmt.Errorf("hermes ACP: provision the isolated session profile at %s", home)
	}
	for _, name := range []string{"active_profile"} {
		raw, err := os.ReadFile(filepath.Join(home, name))
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		if len(raw) > 0 {
			return nil, nil, fmt.Errorf("hermes ACP: profile redirection unsupported")
		}
	}
	if os.Getenv("HERMES_MANAGED_DIR") != "" || cfg.Env["HERMES_MANAGED_DIR"] != "" {
		return nil, nil, fmt.Errorf("hermes ACP: managed configuration unsupported")
	}
	if st, err := os.Stat("/etc/hermes"); err == nil && st.IsDir() {
		return nil, nil, fmt.Errorf("hermes ACP: system managed configuration unsupported")
	}
	if err := prepareLaunchProfile(ctx, home, cfg.SystemPrompt); err != nil {
		return nil, nil, err
	}
	return []string{"acp"}, map[string]string{"HERMES_HOME": home}, nil
}

func validateTurnSettings(_ ports.PermissionMode, s ports.ChatTurnSettings) error {
	if s.Model != "" || s.Effort != "" || (s.Approval != "" && s.Approval != ports.PermissionModeDefault) {
		return fmt.Errorf("%w: Hermes candidate supports native default model/effort and default approvals only", acpdriver.ErrACPSetterUnsupported)
	}
	return nil
}

// Resume never permits fresh fallback. Validate native-owned exact history
// before shared transport launch; this is a guard, not replay completeness
// proof. The candidate remains gated until native replay failures are qualified.
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ReadOnly {
		return nil, fmt.Errorf("%w: Hermes cannot enforce read-only restore", ports.ErrChatResumeFailed)
	}
	home, err := profilePath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	if err := ValidateHistory(ctx, home, cfg.ProviderConversationID, cfg.WorkspacePath); err != nil {
		return nil, err
	}
	cfg.FreshIfMissing = false
	return d.ChatDriver.Resume(ctx, cfg)
}

// Reconnect only adopts the surviving persistent host. It must not mutate
// plugins or require settled disk history while an accepted turn is active.
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ReadOnly {
		return nil, fmt.Errorf("%w: Hermes cannot enforce read-only reconnect", ports.ErrChatRecoveryInconclusive)
	}
	reconnector, ok := d.ChatDriver.(ports.ChatDriverReconnector)
	if !ok {
		return nil, ports.ErrChatRecoveryInconclusive
	}
	cfg.FreshIfMissing = false
	return reconnector.Reconnect(ctx, cfg)
}

// Admission is intentionally source/version scoped until additional releases
// pass the same native plugin/history/lifecycle contract.
func validateInitialize(init acpsdk.InitializeResponse) error {
	if init.AgentInfo == nil || init.AgentInfo.Name != "hermes-agent" || init.AgentInfo.Version != "0.21.6" || !init.AgentCapabilities.LoadSession {
		return fmt.Errorf("%w: Hermes candidate requires hermes-agent 0.21.6 with session/load", ports.ErrChatDriverIncompatible)
	}
	return nil
}
