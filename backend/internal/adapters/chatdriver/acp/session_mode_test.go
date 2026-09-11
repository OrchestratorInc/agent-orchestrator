package acp

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeLikeSessionMode mirrors claudeacp.claudeSessionMode: the AO approval
// mode only reaches the agent through session/set_mode, and the default mode
// has no mapping.
func claudeLikeSessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeAcceptEdits:
		return "acceptEdits"
	case ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeBypassPermissions:
		return "bypassPermissions"
	default:
		return ""
	}
}

func startClaudeLikeConversation(t *testing.T, agent *fakeAgent, permissions ports.PermissionMode) *conversation {
	t.Helper()
	driver := New(Config{
		Harness:     domain.HarnessClaudeCode,
		Probe:       func(context.Context) error { return nil },
		Launch:      func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionMode: claudeLikeSessionMode,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(),
		Permissions:   permissions,
	})
	if err != nil {
		t.Fatalf("Start(%q): %v", permissions, err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened.(*conversation)
}

func (a *fakeAgent) recordedModeSets() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.modeSets)
}

func conversationPermission(c *conversation) ports.PermissionMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.permissionMode
}

// The session's first session/set_mode must happen at Start even though
// conv.start already seeded permissionMode with the same approval: for
// providers like claude, the setter is the only way the mode reaches the agent.
func TestStartSendsInitialSessionModeThroughSetter(t *testing.T) {
	tests := []struct {
		permission ports.PermissionMode
		want       []string
	}{
		{ports.PermissionModeAcceptEdits, []string{"acceptEdits"}},
		{ports.PermissionModeAuto, []string{"auto"}},
		{ports.PermissionModeBypassPermissions, []string{"bypassPermissions"}},
		{ports.PermissionModeDefault, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.permission), func(t *testing.T) {
			agent := &fakeAgent{newConfig: []acpsdk.SessionConfigOption{
				selectConfigOption("mode", "Mode", "mode", "default", "default", "acceptEdits", "auto", "bypassPermissions"),
			}}
			conv := startClaudeLikeConversation(t, agent, tt.permission)
			if got := agent.recordedModeSets(); !slices.Equal(got, tt.want) {
				t.Fatalf("session/set_mode calls = %v, want %v", got, tt.want)
			}
			if got := conversationPermission(conv); got != tt.permission {
				t.Fatalf("permissionMode = %q, want %q", got, tt.permission)
			}
		})
	}
}

// A session whose catalog does not offer Auto (Claude on Haiku) must not fail a
// turn when approval is raised to Auto, must land in the strictest mode it
// does offer, and AO's own bookkeeping (handed to the permission policy) must
// agree with what the agent is actually running.
func TestSendTurnApprovalAutoNotOfferedFallsBackAndKeepsBookkeepingInStep(t *testing.T) {
	tests := []struct {
		name        string
		catalog     []acpsdk.SessionConfigOption
		start       ports.PermissionMode
		approval    ports.PermissionMode
		wantSets    []string
		wantMode    ports.PermissionMode
		wantAgentIs string
	}{
		{
			name: "raise default to auto on a session that never offered auto",
			catalog: []acpsdk.SessionConfigOption{
				selectConfigOption("mode", "Mode", "mode", "default", "default", "acceptEdits"),
			},
			start: ports.PermissionModeDefault, approval: ports.PermissionModeAuto,
			wantSets: []string{"default"}, wantMode: ports.PermissionModeDefault, wantAgentIs: "default",
		},
		{
			name: "auto is offered so it is honoured",
			catalog: []acpsdk.SessionConfigOption{
				selectConfigOption("mode", "Mode", "mode", "default", "default", "acceptEdits", "auto"),
			},
			start: ports.PermissionModeDefault, approval: ports.PermissionModeAuto,
			wantSets: []string{"auto"}, wantMode: ports.PermissionModeAuto, wantAgentIs: "auto",
		},
		{
			name: "no mode option published asks exactly as before",
			catalog: []acpsdk.SessionConfigOption{
				selectConfigOption("model", "Model", "model", "sonnet", "sonnet", "haiku"),
			},
			start: ports.PermissionModeDefault, approval: ports.PermissionModeAuto,
			wantSets: []string{"auto"}, wantMode: ports.PermissionModeAuto, wantAgentIs: "auto",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &fakeAgent{newConfig: tt.catalog}
			conv := startClaudeLikeConversation(t, agent, tt.start)
			// Strict like claude-agent-acp: only advertised modes are accepted.
			if offered, known := conv.configOptionOffers("mode", "auto"); known && !offered {
				agent.mu.Lock()
				agent.offeredModes = []string{"default", "acceptEdits"}
				agent.mu.Unlock()
			}
			agent.mu.Lock()
			agent.modeSets = nil
			agent.mu.Unlock()

			if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
				Text: "go", Settings: ports.ChatTurnSettings{Approval: tt.approval},
			}); err != nil {
				t.Fatalf("SendTurn: %v", err)
			}
			if got := agent.recordedModeSets(); !slices.Equal(got, tt.wantSets) {
				t.Fatalf("session/set_mode calls = %v, want %v", got, tt.wantSets)
			}
			agent.mu.Lock()
			agentMode := agent.mode
			agent.mu.Unlock()
			if agentMode != tt.wantAgentIs {
				t.Fatalf("agent mode = %q, want %q", agentMode, tt.wantAgentIs)
			}
			if got := conversationPermission(conv); got != tt.wantMode {
				t.Fatalf("permissionMode bookkeeping = %q, want %q", got, tt.wantMode)
			}
		})
	}
}
