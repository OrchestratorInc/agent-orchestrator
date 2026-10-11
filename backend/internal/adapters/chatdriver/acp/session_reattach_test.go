package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// errACPSessionNotFound is the wire shape claude-agent-acp answers with after it
// evicted a session whose Claude child process died: a thrown "Session not
// found" that its SDK wraps as -32603.
func errACPSessionNotFound() error {
	return acpsdk.NewInternalError(map[string]any{"details": "Session not found"})
}

func newSessionLossDriver(agent *fakeAgent) *Driver {
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		// Like Claude, the default permission mode needs no session/set_mode.
		SessionMode: func(permission ports.PermissionMode) string {
			if ports.NormalizePermissionMode(permission) == ports.PermissionModeAuto {
				return "auto"
			}
			return ""
		},
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	return driver
}

// Issue #6200: the agent process stayed connected but forgot AO's session, so
// every turn failed in milliseconds on set_config_option/set_mode. The next turn
// must resume the same session id and then run normally.
func TestACPSendTurnReattachesSessionTheAgentForgot(t *testing.T) {
	agent := &fakeAgent{}
	driver := newSessionLossDriver(agent)
	workspace := t.TempDir()
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()

	agent.mu.Lock()
	agent.sessionLost = true
	agent.mu.Unlock()

	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "hello",
		Settings: ports.ChatTurnSettings{
			Model: "sonnet", Approval: ports.PermissionModeAuto,
		},
	})
	if err != nil {
		t.Fatalf("SendTurn after the agent lost its session: %v", err)
	}
	if ref.ProviderTurnID == "" {
		t.Fatal("SendTurn returned no provider turn id")
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.resumeCalls != 1 {
		t.Fatalf("session/resume calls = %d, want 1", agent.resumeCalls)
	}
	if agent.resumeParams.SessionId != "claude-session-1" || agent.resumeParams.Cwd != workspace {
		t.Fatalf("session/resume params = %#v, want the original session and workspace", agent.resumeParams)
	}
	if agent.options["model"] != "sonnet" || agent.mode != "auto" {
		t.Fatalf("settings after reattach: model=%q mode=%q", agent.options["model"], agent.mode)
	}
}

// A turn with no settings to apply (default permissions, no model override)
// first reaches the agent through session/prompt. Losing the session there must
// recover the same way instead of failing every later turn.
func TestACPPromptReattachesSessionTheAgentForgot(t *testing.T) {
	agent := &fakeAgent{promptNoPermission: true}
	driver := newSessionLossDriver(agent)
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()

	agent.mu.Lock()
	agent.sessionLost = true
	agent.mu.Unlock()

	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind != ports.ChatEventTurnCompleted {
			continue
		}
		if event.TurnState != domain.TurnStateCompleted {
			t.Fatalf("turn settled %q after the agent lost its session: %+v", event.TurnState, event)
		}
		break
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.resumeCalls != 1 || agent.resumeParams.SessionId != "claude-session-1" {
		t.Fatalf("session/resume calls=%d params=%#v, want one resume of the original session",
			agent.resumeCalls, agent.resumeParams)
	}
	if text := agent.promptParams.Prompt[0].Text; text == nil || text.Text != "hello" {
		t.Fatalf("resent prompt = %#v, want the original turn", agent.promptParams.Prompt)
	}
}

func TestACPSendTurnKeepsSessionLossErrorWhenAgentCannotResume(t *testing.T) {
	agent := &fakeAgent{capabilities: &acpsdk.AgentCapabilities{}}
	driver := newSessionLossDriver(agent)
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()

	agent.mu.Lock()
	agent.sessionLost = true
	agent.mu.Unlock()

	_, err = conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "hello", Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeAuto},
	})
	if !isACPSessionNotFound(err) {
		t.Fatalf("SendTurn err = %v, want the agent's session-not-found error", err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.resumeCalls != 0 {
		t.Fatalf("session/resume calls = %d, want 0 for an agent without resume", agent.resumeCalls)
	}
}

func TestIsACPSessionNotFound(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"claude-agent-acp details", errACPSessionNotFound(), true},
		{"go sdk coerced error", acpsdk.NewInternalError(map[string]any{"error": "Session not found"}), true},
		{"wrapped", fmt.Errorf("set ACP session option %q: %w", "model", errACPSessionNotFound()), true},
		{"resource not found", &acpsdk.RequestError{Code: -32002, Message: "Resource not found"}, true},
		{"other internal error", acpsdk.NewInternalError(map[string]any{"details": "Mode auto is not available"}), false},
		{"peer disconnected", acpsdk.NewInternalError(map[string]any{"error": "peer disconnected before response"}), false},
		{"method not found", acpsdk.NewMethodNotFound("session/set_mode"), false},
		{"plain error", errors.New("Session not found"), false},
		{"nil", nil, false},
	} {
		if got := isACPSessionNotFound(tc.err); got != tc.want {
			t.Errorf("%s: isACPSessionNotFound = %v, want %v", tc.name, got, tc.want)
		}
	}
}
