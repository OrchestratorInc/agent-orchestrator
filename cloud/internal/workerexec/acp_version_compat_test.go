package workerexec

import (
	"errors"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

const versionIncompatMessage = "Internal error: API Error: 400 Claude Code 2.1.228 does not support this model;" +
	" version 2.1.280 or newer is required." +
	" Run 'claude update', or update the Claude desktop app, then try again."

// TestNormalizeACPRequestErrorRewritesVersionMismatch covers the primary fix:
// the exact HTTP 400 message the bridge emits as an opaque -32603 must become a
// clean, actionable message with no raw JSON-RPC envelope, provider stderr, or
// session ids.
func TestNormalizeACPRequestErrorRewritesVersionMismatch(t *testing.T) {
	clean, ok := normalizeACPRequestError(&acp.RequestError{
		Code:    -32603,
		Message: versionIncompatMessage,
		Data:    map[string]any{"errorKind": "unknown", "sessionId": "dd2c330c-8ec3-46f5-b7d9-28686bcf7f22"},
	})
	if !ok {
		t.Fatal("expected a recognized version-incompatibility error")
	}
	text := clean.Error()
	if !strings.Contains(text, "2.1.228") || !strings.Contains(text, "2.1.280") {
		t.Fatalf("clean error missing versions: %q", text)
	}
	for _, leaked := range []string{`"code"`, "errorKind", "dd2c330c", "CLAUDE_SDK_CAN_USE_TOOL_SHADOWED", "session/query"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("clean error leaks %q: %q", leaked, text)
		}
	}
}

// TestNormalizeACPRequestErrorIgnoresUnrelatedErrors ensures only the exact
// version-incompatibility phrasing is rewritten; everything else is left to the
// caller's normal handling.
func TestNormalizeACPRequestErrorIgnoresUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"plain context deadline", &acp.RequestError{Code: -32603, Message: "Internal error: context deadline exceeded"}},
		{"auth rejection", &acp.RequestError{Code: -32000, Message: "authentication required"}},
		{"method not found", &acp.RequestError{Code: -32601, Message: "Method not found"}},
		{"wrong phrasing", &acp.RequestError{Code: -32603, Message: "Claude Code 2.1.228 is too old"}},
		{"missing required version", &acp.RequestError{Code: -32603, Message: "Claude Code 2.1.228 does not support this model"}},
		{"non-ACP error", errors.New("connection reset by peer")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := normalizeACPRequestError(tc.err); ok {
				t.Fatalf("normalizeACPRequestError(%v) unexpectedly matched", tc.err)
			}
		})
	}
}

// TestACPRequestFailureDropsRawDiagnosticsForVersionMismatch verifies the
// recognized failure returns only the clean message, dropping the raw JSON-RPC
// envelope and the appended provider stderr (bridge logs and Node warnings).
func TestACPRequestFailureDropsRawDiagnosticsForVersionMismatch(t *testing.T) {
	stderr := &lockedBuffer{}
	_, _ = stderr.Write([]byte(
		"[session/query] sessionId=dd2c330c-8ec3-46f5-b7d9-28686bcf7f22 resume=none apiType=native baseUrl=native\n" +
			"(node:3918) [CLAUDE_SDK_CAN_USE_TOOL_SHADOWED] Warning: canUseTool will not be invoked",
	))
	err := acpRequestFailure("ACP prompt", &acp.RequestError{
		Code:    -32603,
		Message: versionIncompatMessage,
		Data:    map[string]any{"errorKind": "unknown"},
	}, stderr)
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	text := err.Error()
	if !strings.Contains(text, "2.1.228") || !strings.Contains(text, "2.1.280") {
		t.Fatalf("clean error missing versions: %q", text)
	}
	for _, leaked := range []string{`"code"`, "errorKind", "dd2c330c", "CLAUDE_SDK_CAN_USE_TOOL_SHADOWED", "session/query"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("failure leaks %q: %q", leaked, text)
		}
	}
}

// TestACPRequestFailureKeepsDiagnosticsForOtherErrors preserves the existing
// behavior for anything unrecognized: the operation, the raw error, and the
// bounded stderr remain available for operators.
func TestACPRequestFailureKeepsDiagnosticsForOtherErrors(t *testing.T) {
	stderr := &lockedBuffer{}
	_, _ = stderr.WriteString("[session/query] sessionId=abc apiType=native")
	err := acpRequestFailure("ACP prompt", &acp.RequestError{Code: -32603, Message: "Internal error: boom"}, stderr)
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	text := err.Error()
	if !strings.Contains(text, "ACP prompt") || !strings.Contains(text, "boom") || !strings.Contains(text, "session/query") {
		t.Fatalf("unrecognized failure lost diagnostics: %q", text)
	}
}
