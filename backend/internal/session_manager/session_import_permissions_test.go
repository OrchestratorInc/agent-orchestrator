package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// permissionModesLauncher reports the permission modes each harness accepts;
// nothing else is called.
type permissionModesLauncher struct {
	ChatLauncher
	modes map[domain.AgentHarness][]ports.PermissionMode
}

func (l permissionModesLauncher) PermissionModes(_ context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error) {
	return l.modes[harness], nil
}

// An import never chose a permission mode. Pi cannot ask for approval, so an
// imported Pi chat must open bypassing permissions, as a new Pi chat does,
// instead of failing on the project default.
func TestImportedChatPermissionsFollowTheHarness(t *testing.T) {
	m := &Manager{chat: permissionModesLauncher{modes: map[domain.AgentHarness][]ports.PermissionMode{
		domain.HarnessPi:         {ports.PermissionModeBypassPermissions},
		domain.HarnessClaudeCode: {ports.PermissionModeAuto, ports.PermissionModeBypassPermissions},
	}}}
	imported := func(harness domain.AgentHarness) domain.SessionRecord {
		rec := domain.SessionRecord{Harness: harness}
		rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "n1"}
		return rec
	}
	ctx := context.Background()
	if got := m.importedChatPermissions(ctx, imported(domain.HarnessPi), ports.PermissionModeAuto); got != ports.PermissionModeBypassPermissions {
		t.Fatalf("imported Pi chat permissions = %q, want bypass", got)
	}
	if got := m.importedChatPermissions(ctx, imported(domain.HarnessClaudeCode), ports.PermissionModeAuto); got != ports.PermissionModeAuto {
		t.Fatalf("a mode the harness accepts was replaced: %q", got)
	}
	chosen := imported(domain.HarnessPi)
	chosen.Metadata.Permissions = ports.PermissionModeAuto
	if got := m.importedChatPermissions(ctx, chosen, ports.PermissionModeAuto); got != ports.PermissionModeAuto {
		t.Fatalf("a mode the session chose was replaced: %q", got)
	}
	if got := m.importedChatPermissions(ctx, domain.SessionRecord{Harness: domain.HarnessPi}, ports.PermissionModeAuto); got != ports.PermissionModeAuto {
		t.Fatalf("a native session's mode was replaced: %q", got)
	}
}
