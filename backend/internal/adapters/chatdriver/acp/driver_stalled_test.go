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
