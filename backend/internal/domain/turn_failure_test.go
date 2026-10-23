package domain

import "testing"

func TestClassifyConversationFailure(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		kind          ConversationFailureKind
		detail        string
	}{
		{
			"OpenCode out of funds",
			`ACP session/prompt: {"code":-32603,"message":"Internal error: Upstream request failed: Insufficient account funds","data":{"ao.persistentEventId":"acp-host:abc:1","providerData":{"errorName":"provider.quota","service":"session"}}}`,
			FailureOutOfCredit, "Insufficient account funds",
		},
		{
			"structured name wins over text",
			`ACP session/prompt: {"code":-32603,"message":"Internal error: request rejected","data":{"providerData":{"errorName":"provider.auth"}}}`,
			FailureAuth, "request rejected",
		},
		{"Anthropic credit", "Your credit balance is too low to access the Anthropic API.", FailureOutOfCredit, ""},
		{"OpenAI quota over 429", "429 You exceeded your current quota: insufficient_quota", FailureOutOfCredit, ""},
		{"OpenRouter 402", "402 Payment Required", FailureOutOfCredit, ""},
		{"invalid key", "Error: invalid x-api-key", FailureAuth, ""},
		{"unauthorized", "401 Unauthorized", FailureAuth, ""},
		{"rate limit", "rate_limit_error: Number of request tokens has exceeded your per-minute rate limit", FailureRateLimited, ""},
		{"too many requests", "Too many requests. Retry after 30 seconds.", FailureRateLimited, ""},
		{"overloaded", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, FailureOverloaded, ""},
		{"context", "prompt is too long: 210000 tokens > 200000 maximum", FailureContextTooLong, ""},
		{"OpenAI context", "context_length_exceeded", FailureContextTooLong, ""},
		{"model", "The model `gpt-9` does not exist or you do not have access to it.", FailureModelUnavailable, ""},
		{"network", "fetch failed: ECONNREFUSED 127.0.0.1:443", FailureNetwork, ""},
		// Recorded from real failed turns across Codex, Pi, and OpenCode sessions.
		{"Codex no credits", "stream disconnected before completion: You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/.", FailureOutOfCredit, "You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/."},
		{"Codex missing auth", "unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: https://api.openai.com/v1/responses, cf-ray: a499b96849ec9f61-MAA, request id: req_6c84", FailureAuth, "Missing bearer or basic authentication in header"},
		{"model at capacity", "Selected model is at capacity. Please try a different model.", FailureOverloaded, ""},
		{"Pi balance", "Pi's model refused the turn  402 Insufficient Balance (request_id: 2e3e056d-317c-44c3-af23-e632b6037ae0)", FailureOutOfCredit, "Pi's model refused the turn 402 Insufficient Balance"},
		{
			"OpenCode header timeout",
			`ACP session/prompt: {"code":-32603,"message":"Internal error: Provider response headers timed out after 300000ms","data":{"providerData":{"errorName":"APIError","service":"session"}}}`,
			FailureNetwork, "Provider response headers timed out after 300000ms",
		},
		{
			"OpenCode 2 refused key",
			`chat driver requires authentication: ACP session/prompt: {"code":-32000,"message":"Authentication required: provider authentication required","data":{"ao.persistentEventId":"acp-host:1"}}`,
			FailureAuth, "Authentication required: provider authentication required",
		},
		{
			"OpenCode 2 dropped connection",
			`ACP session/prompt: {"code":-32603,"message":"Internal error: ECONNRESET: The socket connection was closed unexpectedly. For more information, pass ` + "`verbose: true`" + ` in the second argument to fetch()"}`,
			FailureNetwork, "ECONNRESET: The socket connection was closed unexpectedly.",
		},
		{"AO controller ended", "controller ended before the turn completed", "", ""},
		{"ACP cancelled", `ACP session/prompt: {"code":-32800,"message":"Request cancelled","data":{"error":"context canceled"}}`, "", ""},
		{"OpenCode generic agent error", `ACP session/prompt: {"code":-32603,"message":"Internal error","data":{"providerData":{"details":"Internal error: Agent error"}}}`, "", ""},
		{"OpenCode 404", `ACP session/prompt: {"code":-32603,"message":"Internal error: Provider request failed with HTTP 404","data":{"providerData":{"errorName":"provider.invalid-request","service":"session"}}}`, "", ""},
		{"unknown", "Something odd happened", "", ""},
		{"empty", "  ", "", ""},
		{"line number", "tool failed at line 401 of main.go", "", ""},
		{"port", "listen tcp 127.0.0.1:5030: address already in use", "", ""},
		{"count", "read 429 files", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := ClassifyConversationFailure(tc.message)
			if tc.kind == "" {
				if failure != nil {
					t.Fatalf("failure = %+v, want nil", failure)
				}
				return
			}
			if failure == nil || failure.Kind != tc.kind || failure.Detail != tc.detail {
				t.Fatalf("failure = %+v, want kind %q detail %q", failure, tc.kind, tc.detail)
			}
		})
	}
}
