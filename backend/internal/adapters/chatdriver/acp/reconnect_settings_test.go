package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

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
		modelName   string
		effort      string
		want        map[string]string
	}{
		{
			name: "offered", permissions: ports.PermissionModeBypassPermissions, model: "opus", effort: "high",
			want: map[string]string{"mode": "bypassPermissions", "model": "opus", "effort": "high"},
		},
		{
			// Claude Code offers claude-opus-5-5 as "opus"; applyTurnSettings
			// finds it by name, so the reconnect does too.
			name: "offered under another value", model: "claude-opus-5-5", modelName: "opus",
			want: map[string]string{"model": "opus"},
		},
		{
			name: "not offered", permissions: ports.PermissionModeAuto, model: "haiku",
			want: map[string]string{"mode": "default", "model": "default", "effort": "default"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, _ := liveReconnect(t, ports.ChatResumeConfig{
				Permissions: tc.permissions, Model: tc.model, Effort: tc.effort,
			}, tc.modelName)
			options, err := conv.ListConfigOptions(context.Background())
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

// The restored settings may be a choice stored after the last turn and never
// sent: the provider receives settings only with a turn. So the first turn
// after a live reconnect sends them even though the catalog names them
// current, or a stored model the provider never acknowledged would never
// reach it. Later turns skip settings the session already has again.
func TestPersistentACPReconnectSendsRestoredSettingsOnTheNextTurn(t *testing.T) {
	conv, agent := liveReconnect(t, ports.ChatResumeConfig{Model: "opus", Effort: "high"}, "")
	settings := ports.ChatTurnSettings{Model: "opus", Effort: "high"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The controller activates a live reconnect before its first turn; that
	// releases the provider's replies to this attachment.
	if err := conv.ActivateLiveReconnect(ctx, ""); err != nil {
		t.Fatalf("ActivateLiveReconnect: %v", err)
	}
	if err := conv.applyTurnSettings(ctx, settings); err != nil {
		t.Fatalf("first turn settings: %v", err)
	}
	agent.mu.Lock()
	calls, model, effort := agent.setCalls, agent.options["model"], agent.options["effort"]
	agent.mu.Unlock()
	if calls != 2 || model != "opus" || effort != "high" {
		t.Fatalf("first turn after reconnect sent %d settings (model %q, effort %q), want model opus and effort high sent", calls, model, effort)
	}
	if err := conv.applyTurnSettings(ctx, settings); err != nil {
		t.Fatalf("second turn settings: %v", err)
	}
	agent.mu.Lock()
	calls = agent.setCalls
	agent.mu.Unlock()
	if calls != 2 {
		t.Fatalf("second turn resent applied settings: %d setter calls, want 2", calls)
	}
}

// liveReconnect resumes onto a persistent host that still holds the provider
// session, whose session/new snapshot names every setting at its default. A
// non-empty modelName makes the binding name its model choice by that name.
func liveReconnect(t *testing.T, resume ports.ChatResumeConfig, modelName string) (*conversation, *fakeAgent) {
	t.Helper()
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
	agent := &fakeAgent{}
	agent.conn = acpsdk.NewAgentSideConnection(agent, host, host)
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
				options = append(options, SessionOption{ID: "model", Value: settings.Model, Name: modelName})
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

	resume.SessionID, resume.DataDir, resume.WorkspacePath = "ao-session", t.TempDir(), t.TempDir()
	resume.ProviderConversationID, resume.ProviderScopeID = "provider-session", "scope"
	opened, err := driver.Resume(context.Background(), resume)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	conv, ok := opened.(*conversation)
	if !ok {
		t.Fatalf("live reconnect returned %T, want *conversation", opened)
	}
	return conv, agent
}
