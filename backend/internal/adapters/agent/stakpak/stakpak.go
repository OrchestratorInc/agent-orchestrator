// Package stakpak prepares the native v0.3.88 TUI contract. It is intentionally
// unregistered: native shell cancellation leaks descendants, and restored
// standing instructions accumulate in persisted history.
package stakpak

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrUnqualified is a hard refusal, preventing manager restore fallback to fresh.
var ErrUnqualified = errors.New("stakpak v0.3.88 TUI is unqualified: shell descendants survive cancellation and restored instructions accumulate")

// Plugin provides independent metadata while refusing unqualified execution.
type Plugin struct{ agentbase.Base }

// New returns an unregistered adapter whose execution paths remain refused.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentExitDetector = (*Plugin)(nil)

// Manifest describes the guarded source contract without registering it.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID: "stakpak", Name: "Stakpak", Version: "0.1.0",
		Description:  "Unregistered source preparation for native Stakpak TUI v0.3.88.",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// ResolveBinary discovers a name on PATH; it does not attest version or identity.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{
		Label: "stakpak", Names: []string{"stakpak"}, WinNames: []string{"stakpak.exe"},
	})
}

// AuthStatus cannot infer effective profile credentials or authorization.
func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return ports.AgentAuthStatusUnknown, nil
}

// ExitDetectionMode delegates eventual process-exit observation to the supervisor.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// GetLaunchCommand refuses execution until native cancellation is qualified.
func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnqualified
}

// GetRestoreCommand hard-errors rather than permitting fresh-history fallback.
func (*Plugin) GetRestoreCommand(ctx context.Context, _ ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, ErrUnqualified
}

// GetAgentHooks refuses preparation of an unqualified runtime.
func (*Plugin) GetAgentHooks(ctx context.Context, _ ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnqualified
}

// SessionInfo deliberately inherits unavailable from Base: AO metadata alone
// does not prove the current native conversation after native session switches.

// GetPromptDeliveryStrategy records the observed native TUI contract. Public
// launch remains refused; accepting a positional task does not submit it.
func (*Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}
