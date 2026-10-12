package claudeacp

import (
	"errors"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestClaudeNormalizeRequestErrorVersionIncompatibility covers the primary fix:
// the exact HTTP 400 message Claude Code emits when its version is too old for
// the selected model must be turned into a clean, user-facing error rather than
// a raw JSON-RPC blob.
func TestClaudeNormalizeRequestErrorVersionIncompatibility(t *testing.T) {
	// This is the exact message shape the claude-agent-acp bridge emits as a
	// -32603 "Internal error" when the installed Claude binary is too old.
	msg := "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model;" +
		" version 2.1.280 or newer is required." +
		" Run 'claude update', or update the Claude desktop app, then try again."

	err := claudeNormalizeRequestError("claude-sonnet-4-5", &acpsdk.RequestError{
		Code:    -32603,
		Message: msg,
		Data:    map[string]any{"errorKind": "unknown"},
	}, nil)
	if err == nil {
		t.Fatal("expected a non-nil error for a version-incompatibility message, got nil")
	}
	// Must not expose the raw JSON-RPC internals.
	text := err.Error()
	if strings.Contains(text, `"code"`) || strings.Contains(text, `"errorKind"`) {
		t.Fatalf("error leaks raw ACP payload: %q", text)
	}
	// Must name the installed version.
	if !strings.Contains(text, "2.1.228") {
		t.Fatalf("error does not include installed version: %q", text)
	}
	// Must name the required minimum version.
	if !strings.Contains(text, "2.1.280") {
		t.Fatalf("error does not include required version: %q", text)
	}
	// Must not be classified as a reauth requirement.
	if errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("version-incompatibility must not trigger reauth: %v", err)
	}
}

// TestClaudeNormalizeRequestErrorVersionBelowMinimum verifies the error message
// is correct when the installed version is strictly less than the minimum.
func TestClaudeNormalizeRequestErrorVersionBelowMinimum(t *testing.T) {
	msg := "Internal error: API Error: 400 Claude Code 2.1.100 does not support this model;" +
		" version 2.1.280 or newer is required."

	err := claudeNormalizeRequestError("claude-opus-4-1", &acpsdk.RequestError{Code: -32603, Message: msg}, nil)
	if err == nil {
		t.Fatal("expected non-nil error for version below minimum")
	}
	text := err.Error()
	if !strings.Contains(text, "2.1.100") || !strings.Contains(text, "2.1.280") {
		t.Fatalf("error = %q, want installed (2.1.100) and required (2.1.280) mentioned", text)
	}
}

// TestClaudeNormalizeRequestErrorPassesThroughUnrelatedErrors ensures that
// errors which do not match the version-incompatibility pattern are returned
// as nil so the generic ACP wrapping path takes over. This prevents
// mis-classifying unrelated failures as version errors.
func TestClaudeNormalizeRequestErrorPassesThroughUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{
			"plain context deadline",
			&acpsdk.RequestError{Code: -32603, Message: "Internal error: context deadline exceeded"},
		},
		{
			"auth rejection code -32000",
			&acpsdk.RequestError{Code: -32000, Message: "authentication required"},
		},
		{
			"method not found",
			&acpsdk.RequestError{Code: -32601, Message: "Method not found"},
		},
		{
			"generic internal error without version pattern",
			&acpsdk.RequestError{Code: -32603, Message: "Internal error: API Error: 500 something went wrong"},
		},
		{
			"non-ACP plain error",
			errors.New("connection reset by peer"),
		},
		{
			"version string present but wrong phrasing",
			&acpsdk.RequestError{Code: -32603, Message: "Claude Code 2.1.228 is too old"},
		},
		{
			"partial match missing required version",
			&acpsdk.RequestError{Code: -32603, Message: "Claude Code 2.1.228 does not support this model"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeNormalizeRequestError("some-model", tc.err, nil); got != nil {
				t.Fatalf("claudeNormalizeRequestError = %v, want nil (pass-through)", got)
			}
		})
	}
}

// TestClaudeNormalizeRequestErrorRemediation checks that the clean error
// includes actionable remediation steps and not raw session diagnostics.
func TestClaudeNormalizeRequestErrorRemediation(t *testing.T) {
	msg := "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model;" +
		" version 2.1.280 or newer is required." +
		" Run 'claude update', or update the Claude desktop app, then try again." +
		"\n[session/query] sessionId=dd2c330c-8ec3-46f5-b7d9-28686bcf7f22 resume=none" +
		"\n[CLAUDE_SDK_CAN_USE_TOOL_SHADOWED] Warning: canUseTool will not be invoked"

	err := claudeNormalizeRequestError("some-model", &acpsdk.RequestError{
		Code:    -32603,
		Message: msg,
		Data:    map[string]any{"errorKind": "unknown"},
	}, nil)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	text := err.Error()
	if !strings.Contains(strings.ToLower(text), "update") {
		t.Fatalf("error missing remediation instruction: %q", text)
	}
	// Raw session id must not appear.
	if strings.Contains(text, "dd2c330c") {
		t.Fatalf("error exposes raw session id: %q", text)
	}
	// SDK warning must not appear.
	if strings.Contains(text, "CLAUDE_SDK_CAN_USE_TOOL_SHADOWED") {
		t.Fatalf("error exposes SDK warning text: %q", text)
	}
}

// TestClaudeNormalizeRequestErrorVersionIncompatibleWithSpacesInMessage
// checks that the regex handles extra whitespace in the bridge message.
func TestClaudeNormalizeRequestErrorVersionIncompatibleWithSpacesInMessage(t *testing.T) {
	msg := "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model;  version 2.1.280 or newer is required."

	err := claudeNormalizeRequestError("some-model", &acpsdk.RequestError{Code: -32603, Message: msg}, nil)
	if err == nil {
		t.Fatal("extra whitespace around semicolon must still be recognized")
	}
}

// TestClaudeNormalizeRequestErrorReportsUnsupportedModel verifies that a
// version-incompatibility failure reports the selected model and both versions
// to the notify callback, which is how the daemon learns to stop offering a
// model this binary cannot run.
func TestClaudeNormalizeRequestErrorReportsUnsupportedModel(t *testing.T) {
	msg := "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model;" +
		" version 2.1.280 or newer is required."
	calls := 0
	var gotModel, gotInstalled, gotRequired string
	err := claudeNormalizeRequestError("claude-opus-4-1", &acpsdk.RequestError{Code: -32603, Message: msg},
		func(model, installed, required string) {
			calls++
			gotModel, gotInstalled, gotRequired = model, installed, required
		})
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	if calls != 1 || gotModel != "claude-opus-4-1" || gotInstalled != "2.1.228" || gotRequired != "2.1.280" {
		t.Fatalf("notify = (%q, %q, %q) calls=%d, want (claude-opus-4-1, 2.1.228, 2.1.280) once",
			gotModel, gotInstalled, gotRequired, calls)
	}
}

// TestClaudeNormalizeRequestErrorSkipsNotifyWithoutModel ensures a failure that
// cannot be attributed to a model still normalizes but reports nothing, since
// marking an empty id would be a lie.
func TestClaudeNormalizeRequestErrorSkipsNotifyWithoutModel(t *testing.T) {
	msg := "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model;" +
		" version 2.1.280 or newer is required."
	calls := 0
	err := claudeNormalizeRequestError("", &acpsdk.RequestError{Code: -32603, Message: msg},
		func(string, string, string) { calls++ })
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	if calls != 0 {
		t.Fatalf("notify ran %d times, want 0 without a model id", calls)
	}
}
