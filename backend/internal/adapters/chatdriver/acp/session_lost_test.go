package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeSessionNotFound is the answer claude-agent-acp gives for a session it
// no longer holds: its TypeScript SDK wraps the thrown error as -32603 with the
// text under data.details.
func claudeSessionNotFound() error {
	return &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": "Session not found"}}
}

func TestIsACPSessionNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"claude details", claudeSessionNotFound(), true},
		{"go sdk error key", &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"error": "session not found: abc"}}, true},
		{"resource not found message", &acpsdk.RequestError{Code: acpResourceNotFound, Message: "Session not found"}, true},
		{"other internal error", &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": "context deadline exceeded"}}, false},
		{"invalid params mentioning session", &acpsdk.RequestError{Code: -32602, Message: "Session not found"}, false},
		{"peer disconnected", &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"error": "peer disconnected before response"}}, false},
		{"plain error", errors.New("Session not found"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isACPSessionNotFound(tt.err); got != tt.want {
				t.Fatalf("isACPSessionNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestACPDriverStopsProviderThatLostItsSession(t *testing.T) {
	agent := &fakeAgent{}
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Probe:   func(context.Context) error { return nil },
		Launch:  func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	_ = nextEvent(t, opened.Events())

	agent.mu.Lock()
	agent.configErr = claudeSessionNotFound()
	agent.mu.Unlock()

	_, err = opened.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:     "hello",
		Settings: ports.ChatTurnSettings{Model: "claude-opus-4-6"},
	})
	if !errors.Is(err, errProviderSessionLost) {
		t.Fatalf("SendTurn error = %v, want errProviderSessionLost", err)
	}
	var refusal interface{ ChatRefusal() bool }
	if !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("SendTurn error = %v, want a definitive provider refusal", err)
	}

	// The provider is stopped, so the event stream ends with a plain stop (no
	// recovery-inconclusive marker) that the controller records as exited.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-opened.Events():
			if !ok {
				return
			}
			if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerStopped &&
				errors.Is(event.Err, ports.ErrChatRecoveryInconclusive) {
				t.Fatalf("lost session reported as recovery-inconclusive: %v", event.Err)
			}
		case <-deadline:
			t.Fatal("event stream did not close after the provider lost its session")
		}
	}
}

func TestACPDriverKeepsProviderOnOtherSettingErrors(t *testing.T) {
	agent := &fakeAgent{}
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Probe:   func(context.Context) error { return nil },
		Launch:  func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	_ = nextEvent(t, opened.Events())

	agent.mu.Lock()
	agent.configErr = errors.New("set model failed")
	agent.mu.Unlock()
	if _, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:     "hello",
		Settings: ports.ChatTurnSettings{Model: "claude-opus-4-6"},
	}); err == nil || errors.Is(err, errProviderSessionLost) {
		t.Fatalf("SendTurn error = %v, want the plain setting error", err)
	}
	conv := opened.(*conversation)
	conv.mu.Lock()
	closed := conv.closed
	conv.mu.Unlock()
	if closed {
		t.Fatal("conversation closed after an unrelated setting error")
	}
}
