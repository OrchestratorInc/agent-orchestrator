package coderagents

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type forbiddenConnectPlugin struct{}

func (forbiddenConnectPlugin) Connect(context.Context, map[string]string) (*coderapi.Client, coderapi.Credentials, error) {
	panic("an unsupported Chat driver must not contact the provider")
}

func TestPublicDriverRefusesUnrestrictedServerTools(t *testing.T) {
	for _, tc := range []struct {
		name         string
		permissions  ports.PermissionMode
		readOnly     bool
		disableTools bool
	}{
		{name: "default"},
		{name: "accept-edits", permissions: ports.PermissionModeAcceptEdits},
		{name: "bypass", permissions: ports.PermissionModeBypassPermissions},
		{name: "read-only", readOnly: true},
		{name: "text-only", disableTools: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := New(forbiddenConnectPlugin{}, nil)
			if caps, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverIncompatible) || len(caps) != 0 {
				t.Fatalf("Probe = %v, %v; want incompatible with no capabilities", caps, err)
			}
			if conv, err := d.Start(context.Background(), ports.ChatStartConfig{
				WorkspacePath: t.TempDir(), Permissions: tc.permissions,
				ReadOnly: tc.readOnly, DisableTools: tc.disableTools,
			}); conv != nil || !errors.Is(err, ports.ErrChatDriverIncompatible) {
				t.Fatalf("Start = %v, %v; want incompatible before any provider request", conv, err)
			}
			if conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{
				WorkspacePath: t.TempDir(), Permissions: tc.permissions, ReadOnly: tc.readOnly,
				ProviderConversationID: "8f9d2f8a-3a43-4a5c-9b0e-1f1f2a3b4c5d",
			}); conv != nil || !errors.Is(err, ports.ErrChatDriverIncompatible) {
				t.Fatalf("Resume = %v, %v; want incompatible before any provider request", conv, err)
			}
		})
	}
}

func TestEphemeralTasksNeverCreatePersistentChats(t *testing.T) {
	for _, driver := range []ports.ChatDriver{
		New(forbiddenConnectPlugin{}, nil),
		newDriver(forbiddenConnectPlugin{}, nil),
	} {
		conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
			WorkspacePath: t.TempDir(), Ephemeral: true, ReadOnly: true, DisableTools: true,
		})
		if conv != nil || !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("ephemeral Start = %v, %v; want refusal before creating any native chat", conv, err)
		}
	}
}
