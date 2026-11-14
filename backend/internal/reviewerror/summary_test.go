package reviewerror

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSummary(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"structured", `{"status":400,"error":{"type":"invalid_request_error","message":"Model unavailable for this account"},"request":{"api_key":"synthetic-secret"},"diagnostics":"private dump"}`, "HTTP 400: invalid_request_error: Model unavailable for this account"},
		{"prefixed", `Codex error: {"status":400,"error":{"type":"invalid_request_error","message":"The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account."}}`, "HTTP 400: invalid_request_error: The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account."},
		{"pi prose", "Pi's model refused the turn\n\nThe 'gpt-5.5' model is not supported when using Codex with a ChatGPT account.", "Pi's model refused the turn The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account."},
		{"ordinary failure", Fallback, Fallback},
		{"missing", "", Fallback},
		{"unicode whitespace", "\u2003\t\r\n", Fallback},
		{"malformed", `{"status":400,"error":`, Fallback},
		{"malformed prefixed", "Codex error: not JSON", Fallback},
		{"wrong type", `{"status":"synthetic-secret","message":"safe"}`, Fallback},
		{"null", "null", Fallback},
		{"array", `["synthetic-secret"]`, Fallback},
		{"invalid utf8", "bad\xff", Fallback},
		{"oversized", strings.Repeat("model unavailable ", maxInputBytes), Fallback},
		{"unknown classification", `{"status":999,"type":"synthetic-secret","message":"Model unavailable"}`, "Model unavailable"},
		{"selected secret", `{"status":400,"error":{"type":"invalid_request_error","message":"Authorization: Bearer synthetic-secret"}}`, "HTTP 400: invalid_request_error: " + withheld},
		{"tab credential label", "API\tKEY: demo-value", withheld},
		{"unicode token assignment", "token\u2003=demo-value", withheld},
		{"structured tab credential label", `{"status":400,"error":{"type":"invalid_request_error","message":"API\tKEY: demo-value"}}`, "HTTP 400: invalid_request_error: " + withheld},
		{"structured unicode token assignment", `{"status":400,"error":{"type":"invalid_request_error","message":"token\u2003=demo-value"}}`, "HTTP 400: invalid_request_error: " + withheld},
		{"quoted model diagnosis", `{"message":"\"gpt-5.5\" is unavailable"}`, `Provider error: "gpt-5.5" is unavailable`},
		{"selected prefix prose", `{"message":"Codex error: unavailable"}`, "Provider error: Codex error: unavailable"},
		{"selected null prose", `{"message":"null"}`, "Provider error: null"},
		{"no trusted prefix bypass", "Provider error: API KEY=demo-value", withheld},
		{"empty object", `{}`, Fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Summary(tc.raw)
			if got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
			if again := Summary(got); again != got {
				t.Fatalf("not idempotent: %q becomes %q", got, again)
			}
		})
	}
}

func TestSummaryWithholdsSensitiveMessages(t *testing.T) {
	for _, raw := range []string{
		"aUtHoRiZaTiOn: Bearer synthetic-value",
		"API_KEY = 'synthetic-value'",
		"token=synthetic-value",
		"prompt: synthetic-value",
		"debug: synthetic-value",
		"Cookie: session=synthetic-value\nmodel unavailable",
		"https://user:synthetic-value@example.invalid/?token=synthetic-value",
		"model unavailable; request body: synthetic-value",
		"model unavailable; diagnostic: synthetic-value",
		`model unavailable {"payload":"synthetic-value"}`,
		"model unavailable sk-synthetic-value",
		"model unavailable ghp_synthetic_value",
		"model unavailable\x1b[31m",
		strings.Repeat("model unavailable ", 100) + " Authorization: synthetic-value",
	} {
		if got := Summary(raw); got != withheld {
			t.Errorf("sensitive message was not withheld: %q", got)
		}
	}
}

func TestSummaryByteBound(t *testing.T) {
	for _, message := range []string{strings.Repeat("unavailable ", 200), strings.Repeat("模型不可用 ", 200)} {
		raw := `{"status":400,"error":{"type":"invalid_request_error","message":"` + message + `"}}`
		got := Summary(raw)
		if len(got) > MaxBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
			t.Fatalf("invalid byte bound: bytes=%d valid=%v", len(got), utf8.ValidString(got))
		}
		if !strings.HasPrefix(got, "HTTP 400: invalid_request_error: ") || Summary(got) != got {
			t.Fatalf("lost metadata or not idempotent: %q", got)
		}
	}
	if got := bound(strings.Repeat("a", MaxBytes+1)); len(got) != MaxBytes {
		t.Fatalf("ASCII boundary = %d bytes, want %d", len(got), MaxBytes)
	}
}

func TestEligible(t *testing.T) {
	if !Eligible("failed", "", "") || Eligible("completed", "", "") || Eligible("failed", "approved", "") || Eligible("failed", "", "123") {
		t.Fatal("failure-only/submitted-result guard changed")
	}
}
