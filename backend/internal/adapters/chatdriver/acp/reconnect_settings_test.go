package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A live reconnect rebuilds the catalog from the host's session/new snapshot,
// which predates initial_settings and every change since. Left as is, the
// picker showed the agent's launch default as the session's permission mode
// after every daemon restart, while the provider still ran the mode AO had
// applied (#6758). The restored settings move the catalog without a provider
// round-trip; a value the catalog does not offer stays where the snapshot had it.
func TestPersistentACPReconnectShowsRestoredSettingsAsCurrent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		permissions ports.PermissionMode
		model       string
		effort      string
		want        map[string]string
	}{
		{
			name: "offered", permissions: ports.PermissionModeBypassPermissions, model: "opus", effort: "high",
			want: map[string]string{"mode": "bypassPermissions", "model": "opus", "effort": "high"},
		},
		{
			name: "not offered", permissions: ports.PermissionModeAuto, model: "haiku",
			want: map[string]string{"mode": "default", "model": "default", "effort": "default"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initialize, err := json.Marshal(acpsdk.InitializeResponse{
				ProtocolVersion: acpsdk.ProtocolVersionNumber,
				AgentCapabilities: acpsdk.AgentCapabilities{
					SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			session, err := json.Marshal(acpsdk.NewSessionResponse{
				SessionId: "provider-session",
				ConfigOptions: []acpsdk.SessionConfigOption{
					selectConfigOption("mode", "Mode", "mode", "default", "default", "acceptEdits", "bypassPermissions"),
					selectConfigOption("model", "Model", "model", "default", "default", "opus", "sonnet"),
					selectConfigOption("effort", "Effort", "thought_level", "default", "default", "low", "high"),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			daemon, host := net.Pipe()
			t.Cleanup(func() { _ = host.Close() })
			driver := New(Config{
				Harness: domain.HarnessClaudeCode,
				Capabilities: ports.ChatCapabilities{
					ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true,
				},
				Launch: func(context.Context, LaunchConfig) (Launch, error) {
					return Launch{Command: "fake"}, nil
				},
				SessionMode: func(mode ports.PermissionMode) string {
					switch mode {
					case ports.PermissionModeBypassPermissions:
						return "bypassPermissions"
					case ports.PermissionModeAuto:
						return "auto"
					}
					return ""
				},
				SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
					var options []SessionOption
					if settings.Model != "" {
						options = append(options, SessionOption{ID: "model", Value: settings.Model})
					}
					if settings.Effort != "" {
						options = append(options, SessionOption{ID: "effort", Value: settings.Effort})
					}
					return options
				},
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			driver.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
				return &persistenthost.Transport{
					Stdin: daemon, Stdout: daemon, Reconnected: true,
					ACPState: &persistenthost.ACPState{
						InitializeResult: initialize, SessionResult: session,
						SessionID: "provider-session",
					},
				}, nil
			}

			opened, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
				SessionID: "ao-session", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
				ProviderConversationID: "provider-session", ProviderScopeID: "scope",
				Permissions: tc.permissions, Model: tc.model, Effort: tc.effort,
			})
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			t.Cleanup(func() { _ = opened.Close() })
			controller, ok := opened.(ports.ChatConfigOptionController)
			if !ok {
				t.Fatal("conversation does not implement ChatConfigOptionController")
			}
			options, err := controller.ListConfigOptions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, option := range options {
				got[option.ID] = option.Current.Select
			}
			for id, want := range tc.want {
				if got[id] != want {
					t.Errorf("%s current = %q, want %q (catalog %v)", id, got[id], want, got)
				}
			}
		})
	}
}
