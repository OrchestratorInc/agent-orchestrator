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
		NormalizeRequestError: func(_ string, err error) error {
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
		NormalizeRequestError: func(string, error) error { return nil },
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
		NormalizeRequestError: func(string, error) error {
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
	cfg := Config{NormalizeRequestError: func(string, error) error { called++; return nil }}
	conv := &conversation{}
	conv.normalizeRequestError = cfg.NormalizeRequestError
	if conv.normalizeRequestError == nil {
		t.Fatal("hook must reach the conversation")
	}
	_ = conv.normalizeRequestError("", errors.New("test"))
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

var errorKindVersionRequest = &acpsdk.RequestError{
	Code:    -32603,
	Message: "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model; version 2.1.280 or newer is required.",
	Data:    map[string]any{"errorKind": "unknown", "sessionId": "dd2c330c-8ec3-46f5-b7d9-28686bcf7f22"},
}

// TestNormalizeRequestErrorHookInterceptsSessionNewErrors verifies the hook is
// applied when the provider rejects the model while opening a session, not just
// during a prompt turn. This is the path that previously leaked the raw -32603
// blob because session/new bypassed the hook.
func TestNormalizeRequestErrorHookInterceptsSessionNewErrors(t *testing.T) {
	cleanErr := ports.NewChatProviderFailure(
		"Claude Code 2.1.228 does not support the selected model",
		"Version 2.1.280 or newer is required. Run 'claude update' and try again.",
		nil,
	)
	agent := &fakeAgent{newSessionErr: errorKindVersionRequest}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		NormalizeRequestError: func(_ string, err error) error {
			var reqErr *acpsdk.RequestError
			if errors.As(err, &reqErr) && reqErr.Code == -32603 &&
				strings.Contains(reqErr.Message, "does not support this model") {
				return cleanErr
			}
			return nil
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	_, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err == nil {
		t.Fatal("Start succeeded, want the classified session/new failure")
	}
	text := err.Error()
	if !strings.Contains(text, "2.1.228") || !strings.Contains(text, "2.1.280") {
		t.Fatalf("error missing versions: %q", text)
	}
	if strings.Contains(text, `"code"`) || strings.Contains(text, "errorKind") || strings.Contains(text, "dd2c330c") {
		t.Fatalf("raw ACP payload leaked from session/new: %q", text)
	}
}

// TestNormalizeErrorAppliesHookToNonPromptOperations covers the conversation
// helper directly: config-option setters and session/new must route through the
// provider hook exactly like the prompt path does.
func TestNormalizeErrorAppliesHookToNonPromptOperations(t *testing.T) {
	cleanErr := errors.New("classified provider failure")
	conv := &conversation{
		normalizeRequestError: func(_ string, err error) error {
			var reqErr *acpsdk.RequestError
			if errors.As(err, &reqErr) && reqErr.Code == -32603 {
				return cleanErr
			}
			return nil
		},
	}
	for _, operation := range []string{"ACP session/new", `set ACP session option "model"`, "ACP session/resume"} {
		t.Run(operation, func(t *testing.T) {
			if got := conv.normalizeError(operation, errorKindVersionRequest); !errors.Is(got, cleanErr) {
				t.Fatalf("normalizeError = %v, want the hook's error", got)
			}
			if got := conv.normalizeLoadError(operation, errorKindVersionRequest); !errors.Is(got, cleanErr) {
				t.Fatalf("normalizeLoadError = %v, want the hook's error", got)
			}
		})
	}
}

// TestNormalizeErrorSkipsHookForAuthRejections ensures a -32000 rejection keeps
// taking the reauth path even when a provider hook is installed, matching the
// documented "auth runs before the hook" contract.
func TestNormalizeErrorSkipsHookForAuthRejections(t *testing.T) {
	hookCalled := 0
	conv := &conversation{
		normalizeRequestError: func(string, error) error {
			hookCalled++
			return errors.New("must not be used")
		},
	}
	authErr := &acpsdk.RequestError{Code: -32000, Message: "authentication required"}
	err := conv.normalizeError("ACP session/new", authErr)
	if hookCalled != 0 {
		t.Fatalf("hook called %d times for an auth rejection, want 0", hookCalled)
	}
	if !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("auth rejection = %v, want ErrChatAuthRequired", err)
	}
}

// TestNormalizeACPErrorStripsRawRequestData verifies the generic path no longer
// echoes the JSON-RPC envelope (including Data, which can carry provider
// payloads or session ids) while still exposing the code to callers.
func TestNormalizeACPErrorStripsRawRequestData(t *testing.T) {
	raw := &acpsdk.RequestError{
		Code:    -32603,
		Message: "Internal error: context deadline exceeded",
		Data:    map[string]any{"sessionId": "dd2c330c-8ec3-46f5-b7d9-28686bcf7f22", "payload": "secret"},
	}
	err := normalizeACPError("ACP session/prompt", raw)
	text := err.Error()
	if strings.Contains(text, "secret") || strings.Contains(text, "dd2c330c") || strings.Contains(text, `"code"`) {
		t.Fatalf("raw payload leaked: %q", text)
	}
	if !strings.Contains(text, "code -32603") || !strings.Contains(text, "context deadline exceeded") {
		t.Fatalf("sanitized error lost its code/message: %q", text)
	}
	var reqErr *acpsdk.RequestError
	if !errors.As(err, &reqErr) || reqErr.Code != -32603 {
		t.Fatalf("errors.As could not recover the request error from %v", err)
	}
}
