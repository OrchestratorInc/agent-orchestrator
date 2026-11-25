package acp

import (
	"errors"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNormalizeACPErrorMarksWatchdogStalls(t *testing.T) {
	const message = "provider stopped responding: no ACP output for 15m0s; failing the stalled prompt"
	for name, data := range map[string]any{
		// The relay wraps provider error data when it adds its own event id.
		"wrapped by the relay": map[string]any{
			"providerData":                   map[string]any{persistenthost.ACPPromptStalledDataKey: true},
			persistenthost.ACPEventIDMetaKey: "acp-host:1",
		},
		"top level": map[string]any{persistenthost.ACPPromptStalledDataKey: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := normalizeACPError("ACP session/prompt", &acpsdk.RequestError{Code: -32603, Message: message, Data: data})
			if !errors.Is(err, ports.ErrChatPromptStalled) {
				t.Fatalf("error %v is not ErrChatPromptStalled", err)
			}
			if !strings.Contains(err.Error(), message) || !strings.HasPrefix(err.Error(), "ACP session/prompt: ") {
				t.Fatalf("error text changed: %q", err)
			}
		})
	}
}

func TestNormalizeACPErrorLeavesProviderErrorsAlone(t *testing.T) {
	for name, requestErr := range map[string]*acpsdk.RequestError{
		"no data":              {Code: -32603, Message: "internal error"},
		"unrelated data":       {Code: -32603, Message: "internal error", Data: map[string]any{"providerData": map[string]any{"detail": "x"}}},
		"marker on other code": {Code: -32602, Message: "bad params", Data: map[string]any{persistenthost.ACPPromptStalledDataKey: true}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := normalizeACPError("ACP session/prompt", requestErr); errors.Is(err, ports.ErrChatPromptStalled) {
				t.Fatalf("provider error %v was marked as a watchdog stall", err)
			}
		})
	}
}

func TestUsageLimitedOrMarksResume429s(t *testing.T) {
	const details = "API error: 429 This request would exceed your account's rate limit. Please try again later · model not changed"
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"top level":         {&acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": details}}, true},
		"wrapped by relay":  {&acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"providerData": map[string]any{"details": details}}}, true},
		"other api error":   {&acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": "API error: 500 · model not changed"}}, false},
		"429 on other code": {&acpsdk.RequestError{Code: -32602, Message: "bad params", Data: map[string]any{"details": details}}, false},
		"plain error":       {errors.New(details), false},
		"no data":           {&acpsdk.RequestError{Code: -32603, Message: "Internal error"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := usageLimitedOr(normalizeACPError("ACP session/set_config_option", tc.err))
			if got := errors.Is(err, ports.ErrChatUsageLimited); got != tc.want {
				t.Fatalf("usage limited = %v, want %v (%v)", got, tc.want, err)
			}
		})
	}
}
