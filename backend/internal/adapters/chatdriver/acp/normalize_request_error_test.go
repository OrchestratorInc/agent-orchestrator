package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestNormalizeRequestErrorHookInterceptsMatchingErrors verifies that when a
// NormalizeRequestError hook is configured, a matching -32603 prompt error
// is replaced by the hook's clean error rather than the raw ACP JSON blob.
func TestNormalizeRequestErrorHookInterceptsMatchingErrors(t *testing.T) {
	cleanErr := ports.NewChatProviderFailure(
		"Claude Code 2.1.228 does not support the selected model",
		"Version 2.1.280 or newer is required. Run 'claude update' and try again.",
		nil,
	)
	agent := &fakeAgent{
		promptErr: &acpsdk.RequestError{
			Code:    -32603,
			Message: "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model; version 2.1.280 or newer is required.",
			Data:    map[string]any{"errorKind": "unknown"},
		},
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		NormalizeRequestError: func(err error) error {
			var reqErr *acpsdk.RequestError
			if errors.As(err, &reqErr) && reqErr.Code == -32603 &&
				strings.Contains(reqErr.Message, "does not support this model") {
				return cleanErr
			}
			return nil
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()
	_ = nextEvent(t, conv.Events()) // controller.ready

	ref, sendErr := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
	if sendErr != nil {
		t.Fatalf("SendTurn: %v", sendErr)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}

	var completed ports.ChatEvent
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
			break
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = event
		}
	}

	if completed.TurnState != domain.TurnStateFailed {
		t.Fatalf("turn state = %v, want TurnStateFailed", completed.TurnState)
	}
	if completed.Err == nil {
		t.Fatal("completed.Err is nil, want the clean version-mismatch error")
	}
	// The clean error must appear, not the raw JSON blob.
	text := completed.Err.Error()
	if !strings.Contains(text, "2.1.228") {
		t.Fatalf("clean error missing installed version: %q", text)
	}
	// The raw ACP envelope must not leak through.
	if strings.Contains(text, `"code"`) || strings.Contains(text, `"errorKind"`) {
		t.Fatalf("raw ACP payload leaked into turn error: %q", text)
	}
	// Must not be misclassified as a reauth requirement.
	if errors.Is(completed.Err, ports.ErrChatAuthRequired) {
		t.Fatalf("version error must not trigger reauth: %v", completed.Err)
	}
}

// TestNormalizeRequestErrorHookPassesThroughWhenNil verifies that when no hook
// is configured the driver falls back to the generic normalizeACPError path.
func TestNormalizeRequestErrorHookPassesThroughWhenNil(t *testing.T) {
	rawErr := &acpsdk.RequestError{
		Code:    -32603,
		Message: "Internal error: context deadline exceeded",
	}
	agent := &fakeAgent{promptErr: rawErr}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		// NormalizeRequestError deliberately omitted.
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()
	_ = nextEvent(t, conv.Events())

	ref, sendErr := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hi"})
	if sendErr != nil {
		t.Fatalf("SendTurn: %v", sendErr)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}

	var completed ports.ChatEvent
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
			break
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = event
		}
	}

	if completed.TurnState != domain.TurnStateFailed {
		t.Fatalf("turn state = %v, want TurnStateFailed", completed.TurnState)
	}
	if completed.Err == nil {
		t.Fatal("expected an error on failed turn")
	}
}

// TestNormalizeRequestErrorHookPassesThroughWhenReturnsNil verifies that when
// the hook returns nil for a given error the generic path applies, so hooks
// that only intercept specific patterns do not break other errors.
func TestNormalizeRequestErrorHookPassesThroughWhenReturnsNil(t *testing.T) {
	agent := &fakeAgent{
		promptErr: &acpsdk.RequestError{
			Code:    -32603,
			Message: "Internal error: unrelated provider failure",
		},
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		// Hook returns nil → no match, generic path applies.
		NormalizeRequestError: func(error) error { return nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()
	_ = nextEvent(t, conv.Events())

	ref, sendErr := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hi"})
	if sendErr != nil {
		t.Fatalf("SendTurn: %v", sendErr)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}

	var completed ports.ChatEvent
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
			break
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = event
		}
	}

	if completed.TurnState != domain.TurnStateFailed {
		t.Fatalf("turn state = %v, want TurnStateFailed", completed.TurnState)
	}
}

// TestNormalizeRequestErrorHookDoesNotAffectAuthRejections checks that auth
// rejection errors (code -32000) still reach the reauth path even when a
// NormalizeRequestError hook is set, because auth handling runs before the hook.
func TestNormalizeRequestErrorHookDoesNotAffectAuthRejections(t *testing.T) {
	agent := &fakeAgent{
		promptErr: &acpsdk.RequestError{Code: -32000, Message: "authentication required"},
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		NormalizeRequestError: func(error) error {
			// This hook must never be reached for auth rejections.
			return nil
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()
	_ = nextEvent(t, conv.Events())

	ref, sendErr := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hi"})
	if sendErr != nil {
		t.Fatalf("SendTurn: %v", sendErr)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}

	var completed ports.ChatEvent
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
			break
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = event
		}
	}

	if completed.TurnState != domain.TurnStateFailed {
		t.Fatalf("turn state = %v, want TurnStateFailed", completed.TurnState)
	}
	// Auth rejection must still be detected regardless of the hook.
	if !errors.Is(completed.Err, ports.ErrChatAuthRequired) {
		t.Fatalf("auth rejection must wrap ErrChatAuthRequired even with hook set: %v", completed.Err)
	}
}

// TestDriverPropagatesNormalizeRequestErrorHook ensures the Config field is
// copied to the conversation so live turns can use it. Mirrors the pattern
// used in TestDriverPropagatesTheAuthRejectionHook.
func TestDriverPropagatesNormalizeRequestErrorHook(t *testing.T) {
	called := 0
	cfg := Config{NormalizeRequestError: func(error) error { called++; return nil }}
	conv := &conversation{}
	conv.normalizeRequestError = cfg.NormalizeRequestError
	if conv.normalizeRequestError == nil {
		t.Fatal("hook must reach the conversation")
	}
	_ = conv.normalizeRequestError(errors.New("test"))
	if called != 1 {
		t.Fatalf("hook ran %d times, want 1", called)
	}
}

// TestNilNormalizeRequestErrorHookIsSafe ensures that a nil hook does not panic.
func TestNilNormalizeRequestErrorHookIsSafe(t *testing.T) {
	conv := &conversation{}
	conv.normalizeRequestError = Config{}.NormalizeRequestError
	if conv.normalizeRequestError != nil {
		t.Fatal("unset hook must be nil")
	}
}
