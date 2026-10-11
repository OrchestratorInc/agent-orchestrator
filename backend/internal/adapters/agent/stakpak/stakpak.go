// Package stakpak prepares the native v0.3.88 TUI contract. It is intentionally
// unregistered: permission admission, current identity and restore are unqualified.
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
var ErrUnqualified = errors.New("stakpak TUI permission, identity and activity contracts require native qualification")

// Plugin provides independent metadata while refusing unqualified execution.
type Plugin struct { agentbase.Base }

func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentExitDetector = (*Plugin)(nil)

func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID: "stakpak", Name: "Stakpak", Version: "0.1.0",
		Description: "Unregistered source preparation for native Stakpak TUI v0.3.88.",
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
	if err := ctx.Err(); err != nil { return ports.AgentAuthStatusUnknown, err }
	return ports.AgentAuthStatusUnknown, nil
}

func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	return nil, ErrUnqualified
}

func (*Plugin) GetRestoreCommand(ctx context.Context, _ ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil { return nil, false, err }
	return nil, false, ErrUnqualified
}

func (*Plugin) GetAgentHooks(ctx context.Context, _ ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil { return err }
	return ErrUnqualified
}

// SessionInfo deliberately inherits unavailable from Base: AO metadata alone
// does not prove the current native conversation after native session switches.
